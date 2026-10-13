package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTTPRawDomainsETagAndRange(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "domains.txt")
	if err := os.WriteFile(raw, []byte("example.com\nsub.example.net\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.RawDomainFile = raw
	st := &State{LastUpdate: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	srv := NewHTTPServer(cfg, st, nil)
	request := httptest.NewRequest(http.MethodGet, "/domains", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, request)
	if rec.Code != http.StatusOK || rec.Body.String() != "example.com\nsub.example.net\n" {
		t.Fatalf("raw response: status=%d body=%q", rec.Code, rec.Body.String())
	}
	etag := rec.Header().Get("ETag")
	if etag == "" || rec.Header().Get("Last-Modified") == "" || rec.Header().Get("X-RPZ-Last-Update") == "" {
		t.Fatalf("raw metadata missing: %v", rec.Header())
	}
	request = httptest.NewRequest(http.MethodGet, "/domains", nil)
	request.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, request)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("conditional raw response: %d, want 304", rec.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/domains", nil)
	request.Header.Set("Range", "bytes=0-6")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, request)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "example" {
		t.Fatalf("range raw response: status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestHTTPRawDomainsMissing(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RawDomainFile = filepath.Join(t.TempDir(), "missing.txt")
	srv := NewHTTPServer(cfg, &State{}, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/domains", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing raw response: %d, want 404", rec.Code)
	}
}

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
	body   bytes.Buffer
}

func (r *responseRecorder) Header() http.Header { return r.header }
func (r *responseRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(p)
}
func (r *responseRecorder) WriteHeader(status int) { r.status = status }

// /health harus 200 + {"status":"healthy"}.
func TestHTTPHealthEndpoint(t *testing.T) {
	srv := NewHTTPServer(DefaultConfig(), &State{}, nil)
	request, _ := http.NewRequest(http.MethodGet, "/health", nil)
	response := &responseRecorder{header: make(http.Header)}
	srv.Handler().ServeHTTP(response, request)
	if response.status != http.StatusOK {
		t.Fatalf("health status = %d", response.status)
	}
	if !strings.Contains(response.body.String(), "healthy") {
		t.Fatalf("health body = %q", response.body.String())
	}
}

// /trust.db harus menyajikan file CDB dengan ETag = sha256, dan 404 bila
// file belum dibuat (fail jelas, bukan 200 kosong).
func TestHTTPCDBEndpointServesFileWithETag(t *testing.T) {
	dir := t.TempDir()
	cdbPath := filepath.Join(dir, "trust.db")
	if err := os.WriteFile(cdbPath, []byte("fake-cdb-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.CDBPath = cdbPath
	state := &State{Serial: 5, CDBSHA256: "abc123"}
	srv := NewHTTPServer(cfg, state, nil)

	request, _ := http.NewRequest(http.MethodGet, "/trust.db", nil)
	response := &responseRecorder{header: make(http.Header)}
	srv.Handler().ServeHTTP(response, request)
	if response.status != http.StatusOK {
		t.Fatalf("trust.db status = %d", response.status)
	}
	if got := response.header.Get("ETag"); got != `"abc123"` {
		t.Fatalf("ETag = %q, want \"abc123\"", got)
	}
	if !strings.Contains(response.body.String(), "fake-cdb-bytes") {
		t.Fatalf("isi CDB tidak disajikan: %q", response.body.String())
	}
}

func TestHTTPCDBEndpoint404WhenMissing(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CDBPath = filepath.Join(t.TempDir(), "absent.db")
	srv := NewHTTPServer(cfg, &State{}, nil)
	request, _ := http.NewRequest(http.MethodGet, "/trust.db", nil)
	response := &responseRecorder{header: make(http.Header)}
	srv.Handler().ServeHTTP(response, request)
	if response.status != http.StatusNotFound {
		t.Fatalf("CDB hilang status = %d, want 404", response.status)
	}
}

// /manifest.json harus memuat field kontrak yang dipakai edge.
func TestHTTPManifestFields(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CDBPath = "/var/lib/dnsdist/trust.db"
	state := &State{Serial: 77, TotalDomains: 9, CDBSHA256: "hash"}
	srv := NewHTTPServer(cfg, state, nil)
	request, _ := http.NewRequest(http.MethodGet, "/manifest.json", nil)
	response := &responseRecorder{header: make(http.Header)}
	srv.Handler().ServeHTTP(response, request)
	if response.status != http.StatusOK {
		t.Fatalf("manifest status = %d", response.status)
	}
	body := response.body.String()
	for _, want := range []string{`"serial":77`, `"total_domains":9`, `"sha256":"hash"`, `"status":"ok"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("manifest tidak memuat %s: %s", want, body)
		}
	}
}
