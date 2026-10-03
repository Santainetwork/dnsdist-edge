// Postgres cluster storage adapter for central master deployments.
//
// Uses the pgx/v5 pool (pure Go, no CGO) and follows the same
// ClusterStorage contract as ClusterStore (JSON) and SQLiteClusterStore.
// Schema is auto-created on first use; timestamps travel in UTC.

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresClusterStore implements ClusterStorage backed by PostgreSQL.
type PostgresClusterStore struct {
	mu sync.RWMutex
	db *sql.DB
}

// NewPostgresClusterStore opens a PostgreSQL pool and ensures the schema exists.
// dsn is a postgres:// URL or a libpq key/value string.
func NewPostgresClusterStore(dsn string) (*PostgresClusterStore, error) {
	if dsn == "" {
		return nil, errors.New("postgres DSN kosong")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)

	store := &PostgresClusterStore{db: db}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres (apakah server aktif?): %w", err)
	}
	if err := store.initSchema(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("init postgres schema: %w", err)
	}
	return store, nil
}

func (s *PostgresClusterStore) initSchema(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS cluster_tokens (
		token       TEXT PRIMARY KEY,
		expires_at  TIMESTAMPTZ NOT NULL
	);

	CREATE TABLE IF NOT EXISTS cluster_nodes (
		id             TEXT PRIMARY KEY,
		node_key       TEXT NOT NULL,
		name           TEXT NOT NULL,
		ip             TEXT NOT NULL,
		version        TEXT NOT NULL,
		status         TEXT NOT NULL,
		first_seen_at  TIMESTAMPTZ NOT NULL,
		last_seen_at   TIMESTAMPTZ NOT NULL,
		qps            BIGINT NOT NULL DEFAULT 0,
		queries_total  BIGINT NOT NULL DEFAULT 0,
		blocked_total  BIGINT NOT NULL DEFAULT 0,
		cache_hit_pct  DOUBLE PRECISION NOT NULL DEFAULT 0,
		cpu_pct        DOUBLE PRECISION NOT NULL DEFAULT 0,
		mem_used_mb    BIGINT NOT NULL DEFAULT 0,
		mem_total_mb   BIGINT NOT NULL DEFAULT 0,
		uptime_sec     BIGINT NOT NULL DEFAULT 0,
		dnsdist_running SMALLINT NOT NULL DEFAULT 0,
		db_hash        TEXT NOT NULL DEFAULT '',
		db_updated_at  TEXT NOT NULL DEFAULT ''
	);

	CREATE INDEX IF NOT EXISTS idx_cluster_nodes_last_seen ON cluster_nodes(last_seen_at);
	`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func (s *PostgresClusterStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

func (s *PostgresClusterStore) GenerateEnrollmentToken(duration time.Duration) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if duration <= 0 {
		duration = 24 * time.Hour
	}
	tok := "enroll-" + randHex(16)
	expiresAt := time.Now().Add(duration).UTC()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = s.db.ExecContext(ctx,
		"INSERT INTO cluster_tokens (token, expires_at) VALUES ($1, $2)",
		tok, expiresAt)
	return tok
}

func (s *PostgresClusterStore) ValidateAndConsumeToken(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	tok = strings.TrimSpace(tok)
	if tok == "" {
		return false
	}

	var expiresAt time.Time
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := s.db.QueryRowContext(ctx,
		"SELECT expires_at FROM cluster_tokens WHERE token = $1", tok).
		Scan(&expiresAt)
	if err != nil {
		return false
	}

	// One-time use: delete regardless of expiry.
	_, _ = s.db.ExecContext(ctx, "DELETE FROM cluster_tokens WHERE token = $1", tok)

	return time.Now().Before(expiresAt)
}

func (s *PostgresClusterStore) RegisterNode(req RegisterRequest, remoteIP string) (*NodeRecord, error) {
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

	now := time.Now().UTC()
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cluster_nodes (
			id, node_key, name, ip, version, status,
			first_seen_at, last_seen_at, qps, queries_total, blocked_total,
			cache_hit_pct, cpu_pct, mem_used_mb, mem_total_mb, uptime_sec,
			dnsdist_running, db_hash, db_updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, 0, 0, 0, 0, 0, 0, 0, 0, '', '')
	`, rec.ID, rec.Key, rec.Name, rec.IP, rec.Version, rec.Status, rec.FirstSeenAt, rec.LastSeenAt)
	if err != nil {
		return nil, fmt.Errorf("register node: %w", err)
	}
	return rec, nil
}

func (s *PostgresClusterStore) ProcessHeartbeat(req HeartbeatRequest, remoteIP string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var existingKey, existingName, existingIP, existingVersion string
	var firstSeenAt time.Time
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := s.db.QueryRowContext(ctx, `
		SELECT node_key, name, ip, version, first_seen_at
		FROM cluster_nodes WHERE id = $1`, req.NodeID).
		Scan(&existingKey, &existingName, &existingIP, &existingVersion, &firstSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("node tidak terdaftar")
	} else if err != nil {
		return fmt.Errorf("query node: %w", err)
	}

	if existingKey != req.NodeKey {
		return errors.New("node key tidak cocok")
	}

	now := time.Now().UTC()
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

	_, err = s.db.ExecContext(ctx, `
		UPDATE cluster_nodes SET
			name = $1, ip = $2, version = $3, status = $4, last_seen_at = $5,
			qps = $6, queries_total = $7, blocked_total = $8, cache_hit_pct = $9,
			cpu_pct = $10, mem_used_mb = $11, mem_total_mb = $12, uptime_sec = $13,
			dnsdist_running = $14, db_hash = $15, db_updated_at = $16
		WHERE id = $17
	`,
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

func (s *PostgresClusterStore) ListNodes() ([]*NodeRecord, ClusterAggregate) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var list []*NodeRecord
	var agg ClusterAggregate

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
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
		var memUsed, memTotal int64
		err := rows.Scan(
			&n.ID, &n.Name, &n.IP, &n.Version, &n.Status, &n.FirstSeenAt, &n.LastSeenAt,
			&n.QPS, &n.QueriesTotal, &n.BlockedTotal, &n.CacheHitPct, &n.CPUPct,
			&memUsed, &memTotal, &n.UptimeSec, &dnsdistRunningInt,
			&n.DBHash, &n.DBUpdatedAt,
		)
		if err != nil {
			continue
		}
		n.MemUsedMB = uint64(memUsed)
		n.MemTotalMB = uint64(memTotal)
		n.DnsdistRunning = (dnsdistRunningInt == 1)

		// Dynamic freshness check (same policy as other engines).
		if n.LastSeenAt.IsZero() || now.Sub(n.LastSeenAt) > 3*time.Minute {
			n.Status = "offline"
		} else if !n.DnsdistRunning {
			n.Status = "degraded"
		} else {
			n.Status = "online"
		}

		n.Key = ""
		list = append(list, &n)

		agg.TotalNodes++
		switch n.Status {
		case "online", "degraded":
			if n.Status == "online" {
				agg.OnlineNodes++
			} else {
				agg.DegradedNodes++
			}
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

func (s *PostgresClusterStore) DeleteNode(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := s.db.ExecContext(ctx, "DELETE FROM cluster_nodes WHERE id = $1", id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return errors.New("node tidak ditemukan")
	}
	return nil
}

var _ ClusterStorage = (*PostgresClusterStore)(nil)

// postgresDSNFromEnv resolves the active Postgres DSN:
// explicit flag first, then PANEL_DATABASE_URL, then PANEL_DB_* components.
func postgresDSNFromEnv(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("PANEL_DATABASE_URL"); v != "" {
		return v
	}
	host := os.Getenv("PANEL_DB_HOST")
	port := os.Getenv("PANEL_DB_PORT")
	user := os.Getenv("PANEL_DB_USER")
	pass := os.Getenv("PANEL_DB_PASSWORD")
	dbname := os.Getenv("PANEL_DB_NAME")
	if host == "" || user == "" || dbname == "" {
		return ""
	}
	if port == "" {
		port = "5432"
	}
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=require",
		urlQueryEscape(user), urlQueryEscape(pass), host, port, dbname)
	return dsn
}

func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteRune(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
