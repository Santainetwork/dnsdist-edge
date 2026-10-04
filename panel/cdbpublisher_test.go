package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// withCDBPublisherEnv points the publisher at dir with the given token and
// restores the previous flag values when the test finishes.
func withCDBPublisherEnv(t *testing.T, dir, token string) {
	t.Helper()
	oldDir, oldToken := *flagFilesDir, *flagCDBToken
	*flagFilesDir = dir
	*flagCDBToken = token
	t.Cleanup(func() {
		*flagFilesDir = oldDir
		*flagCDBToken = oldToken
	})
}

// withCDBFallback sets the advertised fallback source list for a test.
func withCDBFallback(t *testing.T, urls string) {
	t.Helper()
	old := *flagCDBFallbackURL
	*flagCDBFallbackURL = urls
	t.Cleanup(func() { *flagCDBFallbackURL = old })
}

func cdbTestFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func cdbDo(t *testing.T, h http.HandlerFunc, target, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if token != "" {
		req.Header.Set("X-CDB-Token", token)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// ─── Manifest ────────────────────────────────────────────────────────────────

func TestCDBManifestSuccess(t *testing.T) {
	dir := t.TempDir()
	body := []byte("cdb-payload-for-manifest")
	cdbTestFile(t, dir, "trust.db", body)
	sum := sha256.Sum256(body)
	wantSHA := hex.EncodeToString(sum[:])

	// Sidecar written by the master builder should be surfaced (version etc.).
	side := `{"version":7,"sha256":"` + wantSHA + `","size":1,"built_at":"2026-01-02T03:04:05Z","entries":4242}`
	cdbTestFile(t, dir, "manifest.json", []byte(side))

	withCDBPublisherEnv(t, dir, "")

	rec := cdbDo(t, handleCDBManifest, "/cdb/manifest.json", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}

	var mf cdbManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &mf); err != nil {
		t.Fatalf("decode manifest: %v (body=%s)", err, rec.Body.String())
	}
	if mf.SHA256 != wantSHA {
		t.Errorf("sha256 = %q, want %q", mf.SHA256, wantSHA)
	}
	if mf.Size != int64(len(body)) {
		t.Errorf("size = %d, want %d", mf.Size, len(body))
	}
	if mf.Version != 7 {
		t.Errorf("version = %d, want 7 (from sidecar)", mf.Version)
	}
	if mf.Entries != 4242 {
		t.Errorf("entries = %d, want 4242 (from sidecar)", mf.Entries)
	}
	if mf.BuiltAt == "" {
		t.Error("built_at must not be empty")
	}
	if mf.DownloadURL != "/cdb/blacklist.db" {
		t.Errorf("download_url = %q, want /cdb/blacklist.db", mf.DownloadURL)
	}
	if got := rec.Header().Get("ETag"); got != `"`+wantSHA+`"` {
		t.Errorf("ETag = %q, want quoted sha", got)
	}
}

func TestCDBManifestUsesContentAddressedHash(t *testing.T) {
	dir := t.TempDir()
	sum := sha256.Sum256([]byte("content"))
	name := "trust." + hex.EncodeToString(sum[:]) + ".db"
	cdbTestFile(t, dir, name, []byte("content"))

	withCDBPublisherEnv(t, dir, "")
	rec := cdbDo(t, handleCDBManifest, "/cdb/manifest.json?file="+name, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var mf cdbManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &mf); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if mf.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 = %q, want embedded digest", mf.SHA256)
	}
	if mf.Filename != name {
		t.Errorf("filename = %q, want %q", mf.Filename, name)
	}
}

// ─── Auth ────────────────────────────────────────────────────────────────────

// TestCDBUntrustedIsAllowedButThrottled covers the operator policy: a request
// without a valid token is NOT rejected outright (a fresh edge must be able to
// bootstrap), but it is throttled hard per IP so the download endpoint cannot be
// used to flood the master.
func TestCDBUntrustedIsAllowedButThrottled(t *testing.T) {
	dir := t.TempDir()
	cdbTestFile(t, dir, "trust.db", []byte("data"))
	withCDBPublisherEnv(t, dir, "s3cret-token")

	handlers := map[string]http.HandlerFunc{
		"/cdb/manifest.json": handleCDBManifest,
		"/cdb/blacklist.db":  handleCDBBlacklist,
		"/cdb/healthz":       handleCDBHealthz,
	}
	for path, h := range handlers {
		resetCDBLimiter()
		// First request without a token must succeed (bootstrap path).
		rec := cdbDo(t, h, path, "")
		if rec.Code != http.StatusOK {
			t.Errorf("%s: untrusted first request = %d, want 200 (must be allowed)", path, rec.Code)
		}
		if rec.Header().Get("X-CDB-Untrusted") != "1" {
			t.Errorf("%s: untrusted response missing X-CDB-Untrusted marker", path)
		}

		// Hammering must eventually be refused with 429 + Retry-After.
		var got429 bool
		for i := 0; i < 20; i++ {
			r := cdbDo(t, h, path, "")
			if r.Code == http.StatusTooManyRequests {
				got429 = true
				if r.Header().Get("Retry-After") == "" {
					t.Errorf("%s: 429 without Retry-After header", path)
				}
				break
			}
		}
		if !got429 {
			t.Errorf("%s: flooding was never throttled (no 429 in 20 rapid requests)", path)
		}
	}
}

// TestCDBValidTokenBypassesThrottle verifies a trusted edge is never limited,
// even after sustained requests.
func TestCDBValidTokenBypassesThrottle(t *testing.T) {
	dir := t.TempDir()
	cdbTestFile(t, dir, "trust.db", []byte("data"))
	withCDBPublisherEnv(t, dir, "s3cret-token")

	resetCDBLimiter()
	for i := 0; i < 50; i++ {
		rec := cdbDo(t, handleCDBBlacklist, "/cdb/blacklist.db", "s3cret-token")
		if rec.Code != http.StatusOK {
			t.Fatalf("trusted request #%d = %d, want 200 (token must bypass throttle)", i+1, rec.Code)
		}
		if rec.Header().Get("X-CDB-Untrusted") == "1" {
			t.Fatalf("trusted request #%d wrongly marked untrusted", i+1)
		}
	}
}

// TestCDBWrongTokenIsThrottledLikeUntrusted confirms a near-miss token does not
// grant the trusted fast path.
func TestCDBWrongTokenIsThrottledLikeUntrusted(t *testing.T) {
	dir := t.TempDir()
	cdbTestFile(t, dir, "trust.db", []byte("data"))
	withCDBPublisherEnv(t, dir, "s3cret-token")

	resetCDBLimiter()
	// A wrong token behaves as untrusted: allowed at first, then throttled.
	first := cdbDo(t, handleCDBManifest, "/cdb/manifest.json", "wrong-token")
	if first.Code != http.StatusOK {
		t.Fatalf("wrong token first request = %d, want 200 (treated as untrusted)", first.Code)
	}
	if first.Header().Get("X-CDB-Untrusted") != "1" {
		t.Error("wrong token was not treated as untrusted")
	}
	var got429 bool
	for i := 0; i < 20; i++ {
		if cdbDo(t, handleCDBManifest, "/cdb/manifest.json", "wrong-token").Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Error("wrong-token flooding was never throttled")
	}
}

func TestCDBNoTokenAllowsAccess(t *testing.T) {
	dir := t.TempDir()
	cdbTestFile(t, dir, "trust.db", []byte("data"))
	withCDBPublisherEnv(t, dir, "")

	resetCDBLimiter()
	rec := cdbDo(t, handleCDBManifest, "/cdb/manifest.json", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when no token configured (body=%s)", rec.Code, rec.Body.String())
	}
}

// ─── Path safety ─────────────────────────────────────────────────────────────

func TestCDBPathTraversalRejected(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cdbTestFile(t, dir, "trust.db", []byte("ok"))
	// A sensitive file outside the publish directory that must never leak.
	secretPath := filepath.Join(root, "secret.db")
	if err := os.WriteFile(secretPath, []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}

	withCDBPublisherEnv(t, dir, "")

	attacks := []string{
		"/cdb/blacklist.db?file=../../etc/passwd",
		"/cdb/blacklist.db?file=../secret.db",
		"/cdb/blacklist.db?file=../../../../etc/passwd",
		"/cdb/blacklist.db?file=/etc/passwd",
		"/cdb/blacklist.db?file=" + secretPath,
		"/cdb/blacklist.db?file=./../secret.db",
		"/cdb/blacklist.db?file=subdir/../../secret.db",
		"/cdb/blacklist.db?file=.",
		"/cdb/blacklist.db?file=..",
		"/cdb/manifest.json?file=../secret.db",
		"/cdb/healthz?file=../secret.db",
	}
	for _, target := range attacks {
		for name, h := range map[string]http.HandlerFunc{
			"blacklist": handleCDBBlacklist,
			"manifest":  handleCDBManifest,
			"healthz":   handleCDBHealthz,
		} {
			rec := cdbDo(t, h, target, "")
			if rec.Code == http.StatusOK {
				t.Errorf("%s handler %s: got 200, want rejection", name, target)
			}
			if body := rec.Body.String(); containsStr(body, "TOPSECRET") {
				t.Errorf("%s handler %s: leaked file content", name, target)
			}
		}
	}
}

func TestCDBSymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.db")
	if err := os.WriteFile(outside, []byte("ESCAPED"), 0o600); err != nil {
		t.Fatal(err)
	}
	// trust.db inside the publish dir is a symlink pointing outside it.
	if err := os.Symlink(outside, filepath.Join(dir, "trust.db")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	withCDBPublisherEnv(t, dir, "")
	rec := cdbDo(t, handleCDBBlacklist, "/cdb/blacklist.db", "")
	if rec.Code == http.StatusOK || containsStr(rec.Body.String(), "ESCAPED") {
		t.Fatalf("symlink escape was served: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCDBUnknownFileRejected(t *testing.T) {
	dir := t.TempDir()
	cdbTestFile(t, dir, "trust.db", []byte("ok"))
	cdbTestFile(t, dir, "sources.txt", []byte("http://evil"))
	withCDBPublisherEnv(t, dir, "")

	// Arbitrary files in the publish dir must not be served.
	for _, f := range []string{"sources.txt", "manifest.json", "notes.md", "whitelist.txt"} {
		rec := cdbDo(t, handleCDBBlacklist, "/cdb/blacklist.db?file="+f, "")
		if rec.Code == http.StatusOK {
			t.Errorf("arbitrary file %q served with 200", f)
		}
	}
}

// ─── healthz ─────────────────────────────────────────────────────────────────

func TestCDBHealthzMissingDB(t *testing.T) {
	dir := t.TempDir()
	withCDBPublisherEnv(t, dir, "")

	rec := cdbDo(t, handleCDBHealthz, "/cdb/healthz", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when DB missing (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestCDBHealthzPresentDB(t *testing.T) {
	dir := t.TempDir()
	cdbTestFile(t, dir, "trust.db", []byte("healthy-db-bytes"))
	withCDBPublisherEnv(t, dir, "")

	rec := cdbDo(t, handleCDBHealthz, "/cdb/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode healthz: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status field = %v, want ok", body["status"])
	}
}

// ─── Download route ──────────────────────────────────────────────────────────

func TestCDBBlacklistServesActiveFile(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("the-real-blacklist-bytes")
	cdbTestFile(t, dir, "trust.db", payload)
	withCDBPublisherEnv(t, dir, "")

	rec := cdbDo(t, handleCDBBlacklist, "/cdb/blacklist.db", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(payload) {
		t.Fatalf("body = %q, want %q", rec.Body.String(), payload)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("content-type = %q, want application/octet-stream", ct)
	}
}

func TestCDBBlacklistSupportsRangeRequests(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("0123456789abcdef")
	cdbTestFile(t, dir, "trust.db", payload)
	withCDBPublisherEnv(t, dir, "")

	req := httptest.NewRequest(http.MethodGet, "/cdb/blacklist.db", nil)
	req.Header.Set("Range", "bytes=4-7")
	rec := httptest.NewRecorder()
	handleCDBBlacklist(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 (body=%s)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "4567" {
		t.Fatalf("range body = %q, want 4567", rec.Body.String())
	}
}

func TestCDBMethodNotAllowed(t *testing.T) {
	dir := t.TempDir()
	cdbTestFile(t, dir, "trust.db", []byte("ok"))
	withCDBPublisherEnv(t, dir, "")

	req := httptest.NewRequest(http.MethodPost, "/cdb/manifest.json", nil)
	rec := httptest.NewRecorder()
	handleCDBManifest(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// ─── Master-managed source distribution ──────────────────────────────────────

func TestCDBSourcesPrefersMasterThenFallbacks(t *testing.T) {
	dir := t.TempDir()
	cdbTestFile(t, dir, "trust.db", []byte("ok"))
	withCDBPublisherEnv(t, dir, "")
	withCDBFallback(t, "https://mirror1.example/trust.db , http://mirror2.example:8084/cdb/blacklist.db")

	req := httptest.NewRequest(http.MethodGet, "http://master.example:8084/cdb/sources.json", nil)
	rec := httptest.NewRecorder()
	handleCDBSources(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	var doc cdbSourcesDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode sources: %v (body=%s)", err, rec.Body.String())
	}
	// Master must be advertised first so edges prefer it.
	if doc.Primary != "http://master.example:8084/cdb/blacklist.db" {
		t.Errorf("primary = %q, want master download URL", doc.Primary)
	}
	if len(doc.AllSources) != 3 {
		t.Fatalf("sources = %v, want 3 entries", doc.AllSources)
	}
	if doc.AllSources[0] != doc.Primary {
		t.Errorf("sources[0] = %q, want primary first", doc.AllSources[0])
	}
	if doc.AllSources[1] != "https://mirror1.example/trust.db" {
		t.Errorf("sources[1] = %q, want trimmed mirror1", doc.AllSources[1])
	}
	if doc.AllSources[2] != "http://mirror2.example:8084/cdb/blacklist.db" {
		t.Errorf("sources[2] = %q, want mirror2", doc.AllSources[2])
	}
	if len(doc.FallbackURLs) != 2 {
		t.Errorf("fallback_urls = %v, want 2 entries", doc.FallbackURLs)
	}
	if doc.UpdateCommand == "" {
		t.Error("update_command should be present when fallbacks exist")
	}
}

func TestCDBSourcesRejectsNonHTTPSchemes(t *testing.T) {
	dir := t.TempDir()
	withCDBPublisherEnv(t, dir, "")
	withCDBFallback(t, "ftp://bad.example/db,file:///etc/passwd,javascript:alert(1),https://good.example/db")

	got := parseCDBFallbackURLs(*flagCDBFallbackURL)
	if len(got) != 1 || got[0] != "https://good.example/db" {
		t.Fatalf("parsed = %v, want only the https URL", got)
	}
}

func TestCDBSourcesDedupesAndRequiresAuth(t *testing.T) {
	dir := t.TempDir()
	withCDBPublisherEnv(t, dir, "tok")
	withCDBFallback(t, "https://dup.example/db,https://dup.example/db")

	// Untrusted requests are allowed but throttled, never hard-rejected.
	resetCDBLimiter()
	rec := cdbDo(t, handleCDBSources, "/cdb/sources.json", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for untrusted bootstrap (body=%s)", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-CDB-Untrusted") != "1" {
		t.Error("untrusted sources request not marked X-CDB-Untrusted")
	}

	req := httptest.NewRequest(http.MethodGet, "/cdb/sources.json", nil)
	req.Header.Set("X-CDB-Token", "tok")
	rec = httptest.NewRecorder()
	handleCDBSources(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var doc cdbSourcesDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(doc.FallbackURLs) != 1 {
		t.Errorf("fallback_urls = %v, want duplicates removed", doc.FallbackURLs)
	}
}

// ─── End-to-end route registration ───────────────────────────────────────────

// TestCDBPublisherRouteRegistration exercises the exact mux.HandleFunc lines the
// coordinator must add to main.go, over a real HTTP server, so a routing or
// path-safety regression is caught the way edge nodes would hit it.
func TestCDBPublisherRouteRegistration(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("registered-route-db-bytes")
	cdbTestFile(t, dir, "trust.db", payload)
	withCDBPublisherEnv(t, dir, "route-token")
	withCDBFallback(t, "https://mirror.example/trust.db")

	mux := http.NewServeMux()
	mux.HandleFunc("/cdb/manifest.json", handleCDBManifest)
	mux.HandleFunc("/cdb/blacklist.db", handleCDBBlacklist)
	mux.HandleFunc("/cdb/healthz", handleCDBHealthz)
	mux.HandleFunc("/cdb/sources.json", handleCDBSources)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	get := func(path, token string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("X-CDB-Token", token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, body
	}

	// Untrusted requests bootstrap successfully, then get throttled under load.
	// They are never hard-rejected, and the trusted token always bypasses.
	resetCDBLimiter()
	for _, p := range []string{"/cdb/manifest.json", "/cdb/blacklist.db", "/cdb/healthz", "/cdb/sources.json"} {
		resp, _ := get(p, "")
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("GET %s without token: status %d, want 200 (bootstrap) or 429 (throttled)", p, resp.StatusCode)
		}
	}
	// A trusted request must never be throttled.
	for _, p := range []string{"/cdb/manifest.json", "/cdb/blacklist.db", "/cdb/healthz", "/cdb/sources.json"} {
		resp, _ := get(p, "route-token")
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Errorf("GET %s with valid token was throttled; token must bypass", p)
		}
	}

	// healthz
	resp, _ := get("/cdb/healthz", "route-token")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}

	// manifest
	resp, body := get("/cdb/manifest.json", "route-token")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("manifest status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	var mf cdbManifest
	if err := json.Unmarshal(body, &mf); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	sum := sha256.Sum256(payload)
	if mf.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("manifest sha mismatch over HTTP")
	}

	// download
	resp, body = get("/cdb/blacklist.db", "route-token")
	if resp.StatusCode != http.StatusOK || string(body) != string(payload) {
		t.Fatalf("download status=%d body=%q, want 200 %q", resp.StatusCode, body, payload)
	}

	// traversal over the wire must not escape
	resp, body = get("/cdb/blacklist.db?file=../../etc/passwd", "route-token")
	if resp.StatusCode == http.StatusOK {
		t.Errorf("traversal returned 200 (body=%s)", body)
	}

	// sources
	resp, body = get("/cdb/sources.json", "route-token")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sources status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	var doc cdbSourcesDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode sources: %v", err)
	}
	if doc.Primary != srv.URL+"/cdb/blacklist.db" {
		t.Errorf("primary = %q, want %q", doc.Primary, srv.URL+"/cdb/blacklist.db")
	}
}

func containsStr(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		indexOfStr(haystack, needle) >= 0
}

func indexOfStr(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
