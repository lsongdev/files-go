package main

import (
	"flag"
	"log"
	"net/http"

	_ "github.com/glebarez/go-sqlite"

	"github.com/lsongdev/files-go/assets"
	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/server"
)

func main() {
	flag.StringVar(&config.ConfigDir, "d", config.ConfigDir, "config directory")
	flag.Parse()
	config, err := config.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	server, err := server.NewFileServer(config)
	if err != nil {
		log.Fatal(err)
	}
	go server.ScanLibraries()

	http.HandleFunc("/", server.IndexView)
	http.HandleFunc("/files", server.ListHandler)
	http.HandleFunc("/file", server.FileHandler)
	http.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets.Files))))
	log.Printf("Server is running on http://%s\n", config.Listen)
	log.Fatal(http.ListenAndServe(config.Listen, nil))
}
