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
	mediaCataloger := mediaengine.NewCataloger(catalogDB)
	processors := []processor.Processor{
		processor.NewImageMetadata(catalogDB, registry),
		processor.NewFFProbe(catalogDB, registry, cfg.Processing.FFProbe, 30*time.Second),
		processor.NewEPUBMetadata(catalogDB, registry),
		processor.NewPDFMetadata(catalogDB, registry, cfg.Processing.PDFInfo, 30*time.Second),
		thumbnailer,
		processor.NewVideoThumbnail(catalogDB, registry, thumbnailer, cfg.Processing.FFmpeg, 60*time.Second),
		processor.NewPDFThumbnail(catalogDB, registry, thumbnailer, cfg.CacheDir, cfg.Processing.PDFToPPM, 60*time.Second),
		processor.NewAudioArtwork(catalogDB, registry, thumbnailer, cfg.Processing.FFmpeg, 30*time.Second),
		mediaCataloger,
	}
	var mediaMatcher *mediaengine.Matcher
	var mediaPoster *mediaengine.Poster
	var metadataProvider mediaengine.MetadataProvider
	if cfg.Media.TMDB.Token != "" {
		metadataProvider = mediaengine.NewTMDB(cfg.Media.TMDB.Token, nil)
		mediaMatcher = mediaengine.NewMatcher(catalogDB, metadataProvider, cfg.Media.TMDB.Language)
		mediaPoster = mediaengine.NewPoster(catalogDB, cfg.CacheDir, nil)
		processors = append(processors,
			mediaMatcher,
			mediaPoster,
		)
	}
	// Local NFO and artwork are applied last so curated sidecars override
	// filename and online-provider metadata for the containing media folder.
	sidecarProcessor := mediaengine.NewSidecarWithProvider(catalogDB, registry, metadataProvider, cfg.Media.TMDB.Language)
	processors = append(processors, sidecarProcessor)
	processing := processor.New(catalogDB, jobQueue, processors...)
	idx.SetEntrySink(processing)
	runMediaReconciliation := func() error {
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
					if err := ctx.Err(); err != nil {
						return err
					}
					if err := mediaMatcher.Process(ctx, entry); err != nil {
						log.Printf("match movie %s: %v", entry.ID, err)
						continue
					}
					if mediaPoster != nil {
						if item, err := catalogDB.MediaItemForEntry(ctx, entry.ID, "video"); err == nil {
							if err := catalogDB.AssociateMediaLibraryFolder(ctx, entry, *item); err != nil {
								log.Printf("reconcile movie folder %s: %v", entry.ID, err)
							}
							if err := mediaPoster.ProcessMedia(ctx, *item); err != nil {
								log.Printf("download movie poster %s: %v", entry.ID, err)
							}
						}
					}
				}
				afterID = entries[len(entries)-1].ID
			}
		}
		refreshTVFolders := func() error {
			if mediaMatcher == nil {
				return nil
			}
			entries, err := catalogDB.EntriesNeedingTVFolderMetadata(ctx, 500)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := mediaCataloger.Process(ctx, entry); err != nil {
					log.Printf("catalog representative TV video %s: %v", entry.ID, err)
					continue
				}
				if err := mediaMatcher.Process(ctx, entry); err != nil {
					log.Printf("match representative TV video %s: %v", entry.ID, err)
					continue
				}
				if err := sidecarProcessor.Process(ctx, entry); err != nil {
					log.Printf("reconcile representative TV folder %s: %v", entry.ID, err)
				}
			}
			return nil
		}
		// Prioritize visible folder identity before the wider, lower-priority
		// reconciliation batches.
		if err := refreshTVFolders(); err != nil {
			return err
		}
		if mediaMatcher != nil {
			afterID := ""
			for {
				entries, err := catalogDB.EntriesAutoMatchedTV(ctx, afterID, 500)
				if err != nil {
					return err
				}
				if len(entries) == 0 {
					break
				}
				for _, entry := range entries {
					if err := ctx.Err(); err != nil {
						return err
					}
					if err := mediaMatcher.Process(ctx, entry); err != nil {
						log.Printf("revalidate TV match %s: %v", entry.ID, err)
					}
				}
				afterID = entries[len(entries)-1].ID
			}
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
				if err := ctx.Err(); err != nil {
					return err
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
					if err := ctx.Err(); err != nil {
						return err
					}
					if err := mediaCataloger.Process(ctx, entry); err != nil {
						log.Printf("catalog media %s: %v", entry.ID, err)
						continue
					}
					if backfill.kind == "video" && mediaMatcher != nil {
						if err := mediaMatcher.Process(ctx, entry); err != nil {
							log.Printf("match video %s: %v", entry.ID, err)
						}
						if err := sidecarProcessor.Process(ctx, entry); err != nil {
							log.Printf("reconcile video sidecars %s: %v", entry.ID, err)
						}
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
					if err := ctx.Err(); err != nil {
						return err
					}
					if err := mediaMatcher.Process(ctx, entry); err != nil {
						log.Printf("enrich episode %s: %v", entry.ID, err)
					}
				}
				afterID = entries[len(entries)-1].ID
			}
		}
		// Recompute derived folder links even when a video's provider match was
		// created by an older version. This also removes collection projections
		// once multiple distinct children are known.
		afterID = ""
		for {
			entries, err := catalogDB.EntriesForMediaFolderReconciliation(ctx, afterID, 500)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				break
			}
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				item, itemErr := catalogDB.MediaItemForEntry(ctx, entry.ID, "series")
				if itemErr != nil {
					item, itemErr = catalogDB.MediaItemForEntry(ctx, entry.ID, "video")
				}
				if itemErr != nil {
					continue
				}
				if err := catalogDB.AssociateMediaLibraryFolder(ctx, entry, *item); err != nil {
					log.Printf("reconcile media folder %s: %v", entry.ID, err)
				}
				if mediaPoster != nil && (item.Type == "movie" || item.Type == "series") {
					if err := mediaPoster.ProcessMedia(ctx, *item); err != nil {
						log.Printf("download media poster %s: %v", entry.ID, err)
					}
				}
			}
			afterID = entries[len(entries)-1].ID
		}
		return nil
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
	requestScan := func(storageID string) {
		if idx.IsScanning(storageID) {
			return
		}
		go func() {
			if err := idx.Scan(ctx, storageID); err != nil && !errors.Is(err, indexer.ErrScanInProgress) && ctx.Err() == nil {
				log.Printf("storage reconciliation %s: %v", storageID, err)
			}
		}()
	}
	for _, item := range cfg.Storages {
		if err := probeStorageAvailability(ctx, catalogDB, registry, item.ID, requestScan); err != nil {
			log.Printf("probe storage %s: %v", item.ID, err)
		}
	}
	go func() {
		if err := runMediaReconciliation(); err != nil && ctx.Err() == nil {
			log.Printf("media reconciliation: %v", err)
		}
	}()
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
		if storageState.State == "offline" {
			continue
		}
		resumeScan := storageState.State == "interrupted"
		if !needsScan && !resumeScan {
			continue
		}
		requestScan(storageID)
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
		availability := time.NewTicker(time.Minute)
		defer availability.Stop()
		reconciliation := time.NewTicker(24 * time.Hour)
		defer reconciliation.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-availability.C:
				for _, item := range cfg.Storages {
					if err := probeStorageAvailability(ctx, catalogDB, registry, item.ID, requestScan); err != nil && ctx.Err() == nil {
						log.Printf("probe storage %s: %v", item.ID, err)
					}
				}
			case <-reconciliation.C:
				for _, item := range cfg.Storages {
					requestScan(item.ID)
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
