# RPZ Master Multimode Implementation Plan

> **For agentic workers:** Execute in order, TDD for Go and shell behavior, commit each coherent milestone.

**Goal:** Make central master select legacy `feeds` or RPZ-upstream `rpz-slave`, independently of local DNSDist resolver installation.

**Architecture:** Keep feeds mode as backward-compatible default and sole owner of its existing CDB builder. Add an installed `rpz-master.service` as sole CDB writer in RPZ-slave mode, preserving `/files/trust.db` for edge. Keep master panel cluster functions, disable feed-builder controls in RPZ-slave. Harden config, sync locking, transfer access, and lifecycle before installing the daemon.

**Tech Stack:** Go 1.24 stdlib + existing miekg/dns/CDB deps; Bash/systemd/nginx; Go tests and shell contract tests.

---

## Files and boundaries

- `tools/rpz-master/config.go`, `main.go`, `slave_client.go`, `builder.go`, `dns_server.go`, `http_server.go`: validated config, safe sync/update, owner-only state, ACL, graceful service.
- `tools/rpz-master/*_test.go`: behavior tests without external upstream dependency.
- `setup/setup-master.sh`: source-mode args, binary install, config, unit, cron ownership, URL output.
- `setup/setup-edge.sh`, `tests/setup-edge-url.sh`: explicit URL propagation to first sync and saved config.
- `panel/main.go`, `panel/static/index.html`, panel tests: source-mode awareness and guard build endpoints.
- `docs/SETUP-MASTER.md`, `README.md`, `CHANGELOG.md`, root `AGENTS.md`, `tools/rpz-master/README.md`, `tools/rpz-master/AGENTS.md`: operator contracts.
- `.gitignore`: ignore generated Go binary without ignoring Go source or module files.
- `docs/superpowers/specs/2026-09-25-rpz-master-multimode-design.md`: approved design status.

## Task 1: Lock in source mode config contract

**Files:** `tools/rpz-master/config_test.go`, `tools/rpz-master/config.go`

- [ ] Add table tests for default paths, valid config, invalid upstream/zone/listen/interval/ACL, TSIG secret file permissions, and mode value.
- [ ] Run `cd tools/rpz-master && GOTOOLCHAIN=local go test ./...`; tests for validation must fail first.
- [ ] Add explicit `source_mode` config (`feeds` or `rpz-slave`); reject feeds mode in daemon startup, since daemon only owns RPZ data.
- [ ] Add validation with stdlib `net`, `net/url`, `time`, and DNS names; no new dependencies.
- [ ] Run tests and commit.

## Task 2: Make RPZ ingest safe and correct

**Files:** `tools/rpz-master/slave_client_test.go`, `tools/rpz-master/builder_test.go`, `tools/rpz-master/slave_client.go`, `tools/rpz-master/builder.go`, `tools/rpz-master/config.go`

- [ ] Add tests proving empty-state sync selects AXFR, existing-state sync requests IXFR, zone parsing rejects out-of-zone owners, delta updates atomically, and failed input leaves current raw/CDB files unchanged.
- [ ] Run focused tests and observe expected failures.
- [ ] Implement bounded AXFR/IXFR handling with miekg/dns transfer; reject unusable/truncated responses; preserve existing raw data until complete transfer validation.
- [ ] Add upstream TSIG from protected secret file; preserve RFC 1995 transfer semantics.
- [ ] Rework delta application to avoid map of every existing domain and propagate scanner/write/flush/close errors.
- [ ] Write CDB to temp, verify close/hash, atomically rename; propagate scanner errors and never write partial state.
- [ ] Run `go test ./...` and `go test -race ./...`; commit.

## Task 3: Make daemon concurrency and downstream serving safe

**Files:** `tools/rpz-master/dns_server_test.go`, `http_server_test.go`, `main.go`, `dns_server.go`, `http_server.go`

- [ ] Add tests for CIDR transfer allow/deny, TSIG validation, loopback default, manifest, rejected unauthenticated sync, duplicate sync exclusion, and graceful server startup/shutdown.
- [ ] Run focused tests and observe failures.
- [ ] Add source IP ACL and TSIG enforcement for AXFR/IXFR; do not expose unrestricted transfer by default.
- [ ] Replace `log.Fatal` in routines with returned errors; sync loop logs retryable failures and remains alive.
- [ ] Serialize sync/build; guard state with shared mutex; publish new serial/state only after CDB commit.
- [ ] Remove unauthenticated `/api/sync`; retain sync through startup/periodic loop and CLI action.
- [ ] Add `-version` alias while preserving `-v`.
- [ ] Run full Go tests/race/vet/build; commit.

## Task 4: Add installer source modes

**Files:** `tests/setup-master-modes.sh`, `setup/setup-master.sh`, `setup/build-master-cdb.sh`, `.gitignore`

- [ ] Add shell tests for default feeds, explicit feeds, RPZ mode validation, independent `--with-dnsdist`, invalid mode/upstream/zone, feeds cron ownership, RPZ unit ownership, and no mode switch on failed preflight.
- [ ] Run shell tests first and observe failures.
- [ ] Add `--source-mode feeds|rpz-slave`, `--rpz-upstream`, `--rpz-zone`, optional bootstrap URL, DNS listen default `0.0.0.0:5354`, interval, transfer ACL, TSIG key/secret-file flags.
- [ ] Keep feeds default and current Nginx routes. RPZ mode validates source/build inputs, installs bundled Go binary or builds source only when module exists, creates root-only config, and installs/enables `rpz-master.service`.
- [ ] Disable/remove this installer's six-hour build cron in RPZ mode. Disable panel build interval and guard manual build/source endpoints when `PANEL_SOURCE_MODE=rpz-slave`.
- [ ] On `feeds` mode switch, build feeds CDB successfully before disabling RPZ daemon. Preserve last-good published DB if build fails.
- [ ] Keep local dnsdist option independent. Do not add AXFR route to resolver by default. Document optional explicit downstream use, avoid port 5353 collision with TPROXY.
- [ ] Verify isolated temp-root installer plan output and bash syntax; commit.

## Task 5: Fix fresh edge URL delivery

**Files:** `tests/setup-edge-url.sh`, `setup/setup-edge.sh`

- [ ] Add test harness with stubbed system tools that asserts `--url URL --install` makes the first updater invocation receive exactly URL in `CENTRAL_DB_URLS` and persists it for later sync.
- [ ] Run test and observe failure.
- [ ] Export `CENTRAL_DB_URLS` (not unused `CENTRAL_DB_URL`) at both first-install and upgrade sync sites; retain legacy URL as a compatible saved-config input.
- [ ] Run edge URL test and existing `tests/update-blacklist-cleanup.sh`; commit.

## Task 6: Docs and local agent guidance

**Files:** listed under Files and boundaries.

- [ ] Document four source/resolver combinations, source ownership, requirements, secure TSIG, transfer ACL, ports, mode switch behavior, edge URL and troubleshooting.
- [ ] Correct RPZ claims to match implementation; fix stale versions/Go/option count in AGENTS; explain AGENTS is local ignored guidance unless explicitly tracked.
- [ ] Add changelog entry without claiming release is published.
- [ ] Run link/path and shell command checks; commit.

## Task 7: Acceptance verification

- [ ] `cd tools/rpz-master && GOTOOLCHAIN=local go test -race ./... && GOTOOLCHAIN=local go vet ./... && GOTOOLCHAIN=local CGO_ENABLED=0 go build -trimpath -o "$JCODE_SCRATCH_DIR/rpz-master" .`
- [ ] `file "$JCODE_SCRATCH_DIR/rpz-master"`; verify CLI help/version.
- [ ] `bash -n setup/setup-master.sh setup/setup-edge.sh setup/update-blacklist.sh tests/setup-master-modes.sh tests/setup-edge-url.sh`
- [ ] Run `bash tests/setup-master-modes.sh`, `bash tests/setup-edge-url.sh`, `bash tests/update-blacklist-cleanup.sh`.
- [ ] Run `cd panel && go test ./...` and repository smoke tests.
- [ ] Verify both source modes, both resolver modes, CDB endpoint contract `/files/trust.db`, current file ownership, no tracked binary, no secret exposure, and `git status`.
- [ ] Review diff against this plan; report remaining unsupported combinations honestly.
