package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTestMux mirrors the production route registration so the acceptance test
// exercises the same handler set the binary serves, not a hand-picked subset.
func buildTestMux(t *testing.T) *http.ServeMux {
	t.Helper()
	dir := t.TempDir()
	old := *flagFilesDir
	*flagFilesDir = dir
	t.Cleanup(func() { *flagFilesDir = old })

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	mux.HandleFunc("/api/health", handleHealth)
	mux.HandleFunc("/api/login", handleLogin)
	mux.HandleFunc("/api/stats", auth(handleStats))
	mux.HandleFunc("/api/rpz/status", auth(handleRPZStatus))
	mux.HandleFunc("/api/rpz/test", auth(handleRPZTest))
	mux.HandleFunc("/api/upstream/status", auth(handleUpstreamStatus))
	mux.HandleFunc("/api/dnstap/status", auth(handleDnstapStatus))
	mux.HandleFunc("/api/config", auth(handleConfig))
	return mux
}

// TestServedIndexHasStitchLayout serves the embedded UI over HTTP and asserts the
// new layout actually shipped, plus that it stays offline-capable.
func TestServedIndexHasStitchLayout(t *testing.T) {
	srv := httptest.NewServer(buildTestMux(t))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	for _, marker := range []string{"stat-tile", "dtable", "Obsidian Telemetry", "rpz-feed-tbody", "fetchRPZStatus"} {
		if !strings.Contains(html, marker) {
			t.Errorf("served UI missing marker %q", marker)
		}
	}
	for _, banned := range []string{"cdn.tailwindcss.com", "fonts.googleapis.com", "fonts.gstatic.com"} {
		if strings.Contains(html, banned) {
			t.Errorf("served UI references external CDN %q; panel must stay offline", banned)
		}
	}
}

// TestStatusEndpointsRequireAuth confirms the new read endpoints are not public.
func TestStatusEndpointsRequireAuth(t *testing.T) {
	srv := httptest.NewServer(buildTestMux(t))
	defer srv.Close()
	for _, path := range []string{"/api/rpz/status", "/api/upstream/status", "/api/dnstap/status", "/api/rpz/test?domain=x.com"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s returned 200 without auth token", path)
		}
	}
}

// TestHealthIsPublicAndJSON verifies the unauthenticated probe the UI pill uses.
func TestHealthIsPublicAndJSON(t *testing.T) {
	srv := httptest.NewServer(buildTestMux(t))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type=%q want json", ct)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["ok"] != true {
		t.Errorf("ok=%v", got["ok"])
	}
}

// TestRPZStatusReportsRealCDB writes a real generator CDB into the served files
// dir and checks the endpoint reflects its size and hash, proving the endpoint is
// wired to disk rather than returning constants.
func TestRPZStatusReportsRealCDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trust.db")
	if err := writeTestCDB(dbPath, []string{"evil.com"}); err != nil {
		t.Fatalf("write cdb: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"sha256":"deadbeefcafe1234"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	old := *flagFilesDir
	*flagFilesDir = dir
	t.Cleanup(func() { *flagFilesDir = old })

	req := httptest.NewRequest(http.MethodGet, "/api/rpz/status", nil)
	rec := httptest.NewRecorder()
	handleRPZStatus(rec, req)

	var got struct {
		CDBSize int64  `json:"cdb_size"`
		CDBHash string `json:"cdb_hash"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	st, _ := os.Stat(dbPath)
	if got.CDBSize != st.Size() {
		t.Errorf("cdb_size=%d want %d", got.CDBSize, st.Size())
	}
	if got.CDBHash != "deadbeefcafe1234" {
		t.Errorf("cdb_hash=%q want manifest value", got.CDBHash)
	}
}
