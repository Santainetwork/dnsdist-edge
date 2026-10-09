package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// List must be deterministic (sorted by ID) and reflect every stored profile
// with its enabled flag and rule count. handleSmartDistProfiles GET must serve
// that same list as JSON.
func TestSmartDistProfileListSortedAndSummarised(t *testing.T) {
	st := newSmartDistProfileStore(filepath.Join(t.TempDir(), "p.json"))

	if got := st.List(); len(got) != 0 {
		t.Fatalf("empty store List = %d items, want 0", len(got))
	}

	// Inserted out of order on purpose; List must sort by ID.
	docB := `{"id":"zz-b","name":"B","smartdist":{"enabled":false,"rules":[]}}`
	docA := `{"id":"aa-a","name":"A","smartdist":{"enabled":true,"rules":[` +
		`{"type":"cname","pattern":"example.com","target":"1.2.3.4"}]}}`
	if _, err := st.Put([]byte(docB)); err != nil {
		t.Fatalf("put B: %v", err)
	}
	if _, err := st.Put([]byte(docA)); err != nil {
		t.Fatalf("put A: %v", err)
	}

	got := st.List()
	if len(got) != 2 {
		t.Fatalf("List = %d items, want 2", len(got))
	}
	if got[0].ID != "aa-a" || got[1].ID != "zz-b" {
		t.Fatalf("order = [%s %s], want [aa-a zz-b]", got[0].ID, got[1].ID)
	}
	if !got[0].Enabled || got[0].Rules != 1 {
		t.Errorf("aa-a summary = enabled:%v rules:%d, want enabled:true rules:1", got[0].Enabled, got[0].Rules)
	}
	if got[1].Enabled || got[1].Rules != 0 {
		t.Errorf("zz-b summary = enabled:%v rules:%d, want enabled:false rules:0", got[1].Enabled, got[1].Rules)
	}
	for _, s := range got {
		if len(s.Hash) != 64 {
			t.Errorf("%s hash len = %d, want 64 (sha256 hex)", s.ID, len(s.Hash))
		}
	}
}

func TestSmartDistProfilesGETServesList(t *testing.T) {
	prev := smartDistStoreInst
	st := newSmartDistProfileStore(filepath.Join(t.TempDir(), "p.json"))
	setSmartDistStore(st)
	t.Cleanup(func() { setSmartDistStore(prev) })

	if _, err := st.Put([]byte(`{"id":"cfg-a","name":"A","smartdist":{"enabled":true,"rules":[]}}`)); err != nil {
		t.Fatalf("put: %v", err)
	}

	rr := httptest.NewRecorder()
	handleSmartDistProfiles(rr, httptest.NewRequest(http.MethodGet, "/api/cluster/profiles", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rr.Code)
	}
	var out struct {
		Profiles []SmartDistProfileSummary `json:"profiles"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if len(out.Profiles) != 1 || out.Profiles[0].ID != "cfg-a" {
		t.Fatalf("profiles = %+v, want one cfg-a", out.Profiles)
	}
}
