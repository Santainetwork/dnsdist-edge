package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveStateCreatesParentAndUsesOwnerOnlyMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "state.json")
	if err := (&State{Serial: 7}).Save(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state mode = %04o, want 0600", got)
	}
}

func TestLoadConfigRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"source_mode":"rpz-slave","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("unknown config field accepted")
	}
}
