package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// newTestPostgresStore connects to the PostgreSQL server named by
// PANEL_TEST_DATABASE_URL, or skips when it is not configured.
// CI has no database; this test runs only on machines that opt in.
func newTestPostgresStore(t *testing.T) *PostgresClusterStore {
	t.Helper()
	dsn := os.Getenv("PANEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PANEL_TEST_DATABASE_URL not set; skipping PostgreSQL integration test")
	}
	store, err := NewPostgresClusterStore(dsn)
	if err != nil {
		t.Fatalf("failed to create postgres cluster store: %v", err)
	}
	return store
}

// cleanupNode removes any node created by the test so reruns stay clean.
func cleanupNode(t *testing.T, store *PostgresClusterStore, id string) {
	t.Helper()
	if err := store.DeleteNode(id); err != nil && !strings.Contains(err.Error(), "tidak ditemukan") {
		t.Logf("cleanup DeleteNode(%s): %v", id, err)
	}
}

func TestPostgresClusterStoreLifecycle(t *testing.T) {
	store := newTestPostgresStore(t)
	defer store.Close()

	tok := store.GenerateEnrollmentToken(1 * time.Hour)
	if tok == "" {
		t.Fatal("expected non-empty enrollment token")
	}

	if _, err := store.RegisterNode(RegisterRequest{
		EnrollToken: "invalid-token",
		Name:        "pg-edge-invalid",
	}, "10.20.0.2"); err == nil {
		t.Fatal("expected error on invalid token, got nil")
	}

	node, err := store.RegisterNode(RegisterRequest{
		EnrollToken: tok,
		Name:        "pg-edge-01",
		Version:     "3.0.0",
		ReportedIP:  "10.20.0.2",
	}, "10.20.0.2")
	if err != nil {
		t.Fatalf("expected successful registration: %v", err)
	}
	defer cleanupNode(t, store, node.ID)

	if node.ID == "" || node.Key == "" {
		t.Fatal("expected node ID and Key to be generated")
	}
	if store.ValidateAndConsumeToken(tok) {
		t.Fatal("expected token to be consumed after registration")
	}

	if err := store.ProcessHeartbeat(HeartbeatRequest{
		NodeID:  node.ID,
		NodeKey: "wrong-key",
	}, "10.20.0.2"); err == nil {
		t.Fatal("expected error on wrong node key")
	}

	if err := store.ProcessHeartbeat(HeartbeatRequest{
		NodeID:         node.ID,
		NodeKey:        node.Key,
		QPS:            1234,
		QueriesTotal:   5678,
		BlockedTotal:   90,
		CacheHitPct:    66.5,
		CPUPct:         12.5,
		DnsdistRunning: true,
		Version:        "3.0.0",
	}, "10.20.0.2"); err != nil {
		t.Fatalf("expected successful heartbeat: %v", err)
	}

	nodes, agg := store.ListNodes()
	var found *NodeRecord
	for _, n := range nodes {
		if n.ID == node.ID {
			found = n
			break
		}
	}
	if found == nil {
		t.Fatal("registered node missing from ListNodes")
	}
	if found.Key != "" {
		t.Fatal("ListNodes must not expose node key")
	}
	if found.QPS != 1234 || found.BlockedTotal != 90 {
		t.Fatalf("telemetry not persisted: qps=%d blocked=%d", found.QPS, found.BlockedTotal)
	}
	if found.Status != "online" {
		t.Fatalf("expected online status, got %q", found.Status)
	}
	if agg.TotalNodes == 0 {
		t.Fatal("expected aggregate to count nodes")
	}

	if err := store.DeleteNode(node.ID); err != nil {
		t.Fatalf("expected successful delete: %v", err)
	}
	if err := store.DeleteNode(node.ID); err == nil {
		t.Fatal("expected error deleting missing node")
	}
}

func TestPostgresDSNFromEnv(t *testing.T) {
	t.Setenv("PANEL_DATABASE_URL", "")
	t.Setenv("PANEL_DB_HOST", "")
	t.Setenv("PANEL_DB_USER", "")
	t.Setenv("PANEL_DB_NAME", "")

	if got := postgresDSNFromEnv("explicit-dsn"); got != "explicit-dsn" {
		t.Fatalf("explicit DSN must win, got %q", got)
	}

	t.Setenv("PANEL_DATABASE_URL", "postgres://url-wins")
	if got := postgresDSNFromEnv(""); got != "postgres://url-wins" {
		t.Fatalf("PANEL_DATABASE_URL must win over parts, got %q", got)
	}

	t.Setenv("PANEL_DATABASE_URL", "")
	t.Setenv("PANEL_DB_HOST", "db.internal")
	t.Setenv("PANEL_DB_USER", "panel")
	t.Setenv("PANEL_DB_PASSWORD", "p@ss w/ord")
	t.Setenv("PANEL_DB_NAME", "trust")
	if got := postgresDSNFromEnv(""); !strings.Contains(got, "postgres://panel:p%40ss%20w%2Ford@db.internal:5432/trust") {
		t.Fatalf("component DSN malformed or unescaped: %q", got)
	}

	// Missing host means "not configured", so fall back to other engines.
	t.Setenv("PANEL_DB_HOST", "")
	if got := postgresDSNFromEnv(""); got != "" {
		t.Fatalf("expected empty DSN when host missing, got %q", got)
	}
}
