package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// resetAuthEnv gives a test an isolated panel.secret/panel.password pair so
// login-handler tests never touch real state.
func resetAuthEnv(t *testing.T, dir string) {
	t.Helper()
	oldSecret := *flagSecret
	*flagSecret = filepath.Join(dir, "panel.secret")
	if err := os.WriteFile(filepath.Join(dir, "panel.password"), []byte("trust-ng-admin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { *flagSecret = oldSecret })
}

func strBody(s string) *bytes.Reader {
	return bytes.NewReader([]byte(s))
}
