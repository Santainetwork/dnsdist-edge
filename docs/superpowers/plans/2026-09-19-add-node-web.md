# Add Node via Web Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a secure Master-to-Edge web handoff for enrolling a new cluster node.

**Architecture:** Reuse the existing protected `POST /api/cluster/token` and public register API. Master UI collects an Edge panel URL, creates a ten-minute one-time token, and opens the Edge URL with enrollment data in a URL fragment. Edge UI consumes the fragment after login, asks for the existing explicit confirmation, then registers through the current backend and clears the fragment.

**Tech Stack:** Go standard library, embedded HTML/CSS/JavaScript, existing JWT/API and Go tests.

---

### Task 1: Add minute-based enrollment TTL compatibility

**Files:**
- Modify: `panel/cluster.go:handleClusterToken`
- Test: `panel/cluster_test.go`

- [ ] **Step 1: Write the failing test**

Add a handler test using a temporary `ClusterStore`, POST `{"minutes":10}`, decode the returned token, and assert the stored expiry is between 9 and 11 minutes from the captured time. Add a second request with `{"hours":1}` and assert the legacy field still produces an expiry close to one hour.

- [ ] **Step 2: Run the focused test**

Run: `cd panel && go test ./... -run TestClusterTokenTTL -v`
Expected: FAIL because `minutes` is currently ignored and defaults to 24 hours.

- [ ] **Step 3: Implement the smallest compatibility change**

In `handleClusterToken`, decode both fields:

```go
var body struct {
    Minutes int `json:"minutes"`
    Hours   int `json:"hours"`
}
var duration time.Duration
switch {
case body.Minutes > 0:
    duration = time.Duration(body.Minutes) * time.Minute
case body.Hours > 0:
    duration = time.Duration(body.Hours) * time.Hour
default:
    duration = 24 * time.Hour
}
tok := clusterStore.GenerateEnrollmentToken(duration)
```

Return `expires_in` using the selected duration. Preserve `hours` compatibility.

- [ ] **Step 4: Run the focused test**

Run: `cd panel && go test ./... -run TestClusterTokenTTL -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/cluster.go panel/cluster_test.go
git commit -m "feat: support minute enrollment token expiry"
```

### Task 2: Add Master Add Node modal and secure handoff URL

**Files:**
- Modify: `panel/static/index.html`
- Test: `panel/main_test.go` or `panel/cluster_test.go`

- [ ] **Step 1: Add focused JavaScript behavior checks**

Keep the implementation dependency-free. Add browser-independent tests only if the repository already supports JavaScript execution; otherwise verify the exact generated markup and Go API behavior through the existing E2E test. The UI must include fields `master-edge-url`, `master-edge-name`, a **Buka Panel Edge** button, and a manual token fallback.

- [ ] **Step 2: Implement URL validation in the page script**

Add a small function:

```js
function parseEdgePanelURL(raw) {
  const u = new URL(raw.trim());
  if (u.protocol !== 'http:' && u.protocol !== 'https:') throw new Error('URL Edge harus memakai HTTP atau HTTPS');
  if (u.username || u.password) throw new Error('URL Edge tidak boleh berisi kredensial');
  return u;
}
```

Add an **Tambah Node** button beside the existing token button. The modal collects Edge URL and name, calls `api('/api/cluster/token', 'POST', {minutes: 10})`, then builds:

```js
const payload = btoa(unescape(encodeURIComponent(JSON.stringify({master_url: location.origin, enroll_token: d.token, node_name: name}))));
const handoff = new URL(edgeURL.href);
handoff.hash = 'dnsdist-enroll=' + payload;
```

Set a visible fallback token and CLI command. Use `window.open(handoff.href, '_blank', 'noopener,noreferrer')` only from the user click handler. Do not place token data in query parameters.

- [ ] **Step 3: Validate failure paths manually in the browser source**

Confirm invalid schemes, credentials, empty URL, and token API failures produce a visible error without opening a new window. Confirm the token is requested only after URL validation.

- [ ] **Step 4: Commit**

```bash
git add panel/static/index.html
git commit -m "feat: add master web node enrollment handoff"
```

### Task 3: Consume enrollment handoff on Edge settings page

**Files:**
- Modify: `panel/static/index.html`
- Test: `panel/e2e_cluster_test.go` if a browser harness exists, otherwise extend source/API acceptance coverage.

- [ ] **Step 1: Add fragment parser**

Add:

```js
function readEnrollmentHandoff() {
  const prefix = '#dnsdist-enroll=';
  if (!location.hash.startsWith(prefix)) return null;
  try {
    const raw = decodeURIComponent(escape(atob(location.hash.slice(prefix.length))));
    const value = JSON.parse(raw);
    if (!value.master_url || !value.enroll_token) return null;
    return value;
  } catch (_) { return null; }
}
```

Use `history.replaceState(null, '', location.pathname + location.search)` immediately after successfully parsing, so the token is not retained in browser history or copied URL.

- [ ] **Step 2: Integrate after authentication**

After the existing initial config/navigation setup, read the handoff. Navigate to settings, fill `edge-m-url`, `edge-m-token`, and `edge-m-name`, and show a clear confirmation message: the user must click **Hubungkan ke Master**. Do not call the register API automatically.

- [ ] **Step 3: Keep current manual flow intact**

Ensure normal direct navigation still renders empty fields and the existing `connectEdgeToMaster()` behavior remains unchanged. On success, clear the token input and remove any remaining fragment.

- [ ] **Step 4: Commit**

```bash
git add panel/static/index.html
git commit -m "feat: accept web enrollment handoff on edge panel"
```

### Task 4: Acceptance tests and verification

**Files:**
- Modify: `panel/e2e_cluster_test.go`
- Modify: `docs/SETUP-EDGE-COMMANDS.md` or relevant cluster documentation if current instructions describe only CLI enrollment.

- [ ] **Step 1: Extend API acceptance coverage**

Test `POST /api/cluster/token` with `{"minutes":10}`, register with the returned token, retry registration with the same token, and assert the retry returns `401 Unauthorized`. Existing heartbeat and node-list assertions remain.

- [ ] **Step 2: Document the web flow**

Add a concise section: Master **Cluster Nodes → Tambah Node**, enter Edge panel URL/name, open Edge panel, verify prefilled settings, click **Hubungkan ke Master**. State that the token is one-time and expires after ten minutes. Retain CLI fallback.

- [ ] **Step 3: Run formatting, tests, and build**

Run:

```bash
cd panel && gofmt -w cluster.go cluster_test.go e2e_cluster_test.go && go test ./...
cd .. && bash panel/build.sh
```

Expected: all Go tests pass and the panel binary builds.

- [ ] **Step 4: Inspect the diff for secret leakage**

Run:

```bash
git diff HEAD~4 --check
grep -R "dnsdist-enroll" -n panel/static/index.html
```

Verify token appears only in fragment-generation/input code, never in query strings, logs, or persisted server records beyond the existing one-time token store.

- [ ] **Step 5: Commit documentation and tests**

```bash
git add panel/e2e_cluster_test.go docs/SETUP-EDGE-COMMANDS.md
git commit -m "test: cover web node enrollment workflow"
```

## Self-review

- TTL, one-time token, URL validation, fragment handoff, explicit Edge confirmation, error paths, manual fallback, tests, and documentation each have a task.
- No new dependency or backend abstraction is required.
- Existing API field `hours` remains compatible.
- No step requires storing Edge credentials on Master.
