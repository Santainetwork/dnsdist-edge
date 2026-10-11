package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

// Kontrak: IP yang tercatat hanya label telemetri. X-Forwarded-For / X-Real-IP
// tidak dipakai kecuali RemoteAddr berasal dari proxy tepercaya. Default: tidak
// ada yang dipercaya. Autentikasi heartbeat tetap NodeKey, bukan IP.
func TestHeartbeatAuthIgnoresForgedClientIP(t *testing.T) {
	prevNets := trustedProxyNets
	t.Cleanup(func() { trustedProxyNets = prevNets })
	trustedProxyNets = nil

	cs := &ClusterStore{
		tokens: map[string]time.Time{},
		nodes: map[string]*NodeRecord{
			"node-bbbb": {ID: "node-bbbb", Key: "good-key", LastSeenAt: time.Now()},
		},
	}
	prev := clusterStore
	clusterStore = cs
	t.Cleanup(func() { clusterStore = prev })

	r := httptest.NewRequest("POST", "/api/cluster/heartbeat", nil)
	r.RemoteAddr = "203.0.113.9:1000"
	r.Header.Set("X-Forwarded-For", "10.0.0.1")

	// Key salah + XFF palsu tetap ditolak.
	if err := cs.ProcessHeartbeat(HeartbeatRequest{NodeID: "node-bbbb", NodeKey: "bad-key"}, clientIP(r)); err == nil {
		t.Fatal("key salah harus ditolak walau IP diforge")
	}
	// Key benar diterima. Tanpa proxy tepercaya, label = RemoteAddr, bukan XFF.
	if err := cs.ProcessHeartbeat(HeartbeatRequest{NodeID: "node-bbbb", NodeKey: "good-key"}, clientIP(r)); err != nil {
		t.Fatalf("key benar harus diterima: %v", err)
	}
	if got := cs.nodes["node-bbbb"].IP; got != "203.0.113.9" {
		t.Fatalf("IP label = %q, want 203.0.113.9 (XFF tidak dipercaya)", got)
	}
}
