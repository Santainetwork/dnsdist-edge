package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanupVersionedDBsKeepsActiveFileOnly(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, "trust."+strings.Repeat("a", 64)+".db")
	old := filepath.Join(dir, "trust."+strings.Repeat("b", 64)+".db")
	unrelated := filepath.Join(dir, "trust.backup.db")
	for _, path := range []string{active, old, unrelated} {
		if err := os.WriteFile(path, []byte("db"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := cleanupVersionedDBs(dir, "trust", active)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active DB removed: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old DB still exists: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated file removed: %v", err)
	}
}
