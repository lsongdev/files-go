package main

import (
	"log"
	"net/http"

	"github.com/song940/files-go/files"
)

func main() {
	config, err := files.LoadConfig(".")
	if err != nil {
		log.Fatal(err)
	}
	server, err := files.NewServer(config)
	if err != nil {
		log.Fatal(err)
	}
	http.HandleFunc("/", server.HomeView)
	http.HandleFunc("/files", server.ListView)
	http.ListenAndServe(":8080", nil)
}
