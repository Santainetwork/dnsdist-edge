package main

import (
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

// TestGenCDBPyProducesPlainKeysDocumentsIncompatibility documents a real defect in
// the Python generator rather than asserting its output works.
//
// tools/gen-cdb.py writes PLAIN TEXT keys ("evil.com."). dnsdist looks up WIRE
// format keys, so a CDB from gen-cdb.py never matches and silently fails to block.
// The panel lookup must NOT match such a database, because doing so would mean the
// panel disagrees with dnsdist about what is blocked.
//
// The Go replacement (tools/gen-cdb-go) writes wire format and is the supported
// path; this test pins the incompatibility so it cannot be forgotten.
func TestGenCDBPyProducesPlainKeysDocumentsIncompatibility(t *testing.T) {
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

	klen := le32(data, 2048)
	if klen == 0 || int(2048+8+klen) > len(data) {
		t.Fatalf("unexpected layout: klen=%d", klen)
	}
	key := data[2056 : 2056+klen]
	if string(key) != "evil.com." {
		t.Fatalf("expected gen-cdb.py to write the plain key %q, got %q", "evil.com.", key)
	}
	t.Logf("confirmed: gen-cdb.py writes PLAIN key %q, which dnsdist's wire lookup cannot match", key)

	// The panel agrees with dnsdist, so it must NOT find this domain.
	if cdbContainsDomain(data, "evil.com") {
		t.Error("panel matched a plain-text-key CDB; it would disagree with dnsdist about blocking")
	}
}
