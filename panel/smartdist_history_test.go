package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// F5.3: setiap perubahan penugasan profil per node dicatat (waktu, dari, ke).
// Riwayat dibatasi 50 entri per node dan tidak ikut disk format (tanpa migrasi).
func TestAssignRecordsHistory(t *testing.T) {
	st := newSmartDistProfileStore(filepath.Join(t.TempDir(), "p.json"))
	if _, err := st.Put([]byte(smartDistTestDoc)); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := st.Assign("n1", "cfg-a"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := st.Assign("n1", ""); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	h := st.History("n1")
	if len(h) != 2 {
		t.Fatalf("history len=%d, want 2: %+v", len(h), h)
	}
	if h[0].From != "" || h[0].To != "cfg-a" {
		t.Errorf("entry0 = %+v, want from='' to=cfg-a", h[0])
	}
	if h[1].From != "cfg-a" || h[1].To != "" {
		t.Errorf("entry1 = %+v, want from=cfg-a to=''", h[1])
	}
	if h[0].At.IsZero() {
		t.Error("timestamp kosong")
	}
}

// Assign ke profil yang sama tidak menambah riwayat (tidak ada perubahan).
func TestAssignSameProfileNoHistory(t *testing.T) {
	st := newSmartDistProfileStore(filepath.Join(t.TempDir(), "p.json"))
	if _, err := st.Put([]byte(smartDistTestDoc)); err != nil {
		t.Fatalf("put: %v", err)
	}
	_ = st.Assign("n1", "cfg-a")
	_ = st.Assign("n1", "cfg-a")
	if got := len(st.History("n1")); got != 1 {
		t.Fatalf("history len=%d, want 1", got)
	}
}

// Riwayat dibatasi maxHistory entri (ring: entri terlama dibuang).
func TestHistoryBounded(t *testing.T) {
	st := newSmartDistProfileStore(filepath.Join(t.TempDir(), "p.json"))
	if _, err := st.Put([]byte(smartDistTestDoc)); err != nil {
		t.Fatalf("put: %v", err)
	}
	for i := 0; i < maxHistory+10; i++ {
		_ = st.Assign("n1", "")
		_ = st.Assign("n1", "cfg-a")
	}
	if got := len(st.History("n1")); got != maxHistory {
		t.Fatalf("history len=%d, want %d", got, maxHistory)
	}
}

// AssignMany juga harus mencatat riwayat untuk setiap node yang berubah.
func TestAssignManyRecordsHistory(t *testing.T) {
	st := newSmartDistProfileStore(filepath.Join(t.TempDir(), "p.json"))
	if _, err := st.Put([]byte(smartDistTestDoc)); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := st.AssignMany([]string{"n1", "n2"}, "cfg-a"); err != nil {
		t.Fatalf("assignmany: %v", err)
	}
	for _, n := range []string{"n1", "n2"} {
		h := st.History(n)
		if len(h) != 1 || h[0].From != "" || h[0].To != "cfg-a" {
			t.Errorf("%s history = %+v, want one '' -> cfg-a", n, h)
		}
	}
}

// Endpoint GET /api/cluster/profiles?history=<node> mengembalikan riwayat.
func TestProfilesHistoryEndpoint(t *testing.T) {
	tmp := t.TempDir()
	prevSD := smartDistStoreInst
	st := newSmartDistProfileStore(filepath.Join(tmp, "p.json"))
	setSmartDistStore(st)
	t.Cleanup(func() { setSmartDistStore(prevSD) })
	if _, err := st.Put([]byte(smartDistTestDoc)); err != nil {
		t.Fatalf("put: %v", err)
	}
	_ = st.Assign("n1", "cfg-a")

	req := httptest.NewRequest(http.MethodGet, "/api/cluster/profiles?history=n1", nil)
	rr := httptest.NewRecorder()
	handleSmartDistProfiles(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		NodeID  string                 `json:"node_id"`
		History []SmartDistAssignEvent `json:"history"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.NodeID != "n1" || len(out.History) != 1 || out.History[0].To != "cfg-a" {
		t.Fatalf("endpoint history = %+v", out)
	}
}
