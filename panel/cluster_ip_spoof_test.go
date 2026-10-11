package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

// Kontrak: IP yang tercatat (X-Forwarded-For / X-Real-IP) hanya label telemetri.
// Autentikasi heartbeat memakai NodeKey, sehingga spoof IP tidak bisa melewati
// auth. Jika suatu hari IP dipakai untuk keputusan akses, tes ini harus diubah
// sadar (dan clientIP wajib dibatasi ke proxy tepercaya).
func TestHeartbeatAuthIgnoresForgedClientIP(t *testing.T) {
	cs := &ClusterStore{
		tokens: map[string]time.Time{},
		nodes: map[string]*NodeRecord{
			"node-bbbb": {ID: "node-bbbb", Key: "good-key", LastSeenAt: time.Now()},
		},
	}
	prev := clusterStore
	clusterStore = cs
	t.Cleanup(func() { clusterStore = prev })

	// Key salah + IP palsu tetap harus ditolak.
	r := httptest.NewRequest("POST", "/api/cluster/heartbeat", nil)
	r.Header.Set("X-Forwarded-For", "10.0.0.1")
	if err := cs.ProcessHeartbeat(HeartbeatRequest{NodeID: "node-bbbb", NodeKey: "bad-key"}, clientIP(r)); err == nil {
		t.Fatal("key salah harus ditolak walau IP diforge")
	}
	// Key benar tetap diterima, IP tercatat sebagai label.
	if err := cs.ProcessHeartbeat(HeartbeatRequest{NodeID: "node-bbbb", NodeKey: "good-key"}, clientIP(r)); err != nil {
		t.Fatalf("key benar harus diterima: %v", err)
	}
	if cs.nodes["node-bbbb"].IP != "10.0.0.1" {
		t.Fatalf("IP label = %q", cs.nodes["node-bbbb"].IP)
	}
}
