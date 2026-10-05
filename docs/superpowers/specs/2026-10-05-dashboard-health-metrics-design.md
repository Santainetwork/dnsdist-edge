# Design Specification: Dashboard Service Health Metric Cards

**Date**: 2026-10-05
**Topic**: Dashboard Service Health Enhancement
**Status**: Approved by user, pending implementation plan

---

## 1. Objective

Enhance the "Kesehatan Layanan" (Service Health) section on the dnsdist panel dashboard with three concise metric cards placed directly above the existing key-value status list:
1. **Active/Configured Upstream count**: Number of resolvers configured in dnsdist.
2. **Block Mode**: Current sinkhole filtering action (`NXDOMAIN`, `REFUSED`, `DROP`, `TRUNCATE`, `SINKHOLE_IP`, etc.).
3. **Cluster Role & Nodes**: Current node role (`Master` or `Edge`) along with the total registered node count.

---

## 2. Layout & UI Structure

- **Section**: `.panel` containing "Kesehatan Layanan" (Service Health) in `panel/static/index.html`.
- **Placement**: A horizontal 3-column metric cards row `.stat-grid` (or `.mini-stat-grid`) rendered inside the Service Health panel right before `<dl class="kv-list">`.
- **Theme compatibility**: Uses existing design tokens (`var(--surface-container)`, `var(--border)`, `var(--text-main)`, `var(--text-muted)` / shadcn/ui neutral palette) without introducing external stylesheets or runtime dependencies.

### Metric Card 1: Upstream Count
- **Header**: Upstream
- **Primary Value**: Number (e.g. `3`)
- **Subtitle/Note**: `terkonfigurasi` (truthful provenance; does not claim live probe health)
- **Data Source**: `GET /api/upstream/status` -> `.count` (or `.upstreams.length`)
- **Fallback**: `—` if request fails or loading

### Metric Card 2: Block Mode
- **Header**: Block Mode
- **Primary Value**: String in uppercase (e.g. `NXDOMAIN`, `REFUSED`, `SINKHOLE_IP`)
- **Subtitle/Note**: `aksi sinkhole`
- **Data Source**: `GET /api/rpz/status` -> `.block_mode`
- **Fallback**: `—` if request fails or loading

### Metric Card 3: Cluster Role & Nodes
- **Header**: Mode Node
- **Primary Value**: `Master` or `Edge`
- **Subtitle/Note**: `N node terdaftar` (e.g. `3 node terdaftar`)
- **Data Source**: `GET /api/cluster/tokens` -> `.is_master` (boolean) & `.nodes` (integer)
- **Fallback**: `—` if request fails or loading

---

## 3. Data Flow & Integration

- **No backend schema or endpoint changes**: All required endpoints already exist and return standard JSON responses:
  - `GET /api/upstream/status` (`count`, `upstreams`)
  - `GET /api/rpz/status` (`block_mode`)
  - `GET /api/cluster/tokens` (`is_master`, `nodes`)
- **Lifecycle / Polling**:
  - Fetched during initial `initDashboard()` / `refreshDashboard()`.
  - Re-fetched on periodic health tick along with existing health polls.
  - Safe error handling: If any single endpoint returns non-200 or network error, that specific metric displays `—` without breaking other UI elements.

---

## 4. Verification & Testing Plan

1. **Static / Contract testing**:
   - Verify `tests/dnsdist-panel-contracts.sh` passes.
   - Verify HTML remains self-contained (zero external scripts/fonts/CDNs).
2. **Go Panel Test Suite**:
   - `GOTOOLCHAIN=local go test -count=1 ./...` in `panel/` to ensure embedded static file and endpoints pass without regression.
3. **Browser / DOM smoke test**:
   - Verify that all three DOM element IDs are updated when API responses resolve.
   - Verify fallback state when APIs return error or empty object.
