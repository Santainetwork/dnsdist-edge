package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestPanelCDBAgreesWithDnsdist is the authoritative regression guard for the
// bug the operator reported: /api/rpz/test answering "not found" for a domain
// that dnsdist was actively blocking.
//
// The CDB is produced by the repo's own Go generator (tools/gen-cdb-go), which
// uses the same library and layout as tools/trust-builder. That output was
// confirmed to BLOCK in a real dnsdist 1.9.16 container. The panel must reach
// the same verdict, so this test ties the panel's lookup to the artifact that
// dnsdist actually consumes.
//
// The earlier implementation selected the wrong index byte ((hash>>8)&0xff
// instead of hash&0xff) and treated the header's second field as a byte count
// instead of a slot count, so it disagreed with dnsdist on every real database.
func TestPanelCDBAgreesWithDnsdist(t *testing.T) {
	genDir := filepath.Join("..", "tools", "gen-cdb-go")
	if _, err := os.Stat(genDir); err != nil {
		t.Skip("tools/gen-cdb-go not present")
	}

	bin := filepath.Join(t.TempDir(), "gen-cdb-go")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = genDir
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build gen-cdb-go: %v\n%s", err, out)
	}

	domains := []string{"blocked.test", "pornhub.com", "evil.example"}
	dbPath := filepath.Join(t.TempDir(), "trust.db")
	args := append([]string{dbPath}, domains...)
	if out, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		t.Fatalf("gen-cdb-go failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}

	// Every generated name must be found: dnsdist blocks all of them.
	for _, d := range domains {
		if !cdbContainsDomain(data, d) {
			t.Errorf("%s: dnsdist blocks this name but panel reports NOT FOUND", d)
		}
	}
	// Names that were never added must not be reported as blocked.
	for _, d := range []string{"random.test", "notpresent.example", "pornhub.com.evil.example"} {
		if cdbContainsDomain(data, d) {
			t.Errorf("%s: false positive (never added to the CDB)", d)
		}
	}
	// Defensive: malformed input must not panic or report a hit.
	if cdbContainsDomain(nil, "blocked.test") {
		t.Error("nil input must not match")
	}
	if cdbContainsDomain(data[:100], "blocked.test") {
		t.Error("truncated input must not match")
	}
	if cdbContainsDomain(make([]byte, 2048), "blocked.test") {
		t.Error("all-zero index must not match")
	}
}
