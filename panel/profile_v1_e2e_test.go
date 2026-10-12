package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// E2E profil v1 dalam proses: master nyata (route + cluster + store) melayani
// profil dengan section dnsdist; agent nyata menarik lewat heartbeat, menerapkan
// SmartDist dan dnsdist-profile.lua, lalu menilai "synced". Gerbang dnsdist
// (check-config, restart) diisolasi karena binary dnsdist tidak ada di tes.
func TestProfileV1EndToEndMasterToDnsdistFile(t *testing.T) {
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
	rec, err := cs.RegisterNode(RegisterRequest{EnrollToken: tok, Name: "edge-v1"}, "10.0.0.20")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	doc := `{"id":"cfg-v1","name":"V1","smartdist":{"enabled":false,"rules":[]},` +
		`"dnsdist":{"block_mode":"adguard","sinkhole_v4":["192.0.2.9"],` +
		`"acl":["10.0.0.0/8"],"ecs_v4":16,"max_qps_per_ip":321,"tcp_max_conns":42}}`
	sum, err := st.Put([]byte(doc))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := st.Assign(rec.ID, "cfg-v1"); err != nil {
		t.Fatalf("assign: %v", err)
	}

	prevSmart := smartDistProfilePath
	smartDistProfilePath = filepath.Join(tmp, "smartdist-profile.lua")
	t.Cleanup(func() { smartDistProfilePath = prevSmart })
	dnsPath := filepath.Join(tmp, "dnsdist-profile.lua")
	prevDn := dnsdistProfilePath
	dnsdistProfilePath = dnsPath
	t.Cleanup(func() { dnsdistProfilePath = prevDn })

	var restarts atomic.Int32
	prevRestart := restartDnsdistFn
	restartDnsdistFn = func() error { restarts.Add(1); return nil }
	t.Cleanup(func() { restartDnsdistFn = prevRestart })
	prevCheck := checkDnsdistConfigFn
	checkDnsdistConfigFn = func(string) error { return nil }
	t.Cleanup(func() { checkDnsdistConfigFn = prevCheck })

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

	if err := agent.sendHeartbeat(); err != nil {
		t.Fatalf("hb1: %v", err)
	}
	if got := syncOf(); got != "pending" {
		t.Fatalf("sebelum apply: sync=%q, want pending", got)
	}

	hb := heartbeatForNode(t, rec.ID, rec.Key)
	if hb.ProfileHash != sum.Hash {
		t.Fatalf("hash heartbeat = %q, want %q", hb.ProfileHash, sum.Hash)
	}
	if err := agent.syncSmartDistProfile(hb.ProfileID, hb.ProfileHash); err != nil {
		t.Fatalf("sync: %v", err)
	}

	b, err := os.ReadFile(dnsPath)
	if err != nil {
		t.Fatalf("dnsdist-profile.lua tidak ditulis: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		"PROFILE_ENABLED = true",
		`PROFILE_BLOCK_MODE = "adguard"`,
		`PROFILE_SINKHOLE_V4 = {"192.0.2.9"}`,
		`PROFILE_ACL = {"10.0.0.0/8"}`,
		"PROFILE_ECS_V4 = 16",
		"PROFILE_MAX_QPS_PER_IP = 321",
		"PROFILE_TCP_MAX_CONNS = 42",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("dnsdist-profile.lua tidak memuat %q\n%s", want, s)
		}
	}
	if restarts.Load() != 1 {
		t.Errorf("restart = %d, want 1 (isi berubah)", restarts.Load())
	}

	if err := agent.sendHeartbeat(); err != nil {
		t.Fatalf("hb2: %v", err)
	}
	if got := syncOf(); got != "synced" {
		t.Fatalf("setelah apply: sync=%q, want synced", got)
	}
}
