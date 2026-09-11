package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lsongdev/files-go/api"
	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/indexer"
	"github.com/lsongdev/files-go/jobs"
	mediaengine "github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/playback"
	"github.com/lsongdev/files-go/processor"
	"github.com/lsongdev/files-go/storage"
	"github.com/lsongdev/files-go/web"
)

func main() {
	flag.StringVar(&config.ConfigDir, "d", config.ConfigDir, "config directory")
	flag.Parse()
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := database.Open(ctx, cfg.Data)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	readerDB, err := database.OpenReader(ctx, cfg.Data)
	if err != nil {
		log.Fatal(err)
	}
	defer readerDB.Close()
	catalogDB := catalog.NewWithReader(db, readerDB)
	registry := storage.NewRegistry()
	for _, item := range cfg.Storages {
		backend, err := storage.NewLocal(item.Path)
		if err != nil {
			log.Fatalf("configure storage %s: %v", item.ID, err)
		}
		if err := registry.Add(item.ID, backend); err != nil {
			log.Fatal(err)
		}
		if err := catalogDB.RegisterStorage(ctx, item.ID, item.Name, item.Type); err != nil {
			log.Fatal(err)
		}
	}
	if err := catalogDB.RecoverInterruptedScans(ctx); err != nil {
		log.Fatal(err)
	}
	for _, item := range cfg.Libraries {
		library := model.Library{ID: item.ID, Name: item.Name, Type: item.Type}
		for _, source := range item.Sources {
			library.Sources = append(library.Sources, model.LibrarySource{StorageID: source.Storage, Path: source.Path})
		}
		if err := catalogDB.RegisterLibrary(ctx, library); err != nil {
			log.Fatal(err)
		}
	}
	idx := indexer.New(catalogDB, registry)
	jobQueue := jobs.New(db, 2*time.Minute)
	processors := []processor.Processor{
		processor.NewImageMetadata(catalogDB, registry),
		processor.NewFFProbe(catalogDB, registry, cfg.Processing.FFProbe, 30*time.Second),
		processor.NewEPUBMetadata(catalogDB, registry),
		processor.NewPDFMetadata(catalogDB, registry, cfg.Processing.PDFInfo, 30*time.Second),
		processor.NewThumbnail(catalogDB, registry, cfg.CacheDir),
		mediaengine.NewCataloger(catalogDB),
	}
	if cfg.Media.TMDB.Token != "" {
		processors = append(processors,
			mediaengine.NewMatcher(catalogDB, mediaengine.NewTMDB(cfg.Media.TMDB.Token, nil), cfg.Media.TMDB.Language),
			mediaengine.NewPoster(catalogDB, cfg.CacheDir, nil),
		)
	}
	processing := processor.New(catalogDB, jobQueue, processors...)
	idx.SetEntrySink(processing)
	afterID := ""
	for {
		entries, err := catalogDB.EntriesMissingMediaAssociation(ctx, "audio", "album", afterID, 500)
		if err != nil {
			log.Fatal(err)
		}
		if len(entries) == 0 {
			break
		}
		if err := processing.EnqueueEntries(ctx, entries); err != nil {
			log.Fatal(err)
		}
		afterID = entries[len(entries)-1].ID
	}
	workerPool := jobs.NewPool(jobQueue, cfg.Processing.Workers)
	workerPool.Handle(processor.JobProcessEntry, processing.Handle)
	workerPool.Start(ctx)
	for _, item := range cfg.Storages {
		var priority []string
		for _, library := range cfg.Libraries {
			for _, source := range library.Sources {
				if source.Storage == item.ID {
					priority = append(priority, source.Path)
				}
			}
		}
		idx.SetPriority(item.ID, priority)
	}
	for _, item := range cfg.Storages {
		storageID := item.ID
		needsScan, err := catalogDB.NeedsInitialScan(ctx, storageID)
		if err != nil {
			log.Fatal(err)
		}
		storageState, err := catalogDB.Storage(ctx, storageID)
		if err != nil {
			log.Fatal(err)
		}
		resumeScan := storageState.State == "interrupted"
		if !needsScan && !resumeScan {
			continue
		}
		go func() {
			if err := idx.Scan(ctx, storageID); err != nil && ctx.Err() == nil {
				log.Printf("startup scan %s: %v", storageID, err)
			}
		}()
	}
	playbackManager := playback.NewManager(ctx, catalogDB, registry, cfg.Processing.FFmpeg, cfg.CacheDir, 2)
	apiServer := api.New(ctx, catalogDB, registry, idx, log.Default(), cfg.CacheDir, playbackManager)
	apiServer.SetJobQueue(jobQueue)
	mux := http.NewServeMux()
	mux.Handle("/api/", apiServer.Handler())
	mux.Handle("/", web.Handler())
	httpServer := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		close(shutdownDone)
	}()
	log.Printf("files-go API listening on http://%s", cfg.Listen)
	serveErr := httpServer.ListenAndServe()
	stop()
	<-shutdownDone
	workerPool.Wait()
	if serveErr != nil && serveErr != http.ErrServerClosed {
		log.Fatal(serveErr)
	}
}
