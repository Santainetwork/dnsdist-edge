package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const smartDistTestProfile = `{"id":"cfg-a","name":"A","smartdist":{"enabled":true,"rules":[` +
	`{"type":"ip_set","name":"cf","file":"/etc/smartdns/ipv4.txt"},` +
	`{"type":"cname","pattern":"-.api.example.com","target":"api.example.com.cdn.cloudflare.net."},` +
	`{"type":"alias","ip_set":"cf","targets":["1.1.1.1","2.2.2.2"],"exclude":"skip"}]}}`

func smartDistHash(b string) string {
	s := sha256.Sum256([]byte(b))
	return hex.EncodeToString(s[:])
}

// newSmartDistAgent membuat agent dengan master httptest yang menghitung fetch.
func newSmartDistAgent(t *testing.T, body string, hits *atomic.Int32) *EdgeClusterAgent {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/api/cluster/profile" || r.URL.Query().Get("node_id") != "n1" || r.URL.Query().Get("node_key") != "k1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	smartDistProfilePath = filepath.Join(t.TempDir(), "smartdist-profile.lua")
	t.Cleanup(func() { smartDistProfilePath = "/etc/dnsdist/smartdist-profile.lua" })
	return &EdgeClusterAgent{
		masterURL:  srv.URL,
		httpClient: srv.Client(),
		state:      EdgeAgentState{NodeID: "n1", NodeKey: "k1"},
	}
}

func TestSmartDistSameHashNoFetch(t *testing.T) {
	var hits atomic.Int32
	a := newSmartDistAgent(t, smartDistTestProfile, &hits)
	a.state.ProfileHash = smartDistHash(smartDistTestProfile)

	if err := a.syncSmartDistProfile("cfg-a", a.state.ProfileHash); err != nil {
		t.Fatalf("err: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("hash sama tetap fetch: %d", hits.Load())
	}
	if _, err := os.Stat(smartDistProfilePath); err == nil {
		t.Fatal("hash sama tetap menulis file")
	}
}

func TestSmartDistDifferentHashFetchAndWrite(t *testing.T) {
	var hits atomic.Int32
	a := newSmartDistAgent(t, smartDistTestProfile, &hits)
	want := smartDistHash(smartDistTestProfile)

	if err := a.syncSmartDistProfile("cfg-a", want); err != nil {
		t.Fatalf("err: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("fetch = %d, want 1", hits.Load())
	}
	b, err := os.ReadFile(smartDistProfilePath)
	if err != nil {
		t.Fatalf("file tidak ditulis: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		"SMARTDIST_ENABLED = true",
		`smartdns_ip_set("cf", "/etc/smartdns/ipv4.txt")`,
		`smartdns_cname("-.api.example.com", "api.example.com.cdn.cloudflare.net.")`,
		`smartdns_ip_rules_alias("cf", {"1.1.1.1", "2.2.2.2"}, "skip")`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("file tidak memuat %q\n%s", want, s)
		}
	}
	if a.state.ProfileHash != want {
		t.Errorf("hash tersimpan = %q, want %q", a.state.ProfileHash, want)
	}
	fi, _ := os.Stat(smartDistProfilePath)
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", fi.Mode().Perm())
	}
}

func TestSmartDistHashMismatchNoWrite(t *testing.T) {
	var hits atomic.Int32
	a := newSmartDistAgent(t, smartDistTestProfile, &hits)
	wrong := smartDistHash("isi lain")

	if err := a.syncSmartDistProfile("cfg-a", wrong); err == nil {
		t.Fatal("hash tidak cocok harus error")
	}
	if _, err := os.Stat(smartDistProfilePath); err == nil {
		t.Fatal("hash tidak cocok tetap menulis file")
	}
	if a.state.ProfileHash != "" {
		t.Errorf("hash tersimpan berubah: %q", a.state.ProfileHash)
	}
}

func TestSmartDistEmptyProfileDisables(t *testing.T) {
	var hits atomic.Int32
	a := newSmartDistAgent(t, smartDistTestProfile, &hits)
	a.state.ProfileHash = "sesuatu"

	if err := a.syncSmartDistProfile("", ""); err != nil {
		t.Fatalf("err: %v", err)
	}
	b, err := os.ReadFile(smartDistProfilePath)
	if err != nil {
		t.Fatalf("file tidak ditulis: %v", err)
	}
	if !strings.Contains(string(b), "SMARTDIST_ENABLED = false") {
		t.Errorf("tidak nonaktif:\n%s", b)
	}
	if hits.Load() != 0 {
		t.Errorf("profil kosong tetap fetch")
	}
	if a.state.ProfileHash != "" {
		t.Errorf("hash belum dikosongkan")
	}
}

func TestSmartDistGenerateLuaEscapesAndRejectsUnknown(t *testing.T) {
	out, err := generateSmartDistLua([]byte(`{"smartdist":{"enabled":false,"rules":[{"type":"cname","pattern":"a\"b\\c","target":"t"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `smartdns_cname("a\"b\\c", "t")`) {
		t.Errorf("escape salah:\n%s", out)
	}
	if !strings.Contains(out, "SMARTDIST_ENABLED = false") {
		t.Errorf("enabled salah:\n%s", out)
	}
	if _, err := generateSmartDistLua([]byte(`{"smartdist":{"rules":[{"type":"bogus"}]}}`)); err == nil {
		t.Error("tipe rule tak dikenal harus error")
	}
}
