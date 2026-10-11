package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Kontrak: node key tidak pernah keluar lewat listing admin (GET /api/cluster/nodes).
// Key adalah kredensial heartbeat node; kebocorannya memungkinkan impersonasi.
func TestClusterNodesListingNeverLeaksNodeKey(t *testing.T) {
	const secret = "SECRET-NODE-KEY-0123456789abcdef"
	cs := &ClusterStore{
		tokens: map[string]time.Time{},
		nodes: map[string]*NodeRecord{
			"node-aaaa": {ID: "node-aaaa", Name: "n1", Key: secret, LastSeenAt: time.Now()},
		},
	}
	prev := clusterStore
	clusterStore = cs
	t.Cleanup(func() { clusterStore = prev })

	rr := httptest.NewRecorder()
	handleClusterNodes(rr, httptest.NewRequest(http.MethodGet, "/api/cluster/nodes", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), secret) {
		t.Fatalf("node key bocor di listing: %s", rr.Body.String())
	}
	var out struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || len(out.Nodes) != 1 {
		t.Fatalf("respons tidak sesuai: %v %s", err, rr.Body.String())
	}
}
