package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Simpan custom blacklist harus memicu build CDB; jika tidak, perubahan
// tidak pernah sampai ke edge. Build dijalankan lewat hook agar tes tidak
// menyentuh sumber feed sungguhan.
func TestMasterCustomBlacklistTriggersBuild(t *testing.T) {
	old := *flagCustomBLFile
	*flagCustomBLFile = filepath.Join(t.TempDir(), "custom.txt")
	t.Cleanup(func() { *flagCustomBLFile = old })
	oldSrc := *flagSourceMode
	*flagSourceMode = "feeds"
	t.Cleanup(func() { *flagSourceMode = oldSrc })

	done := make(chan struct{}, 1)
	prev := masterBuildFn
	masterBuildFn = func() { done <- struct{}{} }
	t.Cleanup(func() { masterBuildFn = prev })

	rr := httptest.NewRecorder()
	handleMasterCustomBlacklist(rr, httptest.NewRequest(http.MethodPost, "/api/master/custom-blacklist",
		strings.NewReader(`{"domains":["evil.example.com"]}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST = %d %s", rr.Code, rr.Body.String())
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("save did not trigger a CDB build")
	}
}
