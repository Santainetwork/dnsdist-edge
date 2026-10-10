package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Jalur 2 "all in one": custom blacklist master, diatur dari panel yang sama.
// Dikompilasi ke CDB oleh BuildMasterCDB (flagCustomBLFile).

func TestMasterCustomBlacklistHandler(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom-blacklist.txt")
	old := *flagCustomBLFile
	*flagCustomBLFile = path
	t.Cleanup(func() { *flagCustomBLFile = old })
	oldSrc := *flagSourceMode
	*flagSourceMode = "feeds"
	t.Cleanup(func() { *flagSourceMode = oldSrc })

	rr := httptest.NewRecorder()
	handleMasterCustomBlacklist(rr, httptest.NewRequest(http.MethodPost, "/api/master/custom-blacklist",
		strings.NewReader(`{"domains":["Evil.example.com","evil.example.com.","bad.example.net"]}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST = %d; body=%s", rr.Code, rr.Body.String())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if got := strings.Count(string(b), "\n"); got != 2 {
		t.Errorf("unique lines = %d, want 2; content=%q", got, b)
	}

	rr = httptest.NewRecorder()
	handleMasterCustomBlacklist(rr, httptest.NewRequest(http.MethodPost, "/api/master/custom-blacklist",
		strings.NewReader(`{"domains":["ok.example.org","bad space"]}`)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid POST = %d, want 400", rr.Code)
	}
	b2, _ := os.ReadFile(path)
	if string(b2) != string(b) {
		t.Errorf("file changed after invalid POST")
	}

	rr = httptest.NewRecorder()
	handleMasterCustomBlacklist(rr, httptest.NewRequest(http.MethodGet, "/api/master/custom-blacklist", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "evil.example.com") {
		t.Errorf("GET = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMasterCustomBlacklistRejectedInRPZSlave(t *testing.T) {
	old := *flagSourceMode
	*flagSourceMode = "rpz-slave"
	t.Cleanup(func() { *flagSourceMode = old })

	rr := httptest.NewRecorder()
	handleMasterCustomBlacklist(rr, httptest.NewRequest(http.MethodPost, "/api/master/custom-blacklist",
		strings.NewReader(`{"domains":["x.example.com"]}`)))
	if rr.Code != http.StatusConflict {
		t.Errorf("rpz-slave POST = %d, want 409", rr.Code)
	}
}
