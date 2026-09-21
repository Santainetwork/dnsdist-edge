# Web Whitelist v2.9.0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Tambahkan pengelolaan whitelist domain/IP melalui Central Master web panel, dengan validasi, backup, atomic write, build aman, test, dan release `v2.9.0`.

**Architecture:** Pertahankan `whitelist.txt` sebagai storage plain file. Tambahkan normalizer backend dan handler authenticated `/api/master/whitelist`; UI memakai pola existing `master/sources`. Route Master hanya didaftarkan saat `flagMaster` aktif. Build tetap menggunakan `loadWhitelistSet` dan content-addressed CDB existing.

**Tech Stack:** Go stdlib, `net`, `net/http`, `encoding/json`, `os`, `bufio`; embedded single-file HTML/CSS/JavaScript; existing `github.com/colinmarc/cdb` and `github.com/miekg/dns`.

---

## Task 1: Normalizer whitelist, test-first

**Files:**
- Create: `panel/whitelist_test.go`
- Modify: `panel/main.go`

- [ ] **Step 1: Tulis test normalisasi yang gagal**

Tambahkan test table untuk fungsi yang akan dibuat:

```go
func TestNormalizeWhitelist(t *testing.T) {
	tests := []struct {
		name, input, want string
		invalid []int
		count, duplicates int
	}{
		{"canonical", " Example.COM.\n192.0.2.1\n# note\nexample.com\n", "example.com\n192.0.2.1\n# note\n", nil, 2, 1},
		{"ipv6", "2001:DB8::1\n", "2001:db8::1\n", nil, 1, 0},
		{"invalid", "https://example.com\n*.example.com\nfoo bar\n", "", []int{1, 2, 3}, 0, 0},
		{"dns-label", "-bad.example\nvalid.example\n", "valid.example\n", []int{1}, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, invalid, count, duplicates := normalizeWhitelist(tt.input)
			if got != tt.want { t.Fatalf("normalized = %q, want %q", got, tt.want) }
			if !reflect.DeepEqual(invalid, tt.invalid) { t.Fatalf("invalid = %v, want %v", invalid, tt.invalid) }
			if count != tt.count || duplicates != tt.duplicates { t.Fatalf("count/duplicates = %d/%d, want %d/%d", count, duplicates, tt.count, tt.duplicates) }
		})
	}
}
```

Use `reflect` in the test import. The expected canonical text preserves comments, terminates each retained line with `\n`, keeps first-seen order, and reports one-based invalid line numbers.

- [ ] **Step 2: Jalankan test untuk memastikan gagal**

```bash
cd panel && go test ./... -run TestNormalizeWhitelist -count=1
```

Expected: FAIL because `normalizeWhitelist` is undefined.

- [ ] **Step 3: Implementasikan normalizer minimal**

In `panel/main.go`, add `reflect` only to the test, not production. Add these stdlib helpers near existing validation helpers:

```go
var whitelistLabelRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func normalizeWhitelist(raw string) (string, []int, int, int) {
	seen := make(map[string]struct{})
	var out []string
	var invalid []int
	count, duplicates := 0, 0
	for lineNo, rawLine := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" { continue }
		if strings.HasPrefix(line, "#") { out = append(out, line); continue }
		line = strings.TrimSuffix(strings.ToLower(line), ".")
		if ip := net.ParseIP(line); ip != nil {
			line = ip.String()
		} else if !validWhitelistDomain(line) {
			invalid = append(invalid, lineNo+1)
			continue
		}
		if _, ok := seen[line]; ok { duplicates++; continue }
		seen[line] = struct{}{}
		out = append(out, line)
		count++
	}
	if len(out) == 0 { return "", invalid, count, duplicates }
	return strings.Join(out, "\n") + "\n", invalid, count, duplicates
}

func validWhitelistDomain(value string) bool {
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "/ *\t\r\n") { return false }
	for _, label := range strings.Split(value, ".") {
		if len(label) > 63 || !whitelistLabelRE.MatchString(label) { return false }
	}
	return true
}
```

Use the existing `regexp`, `net`, and `strings` imports. Do not accept hosts, AdGuard, wildcard, URL, Unicode, or subdomain wildcard syntax.

- [ ] **Step 4: Jalankan test sampai PASS**

```bash
cd panel && gofmt -w main.go whitelist_test.go && go test ./... -run TestNormalizeWhitelist -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/main.go panel/whitelist_test.go
git commit -m "feat: normalize master whitelist entries"
```

## Task 2: Authenticated whitelist API, backup, and tests

**Files:**
- Modify: `panel/main.go`
- Modify: `panel/whitelist_test.go`

- [ ] **Step 1: Tulis test handler yang gagal**

Add tests using `httptest`, temporary `flagWhitelistFile`, and initialized `jwtSecret` only where direct handler testing does not require auth:

```go
func TestHandleMasterWhitelistCanonicalSaveAndBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "whitelist.txt")
	if err := os.WriteFile(path, []byte("old.example\n"), 0600); err != nil { t.Fatal(err) }
	old := *flagWhitelistFile; *flagWhitelistFile = path; defer func() { *flagWhitelistFile = old }()
	req := httptest.NewRequest(http.MethodPost, "/api/master/whitelist", strings.NewReader(`{"whitelist":" Example.COM.\nexample.com\n# note\n"}`))
	rec := httptest.NewRecorder(); handleMasterWhitelist(rec, req)
	if rec.Code != http.StatusOK { t.Fatalf("status = %d, body = %s", rec.Code, rec.Body) }
	got, _ := os.ReadFile(path); if string(got) != "example.com\n# note\n" { t.Fatalf("saved = %q", got) }
	backup, _ := os.ReadFile(path + ".bak"); if string(backup) != "old.example\n" { t.Fatalf("backup = %q", backup) }
}

func TestHandleMasterWhitelistRejectsInvalidWithoutChangingFile(t *testing.T) {
	dir := t.TempDir(); path := filepath.Join(dir, "whitelist.txt")
	os.WriteFile(path, []byte("keep.example\n"), 0600)
	old := *flagWhitelistFile; *flagWhitelistFile = path; defer func() { *flagWhitelistFile = old }()
	req := httptest.NewRequest(http.MethodPost, "/api/master/whitelist", strings.NewReader(`{"whitelist":"https://bad.example\n"}`))
	rec := httptest.NewRecorder(); handleMasterWhitelist(rec, req)
	if rec.Code != http.StatusBadRequest { t.Fatalf("status = %d", rec.Code) }
	got, _ := os.ReadFile(path); if string(got) != "keep.example\n" { t.Fatalf("file changed: %q", got) }
}
```

- [ ] **Step 2: Jalankan test untuk memastikan gagal**

```bash
cd panel && go test ./... -run 'TestHandleMasterWhitelist' -count=1
```

Expected: FAIL because `handleMasterWhitelist` is undefined.

- [ ] **Step 3: Implementasikan handler dan payload limit**

Add the handler beside `handleMasterSources`. Use `errors.As` with `*http.MaxBytesError` so an oversized body returns 413 rather than generic 400:

```go
func handleMasterWhitelist(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		content, err := os.ReadFile(*flagWhitelistFile)
		if err != nil && !os.IsNotExist(err) { jsonErr(w, http.StatusInternalServerError, "cannot read whitelist: "+err.Error()); return }
		_, _, count, _ := normalizeWhitelist(string(content))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"whitelist": string(content), "count": count})
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var body struct { Whitelist string `json:"whitelist"` }
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) { jsonErr(w, http.StatusRequestEntityTooLarge, "whitelist exceeds 1 MiB"); return }
			jsonErr(w, http.StatusBadRequest, "invalid json"); return
		}
		var extra any
		if dec.Decode(&extra) != io.EOF { jsonErr(w, http.StatusBadRequest, "request must contain one JSON object"); return }
		normalized, invalid, count, duplicates := normalizeWhitelist(body.Whitelist)
		if len(invalid) > 0 { jsonErr(w, http.StatusBadRequest, fmt.Sprintf("invalid whitelist lines: %v", invalid)); return }
		path := *flagWhitelistFile
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { jsonErr(w, http.StatusInternalServerError, "cannot create whitelist directory: "+err.Error()); return }
		if old, err := os.ReadFile(path); err == nil {
			if err := atomicWrite(path+".bak", old, 0600); err != nil { jsonErr(w, http.StatusInternalServerError, "cannot backup whitelist: "+err.Error()); return }
		} else if !os.IsNotExist(err) { jsonErr(w, http.StatusInternalServerError, "cannot read whitelist for backup: "+err.Error()); return }
		if err := atomicWriteString(path, normalized, 0600); err != nil { jsonErr(w, http.StatusInternalServerError, "cannot save whitelist: "+err.Error()); return }
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "count": count, "removed_duplicates": duplicates})
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}
```

- [ ] **Step 4: Register all Master routes only in Master mode**

Extract route registration into a testable helper:

```go
func registerMasterRoutes(mux *http.ServeMux, enabled bool) {
	if !enabled { return }
	mux.HandleFunc("/api/master/status", auth(handleMasterStatus))
	mux.HandleFunc("/api/master/build", auth(handleMasterBuild))
	mux.HandleFunc("/api/master/sources", auth(handleMasterSources))
	mux.HandleFunc("/api/master/whitelist", auth(handleMasterWhitelist))
}
```

Replace the three unconditional Master registrations in `main` with:

```go
registerMasterRoutes(mux, *flagMaster)
```

Cluster and publisher registrations keep their current behavior.

- [ ] **Step 5: Run API tests**

```bash
cd panel && gofmt -w main.go whitelist_test.go && go test ./... -run 'TestNormalizeWhitelist|TestHandleMasterWhitelist' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add panel/main.go panel/whitelist_test.go
git commit -m "feat: add master whitelist API"
```

## Task 3: Master UI editor, preview, search, and save-build flow

**Files:**
- Modify: `panel/static/index.html`
- Modify: `panel/cluster_test.go` or create `panel/master_ui_test.go`

- [ ] **Step 1: Tambah regression markers sebelum UI code**

Add a test that requires these exact strings in `indexHTML`: `master-whitelist-search`, `master-whitelist-input`, `updateWhitelistPreview`, `findWhitelistEntry`, `fetchMasterWhitelist`, `saveMasterWhitelist`, `Simpan & Build CDB`.

```go
func TestMasterWhitelistUI(t *testing.T) {
	html := string(indexHTML)
	for _, marker := range []string{"master-whitelist-search", "master-whitelist-input", "updateWhitelistPreview", "findWhitelistEntry", "fetchMasterWhitelist", "saveMasterWhitelist", "Simpan & Build CDB"} {
		if !strings.Contains(html, marker) { t.Fatalf("missing whitelist UI marker %q", marker) }
	}
}
```

- [ ] **Step 2: Jalankan test untuk memastikan gagal**

```bash
cd panel && go test ./... -run TestMasterWhitelistUI -count=1
```

Expected: FAIL because the new markers are absent.

- [ ] **Step 3: Add the card after Sources Editor**

Insert this card after the existing `sources.txt` card:

```html
<div class="card-box">
  <div class="card-box-header"><div>
    <h3>Whitelist Domain &amp; IP</h3>
    <p>Entri exact-match yang dikecualikan dari database blacklist</p>
  </div></div>
  <div class="form-group">
    <div style="display:flex;gap:.5rem;align-items:center">
      <input id="master-whitelist-search" type="search" placeholder="Cari domain atau IP" oninput="updateWhitelistPreview()">
      <span class="hint" id="master-whitelist-count">0 entri</span>
    </div>
    <textarea id="master-whitelist-input" placeholder="example.com&#10;192.0.2.1&#10;# komentar" style="min-height:180px" oninput="updateWhitelistPreview()"></textarea>
    <div class="hint">Satu domain/IP per baris. Subdomain harus ditulis terpisah. Komentar diawali <code>#</code>.</div>
    <div class="hint" id="master-whitelist-preview">Belum ada perubahan.</div>
  </div>
  <div style="display:flex;gap:.75rem;flex-wrap:wrap">
    <button class="btn btn-primary" onclick="saveMasterWhitelist(false)">Simpan Whitelist</button>
    <button class="btn btn-secondary" onclick="saveMasterWhitelist(true)">Simpan &amp; Build CDB</button>
    <button class="btn btn-secondary" onclick="findWhitelistEntry()">Cari Berikutnya</button>
  </div>
</div>
```

- [ ] **Step 4: Add frontend state and functions**

Insert beside existing Master source functions:

```javascript
let whitelistBaseline = '';

function whitelistEntries(text) {
  return new Set(text.split(/\r?\n/).map(s => s.trim().toLowerCase().replace(/\.$/, '')).filter(s => s && !s.startsWith('#')));
}

function updateWhitelistPreview() {
  const text = document.getElementById('master-whitelist-input').value;
  const current = whitelistEntries(text), base = whitelistEntries(whitelistBaseline);
  const added = [...current].filter(v => !base.has(v)).length;
  const removed = [...base].filter(v => !current.has(v)).length;
  document.getElementById('master-whitelist-count').textContent = `${current.size} entri`;
  document.getElementById('master-whitelist-preview').textContent = added || removed ? `Perubahan lokal: +${added} / -${removed}` : 'Belum ada perubahan.';
}

function findWhitelistEntry() {
  const input = document.getElementById('master-whitelist-input');
  const query = document.getElementById('master-whitelist-search').value.trim().toLowerCase();
  if (!query) return;
  const haystack = input.value.toLowerCase();
  let index = haystack.indexOf(query, input.selectionEnd);
  if (index < 0 && input.selectionEnd > 0) index = haystack.indexOf(query);
  if (index < 0) { toast('Entri tidak ditemukan', 'error'); return; }
  input.focus(); input.setSelectionRange(index, index + query.length);
}

async function fetchMasterWhitelist() {
  try {
    const d = await api('/api/master/whitelist');
    whitelistBaseline = d.whitelist || '';
    document.getElementById('master-whitelist-input').value = whitelistBaseline;
    updateWhitelistPreview();
  } catch (_) {}
}

async function saveMasterWhitelist(buildAfter) {
  const input = document.getElementById('master-whitelist-input');
  try {
    const d = await api('/api/master/whitelist', 'POST', { whitelist: input.value });
    await fetchMasterWhitelist();
    toast(`Whitelist tersimpan: ${d.count} entri`);
    if (buildAfter) { await api('/api/master/build', 'POST'); toast('Kompilasi CDB dimulai di background!'); fetchMasterStatus(); }
  } catch (e) { toast('Gagal menyimpan whitelist: ' + e.message, 'error'); }
}
```

Change `nav()` Master branch to call `fetchMasterWhitelist()` after `fetchMasterSources()`.

- [ ] **Step 5: Jalankan UI test**

```bash
cd panel && gofmt -w cluster_test.go && go test ./... -run 'TestMasterWhitelistUI|TestClusterControlsAreHiddenUntilMasterModeConfirmed' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add panel/static/index.html panel/cluster_test.go
 git commit -m "feat: add master whitelist editor"
```

## Task 4: Builder acceptance and route regression

**Files:**
- Modify: `panel/builder.go`
- Modify: `panel/builder_test.go`
- Modify: `panel/cluster_test.go`
- Modify: `panel/main.go`

- [ ] **Step 1: Add isolated builder fixture and CDB lookup**

First make the local install target test-safe. In `panel/builder.go`, add:

```go
var localDBDir = "/var/lib/dnsdist"
```

Replace the two hardcoded `/var/lib/dnsdist` uses after build with `localDBDir`. In the test, point it to a temporary subdirectory and restore it with `defer`; never touch a machine's active DB.

Create an `httptest.Server` that handles HEAD/GET and serves `||blocked.example^\n||allowed.example^\n`; put `server.URL` in `sources.txt`. Whitelist only `allowed.example`, call `BuildMasterCDB`, open the generated `trust.<sha256>.db`, then inspect returned values rather than errors because `cdb.Get` returns `(nil, nil)` for missing keys.

```go
func TestBuildMasterCDBAppliesWhitelist(t *testing.T) {
	body := []byte("||blocked.example^\n||allowed.example^\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method != http.MethodHead { _, _ = w.Write(body) }
	}))
	defer server.Close()

	dir := t.TempDir()
	sources := filepath.Join(dir, "sources.txt")
	whitelist := filepath.Join(dir, "whitelist.txt")
	if err := os.WriteFile(sources, []byte(server.URL+"\n"), 0600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(whitelist, []byte("allowed.example\n"), 0600); err != nil { t.Fatal(err) }
	oldLocalDBDir := localDBDir
	localDBDir = filepath.Join(dir, "local-db")
	defer func() { localDBDir = oldLocalDBDir }()
	if err := os.MkdirAll(localDBDir, 0700); err != nil { t.Fatal(err) }
	if err := BuildMasterCDB(dir, sources, whitelist, "", 1, true); err != nil { t.Fatal(err) }

	matches, err := filepath.Glob(filepath.Join(dir, "trust.*.db"))
	if err != nil || len(matches) != 1 { t.Fatalf("hashed CDB files = %v, err = %v", matches, err) }
	reader, err := cdb.Open(matches[0]); if err != nil { t.Fatal(err) }
	defer reader.Close()
	key := func(domain string) []byte {
		buf := make([]byte, 256)
		n, err := dns.PackDomainName(domain+".", buf, 0, nil, false)
		if err != nil { t.Fatal(err) }
		return buf[:n]
	}
	blocked, err := reader.Get(key("blocked.example"))
	if err != nil || blocked == nil { t.Fatalf("blocked entry missing: value=%v err=%v", blocked, err) }
	allowed, err := reader.Get(key("allowed.example"))
	if err != nil || allowed != nil { t.Fatalf("whitelisted entry present: value=%v err=%v", allowed, err) }
}
```

Add imports `net/http`, `net/http/httptest`, `strconv`, `github.com/colinmarc/cdb`, and `github.com/miekg/dns` to `panel/builder_test.go`. Do not add modules.

- [ ] **Step 2: Add route registration behavior test**

Add a table test that constructs fresh muxes through `registerMasterRoutes`. For `enabled=false`, `GET /api/master/whitelist` must return 404. For `enabled=true` without an Authorization header, it must return 401, proving the authenticated route exists. Repeat the same 404/401 assertion for `/api/master/status`, `/api/master/build`, and `/api/master/sources`. No long-lived server required.

- [ ] **Step 3: Run focused acceptance tests**

```bash
cd panel && gofmt -w builder_test.go cluster_test.go && go test ./... -run 'TestBuildMasterCDBAppliesWhitelist|TestMasterWhitelistUI|TestClusterControlsAreHiddenUntilMasterModeConfirmed' -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add panel/builder_test.go panel/cluster_test.go panel/main.go
git commit -m "test: verify whitelist build exclusion"
```

## Task 5: Version, docs, and release metadata

**Files:**
- Modify: `setup/setup-edge.sh:11`
- Modify: `setup/setup-master.sh:10`
- Modify: `CHANGELOG.md`
- Modify: `README.md`
- Modify: `docs/SETUP-MASTER.md`
- Modify: `docs/POLICY-HUB-ROADMAP.md`
- Modify: `docs/SETUP-EDGE-COMMANDS.md`

- [ ] **Step 1: Update versions**

Change both installer constants from `2.8.0` to `2.9.0`. Do not change `update-blacklist.sh` `3.1.1`, because this feature does not change its behavior.

- [ ] **Step 2: Add changelog entry**

Prepend a dated `## [2.9.0] — 2026-09-21` section covering Web Whitelist MVP, canonical validation, backup/atomic writes, save-build flow, tests, and docs.

- [ ] **Step 3: Update README and Master docs**

Change version badge and last-updated version to `v2.9.0`. In `docs/SETUP-MASTER.md`, document:

```text
GET  /api/master/whitelist
POST /api/master/whitelist
```

Document plain domain/IP exact-match input, `#` comments, one entry per line, `whitelist.txt.bak`, 1 MiB limit, and the fact that subdomains require separate entries. Do not document credentials or claim wildcard support.

- [ ] **Step 4: Mark roadmap phase complete**

Change only the Fase 1 status in `docs/POLICY-HUB-ROADMAP.md` to `Selesai setelah v2.9.0 acceptance`; keep Fase 2 onward planned.

- [ ] **Step 5: Validate documentation edits**

```bash
git diff --check
! grep -RInE 'TBD|TODO|FIXME|PLACEHOLDER' docs/POLICY-HUB-ROADMAP.md docs/superpowers/specs/2026-09-21-web-whitelist-design.md
```

Expected: no output and exit status 0.

- [ ] **Step 6: Commit metadata**

```bash
git add CHANGELOG.md README.md docs/SETUP-MASTER.md docs/POLICY-HUB-ROADMAP.md docs/SETUP-EDGE-COMMANDS.md setup/setup-edge.sh setup/setup-master.sh
git commit -m "chore: prepare v2.9.0 web whitelist release"
```

## Task 6: Full verification and release artifacts

**Files:**
- Create only generated release artifacts in the existing release/output location, if the repository already has one.
- Do not force-add ignored large databases, keys, or credentials.

- [ ] **Step 1: Run Go tests and vet**

```bash
cd panel && go test ./... && go vet ./...
```

Expected: PASS.

- [ ] **Step 2: Build the panel binary**

Run the existing script:

```bash
./panel/build.sh
./tools/dnsdist-panel --help >/dev/null
```

Expected: exit status 0 and `tools/dnsdist-panel` exists. Do not commit the generated binary if the repository ignores it; it is a release asset only.

- [ ] **Step 3: Run shell and CDB smoke checks**

```bash
bash -n setup/setup-edge.sh setup/setup-master.sh setup/update-blacklist.sh tools/dnsdist-health.sh
python3 tools/gen-cdb.py "$(mktemp)" evil.com
```

Remove the temporary smoke DB after the check. Run the repository's trust-builder build if its documented toolchain is available.

- [ ] **Step 4: Run panel HTTP acceptance**

Start a temporary Master panel with temporary config, whitelist, sources, output, secret, and `PANEL_MASTER=true`. Log in through `/api/login`, call authenticated GET/POST whitelist, verify backup and canonical response, trigger build, and verify manifest/build status. Start a second temporary Edge-mode panel and verify `/api/master/whitelist` is 404 and its HTML has no visible Master controls after config load.

- [ ] **Step 5: Inspect release diff**

```bash
git diff HEAD~6..HEAD --stat
git status --short
git log -8 --oneline
```

Expected: only intended source, tests, docs, installer versions, and release metadata changed; no secrets or ignored large files staged.

- [ ] **Step 6: Package only after all checks pass**

Use a temporary staging directory outside the repository. Stage the exact source bundle shape from v2.7.0 under `dnsdist-edge-v2.9.0/`: `LICENSE`, `CHANGELOG.md`, `README.md`, `setup/setup-master.sh`, `setup/update-blacklist.sh`, `setup/setup-edge.sh`, `setup/build-master-cdb.sh`, `setup/dnsdist.conf`, and built `tools/dnsdist-panel`. Create `dnsdist-edge-v2.9.0.tar.gz` and `dnsdist-edge-v2.9.0.zip` from that root, then add separate assets `setup-edge.sh`, `setup-master.sh`, `update-blacklist.sh`, `dnsdist-panel`, and `SHA256SUMS`, matching v2.7.0. Run `sha256sum` over all assets, then verify archive listings and `sha256sum -c SHA256SUMS`. Do not force-add ignored runtime databases, keys, credentials, or generated binary. Do not create a GitHub release or push unless explicitly authorized.

- [ ] **Step 7: Final release verification**

From the temporary artifact directory:

```bash
sha256sum -c SHA256SUMS
tar -tzf dnsdist-edge-v2.9.0.tar.gz >/dev/null
unzip -t dnsdist-edge-v2.9.0.zip
```

Expected: checksum, tar, and zip verification pass. Keep generated assets outside the repository unless a release process explicitly requires tracked metadata.

---

## Self-review

- Spec coverage: Tasks 1–2 cover normalization, API, limits, backup, atomic write, errors, and route isolation. Task 3 covers card, counter, preview, search, save, and save-build. Task 4 covers builder exclusion and regressions. Task 5 covers all versioning/documentation requirements. Task 6 covers tests, build, HTTP acceptance, packaging, and checksum.
- Placeholder scan: no `TBD`, `TODO`, `FIXME`, or vague implementation task remains. The pinned CDB reader API is concrete: `cdb.Open`, `(*cdb.CDB).Get`, and `dns.PackDomainName`. Release filenames and verification commands are explicit.
- Type consistency: `normalizeWhitelist(raw) (string, []int, int, int)` is used consistently by tests and handler. UI uses `{whitelist, count}` GET and `{ok, count, removed_duplicates}` POST consistently.
- Scope: one release, one storage file, no new dependency, no unrelated refactor.
