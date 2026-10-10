package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Perubahan blokir lokal harus berlaku: dnsdist hanya membaca config saat start,
// jadi handler wajib memicu restart setelah tulis berhasil (pola safesearch).
func TestLocalBlockTriggersDnsdistRestart(t *testing.T) {
	prev := localBlock
	localBlock = newLocalBlockStore(filepath.Join(t.TempDir(), "lb.txt"))
	t.Cleanup(func() { localBlock = prev })

	calls := 0
	prevRestart := restartDnsdistFn
	restartDnsdistFn = func() error { calls++; return nil }
	t.Cleanup(func() { restartDnsdistFn = prevRestart })

	rr := httptest.NewRecorder()
	handleLocalBlock(rr, httptest.NewRequest(http.MethodPost, "/api/localblock", strings.NewReader(`{"domains":["x.example.com"]}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST = %d %s", rr.Code, rr.Body.String())
	}
	if calls != 1 {
		t.Errorf("restart calls after add = %d, want 1", calls)
	}

	// Validasi gagal -> tidak ada tulis, tidak ada restart.
	rr = httptest.NewRecorder()
	handleLocalBlock(rr, httptest.NewRequest(http.MethodPost, "/api/localblock", strings.NewReader(`{"domains":["bad space"]}`)))
	if calls != 1 {
		t.Errorf("restart after invalid input = %d total, want still 1", calls)
	}
}
