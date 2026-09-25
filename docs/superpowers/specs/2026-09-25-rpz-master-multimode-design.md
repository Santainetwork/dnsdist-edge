# Central Master Multimode Source Design

**Date:** 2026-09-25
**Status:** Approved for implementation from requested outcome

## Goal

Integrate `tools/rpz-master` into Central Master deployment while preserving the existing non-RPZ feed workflow. Operators choose a data-source mode independently from whether the host also runs DNSDist.

## Mode matrix

| Source mode | Resolver mode | Source owner | Published output |
|---|---|---|---|
| `feeds` (default) | `--no-dnsdist` | `dnsdist-panel` / `trust-builder` | Nginx and panel serve `/files/trust.db` |
| `feeds` | `--with-dnsdist` | `dnsdist-panel` / `trust-builder` | Same output, local DNSDist consumes it |
| `rpz-slave` | `--no-dnsdist` | `rpz-master` | Same HTTP output plus restricted downstream SOA/AXFR/IXFR |
| `rpz-slave` | `--with-dnsdist` | `rpz-master` | Same output, local DNSDist consumes it |

Source modes are exclusive. This prevents feed builder, panel scheduler, and RPZ synchronization from writing the same CDB concurrently.

## CLI and configuration

`setup/setup-master.sh` adds:

- `--source-mode feeds|rpz-slave`
- `--rpz-upstream HOST:PORT`
- `--rpz-zone FQDN`
- `--rpz-bootstrap-url URL` for optional initial raw-domain bootstrap
- `--rpz-dns-listen ADDR`, default `0.0.0.0:5354`
- `--rpz-check-interval DURATION`
- `--rpz-transfer-acl CIDR[,CIDR...]`, default loopback only
- `--rpz-tsig-key NAME`
- `--rpz-tsig-secret-file PATH`

`rpz-slave` requires an upstream and zone. TSIG secrets stay in a root-readable file, not CLI arguments or generated JSON. Advanced settings remain editable in `/etc/dnsdist-master/rpz-master.json`.

Canonical paths:

- binary: `/usr/local/bin/rpz-master`
- config: `/etc/dnsdist-master/rpz-master.json`
- state: `/var/lib/rpz-master/state.json`
- raw domains: `/var/lib/rpz-master/domains.txt`
- published CDB: `/var/www/html/files/trust.db`
- systemd unit: `rpz-master.service`
- downstream RPZ DNS: `0.0.0.0:5354` by default, avoiding the existing TPROXY backend on `127.0.0.1:5353`

The installer prefers a bundled `rpz-master` executable. Source checkouts can build it with Go 1.24. A standalone installer may download the official `rpz-master` release asset only after matching it against `SHA256SUMS`; missing or invalid checksums abort RPZ installation. Never run an unverified binary.

## Lifecycle and data flow

### `feeds`

Existing behavior remains default. `build-master-cdb.sh` and panel auto-build own `trust.db`; the six-hour cron remains. Any installed `rpz-master.service` is disabled to prevent dual writers.

### `rpz-slave`

The feed cron is removed. `rpz-master.service` performs initial IXFR with AXFR fallback, optionally falls back to the HTTP bootstrap URL, compiles the wire-format CDB atomically, stores state atomically, polls the upstream, and serves restricted downstream transfers. Nginx and panel remain readers/publishers only.

The feed source UI and manual build API remain available only in `feeds` mode. In `rpz-slave`, feed source editing, whitelist build, manual feed build, and auto-build are disabled. Status exposes active source mode and RPZ sync status. Keep panel cluster endpoints active in both modes. `rpz-master` does not expose unauthenticated HTTP sync trigger; sync runs on daemon startup and its configured interval.

Edge nodes continue using `/files/trust.db`; no RPZ-specific edge protocol is required. Fresh install explicitly passes the selected central URL into `update-blacklist.sh` through `CENTRAL_DB_URLS`, matching the updater contract.

## Safety and failure behavior

- Config validation fails before service start for invalid modes, addresses, CIDRs, intervals, missing paths, or missing RPZ upstream.
- Periodic sync returns errors to the daemon loop. It never calls `log.Fatal` from a goroutine.
- Only one sync/build operation runs at a time.
- State reads and writes share one mutex.
- Initial transfer can create an empty raw file and apply full AXFR data.
- Delta application holds only delta-sized sets in RAM, not every existing domain.
- Downstream AXFR/IXFR defaults to deny except loopback for local dnsdist. Operators explicitly add client CIDRs.
- No unauthenticated HTTP sync trigger is exposed; periodic daemon sync and local CLI provide sync control.
- Atomic CDB and state replacement preserves the previous usable output on failures.
- Source-mode switching changes writer/service/cron ownership only after validation. It preserves source files and the last valid CDB; switching back to feeds triggers a successful feed build before re-enabling feeds ownership.

## Compatibility

- Default invocation remains `feeds` plus `--no-dnsdist` unless resolver mode is selected.
- Existing `/files/trust.db` and `/files/manifest.json` routes remain canonical.
- Existing edge update logic remains compatible.
- Existing feed source, whitelist, custom blacklist, panel, and resolver files remain untouched when installing RPZ mode.
- `rpz-master` supports `-v` and `-version`.

## Verification

1. Go table tests cover config defaults/validation, secret-file loading, source bootstrap selection, overlapping sync rejection, transfer ACL, HTTP sync authentication, AXFR/IXFR behavior, and memory-bounded delta application.
2. Tests are run once failing before implementation, then green after implementation.
3. Shell contract tests source installer helpers and validate both source modes, generated JSON/unit files, cron ownership, URL propagation, and invalid arguments.
4. Run `go test -race ./...`, `go vet ./...`, static build and `file` verification.
5. Run `bash -n`, shell regression tests, panel Go tests, and existing repository tests.
6. Verify documentation and help expose the same flags, paths, ports, mode names, and endpoint contract.
