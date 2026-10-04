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
type rpzFeed struct {
	Name     string `json:"name"`
	ZoneID   string `json:"zone_id"`
	Active   bool   `json:"active"`
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
					Active: true,
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

// cdbContainsDomain performs an exact-name lookup in a TinyCDB file using the
// DJB hash the format uses. Mirrors tools/gen-cdb.py so panel and generator agree.
func cdbContainsDomain(data []byte, domain string) bool {
	key := []byte(domain + ".")
	const (
		hashInit = 5381
		slots    = 256
	)
	if len(data) < slots*8 {
		return false
	}
	var h uint32 = hashInit
	for _, c := range key {
		h = ((h + (h << 5)) ^ uint32(c))
	}
	slot := (h >> 8) & (slots - 1)
	base := int(slot) * 8

	pos := be32(data, base)
	length := be32(data, base+4)
	if pos > uint32(len(data)) || uint32(len(data))-pos < length {
		return false
	}
	for off := pos; off < pos+length; off += 8 {
		if off+8 > uint32(len(data)) {
			return false
		}
		th := be32(data, int(off))
		tpos := be32(data, int(off)+4)
		if th != h {
			continue
		}
		if int(tpos)+8 > len(data) {
			continue
		}
		klen := be32(data, int(tpos))
		if int(tpos)+8+int(klen) > len(data) {
			continue
		}
		if string(data[tpos+8:tpos+8+klen]) == string(key) {
			return true
		}
	}
	return false
}

// be32 reads a big-endian uint32. TinyCDB stores offsets in host byte order on
// little-endian builds, so this reads little-endian to match the generator.
func be32(b []byte, off int) uint32 {
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
