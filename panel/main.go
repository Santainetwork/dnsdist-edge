// DNSDist Management Panel — HTTPS :8443, stdlib only, embed UI
package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"path/filepath"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "embed"
)

//go:embed static/index.html
var indexHTML []byte

var whitelistLabelRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func normalizeWhitelist(raw string) (string, []int, int, int) {
	seen := make(map[string]struct{})
	var out []string
	var invalid []int
	count, duplicates := 0, 0
	for lineNo, rawLine := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			out = append(out, line)
			continue
		}
		line = strings.TrimSuffix(strings.ToLower(line), ".")
		if ip := net.ParseIP(line); ip != nil {
			line = ip.String()
		} else if !validWhitelistDomain(line) {
			invalid = append(invalid, lineNo+1)
			continue
		}
		if _, ok := seen[line]; ok {
			duplicates++
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
		count++
	}
	if len(out) == 0 {
		return "", invalid, count, duplicates
	}
	return strings.Join(out, "\n") + "\n", invalid, count, duplicates
}

func validWhitelistDomain(value string) bool {
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "/ *\t\r\n") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) > 63 || !whitelistLabelRE.MatchString(label) {
			return false
		}
	}
	return true
}

// ─── Config ──────────────────────────────────────────────────────────────────

var (
	flagAddr          = flag.String("addr", envOr("PANEL_ADDR", ":8443"), "Primary listen address (HTTPS if TLS enabled, else HTTP)")
	flagHTTPAddr      = flag.String("http-addr", envOr("PANEL_HTTP_ADDR", ""), "Optional secondary HTTP listen address (enables dual HTTP+HTTPS mode)")
	flagTLS           = flag.Bool("tls", envBool("PANEL_TLS", true), "Enable TLS on primary address (set false for pure HTTP)")
	flagConf          = flag.String("config", envOr("DNSDIST_CONF", "/etc/dnsdist/dnsdist.conf"), "dnsdist.conf path")
	flagUpstreams     = flag.String("upstreams", envOr("DNSDIST_UPSTREAMS", "/etc/dnsdist/upstreams.conf"), "upstreams.conf path")
	flagCert          = flag.String("cert", envOr("PANEL_CERT", "/var/lib/dnsdist/panel-cert.pem"), "TLS cert path")
	flagKey           = flag.String("key", envOr("PANEL_KEY", "/var/lib/dnsdist/panel-key.pem"), "TLS key path")
	flagSecret        = flag.String("secret-file", envOr("PANEL_SECRET_FILE", "/var/lib/dnsdist/panel.secret"), "JWT secret file")
	flagSetupSh       = flag.String("setup-sh", "/usr/local/bin/setup-edge.sh", "Path to setup-edge.sh")
	flagMaster        = flag.Bool("master", envBool("PANEL_MASTER", false), "Enable Central Master mode (CDB builder & publisher)")
	flagFilesDir      = flag.String("files-dir", envOr("PANEL_FILES_DIR", "/var/www/html/files"), "Directory to serve /files/ from (trust.db, manifest.json)")
	flagSourcesFile   = flag.String("sources-file", envOr("PANEL_SOURCES_FILE", "/etc/dnsdist-master/sources.txt"), "Path to sources.txt for master compilation")
	flagWhitelistFile = flag.String("whitelist-file", envOr("PANEL_WHITELIST_FILE", "/etc/dnsdist-master/whitelist.txt"), "Path to whitelist.txt")
	flagCustomBLFile  = flag.String("custom-bl-file", envOr("PANEL_CUSTOM_BL_FILE", "/etc/dnsdist-master/custom-blacklist.txt"), "Path to custom-blacklist.txt")
	flagBuildInterval = flag.Duration("build-interval", 6*time.Hour, "Automatic build interval (0 to disable auto-build)")
	flagBuildNow          = flag.Bool("build-now", false, "Compile CDB immediately and exit (CLI builder mode)")
	flagDnsdistAPI        = flag.String("dnsdist-api", envOr("DNSDIST_API_URL", "http://127.0.0.1:8083"), "dnsdist web API base URL")
	flagDnsdistKey        = flag.String("dnsdist-key", envOr("DNSDIST_API_KEY", ""), "dnsdist web API key (X-API-Key)")
	flagClusterNodesFile  = flag.String("cluster-nodes-file", envOr("PANEL_CLUSTER_NODES_FILE", "/var/lib/dnsdist/cluster-nodes.json"), "Path to cluster nodes persistence JSON")
	flagMasterURL         = flag.String("master-url", envOr("PANEL_MASTER_URL", ""), "Master URL for edge telemetry and enrollment (e.g. http://10.10.10.1:8084)")
	flagEnrollToken       = flag.String("enroll-token", envOr("PANEL_ENROLL_TOKEN", ""), "Enrollment token for connecting edge node to central master")
	flagNodeName          = flag.String("node-name", envOr("PANEL_NODE_NAME", ""), "Human-readable name of this edge node (defaults to hostname)")
	flagAgentStateFile    = flag.String("agent-state-file", envOr("PANEL_AGENT_STATE_FILE", "/var/lib/dnsdist/cluster-agent.json"), "Path to edge agent state JSON")
	flagHeartbeatInterval = flag.Duration("heartbeat-interval", 60*time.Second, "Edge telemetry heartbeat interval to master")
	flagGenEnrollToken    = flag.Bool("enrollment-token", false, "Generate an enrollment token and exit (CLI mode)")
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		v = strings.ToLower(strings.TrimSpace(v))
		return v == "1" || v == "true" || v == "yes" || v == "on"
	}
	return def
}

// ─── JWT (manual HMAC-SHA256, no external deps) ───────────────────────────────

var jwtSecret []byte

func loadOrGenSecret(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		return b[:32], nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	_ = os.MkdirAll("/var/lib/dnsdist", 0o750)
	if err := atomicWrite(path, b, 0o600); err != nil {
		log.Printf("[panel] WARNING: cannot save secret: %v", err)
	}
	return b, nil
}

func jwtSign(payload map[string]any) string {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	pb, _ := json.Marshal(payload)
	p := base64.RawURLEncoding.EncodeToString(pb)
	mac := hmac.New(sha256.New, jwtSecret)
	mac.Write([]byte(h + "." + p))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return h + "." + p + "." + sig
}

func jwtVerify(token string) (map[string]any, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return nil, errors.New("invalid token")
	}
	mac := hmac.New(sha256.New, jwtSecret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(want, got) {
		return nil, errors.New("invalid signature")
	}
	pb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	if err := json.Unmarshal(pb, &claims); err != nil {
		return nil, err
	}
	if exp, ok := claims["exp"].(float64); ok && time.Now().Unix() > int64(exp) {
		return nil, errors.New("token expired")
	}
	return claims, nil
}

// ─── TLS self-signed cert ─────────────────────────────────────────────────────

func ensureCert(certPath, keyPath string) error {
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return nil // already exists
		}
	}
	log.Printf("[panel] Generating self-signed TLS cert -> %s, %s", certPath, keyPath)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "dnsdist-panel"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	// Add all local IPs
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ip, _, err := net.ParseCIDR(a.String()); err == nil {
				tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	_ = os.MkdirAll("/var/lib/dnsdist", 0o750)
	if err := atomicWrite(certPath, certPEM, 0o644); err != nil {
		return err
	}
	return atomicWrite(keyPath, keyPEM, 0o600)
}

// ─── Stats ────────────────────────────────────────────────────────────────────

type statsCollector struct {
	mu           sync.Mutex
	lastCPUStat  [2]uint64 // idle, total
	lastUDPIn    uint64
	lastUDPTs    time.Time
	qps          atomic.Int64
	udpInTotal   atomic.Uint64
	blockedTotal atomic.Int64
	cacheHitPct  atomic.Int64 // stored as pct * 100 (fixed-point, 2 decimals)
	queriesTotal atomic.Int64
}

var stats statsCollector

func readCPUStat() (idle, total uint64) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		for i := 1; i < len(fields); i++ {
			v, _ := strconv.ParseUint(fields[i], 10, 64)
			total += v
			if i == 4 { // idle
				idle = v
			}
		}
		return
	}
	return
}

func cpuPercent() float64 {
	stats.mu.Lock()
	defer stats.mu.Unlock()
	idle, total := readCPUStat()
	prevIdle, prevTotal := stats.lastCPUStat[0], stats.lastCPUStat[1]
	stats.lastCPUStat = [2]uint64{idle, total}
	if prevTotal == 0 || total == prevTotal {
		return 0
	}
	dIdle := float64(idle - prevIdle)
	dTotal := float64(total - prevTotal)
	return (1 - dIdle/dTotal) * 100
}

func memMB() (used, total uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()
	vals := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 {
			v, _ := strconv.ParseUint(fields[1], 10, 64)
			vals[strings.TrimSuffix(fields[0], ":")] = v
		}
	}
	total = vals["MemTotal"] / 1024
	free := vals["MemFree"] / 1024
	buffers := vals["Buffers"] / 1024
	cached := vals["Cached"] / 1024
	used = total - free - buffers - cached
	return
}

func uptimeSec() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}
	f, _ := strconv.ParseFloat(fields[0], 64)
	return int64(f)
}

func dnsdistRunning() bool {
	cmd := exec.Command("systemctl", "is-active", "--quiet", "dnsdist")
	return cmd.Run() == nil
}

// readUDPIn reads InDatagrams from /proc/net/snmp
func readUDPIn() uint64 {
	f, err := os.Open("/proc/net/snmp")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var header []string
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if strings.HasPrefix(line, "Udp:") {
			if header == nil {
				header = fields
				continue
			}
			for i, h := range header {
				if h == "InDatagrams" && i < len(fields) {
					v, _ := strconv.ParseUint(fields[i], 10, 64)
					return v
				}
			}
		}
	}
	return 0
}

func updateQPS() {
	stats.mu.Lock()
	defer stats.mu.Unlock()
	now := time.Now()
	cur := readUDPIn()
	prev := stats.lastUDPIn
	prevTs := stats.lastUDPTs
	stats.lastUDPIn = cur
	stats.lastUDPTs = now
	if prev == 0 || prevTs.IsZero() {
		return
	}
	dt := now.Sub(prevTs).Seconds()
	if dt <= 0 {
		return
	}
	qps := float64(cur-prev) / dt
	if qps < 0 {
		qps = 0
	}
	stats.qps.Store(int64(qps))
	stats.udpInTotal.Add(cur - prev)
}

var (
	reDnsdistAPIKey   = regexp.MustCompile(`(?m)apiKey\s*=\s*['"]([^'"]+)['"]`)
	reDnsdistPassword = regexp.MustCompile(`(?m)password\s*=\s*['"]([^'"]+)['"]`)
)

func getDnsdistCreds() (apiKey, password string) {
	if *flagDnsdistKey != "" {
		apiKey = *flagDnsdistKey
	}
	if b, err := os.ReadFile(*flagConf); err == nil {
		str := string(b)
		if apiKey == "" {
			if m := reDnsdistAPIKey.FindStringSubmatch(str); len(m) > 1 {
				apiKey = m[1]
			}
		}
		if m := reDnsdistPassword.FindStringSubmatch(str); len(m) > 1 {
			password = m[1]
		}
	}
	return
}

type dnsdistRuleItem struct {
	ID      int     `json:"id"`
	Matches float64 `json:"matches"`
	Rule    string  `json:"rule"`
	Action  string  `json:"action"`
	Name    string  `json:"name"`
}

type dnsdistPoolItem struct {
	Name        string  `json:"name"`
	CacheHits   float64 `json:"cacheHits"`
	CacheMisses float64 `json:"cacheMisses"`
}

type dnsdistServerOverview struct {
	Rules      []dnsdistRuleItem `json:"rules"`
	Pools      []dnsdistPoolItem `json:"pools"`
	Statistics map[string]any    `json:"statistics"`
}

func statFloatVal(val any) float64 {
	switch v := val.(type) {
	case float64:
		return v
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return 0
}

func statFloat(m map[string]any, keys ...string) float64 {
	for _, k := range keys {
		if val, ok := m[k]; ok {
			return statFloatVal(val)
		}
	}
	return 0
}

// fetchDnsdistStats polls dnsdist web API /api/v1/servers/localhost
// and updates blockedTotal, cacheHitPct, queriesTotal atomics.
func fetchDnsdistStats() {
	apiKey, password := getDnsdistCreds()
	client := &http.Client{Timeout: 3 * time.Second}

	// 1. Coba endpoint utama /api/v1/servers/localhost (lengkap dengan rules & pools)
	overviewURL := *flagDnsdistAPI + "/api/v1/servers/localhost"
	req, err := http.NewRequest(http.MethodGet, overviewURL, nil)
	if err == nil {
		if apiKey != "" {
			req.Header.Set("X-API-Key", apiKey)
		}
		if password != "" {
			req.SetBasicAuth("admin", password)
		}
		if resp, err := client.Do(req); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var ov dnsdistServerOverview
				if err := json.NewDecoder(resp.Body).Decode(&ov); err == nil {
					// Hitung blocked: cari semua rule lookup blacklist CDB
					// Format dnsdist rule: (lookup key-value store based on 'qname in wire format') && (qtype==...)
					// Action bisa spoof IP manapun (v4/v6/multi-IP), nxdomain, atau drop.
					var blocked float64
					var foundKVSRule bool
					for _, r := range ov.Rules {
						rLower := strings.ToLower(r.Rule)
						if strings.Contains(rLower, "key-value store") || strings.Contains(rLower, "kvs") {
							blocked += r.Matches
							foundKVSRule = true
						}
					}
					if !foundKVSRule {
						blocked = statFloat(ov.Statistics, "rule-drop", "rdrop")
					}
					stats.blockedTotal.Store(int64(blocked))

					// Hitung cache hit rate
					var cacheHits, cacheMisses float64
					for _, p := range ov.Pools {
						cacheHits += p.CacheHits
						cacheMisses += p.CacheMisses
					}
					if cacheHits == 0 && cacheMisses == 0 {
						cacheHits = statFloat(ov.Statistics, "cache-hits", "packetcache-hits")
						cacheMisses = statFloat(ov.Statistics, "cache-misses", "packetcache-misses")
					}
					totalCache := cacheHits + cacheMisses
					if totalCache > 0 {
						pct := (cacheHits / totalCache) * 10000 // fixed-point * 100
						stats.cacheHitPct.Store(int64(pct))
					}

					// Queries total & QPS
					if q := statFloat(ov.Statistics, "queries"); q > 0 {
						stats.queriesTotal.Store(int64(q))
					}
					if qps := statFloat(ov.Statistics, "queries-per-second"); qps > 0 {
						stats.qps.Store(int64(qps))
					}
					return
				}
			}
		}
	}

	// 2. Fallback: /api/v1/servers/localhost/statistics jika overview gagal
	statsURL := *flagDnsdistAPI + "/api/v1/servers/localhost/statistics"
	req2, err := http.NewRequest(http.MethodGet, statsURL, nil)
	if err != nil {
		return
	}
	if apiKey != "" {
		req2.Header.Set("X-API-Key", apiKey)
	}
	resp2, err := client.Do(req2)
	if err != nil {
		return
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return
	}
	var items []struct {
		Name  string  `json:"name"`
		Value float64 `json:"value"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&items); err != nil {
		return
	}
	var queries, blocked, cacheHits, cacheMisses float64
	for _, it := range items {
		switch it.Name {
		case "queries":
			queries = it.Value
		case "rdrop", "rule-drop":
			blocked += it.Value
		case "cache-hits", "packetcache-hits":
			cacheHits += it.Value
		case "cache-misses", "packetcache-misses":
			cacheMisses += it.Value
		case "queries-per-second":
			if it.Value > 0 {
				stats.qps.Store(int64(it.Value))
			}
		}
	}
	if queries > 0 {
		stats.queriesTotal.Store(int64(queries))
	}
	if blocked > 0 {
		stats.blockedTotal.Store(int64(blocked))
	}
	total := cacheHits + cacheMisses
	if total > 0 {
		pct := cacheHits / total * 10000
		stats.cacheHitPct.Store(int64(pct))
	}
}

func startStatsTicker() {
	// warm up CPU baseline
	readCPUStat()
	go func() {
		t := time.NewTicker(5 * time.Second)
		for range t.C {
			updateQPS()
			fetchDnsdistStats()
		}
	}()
}

// confDir returns the directory containing dnsdist.conf — used for sibling files.
func confDir() string { return filepath.Dir(*flagConf) }

// ─── Config parsing ───────────────────────────────────────────────────────────

type SafeSearch struct {
	Google  bool `json:"google"`
	Bing    bool `json:"bing"`
	YouTube bool `json:"youtube"`
}

type NodeConfig struct {
	IsMaster    bool       `json:"is_master"`
	BlockMode   string     `json:"block_mode"`
	SinkholeIPs []string   `json:"sinkhole_ips"`
	Upstreams   []string   `json:"upstreams"`
	SafeSearch  SafeSearch `json:"safesearch"`
	DotEnabled  bool       `json:"dot_enabled"`
	DohEnabled  bool       `json:"doh_enabled"`
	CertPath    string     `json:"cert_path"`
	KeyPath     string     `json:"key_path"`
}

var reBlockMode = regexp.MustCompile(`(?m)^BLOCK_MODE\s*=\s*'(\w+)'`)
var reSinkhole = regexp.MustCompile(`(?m)^SINKHOLE_IPS\s*=\s*\{([^}]*)\}`)
var reNewServer = regexp.MustCompile(`(?m)^newServer\(\{[^}]*address\s*=\s*'([^']+)'`)
var reTLSLocal = regexp.MustCompile(`(?m)^addTLSLocal\(`)
var reDOHLocal = regexp.MustCompile(`(?m)^addDOHLocal\(`)
var reCertPath = regexp.MustCompile(`addTLSLocal\('[^']+',\s*'([^']+)'`)
var reKeyPath = regexp.MustCompile(`addTLSLocal\('[^']+',\s*'[^']+',\s*'([^']+)'`)

func parseSinkholeIPs(raw string) []string {
	var out []string
	for _, s := range strings.Split(raw, ",") {
		ip := strings.Trim(strings.TrimSpace(s), "'")
		if ip != "" {
			out = append(out, ip)
		}
	}
	return out
}

func loadNodeConfig() NodeConfig {
	cd := confDir()
	cfg := NodeConfig{
		IsMaster:  *flagMaster,
		BlockMode: "rpz",
		CertPath:  filepath.Join(cd, "certs", "server.crt"),
		KeyPath:   filepath.Join(cd, "certs", "server.key"),
	}
	if b, err := os.ReadFile(*flagConf); err == nil {
		content := string(b)
		if m := reBlockMode.FindStringSubmatch(content); m != nil {
			cfg.BlockMode = m[1]
		}
		if m := reSinkhole.FindStringSubmatch(content); m != nil {
			cfg.SinkholeIPs = parseSinkholeIPs(m[1])
		}
		cfg.DotEnabled = reTLSLocal.MatchString(content)
		cfg.DohEnabled = reDOHLocal.MatchString(content)
		if m := reCertPath.FindStringSubmatch(content); m != nil {
			cfg.CertPath = m[1]
		}
		if m := reKeyPath.FindStringSubmatch(content); m != nil {
			cfg.KeyPath = m[1]
		}
	}
	if b, err := os.ReadFile(*flagUpstreams); err == nil {
		for _, m := range reNewServer.FindAllStringSubmatch(string(b), -1) {
			cfg.Upstreams = append(cfg.Upstreams, m[1])
		}
	}
	// safesearch: check safesearch.conf sibling to dnsdist.conf
	if b, err := os.ReadFile(filepath.Join(confDir(), "safesearch.conf")); err == nil {
		sc := string(b)
		cfg.SafeSearch.Google = strings.Contains(sc, "google.com")
		cfg.SafeSearch.Bing = strings.Contains(sc, "bing.com")
		cfg.SafeSearch.YouTube = strings.Contains(sc, "youtube.com")
	}
	return cfg
}

// ─── Atomic file write ────────────────────────────────────────────────────────

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func atomicWriteString(path, data string, mode os.FileMode) error {
	return atomicWrite(path, []byte(data), mode)
}

// ─── SafeSearch conf writer ───────────────────────────────────────────────────

func writeSafeSearch(google, bing, youtube bool) error {
	var sb strings.Builder
	sb.WriteString("-- SafeSearch DNS Rewrite (auto-generated by panel, do not edit)\n")
	if google {
		sb.WriteString(`-- Google SafeSearch
addAction(AndRule({QTypeRule(DNSQType.A), SuffixMatchNodeRule(newSuffixMatchNode()
  :add('google.com'):add('google.co.id'):add('google.com.au'):add('google.co.uk')
  :add('google.ca'):add('google.de'):add('google.fr'):add('google.co.jp')
)}), SpoofAction('216.239.38.120'))
addAction(AndRule({QTypeRule(DNSQType.AAAA), SuffixMatchNodeRule(newSuffixMatchNode()
  :add('google.com'):add('google.co.id'):add('google.com.au'):add('google.co.uk')
)}), SpoofAction('2001:4860:4802:32::78'))
`)
	}
	if bing {
		sb.WriteString(`-- Bing SafeSearch
addAction(SuffixMatchNodeRule(newSuffixMatchNode():add('bing.com')), SpoofAction('204.79.197.220'))
`)
	}
	if youtube {
		sb.WriteString(`-- YouTube Restricted Mode
addAction(SuffixMatchNodeRule(newSuffixMatchNode()
  :add('youtube.com'):add('www.youtube.com'):add('ytimg.com'):add('googlevideo.com')
), SpoofAction('216.239.38.120'))
`)
	}
	return atomicWriteString(filepath.Join(confDir(), "safesearch.conf"), sb.String(), 0o644)
}

// ─── DoT/DoH conf writer ──────────────────────────────────────────────────────

func writeDoTDoH(dot, doh bool, cert, key string) error {
	var sb strings.Builder
	sb.WriteString("-- DoT/DoH listeners (auto-generated by panel, do not edit)\n")
	if dot {
		fmt.Fprintf(&sb, `addTLSLocal('0.0.0.0:853', '%s', '%s', {provider='openssl',minTLSVersion='tls1.2'})
addTLSLocal('[::]:853', '%s', '%s', {provider='openssl',minTLSVersion='tls1.2'})
`, cert, key, cert, key)
	}
	if doh {
		fmt.Fprintf(&sb, `addDOHLocal('0.0.0.0:443', '%s', '%s', '/dns-query', {provider='openssl',minTLSVersion='tls1.2'})
addDOHLocal('[::]:443', '%s', '%s', '/dns-query', {provider='openssl',minTLSVersion='tls1.2'})
`, cert, key, cert, key)
	}
	return atomicWriteString(filepath.Join(confDir(), "dotdoh.conf"), sb.String(), 0o644)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func restartDnsdist() error {
	return exec.Command("systemctl", "restart", "dnsdist").Run()
}

func runSetupSh(args ...string) (string, error) {
	if _, err := os.Stat(*flagSetupSh); err != nil {
		return "", fmt.Errorf("setup-edge.sh not found at %s", *flagSetupSh)
	}
	cmd := exec.Command(*flagSetupSh, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func validIP(s string) bool {
	// bare IPv4/IPv6 or [IPv6]:port or IPv4:port
	s = strings.TrimSpace(s)
	// strip port
	if strings.HasPrefix(s, "[") {
		// [IPv6]:port
		end := strings.LastIndex(s, "]")
		if end < 0 {
			return false
		}
		s = s[1:end]
	} else if strings.Count(s, ":") == 1 {
		// IPv4:port
		host, _, err := net.SplitHostPort(s)
		if err != nil {
			return false
		}
		s = host
	}
	return net.ParseIP(s) != nil
}

func jsonOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ─── Middleware ───────────────────────────────────────────────────────────────

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hdr := r.Header.Get("Authorization")
		token := strings.TrimPrefix(hdr, "Bearer ")
		if token == "" {
			jsonErr(w, http.StatusUnauthorized, "missing token")
			return
		}
		if _, err := jwtVerify(token); err != nil {
			jsonErr(w, http.StatusUnauthorized, err.Error())
			return
		}
		next(w, r)
	}
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	// Load password from secret file (first 32 bytes = JWT secret, next line = password hash)
	// Password stored as sibling of secret file
	passPath := filepath.Join(filepath.Dir(*flagSecret), "panel.password")
	storedPass, _ := os.ReadFile(passPath)
	expected := strings.TrimSpace(string(storedPass))
	if expected == "" {
		expected = "admin" // default if not set
	}
	if body.Password != expected {
		jsonErr(w, http.StatusUnauthorized, "invalid password")
		return
	}
	token := jwtSign(map[string]any{
		"sub": "admin",
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": token})
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	memUsed, memTotal := memMB()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"qps":             stats.qps.Load(),
		"queries_total":   func() int64 { if v := stats.queriesTotal.Load(); v > 0 { return v }; return int64(stats.udpInTotal.Load()) }(),
		"blocked_total":   stats.blockedTotal.Load(),
		"cache_hit_pct":   math.Round(float64(stats.cacheHitPct.Load())/100*10) / 10,
		"cpu_pct":         math.Round(cpuPercent()*10) / 10,
		"mem_used_mb":     memUsed,
		"mem_total_mb":    memTotal,
		"uptime_sec":      uptimeSec(),
		"dnsdist_running": dnsdistRunning(),
	})
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	cfg := loadNodeConfig()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

func handleRPZ(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "POST only"); return
	}
	var body struct {
		IPs []string `json:"ips"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.IPs) == 0 {
		jsonErr(w, http.StatusBadRequest, "ips required"); return
	}
	var valid []string
	for _, ip := range body.IPs {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		if !validIP(ip) {
			jsonErr(w, http.StatusBadRequest, "invalid IP: "+ip); return
		}
		valid = append(valid, ip)
	}
	if len(valid) == 0 {
		jsonErr(w, http.StatusBadRequest, "no valid IPs"); return
	}
	out, err := runSetupSh("--set-rpz", strings.Join(valid, ","))
	if err != nil {
		log.Printf("[panel] --set-rpz error: %v\n%s", err, out)
		jsonErr(w, http.StatusInternalServerError, "setup-edge.sh error: "+err.Error()); return
	}
	jsonOK(w)
}

func handleUpstream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "POST only"); return
	}
	var body struct {
		Servers []string `json:"servers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Servers) == 0 {
		jsonErr(w, http.StatusBadRequest, "servers required"); return
	}
	out, err := runSetupSh("--set-upstream", strings.Join(body.Servers, ","))
	if err != nil {
		log.Printf("[panel] --set-upstream error: %v\n%s", err, out)
		jsonErr(w, http.StatusInternalServerError, "setup-edge.sh error: "+err.Error()); return
	}
	jsonOK(w)
}

func handleSafeSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "POST only"); return
	}
	var body SafeSearch
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid json"); return
	}
	if err := writeSafeSearch(body.Google, body.Bing, body.YouTube); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error()); return
	}
	if err := restartDnsdist(); err != nil {
		log.Printf("[panel] restart dnsdist: %v", err)
	}
	jsonOK(w)
}

func handleDoTDoH(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "POST only"); return
	}
	var body struct {
		DoT  bool   `json:"dot"`
		DoH  bool   `json:"doh"`
		Cert string `json:"cert"`
		Key  string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid json"); return
	}
	cd := confDir()
	if body.Cert == "" {
		body.Cert = filepath.Join(cd, "certs", "server.crt")
	}
	if body.Key == "" {
		body.Key = filepath.Join(cd, "certs", "server.key")
	}
	if err := writeDoTDoH(body.DoT, body.DoH, body.Cert, body.Key); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error()); return
	}
	if err := restartDnsdist(); err != nil {
		log.Printf("[panel] restart dnsdist: %v", err)
	}
	jsonOK(w)
}

func handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "POST only"); return
	}
	var body struct {
		PanelPassword string `json:"panel_password"`
		BlockMode     string `json:"block_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid json"); return
	}
	if body.PanelPassword != "" {
		passPath := filepath.Join(filepath.Dir(*flagSecret), "panel.password")
		if err := atomicWriteString(passPath, body.PanelPassword, 0o600); err != nil {
			jsonErr(w, http.StatusInternalServerError, "cannot save password: "+err.Error())
			return
		}
	}
	if body.BlockMode == "adguard" || body.BlockMode == "rpz" {
		// patch dnsdist.conf block mode
		b, err := os.ReadFile(*flagConf)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, "cannot read config: "+err.Error()); return
		}
		content := reBlockMode.ReplaceAllString(string(b), "BLOCK_MODE = '"+body.BlockMode+"'")
		if err := atomicWriteString(*flagConf, content, 0o644); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error()); return
		}
		if err := restartDnsdist(); err != nil {
			log.Printf("[panel] restart dnsdist: %v", err)
		}
	}
	jsonOK(w)
}

func handleMasterStatus(w http.ResponseWriter, r *http.Request) {
	masterState.mu.Lock()
	defer masterState.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(&masterState)
}

func handleMasterBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	masterState.mu.Lock()
	if masterState.IsBuilding {
		masterState.mu.Unlock()
		jsonErr(w, http.StatusConflict, "Kompilasi sedang berjalan")
		return
	}
	masterState.mu.Unlock()

	go func() {
		_ = BuildMasterCDB(*flagFilesDir, *flagSourcesFile, *flagWhitelistFile, *flagCustomBLFile, 8, true)
	}()
	jsonOK(w)
}

func handleMasterSources(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		content, _ := os.ReadFile(*flagSourcesFile)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"sources": string(content)})
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			Sources string `json:"sources"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		_ = os.MkdirAll(filepath.Dir(*flagSourcesFile), 0755)
		if err := atomicWriteString(*flagSourcesFile, body.Sources, 0644); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonOK(w)
		return
	}
	jsonErr(w, http.StatusMethodNotAllowed, "GET or POST only")
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	flag.Parse()
	log.SetPrefix("[panel] ")

	if *flagBuildNow {
		log.Printf("[master-builder] Memulai kompilasi CDB via CLI...")
		err := BuildMasterCDB(*flagFilesDir, *flagSourcesFile, *flagWhitelistFile, *flagCustomBLFile, 8, true)
		if err != nil {
			log.Fatalf("[master-builder] Kompilasi gagal: %v", err)
		}
		log.Printf("[master-builder] Kompilasi selesai!")
		os.Exit(0)
	}

	if *flagGenEnrollToken {
		cs := newClusterStore(*flagClusterNodesFile)
		tok := cs.GenerateEnrollmentToken(24 * time.Hour)
		fmt.Printf("Enrollment token (berlaku 24 jam):\n%s\n", tok)
		fmt.Printf("\nGunakan di edge node:\n  setup-edge.sh --master-url <MASTER_URL> --enroll-token %s\n", tok)
		os.Exit(0)
	}

	var err error
	jwtSecret, err = loadOrGenSecret(*flagSecret)
	if err != nil {
		log.Fatalf("cannot init JWT secret: %v", err)
	}

	startStatsTicker()

	mux := http.NewServeMux()

	// Static UI
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(indexHTML)
			return
		}
		http.NotFound(w, r)
	})

	// Public
	mux.HandleFunc("/api/login", handleLogin)

	// Protected
	mux.HandleFunc("/api/stats", auth(handleStats))
	mux.HandleFunc("/api/config", auth(handleConfig))
	mux.HandleFunc("/api/rpz", auth(handleRPZ))
	mux.HandleFunc("/api/upstream", auth(handleUpstream))
	mux.HandleFunc("/api/safesearch", auth(handleSafeSearch))
	mux.HandleFunc("/api/dotdoh", auth(handleDoTDoH))
	mux.HandleFunc("/api/settings", auth(handleSettings))

	// Master API Endpoints
	mux.HandleFunc("/api/master/status", auth(handleMasterStatus))
	mux.HandleFunc("/api/master/build", auth(handleMasterBuild))
	mux.HandleFunc("/api/master/sources", auth(handleMasterSources))

	// Master Cluster Management
	if *flagMaster {
		initClusterStore(*flagClusterNodesFile)
		mux.HandleFunc("/api/cluster/token", auth(handleClusterToken))
		mux.HandleFunc("/api/cluster/nodes", auth(handleClusterNodes))
		// Public Cluster Ingestion
		mux.HandleFunc("/api/cluster/register", handleClusterRegister)
		mux.HandleFunc("/api/cluster/heartbeat", handleClusterHeartbeat)
	}

	// Edge Cluster Agent & Config (available on all nodes)
	initEdgeAgent(*flagAgentStateFile, *flagMasterURL, *flagEnrollToken, *flagNodeName, *flagHeartbeatInterval)
	mux.HandleFunc("/api/cluster/config", auth(handleEdgeClusterConfig))

	// File Publisher (for Master Mode: serves trust.db, manifest.json)
	fs := http.FileServer(http.Dir(*flagFilesDir))
	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "public, must-revalidate, proxy-revalidate")
		http.StripPrefix("/files/", fs).ServeHTTP(w, r)
	})

	// Master Auto-Build Cron Ticker
	if *flagMaster && *flagBuildInterval > 0 {
		log.Printf("[master-builder] Penjadwal kompilasi otomatis aktif (interval: %v)", *flagBuildInterval)
		go func() {
			ticker := time.NewTicker(*flagBuildInterval)
			for range ticker.C {
				log.Printf("[master-builder] Menjalankan build otomatis terjadwal...")
				_ = BuildMasterCDB(*flagFilesDir, *flagSourcesFile, *flagWhitelistFile, *flagCustomBLFile, 8, false)
			}
		}()
	}

	// Jika secondary HTTP address diberikan (misal :8084), jalankan listener HTTP di background (Dual Mode)
	if *flagHTTPAddr != "" {
		httpSrv := &http.Server{
			Addr:         *flagHTTPAddr,
			Handler:      cors(mux),
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 30 * time.Second,
		}
		go func() {
			io.WriteString(os.Stderr, fmt.Sprintf("[panel] Starting HTTP (dual listener) on %s\n", *flagHTTPAddr))
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("[panel] HTTP listener error: %v", err)
			}
		}()
	}

	if *flagTLS {
		if err := ensureCert(*flagCert, *flagKey); err != nil {
			log.Fatalf("TLS cert error: %v", err)
		}
		srv := &http.Server{
			Addr:         *flagAddr,
			Handler:      cors(mux),
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 30 * time.Second,
			TLSConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
		}
		io.WriteString(os.Stderr, fmt.Sprintf("[panel] Starting HTTPS on %s\n", *flagAddr))
		if err := srv.ListenAndServeTLS(*flagCert, *flagKey); err != nil {
			log.Fatalf("server error: %v", err)
		}
	} else {
		// Pure HTTP Mode
		srv := &http.Server{
			Addr:         *flagAddr,
			Handler:      cors(mux),
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 30 * time.Second,
		}
		io.WriteString(os.Stderr, fmt.Sprintf("[panel] Starting HTTP (TLS disabled) on %s\n", *flagAddr))
		if err := srv.ListenAndServe(); err != nil {
			log.Fatalf("server error: %v", err)
		}
	}
}
