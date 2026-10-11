package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Input server upstream dari HTTP body harus divalidasi sebelum dikirim ke
// setup-edge.sh. Karakter yang bukan IP/host/port tidak boleh lolos.
func TestUpstreamRejectsInvalidServers(t *testing.T) {
	dir := t.TempDir()
	argLog := filepath.Join(dir, "args.txt")
	fake := filepath.Join(dir, "setup-edge.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argLog + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := *flagSetupSh
	*flagSetupSh = fake
	t.Cleanup(func() { *flagSetupSh = prev })

	bad := []string{"1.1.1.1;touch /tmp/pwned", "$(id)", "8.8.8.8`id`"}
	for _, s := range bad {
		body := `{"servers":["` + strings.ReplaceAll(s, `"`, `\"`) + `"]}`
		req := httptest.NewRequest(http.MethodPost, "/api/upstream", strings.NewReader(body))
		rr := httptest.NewRecorder()
		handleUpstream(rr, req)
		if rr.Code == http.StatusOK {
			t.Errorf("server %q diterima, harus ditolak", s)
		}
	}
	if _, err := os.Stat(argLog); err == nil {
		b, _ := os.ReadFile(argLog)
		t.Fatalf("setup-edge.sh dipanggil dengan input tak valid: %q", b)
	}
}

// Server IP valid (dengan dan tanpa port) tetap diteruskan ke setup-edge.sh.
func TestUpstreamAcceptsValidServers(t *testing.T) {
	dir := t.TempDir()
	argLog := filepath.Join(dir, "args.txt")
	fake := filepath.Join(dir, "setup-edge.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argLog + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := *flagSetupSh
	*flagSetupSh = fake
	t.Cleanup(func() { *flagSetupSh = prev })

	req := httptest.NewRequest(http.MethodPost, "/api/upstream", strings.NewReader(`{"servers":["1.1.1.1","8.8.4.4:53"]}`))
	rr := httptest.NewRecorder()
	handleUpstream(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("server valid ditolak: %d %s", rr.Code, rr.Body.String())
	}
	b, err := os.ReadFile(argLog)
	if err != nil {
		t.Fatalf("setup-edge.sh tidak dipanggil: %v", err)
	}
	if !strings.Contains(string(b), "1.1.1.1,8.8.4.4:53") {
		t.Fatalf("argumen = %q, want 1.1.1.1,8.8.4.4:53", b)
	}
}
