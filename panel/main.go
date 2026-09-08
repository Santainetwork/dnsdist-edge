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

// ─── Config ──────────────────────────────────────────────────────────────────

var (
	flagAddr      = flag.String("addr", envOr("PANEL_ADDR", ":8443"), "Listen address")
	flagConf      = flag.String("config", envOr("DNSDIST_CONF", "/etc/dnsdist/dnsdist.conf"), "dnsdist.conf path")
	flagUpstreams = flag.String("upstreams", envOr("DNSDIST_UPSTREAMS", "/etc/dnsdist/upstreams.conf"), "upstreams.conf path")
	flagCert      = flag.String("cert", envOr("PANEL_CERT", "/var/lib/dnsdist/panel-cert.pem"), "TLS cert path")
	flagKey       = flag.String("key", envOr("PANEL_KEY", "/var/lib/dnsdist/panel-key.pem"), "TLS key path")
	flagSecret    = flag.String("secret-file", envOr("PANEL_SECRET_FILE", "/var/lib/dnsdist/panel.secret"), "JWT secret file")
	flagSetupSh   = flag.String("setup-sh", "/usr/local/bin/setup-edge.sh", "Path to setup-edge.sh")
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
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

func startStatsTicker() {
	// warm up CPU baseline
	readCPUStat()
	go func() {
		t := time.NewTicker(5 * time.Second)
		for range t.C {
			updateQPS()
		}
	}()
}

// ─── Config parsing ───────────────────────────────────────────────────────────

type SafeSearch struct {
	Google  bool `json:"google"`
	Bing    bool `json:"bing"`
	YouTube bool `json:"youtube"`
}

type NodeConfig struct {
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
	cfg := NodeConfig{
		BlockMode:  "rpz",
		CertPath:   "/etc/dnsdist/certs/server.crt",
		KeyPath:    "/etc/dnsdist/certs/server.key",
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
	// safesearch: check /etc/dnsdist/safesearch.conf
	if b, err := os.ReadFile("/etc/dnsdist/safesearch.conf"); err == nil {
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
	return atomicWriteString("/etc/dnsdist/safesearch.conf", sb.String(), 0o644)
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
	return atomicWriteString("/etc/dnsdist/dotdoh.conf", sb.String(), 0o644)
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
	// Simple: store plaintext password in separate file panel.password
	storedPass, _ := os.ReadFile("/var/lib/dnsdist/panel.password")
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
		"queries_total":   stats.udpInTotal.Load(),
		"blocked_total":   0, // ponytail: hook ke dnsdist console socket, add when /run/dnsdist/dnsdist.sock exposed
		"cache_hit_pct":   0.0,
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
	if body.Cert == "" {
		body.Cert = "/etc/dnsdist/certs/server.crt"
	}
	if body.Key == "" {
		body.Key = "/etc/dnsdist/certs/server.key"
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
		_ = atomicWriteString("/var/lib/dnsdist/panel.password", body.PanelPassword, 0o600)
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

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	flag.Parse()
	log.SetPrefix("[panel] ")

	var err error
	jwtSecret, err = loadOrGenSecret(*flagSecret)
	if err != nil {
		log.Fatalf("cannot init JWT secret: %v", err)
	}

	if err := ensureCert(*flagCert, *flagKey); err != nil {
		log.Fatalf("TLS cert error: %v", err)
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
}
