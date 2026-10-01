package main

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// ClusterStorage defines the storage interface for multi-node cluster management.
// It allows pluggable backends: JSON file (default edge), SQLite (edge/standalone embedded),
// and PostgreSQL (central master).
type ClusterStorage interface {
	GenerateEnrollmentToken(duration time.Duration) string
	ValidateAndConsumeToken(tok string) bool
	RegisterNode(req RegisterRequest, remoteIP string) (*NodeRecord, error)
	ProcessHeartbeat(req HeartbeatRequest, remoteIP string) error
	ListNodes() ([]*NodeRecord, ClusterAggregate)
	DeleteNode(id string) error
	Close() error
}

var _ ClusterStorage = (*ClusterStore)(nil)

// SQLiteClusterStore implements ClusterStorage backed by pure-Go SQLite (modernc.org/sqlite).
type SQLiteClusterStore struct {
	mu sync.RWMutex
	db *sql.DB
}

// NewSQLiteClusterStore creates or opens a SQLite database for cluster state.
func NewSQLiteClusterStore(dbPath string) (*SQLiteClusterStore, error) {
	if dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file:") {
		dir := filepath.Dir(dbPath)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("mkdir sqlite dir: %w", err)
			}
		}
	}

	dsn := dbPath
	if dbPath != ":memory:" && !strings.Contains(dsn, "_pragma") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		// Enable WAL mode and busy timeout for concurrent safety
		dsn = fmt.Sprintf("%s%s_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", dsn, sep)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	// Single writer connection pool for SQLite to prevent database locked errors
	db.SetMaxOpenConns(1)

	store := &SQLiteClusterStore{db: db}
	if err := store.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init sqlite schema: %w", err)
	}

	return store, nil
}

func (s *SQLiteClusterStore) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS cluster_tokens (
		token TEXT PRIMARY KEY,
		expires_at TIMESTAMP NOT NULL
	);

	CREATE TABLE IF NOT EXISTS cluster_nodes (
		id TEXT PRIMARY KEY,
		node_key TEXT NOT NULL,
		name TEXT NOT NULL,
		ip TEXT NOT NULL,
		version TEXT NOT NULL,
		status TEXT NOT NULL,
		first_seen_at TIMESTAMP NOT NULL,
		last_seen_at TIMESTAMP NOT NULL,
		qps INTEGER NOT NULL DEFAULT 0,
		queries_total INTEGER NOT NULL DEFAULT 0,
		blocked_total INTEGER NOT NULL DEFAULT 0,
		cache_hit_pct REAL NOT NULL DEFAULT 0,
		cpu_pct REAL NOT NULL DEFAULT 0,
		mem_used_mb INTEGER NOT NULL DEFAULT 0,
		mem_total_mb INTEGER NOT NULL DEFAULT 0,
		uptime_sec INTEGER NOT NULL DEFAULT 0,
		dnsdist_running INTEGER NOT NULL DEFAULT 0,
		db_hash TEXT NOT NULL DEFAULT '',
		db_updated_at TEXT NOT NULL DEFAULT ''
	);

	CREATE INDEX IF NOT EXISTS idx_cluster_nodes_last_seen ON cluster_nodes(last_seen_at);
	`
	_, err := s.db.Exec(schema)
	return err
}

func (s *SQLiteClusterStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

func (s *SQLiteClusterStore) GenerateEnrollmentToken(duration time.Duration) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if duration <= 0 {
		duration = 24 * time.Hour
	}
	tok := "enroll-" + randHex(16)
	expiresAt := time.Now().Add(duration)

	_, _ = s.db.Exec("INSERT INTO cluster_tokens (token, expires_at) VALUES (?, ?)", tok, expiresAt)
	return tok
}

func (s *SQLiteClusterStore) ValidateAndConsumeToken(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	tok = strings.TrimSpace(tok)
	if tok == "" {
		return false
	}

	var expiresAt time.Time
	err := s.db.QueryRow("SELECT expires_at FROM cluster_tokens WHERE token = ?", tok).Scan(&expiresAt)
	if err != nil {
		return false
	}

	// Always delete after check (one-time use or expired)
	_, _ = s.db.Exec("DELETE FROM cluster_tokens WHERE token = ?", tok)

	return time.Now().Before(expiresAt)
}

func (s *SQLiteClusterStore) RegisterNode(req RegisterRequest, remoteIP string) (*NodeRecord, error) {
	if !s.ValidateAndConsumeToken(req.EnrollToken) {
		return nil, errors.New("token pendaftaran tidak valid atau telah kedaluwarsa")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

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

	query := `
	INSERT INTO cluster_nodes (
		id, node_key, name, ip, version, status,
		first_seen_at, last_seen_at, qps, queries_total, blocked_total,
		cache_hit_pct, cpu_pct, mem_used_mb, mem_total_mb, uptime_sec,
		dnsdist_running, db_hash, db_updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0, 0, 0, 0, 0, 0, '', '')
	`
	_, err := s.db.Exec(query, rec.ID, rec.Key, rec.Name, rec.IP, rec.Version, rec.Status, rec.FirstSeenAt, rec.LastSeenAt)
	if err != nil {
		return nil, fmt.Errorf("register node: %w", err)
	}

	return rec, nil
}

func (s *SQLiteClusterStore) ProcessHeartbeat(req HeartbeatRequest, remoteIP string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var existingKey, existingName, existingIP, existingVersion string
	var firstSeenAt time.Time
	err := s.db.QueryRow(`SELECT node_key, name, ip, version, first_seen_at FROM cluster_nodes WHERE id = ?`, req.NodeID).
		Scan(&existingKey, &existingName, &existingIP, &existingVersion, &firstSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("node tidak terdaftar")
	} else if err != nil {
		return fmt.Errorf("query node: %w", err)
	}

	if existingKey != req.NodeKey {
		return errors.New("node key tidak cocok")
	}

	now := time.Now()
	name := existingName
	if req.Name != "" {
		name = req.Name
	}

	ip := existingIP
	if req.ReportedIP != "" {
		ip = req.ReportedIP
	} else if remoteIP != "" {
		ip = remoteIP
	}

	version := existingVersion
	if req.Version != "" {
		version = req.Version
	}

	dnsdistRunningInt := 0
	if req.DnsdistRunning {
		dnsdistRunningInt = 1
	}

	status := "online"
	if !req.DnsdistRunning {
		status = "degraded"
	}

	updateQuery := `
	UPDATE cluster_nodes SET
		name = ?, ip = ?, version = ?, status = ?, last_seen_at = ?,
		qps = ?, queries_total = ?, blocked_total = ?, cache_hit_pct = ?,
		cpu_pct = ?, mem_used_mb = ?, mem_total_mb = ?, uptime_sec = ?,
		dnsdist_running = ?, db_hash = ?, db_updated_at = ?
	WHERE id = ?
	`
	_, err = s.db.Exec(updateQuery,
		name, ip, version, status, now,
		req.QPS, req.QueriesTotal, req.BlockedTotal, req.CacheHitPct,
		req.CPUPct, req.MemUsedMB, req.MemTotalMB, req.UptimeSec,
		dnsdistRunningInt, req.DBHash, req.DBUpdatedAt,
		req.NodeID,
	)
	if err != nil {
		return fmt.Errorf("update heartbeat: %w", err)
	}

	return nil
}

func (s *SQLiteClusterStore) ListNodes() ([]*NodeRecord, ClusterAggregate) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var list []*NodeRecord
	var agg ClusterAggregate

	rows, err := s.db.Query(`
		SELECT id, name, ip, version, status, first_seen_at, last_seen_at,
		       qps, queries_total, blocked_total, cache_hit_pct, cpu_pct,
		       mem_used_mb, mem_total_mb, uptime_sec, dnsdist_running,
		       db_hash, db_updated_at
		FROM cluster_nodes
	`)
	if err != nil {
		return list, agg
	}
	defer rows.Close()

	var cacheSum float64
	var activeCount int

	for rows.Next() {
		var n NodeRecord
		var dnsdistRunningInt int
		err := rows.Scan(
			&n.ID, &n.Name, &n.IP, &n.Version, &n.Status, &n.FirstSeenAt, &n.LastSeenAt,
			&n.QPS, &n.QueriesTotal, &n.BlockedTotal, &n.CacheHitPct, &n.CPUPct,
			&n.MemUsedMB, &n.MemTotalMB, &n.UptimeSec, &dnsdistRunningInt,
			&n.DBHash, &n.DBUpdatedAt,
		)
		if err != nil {
			continue
		}
		n.DnsdistRunning = (dnsdistRunningInt == 1)

		// Dynamic freshness check
		if n.LastSeenAt.IsZero() || now.Sub(n.LastSeenAt) > 3*time.Minute {
			n.Status = "offline"
		} else if !n.DnsdistRunning {
			n.Status = "degraded"
		} else {
			n.Status = "online"
		}

		// Omit Key in returned list
		n.Key = ""
		list = append(list, &n)

		agg.TotalNodes++
		switch n.Status {
		case "online":
			agg.OnlineNodes++
			agg.TotalQPS += n.QPS
			agg.TotalQueries += n.QueriesTotal
			agg.TotalBlocked += n.BlockedTotal
			cacheSum += n.CacheHitPct
			activeCount++
		case "degraded":
			agg.DegradedNodes++
			agg.TotalQPS += n.QPS
			agg.TotalQueries += n.QueriesTotal
			agg.TotalBlocked += n.BlockedTotal
			cacheSum += n.CacheHitPct
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

func (s *SQLiteClusterStore) DeleteNode(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec("DELETE FROM cluster_nodes WHERE id = ?", id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return errors.New("node tidak ditemukan")
	}
	return nil
}

var _ ClusterStorage = (*SQLiteClusterStore)(nil)
