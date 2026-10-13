package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/colinmarc/cdb"
	"github.com/miekg/dns"
)

// Klaim inti (README §11): kunci CDB memakai wire-format DNS sehingga cocok
// dengan KeyValueLookupKeyQName(true) di dnsdist. Tes ini membuka CDB hasil
// kompilasi dan melakukan lookup persis seperti dnsdist: key = PackDomainName.
func TestCDBKeysUseDNSWireFormat(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "domains.txt")
	cdbPath := filepath.Join(dir, "trust.db")
	if err := os.WriteFile(raw, []byte("evil.example\nads.example.org\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileDomainsToCDB(raw, cdbPath, true); err != nil {
		t.Fatal(err)
	}
	db, err := cdb.Open(cdbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, domain := range []string{"evil.example", "ads.example.org"} {
		buf := make([]byte, 256)
		off, err := dns.PackDomainName(dns.Fqdn(domain), buf, 0, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		wire := buf[:off]
		value, err := db.Get(wire)
		if err != nil {
			t.Fatal(err)
		}
		if value == nil {
			t.Fatalf("lookup wire-format untuk %q TIDAK ketemu (CDB tidak kompatibel dnsdist)", domain)
		}
		// Kunci plain-text (bukan wire) tidak boleh ketemu — membuktikan
		// pemakaian wire-format, bukan kebetulan.
		plain, err := db.Get([]byte(dns.Fqdn(domain)))
		if err != nil {
			t.Fatal(err)
		}
		if plain != nil {
			t.Fatalf("lookup plain-text untuk %q ketemu; CDB mungkin tidak wire-format", domain)
		}
	}
}

// Tes jalur PRODUKSI: App.BuildCDB() (yang dipakai -action build-cdb) harus
// menghasilkan CDB wire-format. Tes ini menangkap mutasi di main.go yang
// melewatkan wireFormat=true.
func TestBuildCDBAppPathProducesWireFormat(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.SourceMode = "rpz-slave"
	cfg.RawDomainFile = filepath.Join(dir, "domains.txt")
	cfg.CDBPath = filepath.Join(dir, "trust.db")
	cfg.StatePath = filepath.Join(dir, "state.json")
	if err := os.WriteFile(cfg.RawDomainFile, []byte("blocked-path.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewApp(cfg, &State{}).BuildCDB(); err != nil {
		t.Fatal(err)
	}
	db, err := cdb.Open(cfg.CDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	buf := make([]byte, 256)
	off, err := dns.PackDomainName("blocked-path.example.", buf, 0, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	value, err := db.Get(buf[:off])
	if err != nil {
		t.Fatal(err)
	}
	if value == nil {
		t.Fatal("App.BuildCDB() menghasilkan CDB yang TIDAK wire-format (dnsdist tidak akan cocok)")
	}
}

// Mutasi-penjaga: kompilasi dengan wireFormat=false harus membuat lookup
// wire-format gagal — membuktikan tes di atas diskriminatif.
func TestCDBPlainKeysAreNotWireCompatible(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "domains.txt")
	cdbPath := filepath.Join(dir, "plain.db")
	if err := os.WriteFile(raw, []byte("evil.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileDomainsToCDB(raw, cdbPath, false); err != nil {
		t.Fatal(err)
	}
	db, err := cdb.Open(cdbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	buf := make([]byte, 256)
	off, err := dns.PackDomainName("evil.example.", buf, 0, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	wire := buf[:off]
	value, err := db.Get(wire)
	if err != nil {
		t.Fatal(err)
	}
	if value != nil {
		t.Fatal("CDB plain-text cocok dengan lookup wire-format; tes utama tidak diskriminatif")
	}
}
