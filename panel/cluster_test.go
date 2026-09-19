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

func TestWebNodeEnrollmentHandoffUI(t *testing.T) {
	html := string(indexHTML)
	for _, marker := range []string{
		"master-edge-url",
		"add-node-token",
		"add-node-cli",
		"add-node-handoff",
		"createNodeHandoff",
		"dnsdist-enroll=",
		"parseEdgePanelURL",
		"readEnrollmentHandoff",
		"history.replaceState",
		"window.open(handoff.href, '_blank', 'noopener,noreferrer')",
		"{ minutes: 10 }",
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("index HTML missing enrollment marker %q", marker)
		}
	}
	if strings.Contains(html, "?dnsdist-enroll=") {
		t.Fatal("enrollment token must not be placed in URL query")
	}
	if strings.Contains(html, "window.open('', '_blank', 'noopener,noreferrer')") {
		t.Fatal("noopener blank popup returns null and cannot receive the handoff URL")
	}
}

func TestValidateMasterURL(t *testing.T) {
	for _, raw := range []string{"ftp://master.example", "http://user:pass@master.example", "not-a-url"} {
		if _, err := validateMasterURL(raw); err == nil {
			t.Errorf("validateMasterURL(%q) accepted unsafe URL", raw)
		}
	}
	got, err := validateMasterURL(" https://master.example:8084/path/ ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://master.example:8084/path" {
		t.Fatalf("validated URL = %q", got)
	}
}

func TestClusterTokenTTL(t *testing.T) {
	tmpDir := t.TempDir()
	clusterStore = newClusterStore(filepath.Join(tmpDir, "nodes.json"))

	before := time.Now()
	req := httptest.NewRequest(http.MethodPost, "/api/cluster/token", strings.NewReader(`{"minutes":10}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleClusterToken(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("minutes token status = %d, want 200", rec.Code)
	}
	var response struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	clusterStore.mu.RLock()
	expires := clusterStore.tokens[response.Token]
	clusterStore.mu.RUnlock()
	if expires.Before(before.Add(9*time.Minute)) || expires.After(time.Now().Add(11*time.Minute)) {
		t.Fatalf("minutes token expiry = %v, want about 10 minutes", expires)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/cluster/token", strings.NewReader(`{"hours":1}`))
	rec = httptest.NewRecorder()
	handleClusterToken(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("hours token status = %d, want 200", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	clusterStore.mu.RLock()
	expires = clusterStore.tokens[response.Token]
	clusterStore.mu.RUnlock()
	if expires.Before(time.Now().Add(59*time.Minute)) || expires.After(time.Now().Add(61*time.Minute)) {
		t.Fatalf("hours token expiry = %v, want about 1 hour", expires)
	}
}

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
		Version:     "2.6.0",
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
	defer agent.Stop()

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

func TestDynamicRegistrationEnsuresRunning(t *testing.T) {
	tmpDir := t.TempDir()
	storeFile := filepath.Join(tmpDir, "cluster-nodes.json")
	agentStateFile := filepath.Join(tmpDir, "cluster-agent.json")

	cs := newClusterStore(storeFile)
	clusterStore = cs

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cluster/register", handleClusterRegister)
	mux.HandleFunc("/api/cluster/heartbeat", handleClusterHeartbeat)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	tok := cs.GenerateEnrollmentToken(1 * time.Hour)

	// Inisialisasi agent tanpa master URL awal
	agent := &EdgeClusterAgent{
		stateFile:  agentStateFile,
		masterURL:  "",
		interval:   50 * time.Millisecond,
		httpClient: ts.Client(),
	}
	defer agent.Stop()

	if agent.running {
		t.Fatal("agent should not be running before registration")
	}

	// Dynamic register via web panel
	err := agent.Register(ts.URL, tok, "dynamic-edge")
	if err != nil {
		t.Fatalf("dynamic register failed: %v", err)
	}

	agent.mu.RLock()
	isRunning := agent.running
	agent.mu.RUnlock()

	if !isRunning {
		t.Fatal("agent should be running after dynamic registration")
	}

	// Tunggu ticker mengirim heartbeat
	time.Sleep(120 * time.Millisecond)

	agent.mu.RLock()
	lastHB := agent.state.LastHeartbeat
	agent.mu.RUnlock()

	if lastHB == "" {
		t.Fatal("expected heartbeat to be recorded by background ticker")
	}
}

func TestMasterTracksEdgeRemoteIP(t *testing.T) {
	tmpDir := t.TempDir()
	storeFile := filepath.Join(tmpDir, "cluster-nodes.json")

	cs := newClusterStore(storeFile)

	tok := cs.GenerateEnrollmentToken(1 * time.Hour)
	rec, err := cs.RegisterNode(RegisterRequest{
		EnrollToken: tok,
		Name:        "ip-track-node",
	}, "10.0.0.1")
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}

	if rec.IP != "10.0.0.1" {
		t.Fatalf("expected initial IP 10.0.0.1, got %s", rec.IP)
	}

	// Heartbeat dari IP baru (misal migrasi/DHCP) tanpa reported_ip eksplisit
	err = cs.ProcessHeartbeat(HeartbeatRequest{
		NodeID:         rec.ID,
		NodeKey:        rec.Key,
		DnsdistRunning: true,
	}, "10.0.0.99")
	if err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}

	nodes, _ := cs.ListNodes()
	if nodes[0].IP != "10.0.0.99" {
		t.Fatalf("expected updated IP 10.0.0.99, got %s", nodes[0].IP)
	}
}
