package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Heartbeat harus membawa profile_id + profile_hash ke node. Tanpa test ini,
// konsumen di sendHeartbeat tidak pernah menerima nilai (gap yang dicatat squid).
func TestHeartbeatCarriesAssignedSmartDistProfile(t *testing.T) {
	tmp := t.TempDir()
	cs := newClusterStore(filepath.Join(tmp, "cluster-nodes.json"))
	prevCS, prevSD := clusterStore, smartDistStoreInst
	clusterStore = cs
	t.Cleanup(func() { clusterStore = prevCS })
	st := newSmartDistProfileStore(filepath.Join(tmp, "smartdist-profiles.json"))
	setSmartDistStore(st)
	t.Cleanup(func() { setSmartDistStore(prevSD) })

	tok := cs.GenerateEnrollmentToken(time.Hour)
	rec, err := cs.RegisterNode(RegisterRequest{EnrollToken: tok, Name: "edge-1"}, "10.0.0.5")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	sum, err := st.Put([]byte(smartDistTestDoc))
	if err != nil {
		t.Fatalf("put profile: %v", err)
	}

	hb := func() HeartbeatResponse {
		body, _ := json.Marshal(HeartbeatRequest{NodeID: rec.ID, NodeKey: rec.Key})
		req := httptest.NewRequest(http.MethodPost, "/api/cluster/heartbeat", strings.NewReader(string(body)))
		rr := httptest.NewRecorder()
		handleClusterHeartbeat(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("heartbeat status %d: %s", rr.Code, rr.Body.String())
		}
		var out HeartbeatResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	if out := hb(); out.ProfileID != "" || out.ProfileHash != "" {
		t.Fatalf("node tanpa assignment mendapat profil: %+v", out)
	}

	if err := st.Assign(rec.ID, "cfg-a"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	out := hb()
	if out.ProfileID != "cfg-a" || out.ProfileHash != sum.Hash {
		t.Fatalf("heartbeat profil = (%q,%q), want (cfg-a,%q)", out.ProfileID, out.ProfileHash, sum.Hash)
	}
}
