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
	server := files.NewServer(config)
	http.HandleFunc("/library", server.ListView)
	http.HandleFunc("/files", server.ListAPI)
	http.ListenAndServe(":8080", nil)
}
