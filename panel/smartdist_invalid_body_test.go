package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

// PRD §10: profil tidak valid tidak pernah menggantikan file yang berjalan.
// Kasus: hash body COCOK dengan yang diminta, tetapi isi bukan JSON valid.
// Verifikasi hash lolos, generator Lua gagal -> file lama utuh, hash tetap.
func TestInvalidProfileBodyKeepsRunningFile(t *testing.T) {
	good := smartDistTestProfile
	var hits atomic.Int32
	a := newSmartDistAgent(t, good, &hits)
	// newSmartDistAgent sudah mengarahkan smartDistProfilePath ke TempDir baru;
	// path itu kita pakai untuk seluruh tes ini.
	if err := a.syncSmartDistProfile("cfg-a", smartDistHash(good)); err != nil {
		t.Fatalf("apply sah: %v", err)
	}
	before, err := os.ReadFile(smartDistProfilePath)
	if err != nil {
		t.Fatal(err)
	}
	goodHash := a.state.ProfileHash

	// Master kini melayani body yang bukan JSON. Hash dicocokkan ke body itu,
	// sehingga verifikasi lolos tetapi generator Lua harus gagal.
	bad := "{ini bukan json"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(bad))
	}))
	t.Cleanup(srv.Close)
	a.masterURL = srv.URL
	a.httpClient = srv.Client()

	if err := a.syncSmartDistProfile("cfg-a", smartDistHash(bad)); err == nil {
		t.Fatal("body tidak valid harus error")
	}

	after, err := os.ReadFile(smartDistProfilePath)
	if err != nil {
		t.Fatalf("file hilang: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("file yang berjalan tertimpa oleh profil tidak valid")
	}
	if a.state.ProfileHash != goodHash {
		t.Fatalf("hash berubah ke %q, want tetap %q", a.state.ProfileHash, goodHash)
	}
}
