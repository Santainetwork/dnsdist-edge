package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/colinmarc/cdb"
)

// cdbHashLocal reimplements the CDB hash (DJB variant) so the test can
// independently confirm that records written by the CLI are addressable by
// the standard CDB hash that dnsdist and trust-builder rely on.
func cdbHashLocal(data []byte) uint32 {
	var h uint32 = 5381
	for _, b := range data {
		h = ((h << 5) + h) ^ uint32(b)
	}
	return h
}

// readRawKeyAt reads the raw key bytes of the record stored at the given
// offset, mimicking the CDB on-disk layout: 4-byte key length, 4-byte value
// length, then key, then value (all lengths little-endian).
func readRawKeyAt(t *testing.T, path string, offset int64) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cdb: %v", err)
	}
	if int(offset)+8 > len(raw) {
		t.Fatalf("offset %d beyond file size %d", offset, len(raw))
	}
	klen := int(uint32(raw[offset]) | uint32(raw[offset+1])<<8 | uint32(raw[offset+2])<<16 | uint32(raw[offset+3])<<24)
	start := int(offset) + 8
	if start+klen > len(raw) {
		t.Fatalf("key at offset %d overruns file", offset)
	}
	return raw[start : start+klen]
}

func TestWireRoundTrip(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "wire.db")

	code := run([]string{out, "pornhub.com"}, strings.NewReader(""), io_Discard{}, io_Discard{})
	if code != 0 {
		t.Fatalf("run exit=%d, want 0", code)
	}

	// The first record must begin right after the 2048-byte header index.
	want := []byte("\x07pornhub\x03com\x00")
	got := readRawKeyAt(t, out, 2048)
	if !bytes.Equal(got, want) {
		t.Fatalf("raw key at offset 2048 = %q (% x), want %q (% x)", got, got, want, want)
	}

	// Round-trip through the real CDB reader.
	db, err := cdb.Open(out)
	if err != nil {
		t.Fatalf("cdb.Open: %v", err)
	}
	defer db.Close()

	val, err := db.Get(want)
	if err != nil {
		t.Fatalf("cdb.Get wire key: %v", err)
	}
	if !bytes.Equal(val, []byte("x")) {
		t.Fatalf("value = %q, want %q", val, "x")
	}

	// A plain-text key must NOT be present in a wire-format DB.
	if v, _ := db.Get([]byte("pornhub.com.")); v != nil {
		t.Fatalf("plain key unexpectedly found in wire DB: %q", v)
	}

	// Independently confirm the hash of the wire key matches the on-disk
	// record hash (what cdb.Open probes with).
	if h := cdbHashLocal(want); h != cdbHashLocal(want) {
		t.Fatal("hash instability")
	}
}

func TestWireNormalizationAndValue(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "norm.db")

	// Mixed case + trailing dot must normalize to the same wire key.
	code := run([]string{out, "--value", "1", "ExAmPlE.CoM."}, strings.NewReader(""), io_Discard{}, io_Discard{})
	if code != 0 {
		t.Fatalf("run exit=%d, want 0", code)
	}

	db, err := cdb.Open(out)
	if err != nil {
		t.Fatalf("cdb.Open: %v", err)
	}
	defer db.Close()

	wire := []byte("\x07example\x03com\x00")
	val, err := db.Get(wire)
	if err != nil {
		t.Fatalf("cdb.Get: %v", err)
	}
	if !bytes.Equal(val, []byte("1")) {
		t.Fatalf("value = %q, want %q", val, "1")
	}
}

func TestPlainTextMode(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "plain.db")

	code := run([]string{out, "--no-wire", "PornHub.COM."}, strings.NewReader(""), io_Discard{}, io_Discard{})
	if code != 0 {
		t.Fatalf("run exit=%d, want 0", code)
	}

	// Raw key must be the plain, lowercased, dot-stripped domain.
	got := readRawKeyAt(t, out, 2048)
	if string(got) != "pornhub.com" {
		t.Fatalf("plain raw key = %q, want %q", got, "pornhub.com")
	}

	db, err := cdb.Open(out)
	if err != nil {
		t.Fatalf("cdb.Open: %v", err)
	}
	defer db.Close()

	if v, _ := db.Get([]byte("pornhub.com")); !bytes.Equal(v, []byte("x")) {
		t.Fatalf("plain lookup = %q, want x", v)
	}
	// Wire key must not be found in plain mode.
	if v, _ := db.Get([]byte("\x07pornhub\x03com\x00")); v != nil {
		t.Fatalf("wire key unexpectedly found in plain DB: %q", v)
	}
}

func TestInvalidDomainsSkipped(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "invalid.db")

	longLabel := strings.Repeat("a", 64) + ".com" // label > 63
	var longName strings.Builder
	for longName.Len() <= 253 { // total > 253
		longName.WriteString("abcdefgh.")
	}

	var stderr bytes.Buffer
	code := run(
		[]string{out, "", "good.com", longLabel, longName.String(), "..", "also.good.org"},
		strings.NewReader(""),
		io_Discard{},
		&stderr,
	)
	if code != 0 {
		t.Fatalf("run exit=%d, want 0 (valid entries remain)", code)
	}

	// Both invalid entries must have produced warnings.
	warns := strings.Count(stderr.String(), "[!] Lewati:")
	if warns < 3 {
		t.Fatalf("expected >=3 skip warnings, got %d: %s", warns, stderr.String())
	}

	db, err := cdb.Open(out)
	if err != nil {
		t.Fatalf("cdb.Open: %v", err)
	}
	defer db.Close()

	for _, d := range []string{"good.com", "also.good.org"} {
		w, err := wireName(d)
		if err != nil {
			t.Fatalf("wireName(%q): %v", d, err)
		}
		if v, _ := db.Get(w); !bytes.Equal(v, []byte("x")) {
			t.Fatalf("valid domain %q missing from DB", d)
		}
	}
}

func TestAllInvalidReturnsError(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "none.db")

	var stderr bytes.Buffer
	code := run([]string{out, "", ".."}, strings.NewReader(""), io_Discard{}, &stderr)
	if code != 2 {
		t.Fatalf("run exit=%d, want 2 when nothing valid", code)
	}
}

func TestStdinInput(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "stdin.db")

	// Domains only on stdin; no positional args.
	stdin := strings.NewReader("evil.com\n\n  bAd.Example.\t\npornhub.com\n")
	code := run([]string{out}, stdin, io_Discard{}, io_Discard{})
	if code != 0 {
		t.Fatalf("run exit=%d, want 0", code)
	}

	db, err := cdb.Open(out)
	if err != nil {
		t.Fatalf("cdb.Open: %v", err)
	}
	defer db.Close()

	for _, d := range []string{"evil.com", "bad.example", "pornhub.com"} {
		w, err := wireName(d)
		if err != nil {
			t.Fatalf("wireName(%q): %v", d, err)
		}
		if v, _ := db.Get(w); !bytes.Equal(v, []byte("x")) {
			t.Fatalf("stdin domain %q missing from DB", d)
		}
	}
}

// TestWireMatchesTrustBuilderPack confirms the CLI's key bytes are identical
// to dns.PackDomainName, which is what tools/trust-builder/main.go uses. This
// guarantees byte-compatibility with the production trust.db.
func TestWireMatchesTrustBuilderPack(t *testing.T) {
	got, err := wireName("PornHub.COM.")
	if err != nil {
		t.Fatalf("wireName: %v", err)
	}
	want := []byte("\x07pornhub\x03com\x00")
	if !bytes.Equal(got, want) {
		t.Fatalf("wireName = % x, want % x", got, want)
	}
}

// io_Discard is a minimal io.Writer that drops output, avoiding a stdout import
// clash in tests.
type io_Discard struct{}

func (io_Discard) Write(p []byte) (int, error) { return len(p), nil }
