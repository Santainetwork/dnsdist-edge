package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Error branches of the SmartDist admin handlers. These were untested
// (handleSmartDistProfiles 40.7%, handleSmartDistAssign 52.0% on HEAD).
func newSmartDistHTTPFixture(t *testing.T) {
	t.Helper()
	prevSD := smartDistStoreInst
	setSmartDistStore(newSmartDistProfileStore(filepath.Join(t.TempDir(), "p.json")))
	t.Cleanup(func() { setSmartDistStore(prevSD) })

	prevCS := clusterStore
	clusterStore = newClusterStore(filepath.Join(t.TempDir(), "c.json"))
	t.Cleanup(func() { clusterStore = prevCS })
}

func TestSmartDistProfilesHandlerErrors(t *testing.T) {
	newSmartDistHTTPFixture(t)

	// Method not allowed.
	rr := httptest.NewRecorder()
	handleSmartDistProfiles(rr, httptest.NewRequest(http.MethodPatch, "/api/cluster/profile", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("PATCH = %d, want 405", rr.Code)
	}

	// POST with a body that is not a valid profile -> 400 (not 500).
	rr = httptest.NewRecorder()
	handleSmartDistProfiles(rr, httptest.NewRequest(http.MethodPost, "/api/cluster/profile", strings.NewReader(`{"id":"x"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("POST invalid profile = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}

	// DELETE without id -> 400.
	rr = httptest.NewRecorder()
	handleSmartDistProfiles(rr, httptest.NewRequest(http.MethodDelete, "/api/cluster/profile", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("DELETE no id = %d, want 400", rr.Code)
	}

	// DELETE unknown id -> 404.
	rr = httptest.NewRecorder()
	handleSmartDistProfiles(rr, httptest.NewRequest(http.MethodDelete, "/api/cluster/profile?id=nope", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("DELETE unknown = %d, want 404", rr.Code)
	}
}

func TestSmartDistAssignHandlerErrors(t *testing.T) {
	newSmartDistHTTPFixture(t)

	// Non-POST -> 405.
	rr := httptest.NewRecorder()
	handleSmartDistAssign(rr, httptest.NewRequest(http.MethodGet, "/api/cluster/assign", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want 405", rr.Code)
	}

	// Invalid JSON -> 400.
	rr = httptest.NewRecorder()
	handleSmartDistAssign(rr, httptest.NewRequest(http.MethodPost, "/api/cluster/assign", strings.NewReader("not json")))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad json = %d, want 400", rr.Code)
	}

	// Missing node_id -> 400.
	rr = httptest.NewRecorder()
	handleSmartDistAssign(rr, httptest.NewRequest(http.MethodPost, "/api/cluster/assign", strings.NewReader(`{"profile_id":"p"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("no node_id = %d, want 400", rr.Code)
	}

	// Unknown node -> 404.
	rr = httptest.NewRecorder()
	handleSmartDistAssign(rr, httptest.NewRequest(http.MethodPost, "/api/cluster/assign", strings.NewReader(`{"node_id":"ghost","profile_id":"p"}`)))
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown node = %d, want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "tidak") {
		t.Errorf("unknown node body = %s", rr.Body.String())
	}
}

// Guard the JSON error shape so a future refactor does not silently change it.
func TestSmartDistErrorJSONShape(t *testing.T) {
	newSmartDistHTTPFixture(t)
	rr := httptest.NewRecorder()
	handleSmartDistAssign(rr, httptest.NewRequest(http.MethodPost, "/api/cluster/assign", strings.NewReader(`{}`)))
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("error body not json: %v (%s)", err, rr.Body.String())
	}
	if _, ok := out["error"]; !ok {
		t.Errorf("error body missing \"error\" key: %s", rr.Body.String())
	}
}
