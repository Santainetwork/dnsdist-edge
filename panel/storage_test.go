package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteClusterStore(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cluster.db")

	store, err := NewSQLiteClusterStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite cluster store: %v", err)
	}
	defer store.Close()

	// 1. Generate enrollment token
	tok := store.GenerateEnrollmentToken(1 * time.Hour)
	if tok == "" {
		t.Fatal("expected non-empty enrollment token")
	}

	// 2. Reject registration with invalid token
	_, err = store.RegisterNode(RegisterRequest{
		EnrollToken: "invalid-token",
		Name:        "edge-sqlite-01",
	}, "10.0.0.2")
	if err == nil {
		t.Fatal("expected error on invalid token, got nil")
	}

	// 3. Register node with valid token
	node1, err := store.RegisterNode(RegisterRequest{
		EnrollToken: tok,
		Name:        "edge-sqlite-01",
		Version:     "2.9.0",
		ReportedIP:  "10.0.0.2",
	}, "10.0.0.2")
	if err != nil {
		t.Fatalf("expected successful registration: %v", err)
	}
	if node1.ID == "" || node1.Key == "" {
		t.Fatal("expected node ID and Key to be generated")
	}

	// Token must be consumed
	if store.ValidateAndConsumeToken(tok) {
		t.Fatal("expected token to be consumed after registration")
	}

	// 4. Heartbeat with wrong key
	err = store.ProcessHeartbeat(HeartbeatRequest{
		NodeID:  node1.ID,
		NodeKey: "wrong-secret-key",
	}, "10.0.0.2")
	if err == nil {
		t.Fatal("expected heartbeat error with wrong key, got nil")
	}

	// 5. Heartbeat with correct key
	err = store.ProcessHeartbeat(HeartbeatRequest{
		NodeID:         node1.ID,
		NodeKey:        node1.Key,
		Name:           "edge-sqlite-01",
		ReportedIP:     "10.0.0.2",
		Version:        "2.9.0",
		QPS:            250,
		QueriesTotal:   15000,
		BlockedTotal:   1200,
		CacheHitPct:    88.5,
		CPUPct:         15.2,
		MemUsedMB:      128,
		MemTotalMB:     1024,
		UptimeSec:      7200,
		DnsdistRunning: true,
		DBHash:         "hash-abc123",
		DBUpdatedAt:    "2026-10-02T00:00:00Z",
	}, "10.0.0.2")
	if err != nil {
		t.Fatalf("expected heartbeat success: %v", err)
	}

	// 6. List nodes and check aggregates
	nodes, agg := store.ListNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Key != "" {
		t.Fatal("node key must be scrubbed in ListNodes()")
	}
	if nodes[0].Status != "online" {
		t.Fatalf("expected status 'online', got %s", nodes[0].Status)
	}
	if agg.TotalNodes != 1 || agg.OnlineNodes != 1 || agg.TotalQPS != 250 {
		t.Fatalf("unexpected aggregates: %+v", agg)
	}

	// 7. Test degraded status when dnsdist is down
	err = store.ProcessHeartbeat(HeartbeatRequest{
		NodeID:         node1.ID,
		NodeKey:        node1.Key,
		DnsdistRunning: false,
	}, "10.0.0.2")
	if err != nil {
		t.Fatalf("heartbeat update failed: %v", err)
	}
	nodes, agg = store.ListNodes()
	if nodes[0].Status != "degraded" || agg.DegradedNodes != 1 {
		t.Fatalf("expected degraded node, got status=%s agg=%+v", nodes[0].Status, agg)
	}

	// 8. Re-open DB to verify persistence across restarts
	if err := store.Close(); err != nil {
		t.Fatalf("close db error: %v", err)
	}
	store2, err := NewSQLiteClusterStore(dbPath)
	if err != nil {
		t.Fatalf("failed to reopen sqlite store: %v", err)
	}
	defer store2.Close()

	nodesAfter, _ := store2.ListNodes()
	if len(nodesAfter) != 1 || nodesAfter[0].ID != node1.ID {
		t.Fatalf("expected node persisted in sqlite, got: %+v", nodesAfter)
	}

	// 9. Delete node
	if err := store2.DeleteNode(node1.ID); err != nil {
		t.Fatalf("delete node failed: %v", err)
	}
	nodesAfterDelete, _ := store2.ListNodes()
	if len(nodesAfterDelete) != 0 {
		t.Fatalf("expected 0 nodes after delete, got %d", len(nodesAfterDelete))
	}
	if err := store2.DeleteNode("non-existent-id"); err == nil {
		t.Fatal("expected error deleting non-existent node, got nil")
	}
}

func TestSQLiteTokenTTL(t *testing.T) {
	store, err := NewSQLiteClusterStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create memory sqlite store: %v", err)
	}
	defer store.Close()

	// 1. Valid token within TTL
	tokValid := store.GenerateEnrollmentToken(10 * time.Minute)
	if !store.ValidateAndConsumeToken(tokValid) {
		t.Fatal("expected token to be valid")
	}
	// Consumed token cannot be reused
	if store.ValidateAndConsumeToken(tokValid) {
		t.Fatal("expected token replay to fail")
	}

	// 2. Expired token (inserted with past timestamp)
	_, _ = store.db.Exec("INSERT INTO cluster_tokens (token, expires_at) VALUES (?, ?)", "expired-tok", time.Now().Add(-1*time.Minute))
	if store.ValidateAndConsumeToken("expired-tok") {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestClusterHTTPWithSQLite(t *testing.T) {
	store, err := NewSQLiteClusterStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	defer store.Close()

	// Swap active cluster storage for the test
	oldStore := clusterStorage
	setClusterStorage(store)
	defer setClusterStorage(oldStore)

	// 1. Generate token via HTTP handler
	tokenReq := httptest.NewRequest(http.MethodPost, "/api/cluster/token", bytes.NewBufferString(`{"hours":1}`))
	tokenRec := httptest.NewRecorder()
	handleClusterToken(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", tokenRec.Code)
	}
	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp); err != nil {
		t.Fatal(err)
	}
	if tokenResp.Token == "" {
		t.Fatal("token was empty")
	}

	// 2. Register node via HTTP handler
	regBody, _ := json.Marshal(RegisterRequest{
		EnrollToken: tokenResp.Token,
		Name:        "sqlite-edge-http",
		Version:     "2.9.0",
	})
	regReq := httptest.NewRequest(http.MethodPost, "/api/cluster/register", bytes.NewReader(regBody))
	regReq.RemoteAddr = "10.10.10.5:1234"
	regRec := httptest.NewRecorder()
	handleClusterRegister(regRec, regReq)
	if regRec.Code != http.StatusOK {
		t.Fatalf("register expected 200, got %d: %s", regRec.Code, regRec.Body.String())
	}
	var regResp RegisterResponse
	if err := json.Unmarshal(regRec.Body.Bytes(), &regResp); err != nil {
		t.Fatal(err)
	}
	if regResp.NodeID == "" || regResp.NodeKey == "" {
		t.Fatal("missing node ID or key in registration response")
	}

	// 3. Heartbeat via HTTP handler
	hbBody, _ := json.Marshal(HeartbeatRequest{
		NodeID:         regResp.NodeID,
		NodeKey:        regResp.NodeKey,
		QPS:            300,
		QueriesTotal:   50000,
		BlockedTotal:   1200,
		CacheHitPct:    91.2,
		DnsdistRunning: true,
	})
	hbReq := httptest.NewRequest(http.MethodPost, "/api/cluster/heartbeat", bytes.NewReader(hbBody))
	hbRec := httptest.NewRecorder()
	handleClusterHeartbeat(hbRec, hbReq)
	if hbRec.Code != http.StatusOK {
		t.Fatalf("heartbeat expected 200, got %d: %s", hbRec.Code, hbRec.Body.String())
	}

	// 4. List nodes via HTTP handler
	listReq := httptest.NewRequest(http.MethodGet, "/api/cluster/nodes", nil)
	listRec := httptest.NewRecorder()
	handleClusterNodes(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list nodes expected 200, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var listResp struct {
		Nodes     []NodeRecord     `json:"nodes"`
		Aggregate ClusterAggregate `json:"aggregate"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Nodes) != 1 || listResp.Nodes[0].ID != regResp.NodeID {
		t.Fatalf("expected registered node in list, got %+v", listResp.Nodes)
	}
	if listResp.Aggregate.TotalQPS != 300 || listResp.Aggregate.OnlineNodes != 1 {
		t.Fatalf("unexpected aggregates: %+v", listResp.Aggregate)
	}

	// 5. Delete node via HTTP handler
	delReq := httptest.NewRequest(http.MethodDelete, "/api/cluster/nodes?id="+regResp.NodeID, nil)
	delRec := httptest.NewRecorder()
	handleClusterNodes(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete expected 200, got %d: %s", delRec.Code, delRec.Body.String())
	}
}
