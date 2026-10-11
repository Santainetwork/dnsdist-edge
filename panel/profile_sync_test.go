package main

import "testing"

// F5.2: panel harus menyimpan hash profil yang benar-benar diterapkan node
// dari heartbeat, dan menghitung status sinkron per node.
func TestHeartbeatStoresAppliedProfileHash(t *testing.T) {
	cs := newClusterStore(t.TempDir() + "/nodes.json")
	rec := &NodeRecord{ID: "n1", Key: "k1"}
	cs.nodes["n1"] = rec

	if err := cs.ProcessHeartbeat(HeartbeatRequest{NodeID: "n1", NodeKey: "k1", AppliedProfileHash: "abc"}, "10.0.0.1"); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if rec.AppliedProfileHash != "abc" {
		t.Fatalf("AppliedProfileHash = %q, want abc", rec.AppliedProfileHash)
	}
}

func TestProfileSyncState(t *testing.T) {
	cases := []struct{ assigned, applied, want string }{
		{"", "", ""},
		{"h1", "h1", "synced"},
		{"h2", "h1", "pending"},
		{"h1", "", "pending"},
	}
	for _, c := range cases {
		if got := profileSyncState(c.assigned, c.applied); got != c.want {
			t.Errorf("profileSyncState(%q,%q) = %q, want %q", c.assigned, c.applied, got, c.want)
		}
	}
}
