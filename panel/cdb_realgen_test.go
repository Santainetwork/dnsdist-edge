package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCDBContainsDomainRealGenerator checks the panel's CDB lookup against bytes
// produced by the actual tools/gen-cdb.py. A hand-built fixture would not catch
// a divergence between panel and generator, which is exactly the defect class
// this endpoint must avoid.
func TestCDBContainsDomainRealGenerator(t *testing.T) {
	gen := filepath.Join("..", "tools", "gen-cdb.py")
	if _, err := os.Stat(gen); err != nil {
		t.Skip("gen-cdb.py not present")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	dbPath := filepath.Join(t.TempDir(), "trust.db")
	cmd := exec.Command("python3", gen, dbPath, "evil.com", "blocked.domain")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gen-cdb.py failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read generated db: %v", err)
	}

	for _, dom := range []string{"evil.com", "blocked.domain"} {
		if !cdbContainsDomain(data, dom) {
			t.Errorf("%s should be found (generator included it)", dom)
		}
	}
	for _, dom := range []string{"safe.example", "notpresent.org", "evil.com.evil.com"} {
		if cdbContainsDomain(data, dom) {
			t.Errorf("%s reported as present but was never generated", dom)
		}
	}
	if cdbContainsDomain(nil, "evil.com") {
		t.Error("nil input must not match")
	}
	if cdbContainsDomain(data[:100], "evil.com") {
		t.Error("truncated input must not match")
	}
}
