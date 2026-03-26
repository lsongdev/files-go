package main

import (
	"log"
	"net/http"

	"github.com/emersion/go-webdav"
	_ "github.com/glebarez/go-sqlite"

	// _ "github.com/mattn/go-sqlite3"

	"github.com/lsongdev/files-go/assets"
	"github.com/lsongdev/files-go/server"
)

func main() {
	server, err := server.NewFileServer()
	if err != nil {
		log.Fatal(err)
	}

	go server.ScanLibraries()

	handler := webdav.Handler{
		FileSystem: server,
	}
	http.HandleFunc("/", server.IndexView)
	http.HandleFunc("/files", server.ListHandler)
	http.HandleFunc("/file", server.FileHandler)
	http.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets.Files))))
	http.Handle("/webdav/", &handler)
	log.Println("Server is running on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
