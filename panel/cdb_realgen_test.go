package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCDBLookupMatchesTrustBuilderFormat is the authoritative parity test for the
// panel's CDB lookup.
//
// The production CDB (trust.db) is built by tools/trust-builder, which writes keys
// with dns.PackDomainName: DNS WIRE format, e.g. "pornhub.com" ->
// \x07pornhub\x03com\x00. dnsdist looks them up with
// KeyValueLookupKeyQName(true), also wire format.
//
// The panel must therefore encode wire format too. This was previously wrong: it
// searched for the plain text "domain.", so /api/rpz/test always answered
// "not found" for domains dnsdist was correctly blocking.
//
// Verified separately against real dnsdist: a wire-format CDB yields NXDOMAIN
// (blocked) while a plain-text-key CDB does not match at all.
// See validation/cdb-wire-format-proof.md in the run directory.
func TestCDBLookupMatchesTrustBuilderFormat(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trust.db")

	// Build a wire-format CDB using the same encoding trust-builder produces.
	if err := writeTestCDB(dbPath, []string{"evil.com", "blocked.domain", "pornhub.com"}); err != nil {
		t.Fatalf("build cdb: %v", err)
	}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read cdb: %v", err)
	}

	// The record at the first data offset must hold a wire-format key, proving the
	// fixture itself models production and not a plain-text shortcut.
	klen := le32(data, 2048)
	if klen == 0 || int(2048+8+klen) > len(data) {
		t.Fatalf("fixture is malformed: klen=%d", klen)
	}
	firstKey := data[2056 : 2056+klen]
	if len(firstKey) < 2 || firstKey[len(firstKey)-1] != 0 {
		t.Errorf("fixture key %q is not wire format (must end with a zero root label)", firstKey)
	}
	if len(firstKey) >= 2 && firstKey[0] == '.' {
		t.Errorf("fixture key %q looks like plain text, not wire format", firstKey)
	}

	for _, dom := range []string{"evil.com", "blocked.domain", "pornhub.com"} {
		if !cdbContainsDomain(data, dom) {
			t.Errorf("%s should be FOUND (wire-format key present)", dom)
		}
	}
	for _, dom := range []string{"safe.example", "notpresent.org", "evil.com.evil.com"} {
		if cdbContainsDomain(data, dom) {
			t.Errorf("%s reported present but was never added", dom)
		}
	}
	if cdbContainsDomain(nil, "evil.com") {
		t.Error("nil input must not match")
	}
	if cdbContainsDomain(data[:100], "evil.com") {
		t.Error("truncated input must not match")
	}
}

func TestGenCDBPyPreservesDistinctKeysWithHashCollision(t *testing.T) {
	gen := filepath.Join("..", "tools", "gen-cdb.py")
	if _, err := os.Stat(gen); err != nil {
		t.Skip("gen-cdb.py not present")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	// These distinct DNS wire keys have the same DJB CDB hash, 0x29fc79aa.
	domains := []string{"a22.kk51n1by5n.8rm14bba5.vgtb", "lxyflxgmeu41.2yk"}
	first, ok := wireDomainKey(domains[0])
	if !ok {
		t.Fatalf("wireDomainKey rejected %q", domains[0])
	}
	second, ok := wireDomainKey(domains[1])
	if !ok {
		t.Fatalf("wireDomainKey rejected %q", domains[1])
	}
	if string(first) == string(second) {
		t.Fatal("collision fixture keys must differ")
	}
	// Verify that the fixture indeed collides.
	hash := func(key []byte) uint32 {
		h := uint32(5381)
		for _, b := range key {
			h = ((h + (h << 5)) ^ uint32(b))
		}
		return h
	}
	h1 := hash(first)
	h2 := hash(second)
	if h1 != h2 {
		t.Fatalf("fixture domains do not share CDB hash: %08x vs %08x", h1, h2)
	}
	const expectedHash uint32 = 0x29fc79aa
	if h1 != expectedHash {
		t.Fatalf("fixture hash mismatch: got %08x, want %08x", h1, expectedHash)
	}

	dbPath := filepath.Join(t.TempDir(), "collision.db")
	args := append([]string{gen, dbPath}, domains...)
	out, err := exec.Command("python3", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("gen-cdb.py failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	for _, domain := range domains {
		if !cdbContainsDomain(data, domain) {
			t.Errorf("generator lost %q under full-hash collision", domain)
		}
	}
}

// TestGenCDBPyWritesWireKeys pins the Python generator to DNS wire format.
//
// tools/gen-cdb.py previously returned plain text ("evil.com.") from a function
// documented as wire format, so every CDB it produced silently failed to block
// in dnsdist. It now emits real wire format, matching tools/trust-builder and
// tools/gen-cdb-go. This test keeps it that way.
func TestGenCDBPyWritesWireKeys(t *testing.T) {
	gen := filepath.Join("..", "tools", "gen-cdb.py")
	if _, err := os.Stat(gen); err != nil {
		t.Skip("gen-cdb.py not present")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	dbPath := filepath.Join(t.TempDir(), "py.db")
	out, err := exec.Command("python3", gen, dbPath, "evil.com").CombinedOutput()
	if err != nil {
		t.Fatalf("gen-cdb.py failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// The record's key must be the wire encoding of evil.com.
	key, ok := wireDomainKey("evil.com")
	if !ok {
		t.Fatal("wireDomainKey rejected evil.com")
	}
	if string(key) != "\x04evil\x03com\x00" {
		t.Fatalf("unexpected expected-key encoding: %q", key)
	}
	idx := bytes.Index(data, key)
	if idx < 0 {
		t.Fatalf("gen-cdb.py output does not contain the wire key %q", key)
	}
	t.Logf("confirmed: gen-cdb.py writes WIRE key %q", key)

	// And the panel must find it, i.e. agree with dnsdist.
	if !cdbContainsDomain(data, "evil.com") {
		t.Error("panel could not find a name the Python generator wrote")
	}
}
