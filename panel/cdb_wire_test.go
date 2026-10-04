package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCDBWireFormatKeys is the regression guard for the user-reported bug where
// /api/rpz/test always answered "not found" for domains dnsdist was blocking.
//
// Root cause: cdbContainsDomain searched for the plain text key "domain." while
// dnsdist looks up with KeyValueLookupKeyQName(true), i.e. DNS wire format, and
// tools/trust-builder writes keys via dns.PackDomainName (also wire format).
//
// This test builds a CDB with wire-format keys and asserts the panel finds them.
func TestCDBWireFormatKeys(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "trust.db")
	if err := writeTestCDB(db, []string{"pornhub.com", "blocked.test", "evil.com"}); err != nil {
		t.Fatalf("build cdb: %v", err)
	}
	data, err := os.ReadFile(db)
	if err != nil {
		t.Fatalf("read cdb: %v", err)
	}

	for _, dom := range []string{"pornhub.com", "blocked.test", "evil.com"} {
		if !cdbContainsDomain(data, dom) {
			t.Errorf("%s: expected FOUND (wire-format key present)", dom)
		}
	}
	for _, dom := range []string{"nothere.example", "pornhub.com.evil.com"} {
		if cdbContainsDomain(data, dom) {
			t.Errorf("%s: expected NOT FOUND", dom)
		}
	}
}

// TestWireDomainKeyEncoding pins the exact wire encoding dnsdist expects.
func TestWireDomainKeyEncoding(t *testing.T) {
	cases := map[string]string{
		"pornhub.com":  "\x07pornhub\x03com\x00",
		"a.b.c":        "\x01a\x01b\x01c\x00",
		"Example.COM.": "\x07example\x03com\x00",
		"single":       "\x06single\x00",
	}
	for in, want := range cases {
		got, ok := wireDomainKey(in)
		if !ok {
			t.Errorf("wireDomainKey(%q) rejected valid input", in)
			continue
		}
		if string(got) != want {
			t.Errorf("wireDomainKey(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", ".", "..", "a..b"} {
		if _, ok := wireDomainKey(bad); ok {
			t.Errorf("wireDomainKey(%q) should be rejected", bad)
		}
	}
	// Label longer than 63 must be rejected.
	long := make([]byte, 64)
	for i := range long {
		long[i] = 'a'
	}
	if _, ok := wireDomainKey(string(long) + ".com"); ok {
		t.Error("label > 63 bytes must be rejected")
	}
}
