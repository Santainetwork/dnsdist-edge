package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DownloadDomainsToFile adalah inti jalur bootstrap. Tes:
// 1) sukses -> file ditulis lengkap;
// 2) HTTP non-200 -> error dan file lama TIDAK tersentuh (atomic);
// 3) koneksi gagal di tengah -> file lama tetap utuh.
func TestDownloadDomainsToFileSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("User-Agent tidak dikirim")
		}
		_, _ = w.Write([]byte("a.example\nb.example\n"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "domains.txt")
	if err := DownloadDomainsToFile(srv.URL, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a.example\nb.example\n" {
		t.Fatalf("isi = %q", got)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestDownloadDomainsToFileNon200PreservesExisting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "domains.txt")
	if err := os.WriteFile(dest, []byte("keep.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DownloadDomainsToFile(srv.URL, dest); err == nil {
		t.Fatal("HTTP 500 diterima sebagai sukses")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep.example\n" {
		t.Fatalf("file lama berubah: %q", got)
	}
	// Tidak boleh ada file sementara yang tertinggal.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("ada %d entri di dir; sisa file sementara", len(entries))
	}
}

// Bootstrap lewat URL (bukan file lokal) harus mengunduh lalu mengompilasi CDB.
func TestBootstrapDownloadsAndCompiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("evil-one.example\nevil-two.example\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.SourceMode = "rpz-slave"
	cfg.SourceDomainURL = srv.URL
	cfg.RawDomainFile = filepath.Join(dir, "domains.txt")
	cfg.CDBPath = filepath.Join(dir, "trust.db")
	cfg.StatePath = filepath.Join(dir, "state.json")
	state := &State{}
	if err := NewApp(cfg, state).Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if state.TotalDomains != 2 {
		t.Fatalf("total = %d, want 2", state.TotalDomains)
	}
	raw, err := os.ReadFile(cfg.RawDomainFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "evil-one.example") {
		t.Fatalf("raw file = %q", raw)
	}
	if _, err := os.Stat(cfg.CDBPath); err != nil {
		t.Fatalf("CDB tidak dibuat: %v", err)
	}
	if state.Serial != 0 {
		t.Fatalf("serial rpz-slave harus tetap 0, dapat %d", state.Serial)
	}
}
