package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const smartDistTestDoc = `{"id":"cfg-a","name":"Config A","smartdist":{"enabled":true,"rules":[{"type":"cname","pattern":"example.com","target":"1.2.3.4"}]}}`

// withIsolatedCluster memasang ClusterStore dan SmartDist store sementara,
// lalu mengembalikan node yang sudah terdaftar (id + key).
func withIsolatedCluster(t *testing.T) (nodeID, nodeKey string) {
	t.Helper()
	dir := t.TempDir()
	cs := newClusterStore(filepath.Join(dir, "nodes.json"))
	oldStorage := clusterStorage
	oldStore := clusterStore
	clusterStorage = cs
	clusterStore = cs
	t.Cleanup(func() { clusterStorage = oldStorage; clusterStore = oldStore })

	setSmartDistStore(newSmartDistProfileStore(filepath.Join(dir, "smartdist-profiles.json")))
	t.Cleanup(func() { setSmartDistStore(nil) })

	rec, err := cs.RegisterNode(RegisterRequest{EnrollToken: cs.GenerateEnrollmentToken(0), Name: "n1"}, "10.0.0.1")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return rec.ID, rec.Key
}

func TestSmartDistProfileHashStable(t *testing.T) {
	_, c1, err := parseSmartDistProfile([]byte(smartDistTestDoc))
	if err != nil {
		t.Fatal(err)
	}
	// Urutan key berbeda di input harus menghasilkan bytes & hash yang sama.
	reordered := `{"name":"Config A","smartdist":{"rules":[{"target":"1.2.3.4","pattern":"example.com","type":"cname"}],"enabled":true},"id":"cfg-a"}`
	_, c2, err := parseSmartDistProfile([]byte(reordered))
	if err != nil {
		t.Fatal(err)
	}
	if string(c1) != string(c2) {
		t.Fatalf("canonical bytes differ:\n%s\n%s", c1, c2)
	}
	h := smartDistCanonHash(c1)
	if len(h) != 64 || h != smartDistCanonHash(c2) {
		t.Fatalf("hash unstable or wrong length: %q", h)
	}
}

func TestSmartDistProfileIDValidation(t *testing.T) {
	for _, id := range []string{"", "Upper", "a_b", "spasi spasi", strings.Repeat("a", 41)} {
		doc := `{"id":"` + id + `","name":"x","smartdist":{"enabled":true,"rules":[]}}`
		if _, _, err := parseSmartDistProfile([]byte(doc)); err == nil {
			t.Errorf("id %q accepted, want rejected", id)
		}
	}
	ok := strings.Repeat("a", 40)
	if _, _, err := parseSmartDistProfile([]byte(`{"id":"` + ok + `","name":"x","smartdist":{"enabled":true,"rules":[]}}`)); err != nil {
		t.Errorf("40-char id rejected: %v", err)
	}
}

func TestSmartDistProfileRejectsBadRule(t *testing.T) {
	doc := `{"id":"x","name":"x","smartdist":{"enabled":true,"rules":[{"type":"bogus"}]}}`
	if _, _, err := parseSmartDistProfile([]byte(doc)); err == nil {
		t.Fatal("unknown rule type accepted")
	}
	doc = `{"id":"x","name":"x","smartdist":{"enabled":true,"rules":[]},"extra":1}`
	if _, _, err := parseSmartDistProfile([]byte(doc)); err == nil {
		t.Fatal("unknown top-level field accepted")
	}
}

func TestSmartDistProfileAssignLookup(t *testing.T) {
	st := newSmartDistProfileStore(filepath.Join(t.TempDir(), "p.json"))
	sum, err := st.Put([]byte(smartDistTestDoc))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := st.ProfileForNode("node-1"); ok {
		t.Fatal("unassigned node reported a profile")
	}
	if err := st.Assign("node-1", "cfg-a"); err != nil {
		t.Fatal(err)
	}
	pid, hash, ok := st.ProfileForNode("node-1")
	if !ok || pid != "cfg-a" || hash != sum.Hash {
		t.Fatalf("lookup = (%q,%q,%v), want (cfg-a,%q,true)", pid, hash, ok, sum.Hash)
	}
	if err := st.Assign("node-1", "missing"); err == nil {
		t.Fatal("assign to unknown profile accepted")
	}
	if err := st.Delete("cfg-a"); err == nil {
		t.Fatal("delete of assigned profile accepted")
	}
	if err := st.Assign("node-1", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete("cfg-a"); err != nil {
		t.Fatalf("delete after unassign: %v", err)
	}
}

func TestSmartDistProfilePersistsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	st := newSmartDistProfileStore(path)
	if _, err := st.Put([]byte(smartDistTestDoc)); err != nil {
		t.Fatal(err)
	}
	if err := st.Assign("n", "cfg-a"); err != nil {
		t.Fatal(err)
	}
	st2 := newSmartDistProfileStore(path)
	if pid, _, ok := st2.ProfileForNode("n"); !ok || pid != "cfg-a" {
		t.Fatalf("assignment not reloaded: %q %v", pid, ok)
	}
}

func TestSmartDistProfileConcurrentAccess(t *testing.T) {
	st := newSmartDistProfileStore(filepath.Join(t.TempDir(), "p.json"))
	if _, err := st.Put([]byte(smartDistTestDoc)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = st.Assign("n", "cfg-a")
		}()
		go func() {
			defer wg.Done()
			st.ProfileForNode("n")
		}()
	}
	wg.Wait()
}

func TestSmartDistFetchAuth(t *testing.T) {
	nodeID, nodeKey := withIsolatedCluster(t)

	cases := []struct {
		name string
		id   string
		key  string
		want int
	}{
		{"node key salah", nodeID, "salah", http.StatusUnauthorized},
		{"node id tak dikenal", "node-xxx", nodeKey, http.StatusUnauthorized},
		{"key kosong", nodeID, "", http.StatusUnauthorized},
		{"valid tanpa profil", nodeID, nodeKey, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/cluster/profile?node_id="+c.id+"&node_key="+c.key, nil)
			handleClusterProfileFetch(rr, req)
			if rr.Code != c.want {
				t.Fatalf("status = %d, want %d (body %s)", rr.Code, c.want, rr.Body)
			}
		})
	}
}

func TestSmartDistFetchServesAssignedProfile(t *testing.T) {
	nodeID, nodeKey := withIsolatedCluster(t)
	st := getSmartDistStore()
	sum, err := st.Put([]byte(smartDistTestDoc))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Assign(nodeID, "cfg-a"); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	handleClusterProfileFetch(rr, httptest.NewRequest(http.MethodGet,
		"/api/cluster/profile?node_id="+nodeID+"&node_key="+nodeKey, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body)
	}
	if got := smartDistCanonHash(rr.Body.Bytes()); got != sum.Hash {
		t.Fatalf("served body hash %s != stored hash %s", got, sum.Hash)
	}
	var p SmartDistProfile
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil || p.ID != "cfg-a" {
		t.Fatalf("body not profile JSON: %v %s", err, rr.Body)
	}
}

func TestSmartDistAdminRejectsInvalidAndUnknownNode(t *testing.T) {
	withIsolatedCluster(t)
	rr := httptest.NewRecorder()
	handleSmartDistProfiles(rr, httptest.NewRequest(http.MethodPost, "/api/cluster/profiles",
		strings.NewReader(`{"id":"BAD ID","name":"x","smartdist":{"enabled":true,"rules":[]}}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid id status = %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	handleSmartDistAssign(rr, httptest.NewRequest(http.MethodPost, "/api/cluster/profile/assign",
		strings.NewReader(`{"node_id":"node-nope","profile_id":""}`)))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("assign unknown node status = %d", rr.Code)
	}
}

func TestSmartDistStoreFileWrittenPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	st := newSmartDistProfileStore(path)
	if _, err := st.Put([]byte(smartDistTestDoc)); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", fi.Mode().Perm())
	}
}
