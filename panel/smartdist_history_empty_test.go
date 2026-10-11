package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Node tanpa riwayat harus mendapat "history":[] (bukan null), supaya klien
// yang memanggil .map/.length tidak perlu menebak.
func TestHistoryEmptyIsArrayNotNull(t *testing.T) {
	prevSD := smartDistStoreInst
	setSmartDistStore(newSmartDistProfileStore(filepath.Join(t.TempDir(), "p.json")))
	t.Cleanup(func() { setSmartDistStore(prevSD) })

	rr := httptest.NewRecorder()
	handleSmartDistProfiles(rr, httptest.NewRequest(http.MethodGet, "/api/cluster/profiles?history=ghost", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"history":[]`) {
		t.Fatalf("body = %s, want \"history\":[]", rr.Body.String())
	}
}
