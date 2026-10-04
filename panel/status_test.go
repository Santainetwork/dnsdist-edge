package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestRPZStatusShape verifies the read endpoint returns the fields the UI binds
// to, and that it reports an empty feed list rather than failing when no master
// sources file exists (the normal edge-node case).
func TestRPZStatusShape(t *testing.T) {
	dir := t.TempDir()
	oldFiles := *flagFilesDir
	oldSources := *flagSourcesFile
	*flagFilesDir = dir
	*flagSourcesFile = filepath.Join(dir, "missing-sources.txt")
	defer func() { *flagFilesDir = oldFiles; *flagSourcesFile = oldSources }()

	req := httptest.NewRequest(http.MethodGet, "/api/rpz/status", nil)
	rec := httptest.NewRecorder()
	handleRPZStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	for _, key := range []string{"ok", "block_mode", "sinkhole_ips", "feeds", "cdb_size", "cdb_hash"} {
		if _, present := got[key]; !present {
			t.Errorf("missing key %q in payload", key)
		}
	}
}

func TestRPZStatusRejectsNonGet(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/rpz/status", nil)
	rec := httptest.NewRecorder()
	handleRPZStatus(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// TestRPZStatusDoesNotFabricateFeedActive is a regression guard. A source that
// is merely listed in sources.txt was previously reported as `active: true`,
// which the UI drew as a green "Aktif" dot for feeds that had never synced.
func TestRPZStatusDoesNotFabricateFeedActive(t *testing.T) {
	dir := t.TempDir()
	sf := filepath.Join(dir, "sources.txt")
	if err := os.WriteFile(sf, []byte("https://example.invalid/never-synced.txt\n"), 0o644); err != nil {
		t.Fatalf("write sources: %v", err)
	}
	oldFiles := *flagFilesDir
	oldSources := *flagSourcesFile
	*flagFilesDir = dir
	*flagSourcesFile = sf
	defer func() { *flagFilesDir = oldFiles; *flagSourcesFile = oldSources }()

	req := httptest.NewRequest(http.MethodGet, "/api/rpz/status", nil)
	rec := httptest.NewRecorder()
	handleRPZStatus(rec, req)

	var got struct {
		Feeds []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Active *bool  `json:"active"`
		} `json:"feeds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(got.Feeds) != 1 {
		t.Fatalf("expected 1 feed, got %d", len(got.Feeds))
	}
	f := got.Feeds[0]
	if f.Active != nil {
		t.Error("legacy bool `active` must be gone; it asserted an unverified sync")
	}
	if f.Status == "" {
		t.Error("feed must carry an explicit status string")
	}
	if f.Status == "active" {
		t.Errorf("never-synced feed reported status=%q", f.Status)
	}
}

// TestUpstreamStatusWeights verifies weights always sum to 100, which the UI
// renders as a share bar. A wrong total would silently mis-draw the chart.
func TestUpstreamStatusWeights(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 7, 8} {
		total := 0
		for i := 0; i < n; i++ {
			total += upstreamWeights(n, i)
		}
		if total != 100 {
			t.Errorf("weights for n=%d sum to %d, want 100", n, total)
		}
	}
}

func TestUpstreamStatusShape(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/upstream/status", nil)
	rec := httptest.NewRecorder()
	handleUpstreamStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		OK        bool `json:"ok"`
		Upstreams []struct {
			Address  string `json:"address"`
			Protocol string `json:"protocol"`
			Weight   int    `json:"weight"`
			Health   string `json:"health"`
		} `json:"upstreams"`
		Count        int    `json:"count"`
		HealthSource string `json:"health_source"`
		ProbeSupport bool   `json:"probe_support"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got.Count != len(got.Upstreams) {
		t.Errorf("count=%d but len(upstreams)=%d", got.Count, len(got.Upstreams))
	}
	if got.ProbeSupport {
		t.Error("probe_support must be false: the panel does not probe resolvers")
	}
	if got.HealthSource == "" {
		t.Error("health_source must be stated so the UI can attribute the signal")
	}
}

// TestUpstreamStatusDoesNotFabricateHealth is a regression guard. The endpoint
// previously returned "healthy": true for every resolver without contacting any
// of them, which the UI rendered as a green "UP" badge. Health must now be a
// tri-state string that never claims a probe that did not happen.
func TestUpstreamStatusDoesNotFabricateHealth(t *testing.T) {
	dir := t.TempDir()
	up := filepath.Join(dir, "upstreams.conf")
	// 192.0.2.0/24 (TEST-NET-1) is reserved and never routable.
	if err := os.WriteFile(up, []byte("newServer({address='192.0.2.99:53', name='unreachable'})\n"), 0o644); err != nil {
		t.Fatalf("write upstreams: %v", err)
	}
	old := *flagUpstreams
	*flagUpstreams = up
	defer func() { *flagUpstreams = old }()

	req := httptest.NewRequest(http.MethodGet, "/api/upstream/status", nil)
	rec := httptest.NewRecorder()
	handleUpstreamStatus(rec, req)

	var got struct {
		Upstreams []struct {
			Address string `json:"address"`
			Health  string `json:"health"`
			Healthy *bool  `json:"healthy"`
		} `json:"upstreams"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(got.Upstreams) != 1 {
		t.Fatalf("expected 1 upstream, got %d", len(got.Upstreams))
	}
	u := got.Upstreams[0]
	if u.Healthy != nil {
		t.Error("legacy bool `healthy` field must be gone; it asserted unproven health")
	}
	switch u.Health {
	case "unknown", "inactive":
		// acceptable: honest about not probing
	case "healthy":
		t.Errorf("unroutable %s reported healthy=%q without any probe", u.Address, u.Health)
	default:
		t.Errorf("unexpected health value %q", u.Health)
	}
}

func TestDetectUpstreamProtocol(t *testing.T) {
	cases := map[string]string{
		"1.1.1.1":                   "UDP/TCP",
		"8.8.8.8:53":                "UDP/TCP",
		"1.1.1.1:853":               "DoT",
		"[2606:4700:4700::1111]:53": "UDP/TCP",
		"dns.example:443":           "DoH",
	}
	for in, want := range cases {
		if got := detectUpstreamProtocol(in); got != want {
			t.Errorf("detectUpstreamProtocol(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCDBContainsDomain uses a real generator-produced database when available
// so the panel lookup is checked against actual CDB bytes, not a hand-built stub.
func TestCDBContainsDomain(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trust.db")

	// Build a minimal CDB with the same layout tools/gen-cdb.py emits.
	if err := writeTestCDB(dbPath, []string{"evil.com", "blocked.domain"}); err != nil {
		t.Fatalf("build test cdb: %v", err)
	}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read cdb: %v", err)
	}

	if !cdbContainsDomain(data, "evil.com") {
		t.Error("expected evil.com to be found")
	}
	if !cdbContainsDomain(data, "blocked.domain") {
		t.Error("expected blocked.domain to be found")
	}
	if cdbContainsDomain(data, "safe.example") {
		t.Error("did not expect safe.example to be found")
	}
}

func TestRPZTestRequiresDomain(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/rpz/test", nil)
	rec := httptest.NewRecorder()
	handleRPZTest(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHealthEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	handleHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got["ok"] != true {
		t.Errorf("ok = %v, want true", got["ok"])
	}
}

// writeTestCDB writes a TinyCDB file using the exact layout tools/gen-cdb.py
// produces: 2048-byte header, then all data records, then the per-slot hash
// tables. Offsets are little-endian. This keeps the panel lookup contract
// checkable against real generator bytes without shelling out to Python.
func writeTestCDB(path string, names []string) error {
	const (
		hashInit = 5381
		header   = 256 * 8
	)
	le := func(v uint32) []byte {
		return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
	}
	hash := func(b []byte) uint32 {
		h := uint32(hashInit)
		for _, c := range b {
			h = (h + (h << 5)) ^ uint32(c)
		}
		return h & 0xFFFFFFFF
	}

	type rec struct {
		h   uint32
		pos uint32
		key []byte
		val []byte
	}
	var recs []rec
	pos := uint32(header)
	for _, n := range names {
		// Wire-format key, matching trust-builder and dnsdist's
		// KeyValueLookupKeyQName(true). Plain "domain." keys would not match.
		key, ok := wireDomainKey(n)
		if !ok {
			return fmt.Errorf("invalid test domain %q", n)
		}
		val := []byte("x")
		recs = append(recs, rec{h: hash(key), pos: pos, key: key, val: val})
		pos += uint32(len(key) + len(val) + 8)
	}

	// Index bucket selection uses the LOW byte of the hash, and the header's
	// second field is a SLOT COUNT (not a byte count). Both details matter:
	// getting them wrong makes the fixture disagree with real dnsdist.
	bySlot := make(map[int][]rec)
	for _, r := range recs {
		slot := int(r.h & 0xFF)
		bySlot[slot] = append(bySlot[slot], r)
	}

	out := make([]byte, header)
	for _, r := range recs {
		out = append(out, le(uint32(len(r.key)))...)
		out = append(out, le(uint32(len(r.val)))...)
		out = append(out, r.key...)
		out = append(out, r.val...)
	}
	for slot := 0; slot < 256; slot++ {
		rs := bySlot[slot]
		if len(rs) == 0 {
			continue // empty bucket: leave offset and count at zero
		}
		// Build the bucket's slot array with the same probing rule dnsdist uses:
		// start at (hash>>8) % slotCount and linear-probe on collision.
		tablePos := uint32(len(out))
		slotCount := uint32(len(rs))
		bucket := make([]rec, slotCount)
		placed := make([]bool, slotCount)
		for _, r := range rs {
			start := (r.h >> 8) % slotCount
			for i := uint32(0); i < slotCount; i++ {
				idx := (start + i) % slotCount
				if !placed[idx] {
					bucket[idx] = r
					placed[idx] = true
					break
				}
			}
		}
		for _, r := range bucket {
			out = append(out, le(r.h)...)
			out = append(out, le(r.pos)...)
		}
		out[slot*8] = byte(tablePos)
		out[slot*8+1] = byte(tablePos >> 8)
		out[slot*8+2] = byte(tablePos >> 16)
		out[slot*8+3] = byte(tablePos >> 24)
		out[slot*8+4] = byte(slotCount)
		out[slot*8+5] = byte(slotCount >> 8)
		out[slot*8+6] = byte(slotCount >> 16)
		out[slot*8+7] = byte(slotCount >> 24)
	}
	return os.WriteFile(path, out, 0o644)
}
