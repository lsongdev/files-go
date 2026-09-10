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
	"github.com/lsongdev/files-go/model"
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
	catalogDB := catalog.New(db)
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
		go func() {
			if err := idx.Scan(ctx, storageID); err != nil && ctx.Err() == nil {
				log.Printf("initial scan %s: %v", storageID, err)
			}
		}()
	}
	apiServer := api.New(ctx, catalogDB, registry, idx, log.Default())
	mux := http.NewServeMux()
	mux.Handle("/api/", apiServer.Handler())
	mux.Handle("/", web.Handler())
	httpServer := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	log.Printf("files-go API listening on http://%s", cfg.Listen)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
