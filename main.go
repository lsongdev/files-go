package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lsongdev/files-go/api"
	"github.com/lsongdev/files-go/auth"
	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/enrichment"
	"github.com/lsongdev/files-go/indexer"
	"github.com/lsongdev/files-go/jobs"
	"github.com/lsongdev/files-go/media"
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
	cat := catalog.NewWithReader(db, readerDB)
	registry := storage.NewRegistry()
	for _, item := range cfg.Storages {
		backend, err := storage.NewLocalWithDevice(item.Path, item.DeviceUUID)
		if err != nil {
			log.Fatalf("configure storage %s: %v", item.ID, err)
		}
		if err := registry.Add(item.ID, backend); err != nil {
			log.Fatal(err)
		}
		if err := cat.RegisterStorage(ctx, item.ID, item.Name, item.Type); err != nil {
			log.Fatal(err)
		}
	}
	if err := cat.RecoverInterruptedScans(ctx); err != nil {
		log.Fatal(err)
	}
	for _, item := range cfg.Libraries {
		library := model.Library{ID: item.ID, Name: item.Name, Type: item.Type}
		for _, source := range item.Sources {
			library.Sources = append(library.Sources, model.LibrarySource{StorageID: source.Storage, Path: source.Path})
		}
		if err := cat.RegisterLibrary(ctx, library); err != nil {
			log.Fatal(err)
		}
	}
	configured := make([]string, 0, len(cfg.Libraries))
	for _, item := range cfg.Libraries {
		configured = append(configured, item.ID)
	}
	if err := cat.PruneUnconfiguredLibraries(ctx, configured); err != nil {
		log.Fatal(err)
	}
	idx := indexer.New(cat, registry)
	paths := make(map[string][]string)
	for _, library := range cfg.Libraries {
		for _, source := range library.Sources {
			paths[source.Storage] = append(paths[source.Storage], source.Path)
		}
	}
	for _, item := range cfg.Storages {
		// The storage catalog represents the whole configured filesystem. Library
		// sources are projections and scan priorities, not catalog boundaries.
		idx.SetPriority(item.ID, paths[item.ID])
	}
	queue := jobs.New(db, 2*time.Minute)
	thumbnailer := processor.NewThumbnail(cat, registry, cfg.CacheDir)
	var provider media.MetadataProvider
	if cfg.Media.TMDB.Token != "" {
		provider = media.NewTMDB(cfg.Media.TMDB.Token, nil)
	}
	videoEnricher := media.NewVideoEnricher(cat, provider, cfg.Media.TMDB.Language)
	directoryEnricher := media.NewDirectoryEnricherWithProvider(cat, registry, provider, cfg.Media.TMDB.Language)
	artwork := media.NewArtwork(cat, cfg.CacheDir, nil)
	directoryEnricher.SetArtwork(artwork)
	idx.SetRemovedEntryReconciler(directoryEnricher.Process)
	processing := processor.New(cat, queue,
		enrichment.Image(cat, registry, thumbnailer),
		enrichment.Ebook(cat, registry, thumbnailer),
		enrichment.Document(cat, registry, thumbnailer, cfg.CacheDir, cfg.Processing.PDFInfo, cfg.Processing.PDFToPPM),
		enrichment.Audio(cat, registry, thumbnailer, cfg.Processing.FFProbe, cfg.Processing.FFmpeg),
		enrichment.Video(cat, registry, thumbnailer, videoEnricher, artwork, cfg.Processing.FFProbe, cfg.Processing.FFmpeg),
		enrichment.Sidecar(directoryEnricher),
	)
	idx.SetEntrySink(processing)
	workerPool := jobs.NewPool(queue, cfg.Processing.Workers)
	workerPool.Handle(processor.JobProcessEntry, processing.Handle)
	workerPool.Start(ctx)
	requestScan := func(storageID string) {
		if !idx.CanScanStorage(storageID) || idx.IsScanning(storageID) {
			return
		}
		go func() {
			if err := idx.Scan(ctx, storageID); err != nil && !errors.Is(err, indexer.ErrScanInProgress) && ctx.Err() == nil {
				log.Printf("storage reconciliation %s: %v", storageID, err)
			}
		}()
	}
	for _, item := range cfg.Storages {
		if err := probeStorageAvailability(ctx, cat, registry, item.ID, requestScan); err != nil {
			log.Printf("probe storage %s: %v", item.ID, err)
		}
	}
	for _, item := range cfg.Storages {
		needsScan, err := cat.NeedsInitialScan(ctx, item.ID)
		if err != nil {
			log.Fatal(err)
		}
		state, err := cat.Storage(ctx, item.ID)
		if err != nil {
			log.Fatal(err)
		}
		if state.State != "offline" && (needsScan || state.State == "interrupted") {
			requestScan(item.ID)
		}
	}
	go func() {
		watcher, err := indexer.NewWatcherWithLimit(cat, registry, idx, log.Default(), cfg.Processing.WatchLimit)
		if err != nil {
			log.Printf("filesystem watcher unavailable: %v", err)
			return
		}
		ids := make([]string, 0, len(cfg.Storages))
		for _, item := range cfg.Storages {
			ids = append(ids, item.ID)
		}
		if err := watcher.Start(ctx, ids); err != nil && ctx.Err() == nil {
			log.Printf("filesystem watcher: %v", err)
		}
	}()
	go func() {
		availability := time.NewTicker(time.Minute)
		defer availability.Stop()
		reconciliation := time.NewTicker(24 * time.Hour)
		defer reconciliation.Stop()
		jobCleanup := time.NewTicker(24 * time.Hour)
		defer jobCleanup.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-availability.C:
				for _, item := range cfg.Storages {
					if err := probeStorageAvailability(ctx, cat, registry, item.ID, requestScan); err != nil && ctx.Err() == nil {
						log.Printf("probe storage %s: %v", item.ID, err)
					}
				}
			case <-reconciliation.C:
				for _, item := range cfg.Storages {
					requestScan(item.ID)
				}
			case <-jobCleanup.C:
				if _, err := queue.CleanupHistory(ctx, 30*24*time.Hour); err != nil && ctx.Err() == nil {
					log.Printf("cleanup job history: %v", err)
				}
			}
		}
	}()
	playbackManager := playback.NewManager(ctx, cat, registry, cfg.Processing.FFmpeg, cfg.CacheDir, 2)
	apiServer := api.New(ctx, cat, registry, idx, log.Default(), cfg.CacheDir, playbackManager)
	apiServer.SetJobQueue(queue)
	apiServer.SetMediaProvider(provider, cfg.Media.TMDB.Language)
	authTokens := make([]auth.Token, 0, len(cfg.Auth.Tokens))
	for _, item := range cfg.Auth.Tokens {
		role, ok := auth.ParseRole(item.Role)
		if !ok {
			log.Fatalf("invalid auth role %q", item.Role)
		}
		authTokens = append(authTokens, auth.Token{Name: item.Name, Secret: item.Token, Role: role})
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", auth.Middleware(authTokens)(apiServer.Handler()))
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
