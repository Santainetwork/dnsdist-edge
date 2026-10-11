package main

import (
	"path/filepath"
	"testing"
	"time"
)

// F5.1: aggregate harus melaporkan jumlah node synced vs pending.
func TestAggregateCountsSyncedPending(t *testing.T) {
	tmp := t.TempDir()
	cs := newClusterStore(filepath.Join(tmp, "cluster-nodes.json"))
	prevCS, prevSD := clusterStore, smartDistStoreInst
	clusterStore = cs
	t.Cleanup(func() { clusterStore = prevCS })
	st := newSmartDistProfileStore(filepath.Join(tmp, "smartdist-profiles.json"))
	setSmartDistStore(st)
	t.Cleanup(func() { setSmartDistStore(prevSD) })

	tok := cs.GenerateEnrollmentToken(time.Hour)
	n1, _ := cs.RegisterNode(RegisterRequest{EnrollToken: tok, Name: "a"}, "10.0.0.1")
	tok2 := cs.GenerateEnrollmentToken(time.Hour)
	n2, _ := cs.RegisterNode(RegisterRequest{EnrollToken: tok2, Name: "b"}, "10.0.0.2")
	sum, err := st.Put([]byte(smartDistTestDoc))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	// n1 punya profil, n2 tidak.
	if err := st.Assign(n1.ID, "cfg-a"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	_ = cs.ProcessHeartbeat(HeartbeatRequest{NodeID: n1.ID, NodeKey: n1.Key, AppliedProfileHash: sum.Hash}, "10.0.0.1")
	_ = cs.ProcessHeartbeat(HeartbeatRequest{NodeID: n2.ID, NodeKey: n2.Key}, "10.0.0.2")

	_, agg := cs.ListNodes()
	if agg.SyncedNodes != 1 || agg.PendingNodes != 0 {
		t.Fatalf("agg synced=%d pending=%d, want 1/0", agg.SyncedNodes, agg.PendingNodes)
	}

	// n1 melaporkan hash lama -> pending.
	_ = cs.ProcessHeartbeat(HeartbeatRequest{NodeID: n1.ID, NodeKey: n1.Key, AppliedProfileHash: "stale"}, "10.0.0.1")
	_, agg = cs.ListNodes()
	if agg.SyncedNodes != 0 || agg.PendingNodes != 1 {
		t.Fatalf("agg synced=%d pending=%d, want 0/1", agg.SyncedNodes, agg.PendingNodes)
	}
}