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
