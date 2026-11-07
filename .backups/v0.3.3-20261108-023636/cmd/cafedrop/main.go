package main

import (
	"log"
	"net"
	"net/http"
	"os"

	"cafedrop/internal/config"
	"cafedrop/internal/server"
	"cafedrop/internal/storage"
	"cafedrop/internal/store"
)

func main() {
	cfg := config.Load()
	if err := os.MkdirAll(cfg.DataDir, 0750); err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	fs, err := storage.New(cfg.UploadDir, cfg.MaxUploadBytes)
	if err != nil {
		log.Fatal(err)
	}
	srv := server.New(cfg, st, fs)
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		log.Fatal(err)
	}
	if err := st.StartTrackingRun(); err != nil {
		_ = listener.Close()
		log.Fatal(err)
	}
	log.Printf("CafeDrop listening on %s", cfg.ListenAddr)
	log.Fatal(http.Serve(listener, srv.Handler()))
}
