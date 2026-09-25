package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestHTTPHandlerDoesNotExposeSync(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewHTTPServer(cfg, &State{}, nil)
	request, err := http.NewRequest(http.MethodPost, "/api/sync", nil)
	if err != nil {
		t.Fatal(err)
	}
	response := &responseRecorder{header: make(http.Header)}
	srv.Handler().ServeHTTP(response, request)
	if response.status != http.StatusNotFound {
		t.Fatalf("POST /api/sync status = %d, want 404", response.status)
	}
}

func TestHTTPServerGracefulShutdown(t *testing.T) {
	cfg := DefaultConfig()
	state := &State{Serial: 42, TotalDomains: 3}
	srv := NewHTTPServer(cfg, state, nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(listener) }()

	response, err := http.Get("http://" + listener.Addr().String() + "/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var manifest struct {
		Serial uint32 `json:"serial"`
	}
	if err := json.NewDecoder(response.Body).Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Serial != 42 {
		t.Fatalf("serial = %d, want 42", manifest.Serial)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Serve() error after shutdown: %v", err)
	}
}

type responseRecorder struct {
	header http.Header
	status int
}

func (r *responseRecorder) Header() http.Header { return r.header }
func (r *responseRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return len(p), nil
}
func (r *responseRecorder) WriteHeader(status int) { r.status = status }
