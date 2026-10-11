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

// F5.2 end-to-end: profil ditugaskan -> node lapor applied hash lama (pending)
// -> node lapor hash baru (synced) -> ListNodes menampilkan status yang benar.
func TestProfileSyncStateEndToEnd(t *testing.T) {
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
	if err := st.Assign(rec.ID, "cfg-a"); err != nil {
		t.Fatalf("assign: %v", err)
	}

	send := func(applied string) {
		body, _ := json.Marshal(HeartbeatRequest{NodeID: rec.ID, NodeKey: rec.Key, AppliedProfileHash: applied})
		req := httptest.NewRequest(http.MethodPost, "/api/cluster/heartbeat", strings.NewReader(string(body)))
		rr := httptest.NewRecorder()
		handleClusterHeartbeat(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("heartbeat %d: %s", rr.Code, rr.Body.String())
		}
	}
	syncOf := func() string {
		nodes, _ := cs.ListNodes()
		for _, n := range nodes {
			if n.ID == rec.ID {
				return n.ProfileSync
			}
		}
		t.Fatalf("node %s tidak ada di ListNodes", rec.ID)
		return ""
	}

	send("")
	if got := syncOf(); got != "pending" {
		t.Fatalf("belum terapkan: sync=%q, want pending", got)
	}
	send(strings.Repeat("0", 64))
	if got := syncOf(); got != "pending" {
		t.Fatalf("hash salah: sync=%q, want pending", got)
	}
	send(sum.Hash)
	if got := syncOf(); got != "synced" {
		t.Fatalf("hash cocok: sync=%q, want synced", got)
	}
}
