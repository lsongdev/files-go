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
	"sync"
	"syscall"
	"time"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/pkg/theporndb"
	"github.com/lsongdev/files-go/server"
	"github.com/lsongdev/files-go/store"
	"github.com/lsongdev/files-go/web"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	home, _ := os.UserHomeDir()
	configDir := flag.String("d", filepath.Join(home, ".filesgo"), "directory containing config.yaml")
	flag.Parse()
	cfg, err := config.Load(filepath.Join(*configDir, "config.yaml"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Data, 0o755); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(filepath.Join(cfg.Data, "files.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	scanner := store.NewScanner(st, cfg.Roots, cfg.Exclude, media.Classify)
	if err := scanner.Init(ctx); err != nil {
		return err
	}

	_ = st.CachePrune(ctx, "show:", 30*24*time.Hour)
	_ = st.CachePrune(ctx, "probe:", 30*24*time.Hour)
	var client *media.TMDB
	if cfg.TMDB.Key != "" {
		client = media.NewTMDB(cfg.TMDB.Key, cfg.TMDB.Language, st)
	}
	var tpdb *theporndb.Client
	if cfg.ThePornDB.Token != "" {
		_ = st.CachePrune(ctx, "theporndb:", 30*24*time.Hour)
		tpdb, err = theporndb.New(theporndb.Options{Token: cfg.ThePornDB.Token, Cache: media.ThePornDBCache{Store: st}})
		if err != nil {
			return err
		}
	}
	pdfinfo, _ := exec.LookPath("pdfinfo")
	openSource := scanner.Open
	library := &media.Library{Store: st, TMDB: client, Open: openSource, Hint: func(f store.File) (string, string) {
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
	tag := fmt.Sprintf("theporndb=%t tmdb=%t/%s pdf=%t hints=%v", tpdb != nil, client != nil, cfg.TMDB.Language, pdfinfo != "", hints)
	pipeline, err := media.NewPipeline(ctx, st, tag,
		&media.Probe{Bin: cfg.FFprobe, Open: openSource, Store: st},
		library.Videos(),
		library.Folders(),
		&media.JAV{Client: tpdb, Hint: library.Hint, Store: st},
		media.Photo{Open: openSource, FFprobe: cfg.FFprobe},
		media.Document{Open: openSource, PDFInfo: pdfinfo},
		media.App{Open: openSource},
		media.Archive{Open: openSource},
	)
	if err != nil {
		return err
	}

	api, err := server.New(ctx, cfg, st, scanner, pipeline, client, tpdb)
	if err != nil {
		return err
	}
	var background sync.WaitGroup
	background.Add(2)
	go func() { defer background.Done(); pipeline.Run(ctx, cfg.Workers) }()
	go func() { defer background.Done(); scanner.Run(ctx, cfg.ScanInterval, pipeline.Wake) }()
	defer func() { stop(); api.Close(); background.Wait() }()
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.Handler(web.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if cfg.Auth.Password == "" {
		log.Printf("warning: no auth.password configured, the server is open to anyone who can reach it")
	}
	log.Printf("listening on %s", cfg.Listen)
	err = srv.ListenAndServe()
	stop()
	<-shutdownDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
