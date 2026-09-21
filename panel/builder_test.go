package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/colinmarc/cdb"
	"github.com/miekg/dns"
)

func TestBuildMasterCDBAppliesWhitelist(t *testing.T) {
	body := []byte("||blocked.example^\n||allowed.example^\n2001:0db8::1\n2001:0db8::2\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	sources := filepath.Join(dir, "sources.txt")
	whitelist := filepath.Join(dir, "whitelist.txt")
	if err := os.WriteFile(sources, []byte(server.URL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(whitelist, []byte("allowed.example\n2001:db8::1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldLocalDBDir := localDBDir
	localDBDir = filepath.Join(dir, "local-db")
	defer func() { localDBDir = oldLocalDBDir }()
	if err := os.MkdirAll(localDBDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := BuildMasterCDB(dir, sources, whitelist, "", 1, true); err != nil {
		t.Fatal(err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "trust.*.db"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("hashed CDB files = %v, err = %v", matches, err)
	}
	reader, err := cdb.Open(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	key := func(domain string) []byte {
		buf := make([]byte, 256)
		n, err := dns.PackDomainName(domain+".", buf, 0, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		return buf[:n]
	}
	blocked, err := reader.Get(key("blocked.example"))
	if err != nil || blocked == nil {
		t.Fatalf("blocked entry missing: value=%v err=%v", blocked, err)
	}
	allowed, err := reader.Get(key("allowed.example"))
	if err != nil || allowed != nil {
		t.Fatalf("whitelisted entry present: value=%v err=%v", allowed, err)
	}
	ipKey := func(ip string) []byte { return append([]byte(ip), 0) }
	for _, ip := range []string{"2001:0db8::1", "2001:db8::1"} {
		allowedIP, err := reader.Get(ipKey(ip))
		if err != nil || allowedIP != nil {
			t.Fatalf("whitelisted IPv6 entry %q present: value=%v err=%v", ip, allowedIP, err)
		}
	}
	blockedIP, err := reader.Get(ipKey("2001:db8::2"))
	if err != nil || blockedIP == nil {
		t.Fatalf("blocked IPv6 entry missing: value=%v err=%v", blockedIP, err)
	}
}

func TestCleanupVersionedDBsKeepsActiveFileOnly(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, "trust."+strings.Repeat("a", 64)+".db")
	old := filepath.Join(dir, "trust."+strings.Repeat("b", 64)+".db")
	unrelated := filepath.Join(dir, "trust.backup.db")
	for _, path := range []string{active, old, unrelated} {
		if err := os.WriteFile(path, []byte("db"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := cleanupVersionedDBs(dir, "trust", active)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active DB removed: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old DB still exists: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated file removed: %v", err)
	}
}
