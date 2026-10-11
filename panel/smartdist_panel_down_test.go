package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// PRD §10: panel mati -> node tetap berjalan dengan config terakhir yang sah.
// Node sudah menerapkan profil (hash H1). Panel kemudian mati dan menugaskan
// versi baru (H2) yang tidak bisa diambil. File lama HARUS utuh dan hash tetap H1.
func TestPanelDownKeepsLastGoodProfile(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(smartDistTestProfile))
	}))
	good := smartDistHash(smartDistTestProfile)

	prevPath := smartDistProfilePath
	smartDistProfilePath = filepath.Join(t.TempDir(), "smartdist-profile.lua")
	t.Cleanup(func() { smartDistProfilePath = prevPath })

	a := &EdgeClusterAgent{
		masterURL:  srv.URL,
		httpClient: srv.Client(),
		state:      EdgeAgentState{NodeID: "n1", NodeKey: "k1"},
	}
	if err := a.syncSmartDistProfile("cfg-a", good); err != nil {
		t.Fatalf("apply H1: %v", err)
	}
	before, err := os.ReadFile(smartDistProfilePath)
	if err != nil {
		t.Fatal(err)
	}

	// Panel mati: setiap GET gagal.
	srv.Close()
	newHash := smartDistHash("profil baru yang tidak bisa diambil")
	if err := a.syncSmartDistProfile("cfg-a", newHash); err == nil {
		t.Fatal("sync saat panel mati harus error")
	}

	after, err := os.ReadFile(smartDistProfilePath)
	if err != nil {
		t.Fatalf("file hilang saat panel mati: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("file last-known-good berubah saat panel mati")
	}
	if a.state.ProfileHash != good {
		t.Fatalf("hash tersimpan = %q, want tetap %q", a.state.ProfileHash, good)
	}
}
