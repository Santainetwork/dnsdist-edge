package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Kontrak: /files/ hanya menyajikan file publik CDB. Dotfile/dotdir
// (mis. .cache, cache feed sumber mentah) tidak boleh dilayani atau di-list.
func TestFilesHandlerHidesDotPaths(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cache"), 0755)
	os.WriteFile(filepath.Join(dir, ".cache", "feed.txt"), []byte("RAW-FEED"), 0644)
	os.WriteFile(filepath.Join(dir, "trust.db"), []byte("CDB"), 0644)

	h := filesHandler(dir)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/files/trust.db", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "CDB" {
		t.Fatalf("trust.db harus tetap dilayani: %d %q", rr.Code, rr.Body.String())
	}
	for _, p := range []string{"/files/.cache/feed.txt", "/files/.cache/", "/files/.cache"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404 (dotpath bocor)", p, rr.Code)
		}
	}
}
