package main

import (
	"path/filepath"
	"testing"
)

// PRD §10: dua profil berbeda aktif bersamaan di dua kelompok node, dan
// mengubah profil A tidak mengubah node yang memakai B.
func TestTwoProfilesIsolated(t *testing.T) {
	st := newSmartDistProfileStore(filepath.Join(t.TempDir(), "p.json"))
	docA := `{"id":"cfg-a","name":"A","smartdist":{"enabled":true,"rules":[{"type":"alias","ip_set":"cf","targets":["9.9.9.9"]}]}}`
	docB := `{"id":"cfg-b","name":"B","smartdist":{"enabled":true,"rules":[{"type":"alias","ip_set":"cf","targets":["1.1.1.1"]}]}}`
	sumA, err := st.Put([]byte(docA))
	if err != nil {
		t.Fatalf("put A: %v", err)
	}
	sumB, err := st.Put([]byte(docB))
	if err != nil {
		t.Fatalf("put B: %v", err)
	}
	if sumA.Hash == sumB.Hash {
		t.Fatal("profil A dan B hash sama")
	}
	if err := st.AssignMany([]string{"n1", "n2"}, "cfg-a"); err != nil {
		t.Fatalf("assign A: %v", err)
	}
	if err := st.AssignMany([]string{"n3", "n4"}, "cfg-b"); err != nil {
		t.Fatalf("assign B: %v", err)
	}
	check := func(node, wantPID, wantHash string) {
		t.Helper()
		pid, hash, ok := st.ProfileForNode(node)
		if !ok || pid != wantPID || hash != wantHash {
			t.Fatalf("%s = (%q,%q,%v), want (%q,%q,true)", node, pid, hash, ok, wantPID, wantHash)
		}
	}
	check("n1", "cfg-a", sumA.Hash)
	check("n3", "cfg-b", sumB.Hash)

	// Ubah isi profil A (versi baru). Node B harus tetap pada hash B.
	docA2 := `{"id":"cfg-a","name":"A2","smartdist":{"enabled":true,"rules":[]}}`
	sumA2, err := st.Put([]byte(docA2))
	if err != nil {
		t.Fatalf("update A: %v", err)
	}
	check("n1", "cfg-a", sumA2.Hash)
	check("n2", "cfg-a", sumA2.Hash)
	check("n3", "cfg-b", sumB.Hash)
	check("n4", "cfg-b", sumB.Hash)
}
