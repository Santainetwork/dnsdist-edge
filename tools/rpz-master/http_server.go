package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type HTTPServer struct {
	cfg     Config
	state   *State
	stateMu *sync.RWMutex
	server  *http.Server
}

func NewHTTPServer(cfg Config, st *State, mu *sync.RWMutex, _ ...func() error) *HTTPServer {
	if mu == nil {
		mu = new(sync.RWMutex)
	}
	s := &HTTPServer{cfg: cfg, state: st, stateMu: mu}
	s.server = &http.Server{
		Addr:              cfg.ListenHTTP,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

func (s *HTTPServer) Handler() http.Handler {
	mux := http.NewServeMux()
	cdbHandler := func(w http.ResponseWriter, r *http.Request) {
		s.stateMu.RLock()
		cdbPath := s.cfg.CDBPath
		sha := s.state.CDBSHA256
		s.stateMu.RUnlock()
		if _, err := os.Stat(cdbPath); err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "CDB file belum dibuat", http.StatusNotFound)
				return
			}
			http.Error(w, "CDB file tidak tersedia", http.StatusInternalServerError)
			return
		}
		if sha != "" {
			w.Header().Set("ETag", fmt.Sprintf("\"%s\"", sha))
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filepath.Base(cdbPath)))
		http.ServeFile(w, r, cdbPath)
	}
	mux.HandleFunc("/blacklist.db", cdbHandler)
	mux.HandleFunc("/trust.db", cdbHandler)
	mux.HandleFunc("/files/trust.db", cdbHandler)

	manifestHandler := func(w http.ResponseWriter, _ *http.Request) {
		s.stateMu.RLock()
		manifest := map[string]interface{}{
			"status":        "ok",
			"serial":        s.state.Serial,
			"last_update":   s.state.LastUpdate,
			"total_domains": s.state.TotalDomains,
			"sha256":        s.state.CDBSHA256,
			"cdb_file":      filepath.Base(s.cfg.CDBPath),
		}
		s.stateMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(manifest); err != nil {
			http.Error(w, "manifest gagal ditulis", http.StatusInternalServerError)
		}
	}
	mux.HandleFunc("/manifest.json", manifestHandler)
	mux.HandleFunc("/files/manifest.json", manifestHandler)
	mux.HandleFunc("/status", manifestHandler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "healthy"}); err != nil {
			http.Error(w, "health response gagal ditulis", http.StatusInternalServerError)
		}
	})
	return mux
}

func (s *HTTPServer) Serve(listener net.Listener) error {
	if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}

func (s *HTTPServer) Start() error {
	if s.cfg.ListenHTTP == "" {
		return nil
	}
	listener, err := net.Listen("tcp", s.cfg.ListenHTTP)
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}
	go func() {
		_ = s.Serve(listener)
	}()
	return nil
}

func (s *HTTPServer) Shutdown(ctx context.Context) error {
	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown HTTP: %w", err)
	}
	return nil
}
