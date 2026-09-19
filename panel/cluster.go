package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─── Cluster Models ──────────────────────────────────────────────────────────

// NodeRecord stores latest state & telemetry of an edge node.
type NodeRecord struct {
	ID             string    `json:"id"`
	Key            string    `json:"key,omitempty"` // Auth key for heartbeats
	Name           string    `json:"name"`
	IP             string    `json:"ip"`
	Version        string    `json:"version"`
	Status         string    `json:"status"` // "online", "degraded", "offline"
	FirstSeenAt    time.Time `json:"first_seen_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	QPS            int64     `json:"qps"`
	QueriesTotal   int64     `json:"queries_total"`
	BlockedTotal   int64     `json:"blocked_total"`
	CacheHitPct    float64   `json:"cache_hit_pct"`
	CPUPct         float64   `json:"cpu_pct"`
	MemUsedMB      uint64    `json:"mem_used_mb"`
	MemTotalMB     uint64    `json:"mem_total_mb"`
	UptimeSec      int64     `json:"uptime_sec"`
	DnsdistRunning bool      `json:"dnsdist_running"`
	DBHash         string    `json:"db_hash"`
	DBUpdatedAt    string    `json:"db_updated_at"`
}

// ClusterAggregate provides summary metrics over all active nodes.
type ClusterAggregate struct {
	TotalNodes     int     `json:"total_nodes"`
	OnlineNodes    int     `json:"online_nodes"`
	DegradedNodes  int     `json:"degraded_nodes"`
	OfflineNodes   int     `json:"offline_nodes"`
	TotalQPS       int64   `json:"total_qps"`
	TotalQueries   int64   `json:"total_queries"`
	TotalBlocked   int64   `json:"total_blocked"`
	AvgCacheHitPct float64 `json:"avg_cache_hit_pct"`
}

type RegisterRequest struct {
	EnrollToken string `json:"enroll_token"`
	Name        string `json:"name"`
	ReportedIP  string `json:"reported_ip"`
	Version     string `json:"version"`
}

type RegisterResponse struct {
	NodeID  string `json:"node_id"`
	NodeKey string `json:"node_key"`
	Name    string `json:"name"`
}

type HeartbeatRequest struct {
	NodeID         string  `json:"node_id"`
	NodeKey        string  `json:"node_key"`
	Name           string  `json:"name"`
	ReportedIP     string  `json:"reported_ip"`
	Version        string  `json:"version"`
	QPS            int64   `json:"qps"`
	QueriesTotal   int64   `json:"queries_total"`
	BlockedTotal   int64   `json:"blocked_total"`
	CacheHitPct    float64 `json:"cache_hit_pct"`
	CPUPct         float64 `json:"cpu_pct"`
	MemUsedMB      uint64  `json:"mem_used_mb"`
	MemTotalMB     uint64  `json:"mem_total_mb"`
	UptimeSec      int64   `json:"uptime_sec"`
	DnsdistRunning bool    `json:"dnsdist_running"`
	DBHash         string  `json:"db_hash"`
	DBUpdatedAt    string  `json:"db_updated_at"`
}

type HeartbeatResponse struct {
	OK             bool      `json:"ok"`
	AcknowledgedAt time.Time `json:"ack_at"`
	MasterDBHash   string    `json:"master_db_hash,omitempty"`
}

// ─── Cluster Store ───────────────────────────────────────────────────────────

type clusterDiskFormat struct {
	Tokens map[string]time.Time   `json:"tokens"`
	Nodes  map[string]*NodeRecord `json:"nodes"`
}

type ClusterStore struct {
	mu       sync.RWMutex
	filePath string
	tokens   map[string]time.Time
	nodes    map[string]*NodeRecord
}

func newClusterStore(filePath string) *ClusterStore {
	cs := &ClusterStore{
		filePath: filePath,
		tokens:   make(map[string]time.Time),
		nodes:    make(map[string]*NodeRecord),
	}
	cs.load()
	return cs
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (cs *ClusterStore) load() {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	b, err := os.ReadFile(cs.filePath)
	if err != nil {
		return
	}
	var data clusterDiskFormat
	if err := json.Unmarshal(b, &data); err != nil {
		return
	}
	now := time.Now()
	for t, exp := range data.Tokens {
		if exp.After(now) {
			cs.tokens[t] = exp
		}
	}
	for id, n := range data.Nodes {
		cs.nodes[id] = n
		cs.refreshNodeStatus(n, now)
	}
}

func (cs *ClusterStore) save() error {
	if cs.filePath == "" {
		return nil
	}
	data := clusterDiskFormat{
		Tokens: cs.tokens,
		Nodes:  cs.nodes,
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(cs.filePath), 0o755)
	return atomicWriteString(cs.filePath, string(b), 0o600)
}

func (cs *ClusterStore) refreshNodeStatus(n *NodeRecord, now time.Time) {
	// Node offline jika tidak ada heartbeat dalam 3 menit
	if n.LastSeenAt.IsZero() || now.Sub(n.LastSeenAt) > 3*time.Minute {
		n.Status = "offline"
	} else if !n.DnsdistRunning {
		n.Status = "degraded"
	} else {
		n.Status = "online"
	}
}

// GenerateEnrollmentToken creates a temporary enrollment token (default 24h)
func (cs *ClusterStore) GenerateEnrollmentToken(duration time.Duration) string {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if duration <= 0 {
		duration = 24 * time.Hour
	}
	tok := "enroll-" + randHex(16)
	cs.tokens[tok] = time.Now().Add(duration)
	_ = cs.save()
	return tok
}

// ValidateAndConsumeToken checks if token is valid and deletes it (one-time use)
func (cs *ClusterStore) ValidateAndConsumeToken(tok string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	tok = strings.TrimSpace(tok)
	exp, ok := cs.tokens[tok]
	if !ok || time.Now().After(exp) {
		delete(cs.tokens, tok)
		return false
	}
	delete(cs.tokens, tok)
	_ = cs.save()
	return true
}

func clientIP(r *http.Request) string {
	// Check X-Forwarded-For first
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		ip := strings.TrimSpace(parts[0])
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	// Check X-Real-IP
	if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
		if net.ParseIP(xrip) != nil {
			return xrip
		}
	}
	// RemoteAddr fallback
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && net.ParseIP(host) != nil {
		return host
	}
	if ip := net.ParseIP(r.RemoteAddr); ip != nil {
		return r.RemoteAddr
	}
	return r.RemoteAddr
}

func validateMasterURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("master URL harus memakai HTTP/HTTPS tanpa kredensial")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// RegisterNode enrolls a new edge node using an enrollment token
func (cs *ClusterStore) RegisterNode(req RegisterRequest, remoteIP string) (*NodeRecord, error) {
	if !cs.ValidateAndConsumeToken(req.EnrollToken) {
		return nil, errors.New("token pendaftaran tidak valid atau telah kedaluwarsa")
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	nodeID := "node-" + randHex(8)
	nodeKey := randHex(32)

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = nodeID
	}

	ip := strings.TrimSpace(req.ReportedIP)
	if ip == "" {
		ip = remoteIP
	}

	now := time.Now()
	rec := &NodeRecord{
		ID:          nodeID,
		Key:         nodeKey,
		Name:        name,
		IP:          ip,
		Version:     req.Version,
		Status:      "online",
		FirstSeenAt: now,
		LastSeenAt:  now,
	}
	cs.nodes[nodeID] = rec
	_ = cs.save()
	return rec, nil
}

// ProcessHeartbeat updates node metrics and updates last_seen_at
func (cs *ClusterStore) ProcessHeartbeat(req HeartbeatRequest, remoteIP string) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	rec, ok := cs.nodes[req.NodeID]
	if !ok {
		return errors.New("node tidak terdaftar")
	}
	if rec.Key != req.NodeKey {
		return errors.New("node key tidak cocok")
	}

	now := time.Now()
	rec.LastSeenAt = now
	if req.Name != "" {
		rec.Name = req.Name
	}
	if req.ReportedIP != "" {
		rec.IP = req.ReportedIP
	} else if remoteIP != "" {
		rec.IP = remoteIP
	}
	if req.Version != "" {
		rec.Version = req.Version
	}

	rec.QPS = req.QPS
	rec.QueriesTotal = req.QueriesTotal
	rec.BlockedTotal = req.BlockedTotal
	rec.CacheHitPct = req.CacheHitPct
	rec.CPUPct = req.CPUPct
	rec.MemUsedMB = req.MemUsedMB
	rec.MemTotalMB = req.MemTotalMB
	rec.UptimeSec = req.UptimeSec
	rec.DnsdistRunning = req.DnsdistRunning
	rec.DBHash = req.DBHash
	rec.DBUpdatedAt = req.DBUpdatedAt

	cs.refreshNodeStatus(rec, now)
	_ = cs.save()
	return nil
}

// ListNodes returns all nodes and calculated aggregate statistics
func (cs *ClusterStore) ListNodes() ([]*NodeRecord, ClusterAggregate) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	now := time.Now()
	var list []*NodeRecord
	var agg ClusterAggregate

	var cacheSum float64
	var activeCount int

	for _, n := range cs.nodes {
		cs.refreshNodeStatus(n, now)
		// clone record to avoid exposing private Key
		clone := *n
		clone.Key = ""
		list = append(list, &clone)

		agg.TotalNodes++
		switch clone.Status {
		case "online":
			agg.OnlineNodes++
			agg.TotalQPS += clone.QPS
			agg.TotalQueries += clone.QueriesTotal
			agg.TotalBlocked += clone.BlockedTotal
			cacheSum += clone.CacheHitPct
			activeCount++
		case "degraded":
			agg.DegradedNodes++
			agg.TotalQPS += clone.QPS
			agg.TotalQueries += clone.QueriesTotal
			agg.TotalBlocked += clone.BlockedTotal
			cacheSum += clone.CacheHitPct
			activeCount++
		case "offline":
			agg.OfflineNodes++
		}
	}

	if activeCount > 0 {
		agg.AvgCacheHitPct = math.Round((cacheSum/float64(activeCount))*10) / 10
	}

	return list, agg
}

// DeleteNode removes a node from the cluster
func (cs *ClusterStore) DeleteNode(id string) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if _, ok := cs.nodes[id]; !ok {
		return errors.New("node tidak ditemukan")
	}
	delete(cs.nodes, id)
	return cs.save()
}

// ─── Master HTTP Handlers ───────────────────────────────────────────────────

var clusterStore *ClusterStore

func initClusterStore(filePath string) {
	clusterStore = newClusterStore(filePath)
	// Background ticker to keep node statuses fresh
	go func() {
		t := time.NewTicker(30 * time.Second)
		for range t.C {
			clusterStore.mu.Lock()
			now := time.Now()
			for _, n := range clusterStore.nodes {
				clusterStore.refreshNodeStatus(n, now)
			}
			clusterStore.mu.Unlock()
		}
	}()
}

// handleClusterToken creates a new enrollment token (Protected via JWT)
func handleClusterToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Minutes int `json:"minutes"`
		Hours   int `json:"hours"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var duration time.Duration
	switch {
	case body.Minutes > 0:
		duration = time.Duration(body.Minutes) * time.Minute
	case body.Hours > 0:
		duration = time.Duration(body.Hours) * time.Hour
	default:
		duration = 24 * time.Hour
	}
	tok := clusterStore.GenerateEnrollmentToken(duration)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token":      tok,
		"expires_in": duration.String(),
	})
}

// handleClusterRegister registers an edge node with an enrollment token (Public)
func handleClusterRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid json payload")
		return
	}
	rec, err := clusterStore.RegisterNode(req, clientIP(r))
	if err != nil {
		jsonErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(RegisterResponse{
		NodeID:  rec.ID,
		NodeKey: rec.Key,
		Name:    rec.Name,
	})
}

// handleClusterHeartbeat receives periodic telemetry from edge nodes (Public + NodeKey Auth)
func handleClusterHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid json payload")
		return
	}
	if err := clusterStore.ProcessHeartbeat(req, clientIP(r)); err != nil {
		jsonErr(w, http.StatusUnauthorized, err.Error())
		return
	}

	// Read master current DB hash if available
	var masterDBHash string
	if *flagFilesDir != "" {
		mfPath := filepath.Join(*flagFilesDir, "manifest.json")
		if b, err := os.ReadFile(mfPath); err == nil {
			var mf struct {
				SHA256 string `json:"sha256"`
			}
			if json.Unmarshal(b, &mf) == nil {
				masterDBHash = mf.SHA256
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(HeartbeatResponse{
		OK:             true,
		AcknowledgedAt: time.Now().UTC(),
		MasterDBHash:   masterDBHash,
	})
}

// handleClusterNodes lists all nodes & aggregate stats, or deletes a node (Protected via JWT)
func handleClusterNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		nodes, agg := clusterStore.ListNodes()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"nodes":     nodes,
			"aggregate": agg,
		})
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			jsonErr(w, http.StatusBadRequest, "id parameter required")
			return
		}
		if err := clusterStore.DeleteNode(id); err != nil {
			jsonErr(w, http.StatusNotFound, err.Error())
			return
		}
		jsonOK(w)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "GET or DELETE only")
	}
}

// ─── Edge Cluster Agent ───────────────────────────────────────────────────────

type EdgeAgentState struct {
	NodeID        string `json:"node_id"`
	NodeKey       string `json:"node_key"`
	MasterURL     string `json:"master_url"`
	Name          string `json:"name"`
	LastHeartbeat string `json:"last_heartbeat,omitempty"`
	LastError     string `json:"last_error,omitempty"`
}

type EdgeClusterAgent struct {
	mu         sync.RWMutex
	stateFile  string
	masterURL  string
	enrollTok  string
	nodeName   string
	interval   time.Duration
	running    bool
	stopCh     chan struct{}
	state      EdgeAgentState
	httpClient *http.Client
}

var edgeAgent *EdgeClusterAgent

func (a *EdgeClusterAgent) ensureRunning() {
	a.mu.Lock()
	if a.running || a.interval <= 0 {
		a.mu.Unlock()
		return
	}
	a.running = true
	stopCh := make(chan struct{})
	a.stopCh = stopCh
	a.mu.Unlock()
	go a.run(stopCh)
}

func (a *EdgeClusterAgent) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.running {
		return
	}
	a.running = false
	if a.stopCh != nil {
		close(a.stopCh)
		a.stopCh = nil
	}
}

func initEdgeAgent(stateFile, masterURL, enrollTok, nodeName string, interval time.Duration) {
	edgeAgent = &EdgeClusterAgent{
		stateFile:  stateFile,
		masterURL:  strings.TrimRight(strings.TrimSpace(masterURL), "/"),
		enrollTok:  strings.TrimSpace(enrollTok),
		nodeName:   strings.TrimSpace(nodeName),
		interval:   interval,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
	edgeAgent.loadState()

	// Jika masterURL dikonfigurasi via flag atau state, jalankan loop agent
	if edgeAgent.masterURL != "" || edgeAgent.state.MasterURL != "" {
		edgeAgent.ensureRunning()
	}
}

func (a *EdgeClusterAgent) loadState() {
	a.mu.Lock()
	defer a.mu.Unlock()

	b, err := os.ReadFile(a.stateFile)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &a.state)
	if a.state.MasterURL != "" && a.masterURL == "" {
		a.masterURL = a.state.MasterURL
	}
}

func (a *EdgeClusterAgent) saveState() error {
	if a.stateFile == "" {
		return nil
	}
	b, err := json.MarshalIndent(a.state, "", "  ")
	if err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(a.stateFile), 0o755)
	return atomicWriteString(a.stateFile, string(b), 0o600)
}

func (a *EdgeClusterAgent) getEdgeDBHash() (hash, updatedAt string) {
	// 1. Coba baca blacklist.db.manifest.json
	for _, p := range []string{
		"/var/lib/dnsdist/blacklist.db.manifest.json",
		"/var/lib/dnsdist/manifest.json",
	} {
		if b, err := os.ReadFile(p); err == nil {
			var mf struct {
				SHA256  string `json:"sha256"`
				BuiltAt string `json:"built_at"`
			}
			if json.Unmarshal(b, &mf) == nil && mf.SHA256 != "" {
				return mf.SHA256, mf.BuiltAt
			}
		}
	}

	// 2. Coba baca symlink target blacklist.db -> blacklist.<sha>.db
	if target, err := os.Readlink("/var/lib/dnsdist/blacklist.db"); err == nil {
		base := filepath.Base(target)
		if strings.HasPrefix(base, "blacklist.") && strings.HasSuffix(base, ".db") {
			parts := strings.Split(base, ".")
			if len(parts) >= 3 {
				return parts[1], ""
			}
		}
	}

	return "", ""
}

func (a *EdgeClusterAgent) Register(targetURL, token, name string) error {
	a.mu.Lock()
	targetURL = strings.TrimRight(strings.TrimSpace(targetURL), "/")
	if targetURL == "" {
		a.mu.Unlock()
		return errors.New("master URL tidak boleh kosong")
	}
	if name == "" {
		name = a.nodeName
	}
	if name == "" {
		h, err := os.Hostname()
		if err == nil && h != "" {
			name = h
		} else {
			name = "edge-" + randHex(4)
		}
	}
	a.mu.Unlock()

	reqBody := RegisterRequest{
		EnrollToken: token,
		Name:        name,
		Version:     "2.6.0",
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	resp, err := a.httpClient.Post(targetURL+"/api/cluster/register", "application/json", strings.NewReader(string(b)))
	if err != nil {
		a.mu.Lock()
		a.state.LastError = "koneksi master gagal: " + err.Error()
		_ = a.saveState()
		a.mu.Unlock()
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		msg := errResp.Error
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		a.mu.Lock()
		a.state.LastError = "pendaftaran ditolak: " + msg
		_ = a.saveState()
		a.mu.Unlock()
		return errors.New(msg)
	}

	var regResp RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		return err
	}

	a.mu.Lock()
	a.masterURL = targetURL
	a.state.MasterURL = targetURL
	a.state.NodeID = regResp.NodeID
	a.state.NodeKey = regResp.NodeKey
	a.state.Name = regResp.Name
	a.state.LastError = ""
	_ = a.saveState()
	a.mu.Unlock()

	log.Printf("[cluster-agent] Berhasil mendaftar ke Master (%s) dengan Node ID: %s", targetURL, regResp.NodeID)
	// Pastikan background heartbeat runner aktif
	a.ensureRunning()
	// Kirim heartbeat perdana
	_ = a.sendHeartbeat()
	return nil
}

func (a *EdgeClusterAgent) sendHeartbeat() error {
	a.mu.RLock()
	masterURL := a.masterURL
	nodeID := a.state.NodeID
	nodeKey := a.state.NodeKey
	nodeName := a.state.Name
	a.mu.RUnlock()

	if masterURL == "" || nodeID == "" || nodeKey == "" {
		return errors.New("node belum terdaftar")
	}

	memUsed, memTotal := memMB()
	dbHash, dbUpdated := a.getEdgeDBHash()

	qTotal := stats.queriesTotal.Load()
	if qTotal == 0 {
		qTotal = int64(stats.udpInTotal.Load())
	}

	hbReq := HeartbeatRequest{
		NodeID:         nodeID,
		NodeKey:        nodeKey,
		Name:           nodeName,
		Version:        "2.6.0",
		QPS:            stats.qps.Load(),
		QueriesTotal:   qTotal,
		BlockedTotal:   stats.blockedTotal.Load(),
		CacheHitPct:    math.Round(float64(stats.cacheHitPct.Load())/100*10) / 10,
		CPUPct:         math.Round(cpuPercent()*10) / 10,
		MemUsedMB:      memUsed,
		MemTotalMB:     memTotal,
		UptimeSec:      uptimeSec(),
		DnsdistRunning: dnsdistRunning(),
		DBHash:         dbHash,
		DBUpdatedAt:    dbUpdated,
	}

	b, err := json.Marshal(hbReq)
	if err != nil {
		return err
	}

	resp, err := a.httpClient.Post(masterURL+"/api/cluster/heartbeat", "application/json", strings.NewReader(string(b)))
	if err != nil {
		a.mu.Lock()
		a.state.LastError = "heartbeat gagal: " + err.Error()
		_ = a.saveState()
		a.mu.Unlock()
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		msg := errResp.Error
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		a.mu.Lock()
		a.state.LastError = "heartbeat ditolak: " + msg
		_ = a.saveState()
		a.mu.Unlock()
		return errors.New(msg)
	}

	var hbResp HeartbeatResponse
	_ = json.NewDecoder(resp.Body).Decode(&hbResp)

	a.mu.Lock()
	a.state.LastHeartbeat = time.Now().UTC().Format(time.RFC3339)
	a.state.LastError = ""
	_ = a.saveState()
	a.mu.Unlock()

	return nil
}

func (a *EdgeClusterAgent) run(stopCh <-chan struct{}) {
	log.Printf("[cluster-agent] Edge cluster agent aktif (interval: %v)", a.interval)

	// Cek apakah perlu auto-register saat startup
	a.mu.RLock()
	needRegister := (a.state.NodeID == "" || a.state.NodeKey == "") && a.enrollTok != "" && a.masterURL != ""
	a.mu.RUnlock()

	if needRegister {
		log.Printf("[cluster-agent] Mencoba mendaftar otomatis ke Master: %s...", a.masterURL)
		if err := a.Register(a.masterURL, a.enrollTok, a.nodeName); err != nil {
			log.Printf("[cluster-agent] Pendaftaran awal gagal: %v (akan dicoba kembali)", err)
		}
	} else if a.state.NodeID != "" {
		_ = a.sendHeartbeat()
	}

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			a.mu.RLock()
			enrolled := a.state.NodeID != "" && a.state.NodeKey != ""
			a.mu.RUnlock()

			if !enrolled {
				a.mu.RLock()
				canRetryReg := a.enrollTok != "" && a.masterURL != ""
				mURL := a.masterURL
				tok := a.enrollTok
				name := a.nodeName
				a.mu.RUnlock()

				if canRetryReg {
					if err := a.Register(mURL, tok, name); err != nil {
						log.Printf("[cluster-agent] Retry register gagal: %v", err)
					}
				}
				continue
			}

			if err := a.sendHeartbeat(); err != nil {
				log.Printf("[cluster-agent] Heartbeat warning: %v", err)
			}
		}
	}
}

// handleEdgeClusterConfig serves GET status & POST connect for Edge Web Panel
func handleEdgeClusterConfig(w http.ResponseWriter, r *http.Request) {
	if edgeAgent == nil {
		jsonErr(w, http.StatusNotFound, "cluster agent not initialized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		edgeAgent.mu.RLock()
		enrolled := edgeAgent.state.NodeID != "" && edgeAgent.state.NodeKey != ""
		data := map[string]any{
			"enrolled":       enrolled,
			"node_id":        edgeAgent.state.NodeID,
			"name":           edgeAgent.state.Name,
			"master_url":     edgeAgent.masterURL,
			"last_heartbeat": edgeAgent.state.LastHeartbeat,
			"last_error":     edgeAgent.state.LastError,
		}
		edgeAgent.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)

	case http.MethodPost:
		var body struct {
			MasterURL   string `json:"master_url"`
			EnrollToken string `json:"enroll_token"`
			NodeName    string `json:"node_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid json payload")
			return
		}
		if body.MasterURL == "" || body.EnrollToken == "" {
			jsonErr(w, http.StatusBadRequest, "master_url dan enroll_token wajib diisi")
			return
		}
		masterURL, err := validateMasterURL(body.MasterURL)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := edgeAgent.Register(masterURL, body.EnrollToken, body.NodeName); err != nil {
			jsonErr(w, http.StatusBadRequest, "pendaftaran gagal: "+err.Error())
			return
		}
		jsonOK(w)

	default:
		jsonErr(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}
