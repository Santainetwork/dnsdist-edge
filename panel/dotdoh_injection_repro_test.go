package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Kontrak DoT/DoH: cert/key dari body dievaluasi sebagai literal Lua oleh
// dnsdist. Payload yang keluar dari literal harus ditolak (400) sebelum
// dotdoh.conf ditulis. Path absolut yang aman tetap diterima.
func TestDoTDoHRejectsLuaInjection(t *testing.T) {
	dir := t.TempDir()
	prevConf := *flagConf
	*flagConf = filepath.Join(dir, "dnsdist.conf")
	t.Cleanup(func() { *flagConf = prevConf })
	prevRestart := restartDnsdistFn
	restartDnsdistFn = func() error { return nil }
	t.Cleanup(func() { restartDnsdistFn = prevRestart })
	outFile := filepath.Join(dir, "dotdoh.conf")

	post := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		handleDoTDoH(rr, httptest.NewRequest(http.MethodPost, "/api/dotdoh", strings.NewReader(body)))
		return rr
	}

	payloads := []string{
		`/x.crt', '/x.key'); os.execute('touch /tmp/pwned'); addTLSLocal('a', '/y`,
		`/c.crt'`,
		"relatif/cert.crt",
		"/a/../../etc/passwd",
		"/x\nos.execute('id')",
	}
	for _, p := range payloads {
		body := `{"dot":true,"cert":` + jsonQuote(p) + `,"key":"/k.key"}`
		if rr := post(body); rr.Code != http.StatusBadRequest {
			t.Errorf("cert %q: status %d, want 400", p, rr.Code)
		}
	}
	if _, err := os.Stat(outFile); err == nil {
		b, _ := os.ReadFile(outFile)
		t.Fatalf("dotdoh.conf ditulis untuk input tak valid: %s", b)
	}

	rr := post(`{"dot":true,"cert":"/etc/dnsdist/certs/a.crt","key":"/etc/dnsdist/certs/a.key"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("path aman ditolak: %d %s", rr.Code, rr.Body.String())
	}
	b, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("dotdoh.conf tidak ditulis: %v", err)
	}
	if !strings.Contains(string(b), "/etc/dnsdist/certs/a.crt") {
		t.Fatalf("cert aman tidak ada di dotdoh.conf: %s", b)
	}
}

func jsonQuote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
