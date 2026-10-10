package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Daftar blokir lokal per node (jalur 1 "all in one").
// Harus tegas: domain tidak valid ditolak, duplikat dibuang, file ditulis atomik.

func TestLocalBlockNormalizeDomain(t *testing.T) {
	cases := map[string]string{
		"Example.COM":        "example.com",
		"  ads.example.net ": "ads.example.net",
		"a-b.example.org.":   "a-b.example.org",
	}
	for in, want := range cases {
		got, err := normalizeLocalBlockDomain(in)
		if err != nil || got != want {
			t.Errorf("normalize(%q) = %q,%v; want %q,nil", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "http://x.com", "bad space.com", "-lead.com", "a..b.com", "under_score.com", strings.Repeat("a", 64) + ".com"} {
		if _, err := normalizeLocalBlockDomain(bad); err == nil {
			t.Errorf("normalize(%q) accepted, want error", bad)
		}
	}
}

func TestLocalBlockStoreAddRemoveAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local-block.txt")
	s := newLocalBlockStore(path)

	if err := s.Add([]string{"ads.example.com", "TRACK.example.net", "ads.example.com"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	got := s.List()
	if len(got) != 2 {
		t.Fatalf("list = %v, want 2 unique domains", got)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if !strings.Contains(string(b), "ads.example.com\n") || !strings.Contains(string(b), "track.example.net\n") {
		t.Errorf("file content = %q", b)
	}

	// Add invalid batch: nothing must change (all-or-nothing).
	if err := s.Add([]string{"ok.example.org", "bad space"}); err == nil {
		t.Fatal("invalid batch accepted")
	}
	if n := len(s.List()); n != 2 {
		t.Errorf("partial write after invalid batch: %d domains", n)
	}

	if err := s.Remove("ads.example.com"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if n := len(s.List()); n != 1 {
		t.Errorf("after remove = %d, want 1", n)
	}
}

func TestLocalBlockStoreSurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local-block.txt")
	s := newLocalBlockStore(path)
	if err := s.Add([]string{"persist.example.com"}); err != nil {
		t.Fatal(err)
	}
	s2 := newLocalBlockStore(path)
	if got := s2.List(); len(got) != 1 || got[0] != "persist.example.com" {
		t.Errorf("reload list = %v", got)
	}
}

func TestLocalBlockHandler(t *testing.T) {
	prev := localBlock
	localBlock = newLocalBlockStore(filepath.Join(t.TempDir(), "local-block.txt"))
	t.Cleanup(func() { localBlock = prev })

	// POST tambah banyak domain.
	rr := httptest.NewRecorder()
	handleLocalBlock(rr, httptest.NewRequest(http.MethodPost, "/api/localblock", strings.NewReader(`{"domains":["ads.example.com","track.example.net"]}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST = %d; body=%s", rr.Code, rr.Body.String())
	}

	// GET daftar.
	rr = httptest.NewRecorder()
	handleLocalBlock(rr, httptest.NewRequest(http.MethodGet, "/api/localblock", nil))
	var out struct {
		Domains []string `json:"domains"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || len(out.Domains) != 2 {
		t.Fatalf("GET domains = %v err=%v body=%s", out.Domains, err, rr.Body.String())
	}

	// POST domain tidak valid -> 400, daftar tidak berubah.
	rr = httptest.NewRecorder()
	handleLocalBlock(rr, httptest.NewRequest(http.MethodPost, "/api/localblock", strings.NewReader(`{"domains":["bad space"]}`)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid POST = %d, want 400", rr.Code)
	}
	if n := len(localBlock.List()); n != 2 {
		t.Errorf("list changed after invalid POST: %d", n)
	}

	// DELETE satu domain.
	rr = httptest.NewRecorder()
	handleLocalBlock(rr, httptest.NewRequest(http.MethodDelete, "/api/localblock?domain=ads.example.com", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE = %d; body=%s", rr.Code, rr.Body.String())
	}
	if n := len(localBlock.List()); n != 1 {
		t.Errorf("after DELETE = %d, want 1", n)
	}

	// Metode lain -> 405.
	rr = httptest.NewRecorder()
	handleLocalBlock(rr, httptest.NewRequest(http.MethodPatch, "/api/localblock", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("PATCH = %d, want 405", rr.Code)
	}
}
