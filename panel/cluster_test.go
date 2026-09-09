package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestClusterStore(t *testing.T) {
	tmpDir := t.TempDir()
	storeFile := filepath.Join(tmpDir, "cluster-nodes.json")

	cs := newClusterStore(storeFile)

	// 1. Generate token
	tok := cs.GenerateEnrollmentToken(1 * time.Hour)
	if tok == "" {
		t.Fatal("expected non-empty token")
	}

	// 2. Register node with invalid token
	_, err := cs.RegisterNode(RegisterRequest{
		EnrollToken: "invalid-token",
		Name:        "edge-01",
	}, "192.168.1.100")
	if err == nil {
		t.Fatal("expected error on invalid token, got nil")
	}

	// 3. Register node with valid token
	node1, err := cs.RegisterNode(RegisterRequest{
		EnrollToken: tok,
		Name:        "edge-01",
		Version:     "2.5.0",
	}, "192.168.1.100")
	if err != nil {
		t.Fatalf("expected successful registration, got error: %v", err)
	}
	if node1.ID == "" || node1.Key == "" {
		t.Fatal("expected non-empty node ID and Key")
	}

	// Token must be consumed
	if cs.ValidateAndConsumeToken(tok) {
		t.Fatal("expected token to be consumed after registration")
	}

	// 4. Heartbeat with wrong key
	err = cs.ProcessHeartbeat(HeartbeatRequest{
		NodeID:  node1.ID,
		NodeKey: "wrong-key",
	}, "192.168.1.100")
	if err == nil {
		t.Fatal("expected error on wrong key, got nil")
	}

	// 5. Heartbeat with valid key
	now := time.Now()
	err = cs.ProcessHeartbeat(HeartbeatRequest{
		NodeID:         node1.ID,
		NodeKey:        node1.Key,
		QPS:            45,
		QueriesTotal:   10000,
		BlockedTotal:   250,
		CacheHitPct:    85.5,
		DnsdistRunning: true,
		DBHash:         "abcdef123456",
		DBUpdatedAt:    now.Format(time.RFC3339),
	}, "192.168.1.100")
	if err != nil {
		t.Fatalf("expected heartbeat success, got: %v", err)
	}

	// 6. Check list and aggregate
	nodes, agg := cs.ListNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if agg.TotalNodes != 1 || agg.OnlineNodes != 1 {
		t.Fatalf("expected 1 total & 1 online node, got total=%d online=%d", agg.TotalNodes, agg.OnlineNodes)
	}
	if agg.TotalQPS != 45 || agg.TotalBlocked != 250 || agg.AvgCacheHitPct != 85.5 {
		t.Fatalf("unexpected agg metrics: %+v", agg)
	}

	// Check key is stripped in ListNodes() for security
	if nodes[0].Key != "" {
		t.Errorf("expected Key to be empty in listed node, got %s", nodes[0].Key)
	}

	// 7. Test offline detection
	node1.LastSeenAt = now.Add(-4 * time.Minute)
	cs.refreshNodeStatus(node1, now)
	if node1.Status != "offline" {
		t.Fatalf("expected status offline after 4 minutes, got %s", node1.Status)
	}

	// 8. Test degraded detection (recent heartbeat but dnsdist stopped)
	node1.LastSeenAt = now
	node1.DnsdistRunning = false
	cs.refreshNodeStatus(node1, now)
	if node1.Status != "degraded" {
		t.Fatalf("expected status degraded when dnsdist not running, got %s", node1.Status)
	}

	// 9. Test persistence (reload from disk)
	cs2 := newClusterStore(storeFile)
	nodes2, _ := cs2.ListNodes()
	if len(nodes2) != 1 {
		t.Fatalf("expected 1 node loaded from disk, got %d", len(nodes2))
	}
	if nodes2[0].ID != node1.ID {
		t.Fatalf("expected node id %s, got %s", node1.ID, nodes2[0].ID)
	}
}

func TestEdgeAgentRegistrationAndHeartbeat(t *testing.T) {
	tmpDir := t.TempDir()
	storeFile := filepath.Join(tmpDir, "cluster-nodes.json")
	agentStateFile := filepath.Join(tmpDir, "cluster-agent.json")

	// 1. Setup mock Master Server
	cs := newClusterStore(storeFile)
	clusterStore = cs

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cluster/register", handleClusterRegister)
	mux.HandleFunc("/api/cluster/heartbeat", handleClusterHeartbeat)
	mux.HandleFunc("/api/cluster/nodes", handleClusterNodes)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 2. Generate token on master
	tok := cs.GenerateEnrollmentToken(1 * time.Hour)

	// 3. Setup Edge Agent
	agent := &EdgeClusterAgent{
		stateFile:  agentStateFile,
		masterURL:  ts.URL,
		enrollTok:  tok,
		nodeName:   "edge-jakarta-01",
		interval:   500 * time.Millisecond,
		httpClient: ts.Client(),
	}

	// Register edge to master
	err := agent.Register(ts.URL, tok, "edge-jakarta-01")
	if err != nil {
		t.Fatalf("agent register failed: %v", err)
	}

	if agent.state.NodeID == "" || agent.state.NodeKey == "" {
		t.Fatalf("expected agent to store NodeID and NodeKey, got %+v", agent.state)
	}

	// Verify master sees node
	nodes, agg := cs.ListNodes()
	if len(nodes) != 1 || agg.TotalNodes != 1 {
		t.Fatalf("expected 1 node registered on master, got %d", len(nodes))
	}
	if nodes[0].Name != "edge-jakarta-01" {
		t.Fatalf("expected node name 'edge-jakarta-01', got %s", nodes[0].Name)
	}

	// 4. Send heartbeat
	err = agent.sendHeartbeat()
	if err != nil {
		t.Fatalf("sendHeartbeat failed: %v", err)
	}

	// Check master recorded heartbeat
	nodesAfter, _ := cs.ListNodes()
	if nodesAfter[0].Status == "offline" {
		t.Fatalf("expected active status (online or degraded), got offline")
	}

	// 5. Test agent state persistence
	agent2 := &EdgeClusterAgent{
		stateFile:  agentStateFile,
		httpClient: ts.Client(),
	}
	agent2.loadState()
	if agent2.state.NodeID != agent.state.NodeID || agent2.state.NodeKey != agent.state.NodeKey {
		t.Fatalf("agent state persistence mismatch: expected %s, got %s", agent.state.NodeID, agent2.state.NodeID)
	}
}
