package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PRD §10: saat panel kembali, node sinkron ulang di heartbeat berikutnya.
// Node menerapkan H1; panel mati; panel kembali dengan profil H2; heartbeat
// berikutnya membawa H2, agent menerapkannya, dan panel menilai "synced".
func TestPanelRecoversAndNodeResyncs(t *testing.T) {
	tmp := t.TempDir()
	cs := newClusterStore(filepath.Join(tmp, "cluster-nodes.json"))
	prevCS := clusterStore
	clusterStore = cs
	t.Cleanup(func() { clusterStore = prevCS })
	st := newSmartDistProfileStore(filepath.Join(tmp, "smartdist-profiles.json"))
	prevSD := smartDistStoreInst
	setSmartDistStore(st)
	t.Cleanup(func() { setSmartDistStore(prevSD) })

	tok := cs.GenerateEnrollmentToken(time.Hour)
	rec, err := cs.RegisterNode(RegisterRequest{EnrollToken: tok, Name: "edge-rec"}, "10.0.0.11")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	h1, err := st.Put([]byte(smartDistTestDoc))
	if err != nil {
		t.Fatalf("put H1: %v", err)
	}
	if err := st.Assign(rec.ID, "cfg-a"); err != nil {
		t.Fatalf("assign: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cluster/heartbeat", handleClusterHeartbeat)
	mux.HandleFunc("/api/cluster/profile", handleClusterProfileFetch)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

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

	// Siklus 1: heartbeat -> apply H1 -> heartbeat lapor H1 -> synced.
	if err := agent.sendHeartbeat(); err != nil {
		t.Fatalf("hb1: %v", err)
	}
	hb := heartbeatForNode(t, rec.ID, rec.Key)
	if err := agent.syncSmartDistProfile(hb.ProfileID, hb.ProfileHash); err != nil {
		t.Fatalf("apply H1: %v", err)
	}
	if err := agent.sendHeartbeat(); err != nil {
		t.Fatalf("hb2: %v", err)
	}
	if got := syncOf(); got != "synced" {
		t.Fatalf("siklus 1: sync=%q, want synced", got)
	}

	// Panel mati: node tidak bisa menjangkau master. Heartbeat gagal.
	srv.Close()
	if err := agent.sendHeartbeat(); err == nil {
		t.Fatal("heartbeat saat panel mati harus gagal")
	}

	// Panel kembali pada alamat baru: master menugaskan H2 (profil diubah).
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/api/cluster/heartbeat", handleClusterHeartbeat)
	mux2.HandleFunc("/api/cluster/profile", handleClusterProfileFetch)
	srv2 := httptest.NewServer(mux2)
	t.Cleanup(srv2.Close)
	agent.masterURL = srv2.URL
	agent.httpClient = srv2.Client()

	h2doc := strings.Replace(smartDistTestDoc, `"enabled":true`, `"enabled":false`, 1)
	h2, err := st.Put([]byte(h2doc))
	if err != nil {
		t.Fatalf("put H2: %v", err)
	}
	if h2.Hash == h1.Hash {
		t.Fatal("H2 harus berbeda dari H1")
	}

	// Heartbeat pertama setelah panel kembali: panel melihat H1 lama -> pending.
	if err := agent.sendHeartbeat(); err != nil {
		t.Fatalf("hb3: %v", err)
	}
	if got := syncOf(); got != "pending" {
		t.Fatalf("setelah kembali sebelum apply: sync=%q, want pending", got)
	}

	// Heartbeat membawa H2; agent menerapkan; heartbeat berikutnya lapor H2.
	hb2 := heartbeatForNode(t, rec.ID, rec.Key)
	if hb2.ProfileHash != h2.Hash {
		t.Fatalf("heartbeat hash = %q, want H2 %q", hb2.ProfileHash, h2.Hash)
	}
	if err := agent.syncSmartDistProfile(hb2.ProfileID, hb2.ProfileHash); err != nil {
		t.Fatalf("apply H2: %v", err)
	}
	if err := agent.sendHeartbeat(); err != nil {
		t.Fatalf("hb4: %v", err)
	}
	if got := syncOf(); got != "synced" {
		t.Fatalf("setelah resync: sync=%q, want synced", got)
	}
	if _, err := os.Stat(smartDistProfilePath); err != nil {
		t.Fatalf("file profil hilang: %v", err)
	}
}
