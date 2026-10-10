package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Bulk assign: satu request menugaskan banyak node ke satu profil (PRD F3.1).
// Kontrak lama (node_id tunggal) harus tetap jalan.
func TestSmartDistAssignBulk(t *testing.T) {
	newSmartDistHTTPFixture(t)
	if _, err := getSmartDistStore().Put([]byte(smartDistTestProfile)); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	for _, id := range []string{"n1", "n2", "n3"} {
		clusterStore.nodes[id] = &NodeRecord{ID: id, Name: id}
	}

	body := `{"node_ids":["n1","n2","n3"],"profile_id":"cfg-a"}`
	rr := httptest.NewRecorder()
	handleSmartDistAssign(rr, httptest.NewRequest(http.MethodPost, "/api/cluster/profile/assign", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("bulk assign = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var out struct {
		Assigned []string `json:"assigned"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("response not json: %v (%s)", err, rr.Body.String())
	}
	if len(out.Assigned) != 3 {
		t.Fatalf("assigned = %v, want 3 nodes", out.Assigned)
	}
	for _, id := range []string{"n1", "n2", "n3"} {
		if got := getSmartDistStore().assignedProfile(id); got != "cfg-a" {
			t.Errorf("%s profile = %q, want cfg-a", id, got)
		}
	}
}

// Bulk assign unknown node -> 404, dan tidak ada node yang ter-assign sebagian.
func TestSmartDistAssignBulkUnknownNode(t *testing.T) {
	newSmartDistHTTPFixture(t)
	if _, err := getSmartDistStore().Put([]byte(smartDistTestProfile)); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	clusterStore.nodes["n1"] = &NodeRecord{ID: "n1", Name: "n1"}

	body := `{"node_ids":["n1","ghost"],"profile_id":"cfg-a"}`
	rr := httptest.NewRecorder()
	handleSmartDistAssign(rr, httptest.NewRequest(http.MethodPost, "/api/cluster/profile/assign", strings.NewReader(body)))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("bulk assign with ghost = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
	if got := getSmartDistStore().assignedProfile("n1"); got != "" {
		t.Errorf("n1 was assigned %q despite ghost failure (partial write)", got)
	}
}

// Bulk assign ke profil yang tidak ada -> 400.
func TestSmartDistAssignBulkUnknownProfile(t *testing.T) {
	newSmartDistHTTPFixture(t)
	if _, err := getSmartDistStore().Put([]byte(smartDistTestProfile)); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	clusterStore.nodes["n1"] = &NodeRecord{ID: "n1", Name: "n1"}

	body := `{"node_ids":["n1"],"profile_id":"nope"}`
	rr := httptest.NewRecorder()
	handleSmartDistAssign(rr, httptest.NewRequest(http.MethodPost, "/api/cluster/profile/assign", strings.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bulk assign unknown profile = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// assignedProfile membaca penugasan node dari store (helper tes).
func (s *SmartDistProfileStore) assignedProfile(nodeID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.assign[nodeID]
}
