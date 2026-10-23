package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/server"
	"github.com/lsongdev/files-go/store"
	"github.com/lsongdev/files-go/tmdb"
	"github.com/lsongdev/files-go/web"
)

func main() {
	home, _ := os.UserHomeDir()
	configPath := flag.String("c", filepath.Join(home, ".filesgo", "config.yaml"), "config file")
	flag.Parse()
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Data, 0o755); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(filepath.Join(cfg.Data, "files.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	scanner := store.NewScanner(st, cfg.Roots, cfg.Exclude, media.Classify)
	if err := scanner.Init(ctx); err != nil {
		log.Fatal(err)
	}

	var client *tmdb.Client
	if cfg.TMDB.Key != "" {
		client = tmdb.New(cfg.TMDB.Key, cfg.TMDB.Language, st)
	}
	pdfinfo, _ := exec.LookPath("pdfinfo")
	resolve := scanner.Abs
	library := &media.Library{Store: st, TMDB: client, Path: resolve, Hint: func(f store.File) (string, string) {
		root, _ := scanner.Root(f.Root)
		return root.TypeOf(f.Path)
	}}
	// Anything that changes enrichment results is part of the version tag,
	// so changing it re-enriches the affected files in the background.
	hints := map[string]string{}
	for _, root := range cfg.Roots {
		if root.Type != "" || len(root.Types) > 0 {
			hints[root.ID] = fmt.Sprint(root.Type, root.Types) // maps print sorted
		}
	}
	tag := fmt.Sprintf("tmdb=%t/%s pdf=%t hints=%v", client != nil, cfg.TMDB.Language, pdfinfo != "", hints)
	pipeline := media.NewPipeline(st, tag,
		&media.Probe{Bin: cfg.FFprobe, Path: resolve},
		library.Videos(),
		library.Folders(),
		media.Photo{Path: resolve},
		media.Document{Path: resolve, PDFInfo: pdfinfo},
		media.App{Path: resolve},
		media.Archive{Path: resolve},
	)
	go pipeline.Run(ctx, cfg.Workers)
	go func() {
		for {
			scanner.ScanAll(ctx)
			pipeline.Wake()
			select {
			case <-ctx.Done():
				return
			case <-time.After(cfg.ScanInterval):
			}
		}
	}()

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server.New(ctx, cfg, st, scanner, pipeline, client).Handler(web.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if cfg.Auth.Password == "" {
		log.Printf("warning: no auth.password configured, the server is open to anyone who can reach it")
	}
	log.Printf("listening on %s", cfg.Listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
