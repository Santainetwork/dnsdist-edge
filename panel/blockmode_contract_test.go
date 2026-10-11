package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Kontrak block_mode: hanya "adguard" dan "rpz" yang ditulis ke dnsdist.conf.
// Nilai lain (termasuk payload Lua) tidak boleh masuk ke literal BLOCK_MODE.
func TestBlockModeRejectsLuaPayload(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "dnsdist.conf")
	if err := os.WriteFile(conf, []byte("BLOCK_MODE = 'rpz'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prevConf := *flagConf
	*flagConf = conf
	t.Cleanup(func() { *flagConf = prevConf })
	prevRestart := restartDnsdistFn
	restartDnsdistFn = func() error { return nil }
	t.Cleanup(func() { restartDnsdistFn = prevRestart })
	prevSecret := *flagSecret
	*flagSecret = filepath.Join(dir, "secret")
	t.Cleanup(func() { *flagSecret = prevSecret })

	payload := `x'; os.execute('touch /tmp/pwned'); BLOCK_MODE = 'y`
	body := `{"block_mode":` + jsonQuote(payload) + `}`
	rr := httptest.NewRecorder()
	handleSettings(rr, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body)))

	b, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "os.execute") {
		t.Fatalf("payload masuk ke dnsdist.conf: %s", b)
	}
	if !strings.Contains(string(b), "BLOCK_MODE = 'rpz'") {
		t.Fatalf("nilai lama berubah untuk input tak valid: %s", b)
	}
}
