package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// F5.2 loop tertutup: agent asli menerapkan profil dari heartbeat, lalu
// heartbeat berikutnya (sendHeartbeat sungguhan) melaporkan applied hash dan
// panel menilai node "synced". Sebelum applied hash dilaporkan, status "pending".
func TestProfileSyncClosedLoopRealAgent(t *testing.T) {
	tmp := t.TempDir()
	cs := newClusterStore(filepath.Join(tmp, "cluster-nodes.json"))
	prevCS := clusterStore
	clusterStore = cs
	t.Cleanup(func() { clusterStore = prevCS })
	st := newSmartDistProfileStore(filepath.Join(tmp, "smartdist-profiles.json"))
	prevSD := smartDistStoreInst
	setSmartDistStore(st)
	t.Cleanup(func() { setSmartDistStore(prevSD) })

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cluster/heartbeat", handleClusterHeartbeat)
	mux.HandleFunc("/api/cluster/profile", handleClusterProfileFetch)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tok := cs.GenerateEnrollmentToken(time.Hour)
	rec, err := cs.RegisterNode(RegisterRequest{EnrollToken: tok, Name: "edge-loop"}, "10.0.0.9")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	sum, err := st.Put([]byte(smartDistTestDoc))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := st.Assign(rec.ID, "cfg-a"); err != nil {
		t.Fatalf("assign: %v", err)
	}

	prevPath := smartDistProfilePath
	smartDistProfilePath = filepath.Join(tmp, "smartdist-profile.lua")
	t.Cleanup(func() { smartDistProfilePath = prevPath })

	agent := &EdgeClusterAgent{
		masterURL:  srv.URL,
		httpClient: srv.Client(),
		state:      EdgeAgentState{NodeID: rec.ID, NodeKey: rec.Key},
	}
	syncOf := func() string {
		nodes, _ := cs.ListNodes()
		for _, n := range nodes {
			if n.ID == rec.ID {
				return n.ProfileSync
			}
		}
		t.Fatalf("node hilang")
		return ""
	}

	// Heartbeat pertama: agent belum menerapkan apa pun -> pending.
	if err := agent.sendHeartbeat(); err != nil {
		t.Fatalf("heartbeat 1: %v", err)
	}
	if got := syncOf(); got != "pending" {
		t.Fatalf("sebelum apply: sync=%q, want pending", got)
	}

	// Agent menerapkan profil dari heartbeat (jalur nyata: fetch, verifikasi, tulis).
	hb := heartbeatForNode(t, rec.ID, rec.Key)
	if err := agent.syncSmartDistProfile(hb.ProfileID, hb.ProfileHash); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Heartbeat kedua dari agent sungguhan: harus melapor hash yang diterapkan.
	if err := agent.sendHeartbeat(); err != nil {
		t.Fatalf("heartbeat 2: %v", err)
	}
	if got := syncOf(); got != "synced" {
		t.Fatalf("setelah apply: sync=%q, want synced (applied=%s want=%s)", got, agent.state.ProfileHash, sum.Hash)
	}
}
