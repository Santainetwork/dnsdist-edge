package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileDomainsToCDBScannerErrorPreservesCurrentFile(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "domains.txt")
	cdbPath := filepath.Join(dir, "trust.db")
	original := []byte("last-good-cdb")
	if err := os.WriteFile(cdbPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(raw, []byte(strings.Repeat("x", 1024*1024+1)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileDomainsToCDB(raw, cdbPath, true); err == nil {
		t.Fatal("oversized input compiled successfully")
	}
	got, err := os.ReadFile(cdbPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("current CDB changed after scanner failure: %q", got)
	}
}

func TestPublishDeltaCompileFailurePreservesRawFile(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "domains.txt")
	cdbPath := filepath.Join(dir, "trust.db")
	if err := os.WriteFile(raw, []byte("keep.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(cdbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.RawDomainFile = raw
	cfg.CDBPath = cdbPath
	if _, err := PublishDelta(cfg, &Delta{Full: true, Added: []string{"new.example"}}); err == nil {
		t.Fatal("publication succeeded with directory as CDB destination")
	}
	got, err := os.ReadFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep.example\n" {
		t.Fatalf("raw file changed after compile/publish failure: %q", got)
	}
}

// Jalur SUKSES PublishDelta: delta non-Full diterapkan ke raw yang ada,
// CDB + manifest baru ditulis, dan file lama diganti atomik.
func TestPublishDeltaSuccessReplacesFilesAtomically(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "domains.txt")
	cdbPath := filepath.Join(dir, "trust.db")
	if err := os.WriteFile(raw, []byte("old.example\nkeep.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.RawDomainFile = raw
	cfg.CDBPath = cdbPath
	res, err := PublishDelta(cfg, &Delta{
		FromSerial: 1, ToSerial: 2,
		Added:   []string{"new.example"},
		Deleted: []string{"old.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalEntries != 2 {
		t.Fatalf("total = %d, want 2", res.TotalEntries)
	}
	got, err := os.ReadFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep.example\nnew.example\n" {
		t.Fatalf("raw = %q", got)
	}
	if _, err := os.Stat(cdbPath); err != nil {
		t.Fatalf("CDB tidak ada: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatalf("manifest tidak ada: %v", err)
	}
	// Tidak ada file staging tersisa.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".domains-") || strings.HasPrefix(e.Name(), ".publish-") {
			t.Fatalf("sisa file staging: %s", e.Name())
		}
	}
}

// PublishDelta dengan delta nil harus error (bukan panic).
func TestPublishDeltaNilDelta(t *testing.T) {
	if _, err := PublishDelta(DefaultConfig(), nil); err == nil {
		t.Fatal("delta nil diterima")
	}
}
