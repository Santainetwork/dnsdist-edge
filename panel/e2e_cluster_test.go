package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEndToEndClusterWorkflow runs a complete integration test against the compiled
// tools/dnsdist-panel binary and its public HTTP interfaces.
func TestEndToEndClusterWorkflow(t *testing.T) {
	binPath, err := filepath.Abs("../tools/dnsdist-panel")
	if err != nil {
		t.Fatalf("cannot get bin path: %v", err)
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("binary %s not found: %v", binPath, err)
	}

	tmpDir := t.TempDir()
	clusterNodesFile := filepath.Join(tmpDir, "nodes.json")
	secretFile := filepath.Join(tmpDir, "panel.secret")
	passFile := filepath.Join(tmpDir, "panel.password")
	_ = os.WriteFile(passFile, []byte("supersecret123\n"), 0600)

	// Step 1: Verify CLI enrollment token generation
	cmdCLI := exec.Command(binPath, "-enrollment-token", "-cluster-nodes-file", clusterNodesFile)
	outCLI, err := cmdCLI.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI -enrollment-token failed: %v, output: %s", err, string(outCLI))
	}
	outStr := string(outCLI)
	if !strings.Contains(outStr, "enroll-") {
		t.Fatalf("CLI output missing token prefix 'enroll-': %s", outStr)
	}
	t.Logf("[E2E CHECK 1] CLI Token Generation: SUCCESS (%s)", strings.TrimSpace(strings.Split(outStr, "\n")[1]))

	// Step 2: Start Master Server process on random free port
	masterPort := "18084"
	masterAddr := "127.0.0.1:" + masterPort
	masterURL := "http://" + masterAddr

	cmdServer := exec.Command(binPath,
		"-addr", masterAddr,
		"-tls=false",
		"-master=true",
		"-cluster-nodes-file", clusterNodesFile,
		"-secret-file", secretFile,
		"-config", filepath.Join(tmpDir, "dnsdist.conf"),
		"-files-dir", tmpDir,
	)
	cmdServer.Dir = tmpDir
	var serverLogs bytes.Buffer
	cmdServer.Stdout = &serverLogs
	cmdServer.Stderr = &serverLogs

	if err := cmdServer.Start(); err != nil {
		t.Fatalf("cannot start master server: %v", err)
	}
	defer func() {
		_ = cmdServer.Process.Kill()
		_ = cmdServer.Wait()
	}()

	// Wait for master server to listen
	var ready bool
	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		resp, err := client.Get(masterURL + "/")
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
	}
	if !ready {
		t.Fatalf("master server failed to become ready: %s", serverLogs.String())
	}
	t.Logf("[E2E CHECK 2] Master HTTP Server Startup: SUCCESS (%s)", masterURL)

	// Step 3: Login to Master to obtain JWT
	loginBody := map[string]string{"password": "supersecret123"}
	bLogin, _ := json.Marshal(loginBody)
	respLogin, err := client.Post(masterURL+"/api/login", "application/json", bytes.NewReader(bLogin))
	if err != nil || respLogin.StatusCode != http.StatusOK {
		t.Fatalf("login failed: err=%v status=%v", err, respLogin.StatusCode)
	}
	var loginResp struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(respLogin.Body).Decode(&loginResp)
	respLogin.Body.Close()
	if loginResp.Token == "" {
		t.Fatal("empty JWT token received")
	}
	jwtToken := loginResp.Token
	t.Logf("[E2E CHECK 3] Master JWT Authentication: SUCCESS")

	// Step 4: Generate Enrollment Token via API
	reqTok, _ := http.NewRequest(http.MethodPost, masterURL+"/api/cluster/token", bytes.NewReader([]byte(`{"hours":12}`)))
	reqTok.Header.Set("Authorization", "Bearer "+jwtToken)
	reqTok.Header.Set("Content-Type", "application/json")
	respTok, err := client.Do(reqTok)
	if err != nil || respTok.StatusCode != http.StatusOK {
		t.Fatalf("token generation failed: err=%v status=%v", err, respTok.StatusCode)
	}
	var tokResp struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(respTok.Body).Decode(&tokResp)
	respTok.Body.Close()
	enrollToken := tokResp.Token
	if !strings.HasPrefix(enrollToken, "enroll-") {
		t.Fatalf("invalid enrollment token from API: %s", enrollToken)
	}
	t.Logf("[E2E CHECK 4] Web API Token Generation: SUCCESS (%s)", enrollToken)

	// Step 5: Register Edge Node using token
	regBody := map[string]string{
		"enroll_token": enrollToken,
		"name":         "edge-integration-01",
		"version":      "2.5.1",
	}
	bReg, _ := json.Marshal(regBody)
	respReg, err := client.Post(masterURL+"/api/cluster/register", "application/json", bytes.NewReader(bReg))
	if err != nil || respReg.StatusCode != http.StatusOK {
		t.Fatalf("node registration failed: err=%v status=%v", err, respReg.StatusCode)
	}
	var regData struct {
		NodeID  string `json:"node_id"`
		NodeKey string `json:"node_key"`
		Name    string `json:"name"`
	}
	_ = json.NewDecoder(respReg.Body).Decode(&regData)
	respReg.Body.Close()
	if regData.NodeID == "" || regData.NodeKey == "" {
		t.Fatalf("registration missing node_id/key: %+v", regData)
	}
	t.Logf("[E2E CHECK 5] Edge Node Registration: SUCCESS (ID: %s, Name: %s)", regData.NodeID, regData.Name)

	// Step 5b: Verify token replay attack is rejected (single-use token)
	respReplay, err := client.Post(masterURL+"/api/cluster/register", "application/json", bytes.NewReader(bReg))
	if err == nil {
		defer respReplay.Body.Close()
		if respReplay.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected token replay to be 401 Unauthorized, got %d", respReplay.StatusCode)
		}
	}
	t.Logf("[E2E CHECK 5b] Token Single-Use Enforcement: SUCCESS (Replay rejected)")

	// Step 6: Edge sends Telemetry Heartbeat
	hbBody := map[string]any{
		"node_id":         regData.NodeID,
		"node_key":        regData.NodeKey,
		"name":            "edge-integration-01",
		"version":         "2.5.1",
		"qps":             125,
		"queries_total":   50000,
		"blocked_total":   1420,
		"cache_hit_pct":   92.4,
		"cpu_pct":         14.2,
		"mem_used_mb":     512,
		"mem_total_mb":    2048,
		"uptime_sec":      3600,
		"dnsdist_running": true,
		"db_hash":         "sha256-abcdef1234567890",
		"db_updated_at":   time.Now().UTC().Format(time.RFC3339),
	}
	bHB, _ := json.Marshal(hbBody)
	respHB, err := client.Post(masterURL+"/api/cluster/heartbeat", "application/json", bytes.NewReader(bHB))
	if err != nil || respHB.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat failed: err=%v status=%v", err, respHB.StatusCode)
	}
	var hbResp struct {
		OK bool `json:"ok"`
	}
	_ = json.NewDecoder(respHB.Body).Decode(&hbResp)
	respHB.Body.Close()
	if !hbResp.OK {
		t.Fatal("heartbeat response ok != true")
	}
	t.Logf("[E2E CHECK 6] Telemetry Ingestion: SUCCESS")

	// Step 7: Query Master Cluster Nodes and Aggregate Metrics
	reqNodes, _ := http.NewRequest(http.MethodGet, masterURL+"/api/cluster/nodes", nil)
	reqNodes.Header.Set("Authorization", "Bearer "+jwtToken)
	respNodes, err := client.Do(reqNodes)
	if err != nil || respNodes.StatusCode != http.StatusOK {
		t.Fatalf("get cluster nodes failed: err=%v status=%v", err, respNodes.StatusCode)
	}
	var clusterData struct {
		Nodes []struct {
			ID           string  `json:"id"`
			Name         string  `json:"name"`
			Status       string  `json:"status"`
			QPS          int64   `json:"qps"`
			QueriesTotal int64   `json:"queries_total"`
			BlockedTotal int64   `json:"blocked_total"`
			CacheHitPct  float64 `json:"cache_hit_pct"`
			DBHash       string  `json:"db_hash"`
		} `json:"nodes"`
		Aggregate struct {
			TotalNodes     int     `json:"total_nodes"`
			OnlineNodes    int     `json:"online_nodes"`
			TotalQPS       int64   `json:"total_qps"`
			TotalQueries   int64   `json:"total_queries"`
			TotalBlocked   int64   `json:"total_blocked"`
			AvgCacheHitPct float64 `json:"avg_cache_hit_pct"`
		} `json:"aggregate"`
	}
	_ = json.NewDecoder(respNodes.Body).Decode(&clusterData)
	respNodes.Body.Close()

	if clusterData.Aggregate.TotalNodes != 1 || clusterData.Aggregate.OnlineNodes != 1 {
		t.Fatalf("unexpected node counts in aggregate: %+v", clusterData.Aggregate)
	}
	if clusterData.Aggregate.TotalQPS != 125 || clusterData.Aggregate.TotalBlocked != 1420 || clusterData.Aggregate.AvgCacheHitPct != 92.4 {
		t.Fatalf("unexpected aggregate metrics: %+v", clusterData.Aggregate)
	}
	if len(clusterData.Nodes) != 1 || clusterData.Nodes[0].Status != "online" {
		t.Fatalf("unexpected node list output: %+v", clusterData.Nodes)
	}
	t.Logf("[E2E CHECK 7] Cluster Status & Aggregate Aggregation: SUCCESS (Online: %d, QPS: %d, Blocked: %d, Cache: %.1f%%)",
		clusterData.Aggregate.OnlineNodes, clusterData.Aggregate.TotalQPS, clusterData.Aggregate.TotalBlocked, clusterData.Aggregate.AvgCacheHitPct)

	// Step 8: Delete node
	reqDel, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/cluster/nodes?id=%s", masterURL, regData.NodeID), nil)
	reqDel.Header.Set("Authorization", "Bearer "+jwtToken)
	respDel, err := client.Do(reqDel)
	if err != nil || respDel.StatusCode != http.StatusOK {
		t.Fatalf("delete node failed: err=%v status=%v", err, respDel.StatusCode)
	}
	respDel.Body.Close()

	// Verify node is gone
	reqNodes2, _ := http.NewRequest(http.MethodGet, masterURL+"/api/cluster/nodes", nil)
	reqNodes2.Header.Set("Authorization", "Bearer "+jwtToken)
	respNodes2, _ := client.Do(reqNodes2)
	bDelCheck, _ := io.ReadAll(respNodes2.Body)
	respNodes2.Body.Close()
	if strings.Contains(string(bDelCheck), regData.NodeID) {
		t.Fatalf("node still present after deletion: %s", string(bDelCheck))
	}
	t.Logf("[E2E CHECK 8] Node Deletion: SUCCESS")
}
