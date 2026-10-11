package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Kontrak untuk UI: DELETE profil yang masih di-assign harus 409 dengan pesan
// JSON {"error": ...} (api() di index.html melempar d.error ke toast).
// Profil yang tidak ada: 404. Setelah unassign: 200.
func TestDeleteAssignedProfileHTTPContract(t *testing.T) {
	prevSD := smartDistStoreInst
	st := newSmartDistProfileStore(filepath.Join(t.TempDir(), "p.json"))
	setSmartDistStore(st)
	t.Cleanup(func() { setSmartDistStore(prevSD) })
	if _, err := st.Put([]byte(smartDistTestDoc)); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := st.Assign("n1", "cfg-a"); err != nil {
		t.Fatalf("assign: %v", err)
	}

	del := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/api/cluster/profiles?id="+id, nil)
		rr := httptest.NewRecorder()
		handleSmartDistProfiles(rr, req)
		return rr
	}

	rr := del("cfg-a")
	if rr.Code != http.StatusConflict {
		t.Fatalf("delete in-use status=%d, want 409: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || !strings.Contains(body.Error, "di-assign") {
		t.Fatalf("409 body = %q (err %v), want JSON error containing 'di-assign'", rr.Body.String(), err)
	}
	if rr := del("tidak-ada"); rr.Code != http.StatusNotFound {
		t.Fatalf("delete unknown status=%d, want 404", rr.Code)
	}
	_ = st.Assign("n1", "")
	if rr := del("cfg-a"); rr.Code != http.StatusOK {
		t.Fatalf("delete after unassign status=%d, want 200: %s", rr.Code, rr.Body.String())
	}
}
