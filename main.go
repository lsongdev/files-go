package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	thumbnailer := processor.NewThumbnail(catalogDB, registry, cfg.CacheDir)
	processors := []processor.Processor{
		processor.NewImageMetadata(catalogDB, registry),
		processor.NewFFProbe(catalogDB, registry, cfg.Processing.FFProbe, 30*time.Second),
		processor.NewEPUBMetadata(catalogDB, registry),
		processor.NewPDFMetadata(catalogDB, registry, cfg.Processing.PDFInfo, 30*time.Second),
		thumbnailer,
		processor.NewVideoThumbnail(catalogDB, registry, thumbnailer, cfg.Processing.FFmpeg, 60*time.Second),
		processor.NewPDFThumbnail(catalogDB, registry, thumbnailer, cfg.CacheDir, cfg.Processing.PDFToPPM, 60*time.Second),
		processor.NewAudioArtwork(catalogDB, registry, thumbnailer, cfg.Processing.FFmpeg, 30*time.Second),
		mediaengine.NewCataloger(catalogDB),
	}
	var mediaMatcher *mediaengine.Matcher
	var mediaPoster *mediaengine.Poster
	if cfg.Media.TMDB.Token != "" {
		mediaMatcher = mediaengine.NewMatcher(catalogDB, mediaengine.NewTMDB(cfg.Media.TMDB.Token, nil), cfg.Media.TMDB.Language)
		mediaPoster = mediaengine.NewPoster(catalogDB, cfg.CacheDir, nil)
		processors = append(processors,
			mediaMatcher,
			mediaPoster,
		)
	}
	// Local NFO and artwork are applied last so curated sidecars override
	// filename and online-provider metadata for the containing media folder.
	sidecarProcessor := mediaengine.NewSidecar(catalogDB, registry)
	processors = append(processors, sidecarProcessor)
	processing := processor.New(catalogDB, jobQueue, processors...)
	idx.SetEntrySink(processing)
	runMediaBackfills := func() error {
		const maintenanceName = "media-folders-v3"
		completed, err := catalogDB.MaintenanceCompleted(ctx, maintenanceName)
		if err != nil || completed {
			return err
		}
		refreshMovieMetadata := func() error {
			if mediaMatcher == nil {
				return nil
			}
			afterID := ""
			for {
				entries, err := catalogDB.EntriesNeedingMovieMetadata(ctx, afterID, 500)
				if err != nil {
					return err
				}
				if len(entries) == 0 {
					return nil
				}
				for _, entry := range entries {
					if err := mediaMatcher.Process(ctx, entry); err != nil {
						return fmt.Errorf("match movie %s: %w", entry.ID, err)
					}
					if mediaPoster != nil {
						if item, err := catalogDB.MediaItemForEntry(ctx, entry.ID, "video"); err == nil {
							if err := catalogDB.AssociateMediaLibraryFolder(ctx, entry, *item); err != nil {
								return err
							}
							if err := mediaPoster.ProcessMedia(ctx, *item); err != nil {
								return fmt.Errorf("download movie poster %s: %w", entry.ID, err)
							}
						}
					}
				}
				afterID = entries[len(entries)-1].ID
			}
		}
		previousCompleted, err := catalogDB.MaintenanceCompleted(ctx, "media-folders-v2")
		if err != nil {
			return err
		}
		if previousCompleted {
			if err := refreshMovieMetadata(); err != nil {
				return err
			}
			return catalogDB.CompleteMaintenance(ctx, maintenanceName)
		}
		afterID := ""
		for {
			entries, err := catalogDB.EntriesMediaSidecars(ctx, afterID, 500)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				break
			}
			for _, entry := range entries {
				if strings.EqualFold(entry.Extension, "nfo") &&
					!strings.EqualFold(entry.Name, "tvshow.nfo") && !strings.EqualFold(entry.Name, "movie.nfo") {
					if err := processing.ReprocessEntryPriority(ctx, entry, 300); err != nil {
						return err
					}
					continue
				}
				if err := sidecarProcessor.Process(ctx, entry); err != nil && ctx.Err() == nil {
					log.Printf("media sidecar %s: %v", entry.ID, err)
				}
			}
			afterID = entries[len(entries)-1].ID
		}
		if err := refreshMovieMetadata(); err != nil {
			return err
		}
		for _, backfill := range []struct{ kind, role string }{
			{"audio", "album"},
			{"video", "video"},
			{"photo", "photo"},
			{"book", "book"},
		} {
			afterID := ""
			for {
				entries, err := catalogDB.EntriesMissingMediaAssociation(ctx, backfill.kind, backfill.role, afterID, 500)
				if err != nil {
					return err
				}
				if len(entries) == 0 {
					break
				}
				for _, entry := range entries {
					if err := processing.ReprocessEntryPriority(ctx, entry, 100); err != nil {
						return err
					}
				}
				afterID = entries[len(entries)-1].ID
			}
		}
		afterID = ""
		for {
			entries, err := catalogDB.EntriesMissingAudioArtwork(ctx, afterID, 500)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				break
			}
			if err := processing.EnqueueEntries(ctx, entries); err != nil {
				return err
			}
			afterID = entries[len(entries)-1].ID
		}
		if mediaMatcher != nil {
			afterID = ""
			for {
				entries, err := catalogDB.EntriesNeedingEpisodeMetadata(ctx, afterID, 500)
				if err != nil {
					return err
				}
				if len(entries) == 0 {
					break
				}
				for _, entry := range entries {
					if err := processing.ReprocessEntryPriority(ctx, entry, 100); err != nil {
						return err
					}
				}
				afterID = entries[len(entries)-1].ID
			}
		}
		return catalogDB.CompleteMaintenance(ctx, maintenanceName)
	}
	workerPool := jobs.NewPool(jobQueue, cfg.Processing.Workers)
	workerPool.Handle(processor.JobProcessEntry, processing.Handle)
	workerPool.Start(ctx)
	go func() {
		if err := runMediaBackfills(); err != nil && ctx.Err() == nil {
			log.Printf("media backfill: %v", err)
		}
	}()
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
	go func() {
		filesystemWatcher, err := indexer.NewWatcher(catalogDB, registry, idx, log.Default())
		if err != nil {
			log.Printf("filesystem watcher unavailable: %v", err)
			return
		}
		storageIDs := make([]string, 0, len(cfg.Storages))
		for _, item := range cfg.Storages {
			storageIDs = append(storageIDs, item.ID)
		}
		if err := filesystemWatcher.Start(ctx, storageIDs); err != nil && ctx.Err() == nil {
			log.Printf("start filesystem watcher: %v", err)
		}
	}()
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, item := range cfg.Storages {
					if err := idx.Scan(ctx, item.ID); err != nil && !errors.Is(err, indexer.ErrScanInProgress) && ctx.Err() == nil {
						log.Printf("scheduled reconciliation %s: %v", item.ID, err)
					}
				}
			}
		}
	}()
	playbackManager := playback.NewManager(ctx, catalogDB, registry, cfg.Processing.FFmpeg, cfg.CacheDir, 2)
	apiServer := api.New(ctx, catalogDB, registry, idx, log.Default(), cfg.CacheDir, playbackManager)
	apiServer.SetJobQueue(jobQueue)
	apiServer.SetMediaMatcher(mediaMatcher)
	apiServer.SetMediaPoster(mediaPoster)
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
