package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LoadState: file hilang -> State kosong (bukan error); valid -> terbaca;
// korup -> error (fail-closed, jangan diam-diam menganggap serial 0 lalu AXFR).
func TestLoadStateMissingValidCorrupt(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.json")
	st, err := LoadState(missing)
	if err != nil {
		t.Fatalf("file hilang harus jadi State kosong, dapat error: %v", err)
	}
	if st.Serial != 0 || st.TotalDomains != 0 {
		t.Fatalf("State kosong = %+v", st)
	}

	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"serial":42,"total_domains":7}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err = LoadState(good)
	if err != nil {
		t.Fatal(err)
	}
	if st.Serial != 42 || st.TotalDomains != 7 {
		t.Fatalf("State valid = %+v", st)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(bad); err == nil {
		t.Fatal("state korup diterima (harus fail-closed)")
	}
}

// run(): dispatcher CLI. Uji yang tidak butuh jaringan: -version, action tak
// dikenal, config tak ada, flag override terpasang sebelum validasi.
func TestRunDispatchesAndOverrides(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-version"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "rpz-master v") {
		t.Fatalf("versi tidak tercetak: %q", out.String())
	}

	// Action tak dikenal harus error.
	if err := run([]string{"-action", "bogus"}, &bytes.Buffer{}); err == nil {
		t.Fatal("action tak dikenal diterima")
	}

	// Config tak ada -> error.
	if err := run([]string{"-action", "build-cdb", "-c", "/nonexistent/config.json"}, &bytes.Buffer{}); err == nil {
		t.Fatal("config hilang diterima")
	}
}

// Override flag (-zone, -input) harus mengalahkan config, dan validasi tetap
// jalan setelah override.
func TestRunFlagOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	raw := filepath.Join(dir, "domains.txt")
	if err := os.WriteFile(raw, []byte("override.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := `{
	  "source_mode": "feeds",
	  "zone": "original.example.",
	  "raw_domain_file": "` + raw + `",
	  "cdb_path": "` + filepath.Join(dir, "trust.db") + `",
	  "state_path": "` + filepath.Join(dir, "state.json") + `",
	  "listen_dns": "127.0.0.1:15399"
	}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	// build-cdb dengan -zone override; kalau override tidak diterapkan,
	// zone default valid juga, jadi uji lewat efek: build sukses + CDB ada.
	if err := run([]string{"-action", "build-cdb", "-c", cfgPath, "-zone", "overridden.example."}, &bytes.Buffer{}); err != nil {
		t.Fatalf("build-cdb dengan override gagal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "trust.db")); err != nil {
		t.Fatalf("CDB tidak dibuat: %v", err)
	}
	// Override zone invalid harus ditolak validasi.
	if err := run([]string{"-action", "build-cdb", "-c", cfgPath, "-zone", "bad..zone"}, &bytes.Buffer{}); err == nil {
		t.Fatal("zone override invalid diterima")
	}
}
