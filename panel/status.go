package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ─── Status read endpoints (Stitch dashboard needs) ──────────────────────────
//
// These are read-only projections. They never shell out and never mutate state.
// The panel previously only exposed write endpoints for RPZ/upstream, which
// forced the UI to show input forms instead of live operational data.

// rpzFeed describes one blocklist source as observed on disk.
//
// Status is a string, not a bool: the panel can only see that a source is
// listed in the sources file. It cannot prove the feed was fetched. Reporting
// `active: true` previously made the UI draw a green "Aktif" dot for feeds that
// had never synced.
type rpzFeed struct {
	Name     string `json:"name"`
	ZoneID   string `json:"zone_id"`
	Status   string `json:"status"`
	Rules    int64  `json:"rules"`
	LastSync string `json:"last_sync,omitempty"`
}

// handleRPZStatus reports current sinkhole IPs, block mode, CDB size/hash, and
// configured feed sources. Read-only.
func handleRPZStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	cfg := loadNodeConfig()

	// CDB artefact facts from the served files directory.
	var (
		dbSize int64
		dbHash string
		dbMod  time.Time
	)
	if *flagFilesDir != "" {
		if st, err := os.Stat(filepath.Join(*flagFilesDir, "trust.db")); err == nil {
			dbSize = st.Size()
			dbMod = st.ModTime()
		}
		if b, err := os.ReadFile(filepath.Join(*flagFilesDir, "manifest.json")); err == nil {
			var mf struct {
				SHA256 string `json:"sha256"`
			}
			if json.Unmarshal(b, &mf) == nil {
				dbHash = mf.SHA256
			}
		}
	}

	feeds := loadRPZFeeds()

	var totalRules int64
	for _, f := range feeds {
		totalRules += f.Rules
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":           true,
		"block_mode":   cfg.BlockMode,
		"sinkhole_ips": cfg.SinkholeIPs,
		"feeds":        feeds,
		"total_rules":  totalRules,
		"cdb_size":     dbSize,
		"cdb_hash":     dbHash,
		"cdb_updated":  dbMod.Format(time.RFC3339),
	})
}

// loadRPZFeeds parses the master sources file when present. When the panel runs
// as an edge node there is no sources file, so it returns a single synthetic
// entry describing the local CDB rather than inventing remote feeds.
func loadRPZFeeds() []rpzFeed {
	if *flagSourcesFile != "" {
		if b, err := os.ReadFile(*flagSourcesFile); err == nil {
			var out []rpzFeed
			for _, line := range strings.Split(string(b), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				name := line
				if i := strings.IndexAny(line, " \t"); i > 0 {
					name = line[:i]
				}
				out = append(out, rpzFeed{
					Name:   name,
					ZoneID: filepath.Base(name),
					// "configured" is the strongest truthful claim: the source
					// is listed, but the panel did not fetch or verify it.
					Status: "configured",
				})
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return []rpzFeed{}
}

// upstreamDetail describes one configured upstream resolver.
type upstreamDetail struct {
	Address  string `json:"address"`
	Protocol string `json:"protocol"`
	Weight   int    `json:"weight"`
	// Health is reported as a tri-state string because the panel has no per
	// resolver probe. Claiming bool true previously made the UI show a green
	// "UP" badge for resolvers that were never contacted.
	//   "unknown"  - configured, dnsdist running, no per-resolver probe exists
	//   "inactive" - dnsdist itself is not running, so no resolver is serving
	Health  string `json:"health"`
	Latency string `json:"latency,omitempty"`
}

// handleUpstreamStatus reports configured upstreams with parsed protocol and a
// stable ordering. Read-only. It never probes resolvers, so it reports
// "unknown" rather than asserting health it cannot prove.
func handleUpstreamStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	cfg := loadNodeConfig()

	// The only health signal the panel actually owns is whether dnsdist runs.
	health := "unknown"
	if !dnsdistRunning() {
		health = "inactive"
	}

	details := make([]upstreamDetail, 0, len(cfg.Upstreams))
	for i, addr := range cfg.Upstreams {
		details = append(details, upstreamDetail{
			Address:  addr,
			Protocol: detectUpstreamProtocol(addr),
			Weight:   upstreamWeights(len(cfg.Upstreams), i),
			Health:   health,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":            true,
		"upstreams":     details,
		"count":         len(details),
		"health_source": "dnsdist.service",
		"health_note":   "panel does not probe individual resolvers; health reflects dnsdist run state only",
		"probe_support": false,
	})
}

// upstreamWeights distributes 100 across n resolvers with the remainder on the
// first entries, matching dnsdist's equal-share default.
func upstreamWeights(n, i int) int {
	if n <= 0 {
		return 0
	}
	base := 100 / n
	rem := 100 % n
	w := base
	if i < rem {
		w++
	}
	return w
}

// detectUpstreamProtocol infers transport from the address form. Port 853 is
// treated as DoT, everything else as plain UDP/TCP.
func detectUpstreamProtocol(addr string) string {
	if strings.Contains(addr, ":853") {
		return "DoT"
	}
	if strings.Contains(addr, ":443") {
		return "DoH"
	}
	return "UDP/TCP"
}

// handleDnstapStatus summarises the dnstap aggregator so the dashboard can show
// whether the decoder is live without exposing per-query data. Read-only.
func handleDnstapStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	top := dnstapAgg.TopBlocked(1)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":        true,
		"enabled":   *flagDnstapAddr != "",
		"listening": *flagDnstapAddr,
		"has_data":  len(top) > 0,
	})
}

// handleRPZTest evaluates a domain against the sinkhole decision the panel can
// actually justify: whether the resolved name appears in the loaded CDB. It does
// not perform a live DNS query. Read-only.
func handleRPZTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	if domain == "" {
		jsonErr(w, http.StatusBadRequest, "domain parameter required")
		return
	}
	domain = strings.TrimSuffix(strings.ToLower(domain), ".")

	matched := false
	dbPath := ""
	if *flagFilesDir != "" {
		dbPath = filepath.Join(*flagFilesDir, "trust.db")
		if b, err := os.ReadFile(dbPath); err == nil {
			matched = cdbContainsDomain(b, domain)
		}
	}

	cfg := loadNodeConfig()
	action := "PASS"
	if matched {
		action = strings.ToUpper(cfg.BlockMode)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":           true,
		"domain":       domain,
		"matched":      matched,
		"action":       action,
		"sinkhole_ips": cfg.SinkholeIPs,
		"database":     dbPath,
	})
}

// wireDomainKey encodes a domain into the DNS wire format used as the CDB key.
//
// This must match how the CDB is written and how dnsdist looks it up:
//   - tools/trust-builder/main.go writes keys via dns.PackDomainName, producing
//     length-prefixed labels: "pornhub.com" -> \x07pornhub\x03com\x00
//   - setup/dnsdist.conf uses KeyValueLookupKeyQName(true), i.e. wire format
//
// An earlier version searched for the plain text "domain." and therefore never
// matched a real trust.db, so /api/rpz/test always answered "not found" even for
// domains that dnsdist was correctly blocking.
func wireDomainKey(domain string) ([]byte, bool) {
	d := strings.ToLower(strings.TrimSpace(domain))
	d = strings.Trim(d, ".")
	if d == "" || len(d) > 253 {
		return nil, false
	}
	key := make([]byte, 0, len(d)+2)
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 {
			return nil, false
		}
		key = append(key, byte(len(label)))
		key = append(key, label...)
	}
	key = append(key, 0)
	return key, true
}

// cdbContainsDomain reports whether domain is present in a TinyCDB database.
//
// CDB index semantics (verified against colinmarc/cdb's reader AND against real
// dnsdist, which blocked a name this function previously failed to find):
//
//   - The 2048-byte header holds 256 index entries of 8 bytes each:
//     uint32 tableOffset, uint32 tableSlotCount.
//   - The index entry for a key is chosen by the LOW byte of the hash:
//     index = hash & 0xff.   (NOT (hash>>8)&0xff - that was the bug.)
//   - The second field is a SLOT COUNT, not a byte count. Each slot is 8 bytes,
//     so the table occupies tableSlotCount*8 bytes.
//   - Inside the table, probing starts at (hash >> 8) % tableSlotCount and walks
//     forward with wraparound. A zero hash means an empty slot, which ends the
//     probe chain.
//
// The earlier implementation used the wrong index byte and treated the length
// field as bytes, so /api/rpz/test reported "not found" for names that dnsdist
// was actively blocking - exactly the symptom reported by the operator.
func cdbContainsDomain(data []byte, domain string) bool {
	key, ok := wireDomainKey(domain)
	if !ok {
		return false
	}
	const (
		hashInit = 5381
		indexLen = 256
		slotSize = 8
	)
	if len(data) < indexLen*slotSize {
		return false
	}

	// CDB hash: h = h*33 ^ byte, starting at 5381.
	var h uint32 = hashInit
	for _, c := range key {
		h = ((h + (h << 5)) ^ uint32(c))
	}

	// Index entry is selected by the LOW byte of the hash.
	idx := int(h&0xff) * slotSize
	tableOff := le32(data, idx)
	slotCount := le32(data, idx+4)
	if slotCount == 0 {
		return false // no entries hashed to this index bucket
	}
	// Guard against a corrupt header pointing outside the file.
	if uint64(tableOff)+uint64(slotCount)*slotSize > uint64(len(data)) {
		return false
	}

	start := (h >> 8) % slotCount
	for i := uint32(0); i < slotCount; i++ {
		slot := (start + i) % slotCount
		off := int(tableOff) + int(slot)*slotSize
		if off+slotSize > len(data) {
			return false
		}
		th := le32(data, off)
		if th == 0 {
			return false // empty slot: end of probe chain
		}
		if th != h {
			continue
		}
		tpos := le32(data, off+4)
		if uint64(tpos)+8 > uint64(len(data)) {
			continue
		}
		klen := le32(data, int(tpos))
		if uint64(tpos)+8+uint64(klen) > uint64(len(data)) {
			continue
		}
		if string(data[tpos+8:tpos+8+klen]) == string(key) {
			return true
		}
	}
	return false
}

// le32 reads a little-endian uint32. TinyCDB stores its offsets little-endian,
// which is what tools/trust-builder and tools/gen-cdb.py both emit.
// (Previously misnamed be32, which described the opposite byte order.)
func le32(b []byte, off int) uint32 {
	if off+4 > len(b) {
		return 0
	}
	return uint32(b[off]) | uint32(b[off+1])<<8 | uint32(b[off+2])<<16 | uint32(b[off+3])<<24
}

// handleClusterTokens reports the enrollment posture of the cluster.
//
// The ClusterStorage interface does not expose token listing, and expanding that
// interface across the JSON, SQLite, and Postgres backends is out of scope here.
// Instead this endpoint reports what the interface can actually answer: how many
// nodes exist and whether the panel is acting as master, which is what the UI
// needs to decide whether to offer token generation.
func handleClusterTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	nodes, agg := getClusterStorage().ListNodes()
	_ = nodes
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":              true,
		"is_master":       *flagMaster,
		"token_supported": *flagMaster,
		"nodes":           agg.TotalNodes,
		"online":          agg.OnlineNodes,
		"note":            "token listing is not exposed by the storage interface; generate tokens via POST /api/cluster/token",
	})
}

// handleHealth is an unauthenticated liveness probe used by the UI status pill.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"uptime":  uptimeSec(),
		"dnsdist": dnsdistRunning(),
		"qps":     stats.qps.Load(),
	})
}

// parseIntDefault is a small helper for optional numeric query params.
func parseIntDefault(raw string, def int) int {
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return def
	}
	return v
}
