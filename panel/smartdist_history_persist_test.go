package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Kontrak F5.3 persisten: riwayat penugasan bertahan setelah store dibuka ulang
// dari disk. Sebelumnya riwayat hanya di memori dan hilang saat restart.
func TestAssignHistorySurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smartdist.json")
	s := newSmartDistProfileStore(path)
	if _, err := s.Put([]byte(smartDistTestProfile)); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if err := s.Assign("node-h1", "cfg-a"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := s.Assign("node-h1", ""); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	before := s.History("node-h1")
	if len(before) != 2 {
		t.Fatalf("riwayat sebelum reload = %d, want 2", len(before))
	}

	reloaded := newSmartDistProfileStore(path)
	after := reloaded.History("node-h1")
	if len(after) != len(before) {
		t.Fatalf("riwayat setelah reload = %d, want %d (tidak persisten)", len(after), len(before))
	}
	if after[0].To != "cfg-a" || after[1].To != "" {
		t.Fatalf("urutan/isi riwayat rusak: %+v", after)
	}
	_ = os.Remove(path)
}

// Kompatibilitas mundur: file lama tanpa field riwayat tetap terbaca, riwayat kosong.
func TestLegacyFileWithoutHistoryLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.json")
	legacy := `{"profiles":{},"assign":{}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSmartDistProfileStore(path)
	if h := s.History("node-x"); h == nil || len(h) != 0 {
		t.Fatalf("riwayat legacy harus kosong non-nil, got %#v", h)
	}
}
