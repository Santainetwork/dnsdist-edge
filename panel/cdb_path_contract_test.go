package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Kontrak resolveCDBPath: nama CDB harus lolos whitelist, dan symlink di dalam
// direktori publish tidak boleh menunjuk keluar. Melanggar ini = baca file
// sembarang lewat /cdb/blacklist.db (http.ServeFile mengikuti symlink).
func TestResolveCDBPathBlocksTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	pub := filepath.Join(root, "publish")
	if err := os.MkdirAll(pub, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	// trust.db (nama sah) adalah symlink keluar: hanya EvalSymlinks yang bisa menolaknya.
	if err := os.Symlink(secret, filepath.Join(pub, "trust.db")); err != nil {
		t.Fatal(err)
	}

	prev := *flagFilesDir
	*flagFilesDir = pub
	t.Cleanup(func() { *flagFilesDir = prev })

	for _, name := range []string{"trust.db", "../secret.txt", "/etc/passwd", "a/../trust.db", ".."} {
		if p, err := resolveCDBPath(name); err == nil {
			t.Errorf("harus ditolak: %q -> %q", name, p)
		}
	}
}
