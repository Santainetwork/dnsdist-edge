package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Kontrak handleRPZ (--set-rpz): IP tak valid ditolak sebelum setup-edge.sh
// dipanggil; IP valid diteruskan sebagai satu argumen dipisah koma.
func TestRPZContract(t *testing.T) {
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

	post := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		handleRPZ(rr, httptest.NewRequest(http.MethodPost, "/api/rpz", strings.NewReader(body)))
		return rr
	}

	for _, bad := range []string{`{"ips":["1.1.1.1;id"]}`, `{"ips":["$(id)"]}`, `{"ips":[]}`} {
		if rr := post(bad); rr.Code == http.StatusOK {
			t.Errorf("body %s diterima, harus ditolak", bad)
		}
	}
	if _, err := os.Stat(argLog); err == nil {
		t.Fatal("setup-edge.sh dipanggil untuk input tak valid")
	}

	rr := post(`{"ips":["10.0.0.1"," ","2001:db8::1"]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("input valid ditolak: %d %s", rr.Code, rr.Body.String())
	}
	b, err := os.ReadFile(argLog)
	if err != nil {
		t.Fatalf("setup-edge.sh tidak dipanggil: %v", err)
	}
	if !strings.Contains(string(b), "10.0.0.1,2001:db8::1") {
		t.Fatalf("argumen = %q, want 10.0.0.1,2001:db8::1 (spasi kosong dibuang)", b)
	}
}
