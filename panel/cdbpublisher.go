// CDB Publisher — lets a central master serve its already-built CDB to edge
// nodes over HTTP, so edges download + verify the database instead of
// rebuilding it themselves.
//
// Routes (registered by main.go):
//
//	GET /cdb/manifest.json  → metadata of the active CDB (version, sha256, size, built_at)
//	GET /cdb/blacklist.db   → the active CDB file, served from --files-dir
//	GET /cdb/healthz        → 200 if the CDB is present and readable, else 500
//	GET /cdb/sources.json   → ordered CDB source list advertised to edge nodes
//
// Auth: the request must carry header X-CDB-Token matching --cdb-token /
// PANEL_CDB_TOKEN. When no token is configured access is allowed, but a
// one-time warning is logged.
//
// Master-managed source distribution: the master advertises this publisher as
// the primary CDB source and lists operator-configured fallback URLs
// (--cdb-fallback-url / PANEL_CDB_FALLBACK_URL, or the panel-managed
// /api/master/cdb-sources endpoint). Edges sync from the master first and fall
// back to the advertised URLs when the master is unreachable.
//
// Only files that live inside --files-dir and that match a whitelisted CDB
// filename are ever served. Directory listings and arbitrary files are refused.
package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// flagCDBToken is the shared secret edge nodes must present as X-CDB-Token.
// Declared here (not in main.go) so this file owns the publisher end to end.
var flagCDBToken = flag.String("cdb-token", envOr("PANEL_CDB_TOKEN", ""),
	"Shared secret required in X-CDB-Token for /cdb/* publisher routes (empty = unauthenticated)")

// flagCDBFallbackURL is the ordered list of CDB URLs the master advertises to
// edges as fallback sources for when the master itself is unreachable.
var flagCDBFallbackURL = flag.String("cdb-fallback-url", envOr("PANEL_CDB_FALLBACK_URL", ""),
	"Comma-separated fallback CDB source URLs advertised to edge nodes via /cdb/sources.json")

const (
	cdbSidecarFilename = "manifest.json"
	cdbDownloadRoute   = "/cdb/blacklist.db"
	cdbSourcesRoute    = "/cdb/sources.json"
)

var (
	errCDBUnavailable = errors.New("cdb: no active database available")
	errCDBForbidden   = errors.New("cdb: requested path is not permitted")
)

// cdbActiveNames is the ordered list of filenames treated as "the active CDB"
// inside --files-dir. The master builder writes trust.db (a symlink to
// trust.<sha>.db); blacklist.db is accepted for edge/legacy layouts.
var cdbActiveNames = []string{"trust.db", "blacklist.db"}

// cdbContentAddressedRE matches content-addressed filenames such as
// trust.<64 hex>.db / blacklist.<64 hex>.db. For those the SHA-256 is already
// embedded in the name, so it can be trusted without re-hashing the file.
var cdbContentAddressedRE = regexp.MustCompile(`^(?:trust|blacklist)\.([0-9a-f]{64})\.db$`)

// ─── Path resolution & safety ────────────────────────────────────────────────

func cdbFilesDir() string { return filepath.Clean(*flagFilesDir) }

// cdbNameAllowed reports whether name is a bare, whitelisted CDB filename.
// It rejects separators, "..", absolute paths and any other file outright.
func cdbNameAllowed(name string) bool {
	switch name {
	case "trust.db", "blacklist.db":
		return true
	}
	return cdbContentAddressedRE.MatchString(name)
}

func cdbActiveName() (string, bool) {
	dir := cdbFilesDir()
	for _, name := range cdbActiveNames {
		if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && !fi.IsDir() {
			return name, true
		}
	}
	return "", false
}

// cdbWithinDir reports whether path is dir itself or lexically nested under dir.
func cdbWithinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	return true
}

// resolveCDBPath validates name (or the active CDB when name is empty) and
// returns the resolved, symlink-free path of a regular file inside --files-dir.
func resolveCDBPath(name string) (string, error) {
	if name == "" {
		active, ok := cdbActiveName()
		if !ok {
			return "", errCDBUnavailable
		}
		name = active
	}

	// A whitelisted name cannot contain separators or "..", which blocks
	// traversal, absolute paths and directory listings in one step.
	if !cdbNameAllowed(name) {
		return "", errCDBForbidden
	}

	dir := cdbFilesDir()
	full := filepath.Join(dir, name)
	if !cdbWithinDir(dir, full) {
		return "", errCDBForbidden
	}

	// Resolve symlinks (trust.db → trust.<sha>.db) and re-check containment so
	// a symlink cannot be used to escape the publish directory.
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", errCDBUnavailable
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errCDBUnavailable
		}
		return "", err
	}
	if !cdbWithinDir(realDir, resolved) {
		return "", errCDBForbidden
	}

	fi, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errCDBUnavailable
		}
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", errCDBForbidden
	}
	return resolved, nil
}

// ─── Digest helpers ──────────────────────────────────────────────────────────

type cdbHashKey struct {
	path    string
	size    int64
	modTime time.Time
}

var (
	cdbHashMu    sync.Mutex
	cdbHashCache cdbHashKey
	cdbHashSum   string
)

// cdbFileSHA256 returns the SHA-256 of the file at path plus its size. When the
// filename is content-addressed the embedded digest is used; otherwise the file
// is hashed and the result memoised until its size/mtime change.
func cdbFileSHA256(path string) (string, int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	if m := cdbContentAddressedRE.FindStringSubmatch(filepath.Base(path)); m != nil {
		return m[1], fi.Size(), nil
	}

	cdbHashMu.Lock()
	if cdbHashSum != "" && cdbHashCache.path == path &&
		cdbHashCache.size == fi.Size() && cdbHashCache.modTime.Equal(fi.ModTime()) {
		sum := cdbHashSum
		cdbHashMu.Unlock()
		return sum, fi.Size(), nil
	}
	cdbHashMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	sum := hex.EncodeToString(h.Sum(nil))

	cdbHashMu.Lock()
	cdbHashCache = cdbHashKey{path: path, size: fi.Size(), modTime: fi.ModTime()}
	cdbHashSum = sum
	cdbHashMu.Unlock()

	return sum, n, nil
}

// ─── Auth & request guards ───────────────────────────────────────────────────

var cdbNoTokenWarn sync.Once

// cdbAuthorize enforces the X-CDB-Token shared secret. With no token configured
// it allows the request but warns once so the operator notices the exposure.
func cdbAuthorize(w http.ResponseWriter, r *http.Request) bool {
	token := strings.TrimSpace(*flagCDBToken)
	if token == "" {
		cdbNoTokenWarn.Do(func() {
			log.Printf("[cdb-publisher] WARNING: no --cdb-token/PANEL_CDB_TOKEN configured; /cdb/* is unauthenticated")
		})
		return true
	}
	got := strings.TrimSpace(r.Header.Get("X-CDB-Token"))
	if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
		w.Header().Set("WWW-Authenticate", `X-CDB-Token realm="cdb-publisher"`)
		jsonErr(w, http.StatusUnauthorized, "missing or invalid X-CDB-Token")
		return false
	}
	return true
}

func cdbRequireGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		jsonErr(w, http.StatusMethodNotAllowed, "GET or HEAD only")
		return false
	}
	return true
}

func cdbWriteResolveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errCDBForbidden):
		jsonErr(w, http.StatusForbidden, err.Error())
	case errors.Is(err, errCDBUnavailable), os.IsNotExist(err):
		jsonErr(w, http.StatusNotFound, err.Error())
	default:
		jsonErr(w, http.StatusInternalServerError, err.Error())
	}
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// cdbManifest is the metadata document describing the active CDB.
type cdbManifest struct {
	Version     int    `json:"version"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	BuiltAt     string `json:"built_at"`
	Filename    string `json:"filename,omitempty"`
	Entries     int    `json:"entries,omitempty"`
	Source      string `json:"source,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
}

// cdbSidecar mirrors the manifest.json the master builder writes to --files-dir.
type cdbSidecar struct {
	Version     int    `json:"version"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	BuiltAt     string `json:"built_at"`
	Entries     int    `json:"entries"`
	Source      string `json:"source"`
	DownloadURL string `json:"download_url"`
}

func cdbMergeSidecar(mf *cdbManifest) {
	b, err := os.ReadFile(filepath.Join(cdbFilesDir(), cdbSidecarFilename))
	if err != nil {
		return
	}
	var side cdbSidecar
	if json.Unmarshal(b, &side) != nil {
		return
	}
	if side.Version > 0 {
		mf.Version = side.Version
	}
	if side.BuiltAt != "" {
		mf.BuiltAt = side.BuiltAt
	}
	if side.Entries > 0 {
		mf.Entries = side.Entries
	}
	if side.Source != "" {
		mf.Source = side.Source
	}
}

// handleCDBManifest serves metadata for the active CDB.
func handleCDBManifest(w http.ResponseWriter, r *http.Request) {
	if !cdbRequireGET(w, r) || !cdbAuthorize(w, r) {
		return
	}
	path, err := resolveCDBPath(strings.TrimSpace(r.URL.Query().Get("file")))
	if err != nil {
		cdbWriteResolveError(w, err)
		return
	}

	sum, size, err := cdbFileSHA256(path)
	if err != nil {
		cdbWriteResolveError(w, err)
		return
	}

	mf := cdbManifest{
		Version:  1,
		SHA256:   sum,
		Size:     size,
		BuiltAt:  cdbFileBuiltAt(path),
		Filename: filepath.Base(path),
	}
	cdbMergeSidecar(&mf)

	// Live digest/size of the active file are authoritative; the download route
	// always points at this publisher so peers fetch from here.
	mf.SHA256 = sum
	mf.Size = size
	mf.DownloadURL = cdbDownloadRoute

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", `"`+sum+`"`)
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_ = json.NewEncoder(w).Encode(mf)
}

func cdbFileBuiltAt(path string) string {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime().UTC().Format(time.RFC3339)
	}
	return ""
}

// handleCDBBlacklist streams the active CDB file. http.ServeFile adds
// Range + If-Modified-Since support, which the edge sync script (aria2c -x8)
// relies on for multi-connection downloads.
func handleCDBBlacklist(w http.ResponseWriter, r *http.Request) {
	if !cdbRequireGET(w, r) || !cdbAuthorize(w, r) {
		return
	}
	path, err := resolveCDBPath(strings.TrimSpace(r.URL.Query().Get("file")))
	if err != nil {
		cdbWriteResolveError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="blacklist.db"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, path)
}

// handleCDBHealthz reports 200 when the active CDB is present and readable, and
// 500 otherwise.
func handleCDBHealthz(w http.ResponseWriter, r *http.Request) {
	if !cdbRequireGET(w, r) || !cdbAuthorize(w, r) {
		return
	}
	path, err := resolveCDBPath(strings.TrimSpace(r.URL.Query().Get("file")))
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "cdb unhealthy: "+err.Error())
		return
	}

	f, err := os.Open(path)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "cdb unhealthy: "+err.Error())
		return
	}
	defer f.Close()

	buf := make([]byte, 1)
	if _, err := f.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
		jsonErr(w, http.StatusInternalServerError, "cdb unreadable: "+err.Error())
		return
	}
	fi, err := f.Stat()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "cdb unreadable: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   "ok",
		"file":     filepath.Base(path),
		"size":     fi.Size(),
		"built_at": fi.ModTime().UTC().Format(time.RFC3339),
	})
}

// ─── Master-managed source distribution ──────────────────────────────────────

// cdbSourcesDoc is the source list advertised to edge nodes. The master's own
// publisher is always listed first ("prefer the master"), followed by the
// operator-configured fallbacks used when the master is unreachable.
type cdbSourcesDoc struct {
	Primary       string   `json:"primary"`
	FallbackURLs  []string `json:"fallback_urls"`
	AllSources    []string `json:"sources"`
	AdvertisedBy  string   `json:"advertised_by"`
	UpdateCommand string   `json:"update_command,omitempty"`
}

// parseCDBFallbackURLs splits the comma-separated fallback flag, trimming
// blanks and rejecting non-HTTP(S) entries so a typo cannot inject garbage
// into the edge sync list.
func parseCDBFallbackURLs(raw string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		u := strings.TrimSpace(part)
		if u == "" {
			continue
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			continue
		}
		if _, dup := seen[u]; dup {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

// cdbSelfURL reconstructs this publisher's absolute base URL from the request so
// the advertised primary source works regardless of how the master is reached.
func cdbSelfURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if xf := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); xf == "http" || xf == "https" {
		scheme = xf
	}
	host := r.Host
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// handleCDBSources advertises the ordered CDB source list to edge nodes. Edges
// try the master publisher first and only fall back to the listed URLs when the
// master is down.
func handleCDBSources(w http.ResponseWriter, r *http.Request) {
	if !cdbRequireGET(w, r) || !cdbAuthorize(w, r) {
		return
	}

	fallbacks := parseCDBFallbackURLs(*flagCDBFallbackURL)
	self := cdbSelfURL(r)

	var all []string
	if self != "" {
		all = append(all, self+cdbDownloadRoute)
	}
	all = append(all, fallbacks...)

	doc := cdbSourcesDoc{
		FallbackURLs: fallbacks,
		AllSources:   all,
		AdvertisedBy: self,
	}
	if self != "" {
		doc.Primary = self + cdbDownloadRoute
	}
	if len(fallbacks) > 0 {
		doc.UpdateCommand = "update-blacklist.sh --set-cdb-sources \"" +
			strings.Join(all, ",") + "\""
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_ = json.NewEncoder(w).Encode(doc)
}
