package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// End-to-end inside Go: a real master (route handlers + cluster + profile
// store) serves a profile, and a real EdgeClusterAgent pulls it through
// heartbeat-driven sync and writes the node-local Lua file. Nothing is mocked
// except the filesystem location.
func TestSmartDistEndToEndMasterToNodeFile(t *testing.T) {
	tmp := t.TempDir()

	cs := newClusterStore(filepath.Join(tmp, "cluster-nodes.json"))
	prevCS := clusterStore
	clusterStore = cs
	t.Cleanup(func() { clusterStore = prevCS })

	st := newSmartDistProfileStore(filepath.Join(tmp, "smartdist-profiles.json"))
	prevSD := smartDistStoreInst
	setSmartDistStore(st)
	t.Cleanup(func() { setSmartDistStore(prevSD) })

	// Master HTTP surface: heartbeat + profile fetch, same handlers main.go wires.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/cluster/heartbeat", handleClusterHeartbeat)
	mux.HandleFunc("/api/cluster/profile", handleClusterProfileFetch)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Register a node and give it profile cfg-a with an alias rule.
	tok := cs.GenerateEnrollmentToken(time.Hour)
	rec, err := cs.RegisterNode(RegisterRequest{EnrollToken: tok, Name: "edge-e2e"}, "10.0.0.7")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	doc := `{"id":"cfg-a","name":"A","smartdist":{"enabled":true,"rules":[` +
		`{"type":"alias","ip_set":"cf","targets":["9.9.9.9"]}]}}`
	sum, err := st.Put([]byte(doc))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := st.Assign(rec.ID, "cfg-a"); err != nil {
		t.Fatalf("assign: %v", err)
	}

	// Node side: a real agent, pointed at the master, with a temp profile path.
	prevPath := smartDistProfilePath
	smartDistProfilePath = filepath.Join(tmp, "smartdist-profile.lua")
	t.Cleanup(func() { smartDistProfilePath = prevPath })
	agent := &EdgeClusterAgent{
		masterURL:  srv.URL,
		httpClient: srv.Client(),
		state:      EdgeAgentState{NodeID: rec.ID, NodeKey: rec.Key},
	}

	// Step 1: heartbeat tells the node which profile and hash to run.
	hbOut := heartbeatForNode(t, rec.ID, rec.Key)
	if hbOut.ProfileID != "cfg-a" || hbOut.ProfileHash != sum.Hash {
		t.Fatalf("heartbeat profile = (%q,%q), want (cfg-a,%q)", hbOut.ProfileID, hbOut.ProfileHash, sum.Hash)
	}

	// Step 2: the agent syncs from that heartbeat response.
	if err := agent.syncSmartDistProfile(hbOut.ProfileID, hbOut.ProfileHash); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Step 3: the written file is the Lua the plugin reads.
	b, err := os.ReadFile(smartDistProfilePath)
	if err != nil {
		t.Fatalf("profile file not written: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		"SMARTDIST_ENABLED = true",
		`smartdns_ip_rules_alias("cf", {"9.9.9.9"})`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("node file missing %q\n%s", want, s)
		}
	}
	if agent.state.ProfileHash != sum.Hash {
		t.Errorf("node stored hash = %q, want %q", agent.state.ProfileHash, sum.Hash)
	}

	// Step 3b: the written file must be loadable Lua that reports enabled=true to
	// the plugin's flag reader (rule calls stubbed, as the real reader does).
	if lua, err := exec.LookPath("lua5.1"); err == nil {
		// Same stubbing the plugin uses when it reads the flag. The reader runs as a
		// script file: `lua -e` does not populate `arg`, so the path cannot be passed
		// that way (confirmed: 'attempt to index global arg (a nil value)').
		reader := filepath.Join(t.TempDir(), "reader.lua")
		src := "local env={smartdns_ip_set=function() end,smartdns_cname=function() end,smartdns_ip_rules_alias=function() end}\n" +
			"local c=assert(loadfile(arg[1])); setfenv(c,env); c()\n" +
			"io.write(env.SMARTDIST_ENABLED==true and \"yes\" or \"no\")\n"
		if err := os.WriteFile(reader, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(lua, reader, smartDistProfilePath).CombinedOutput()
		if err != nil {
			t.Fatalf("lua load of generated profile failed: %v\n%s", err, out)
		}
		if string(out) != "yes" {
			t.Fatalf("plugin flag reader would see enabled=%q, want yes", string(out))
		}
	} else {
		t.Log("lua5.1 not found; skipping plugin-load check")
	}

	// Step 4: a second heartbeat with the same hash must not refetch.
	hits := 0
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/cluster/profile" {
			hits++
		}
		mux.ServeHTTP(w, r)
	})
	if err := agent.syncSmartDistProfile(hbOut.ProfileID, hbOut.ProfileHash); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if hits != 0 {
		t.Errorf("same hash refetched %d time(s), want 0", hits)
	}

	// Step 5: a tampered body (hash mismatch) must be rejected: sync errors, the
	// file is not rewritten, and the stored hash does not advance. This exercises
	// the sha256 verification branch as a rejection, not just a happy path.
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/cluster/profile" {
			w.Write([]byte(`{"id":"cfg-a","name":"TAMPERED","smartdist":{"enabled":true,"rules":[]}}`))
			return
		}
		mux.ServeHTTP(w, r)
	})
	before, err := os.ReadFile(smartDistProfilePath)
	if err != nil {
		t.Fatalf("read before tamper: %v", err)
	}
	prevHash := agent.state.ProfileHash
	// A different wantHash forces a fetch so the verification branch actually runs.
	// Body is VALID JSON (so the parser accepts it) but differs from the hash the
	// heartbeat promised. Only the sha256 check can reject it; a non-JSON body would
	// be rejected by the parser and would not prove the hash branch is enforced.
	if err := agent.syncSmartDistProfile("cfg-a", "deadbeef"); err == nil {
		t.Fatalf("tampered body accepted; verification branch not enforced")
	}
	if agent.state.ProfileHash != prevHash {
		t.Errorf("stored hash advanced on tampered body: %q -> %q", prevHash, agent.state.ProfileHash)
	}
	after, err := os.ReadFile(smartDistProfilePath)
	if err != nil {
		t.Fatalf("read after tamper: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("file rewritten despite hash mismatch")
	}
}

func heartbeatForNode(t *testing.T, nodeID, nodeKey string) HeartbeatResponse {
	t.Helper()
	body := `{"node_id":"` + nodeID + `","node_key":"` + nodeKey + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/cluster/heartbeat", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handleClusterHeartbeat(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("heartbeat status %d: %s", rr.Code, rr.Body.String())
	}
	var out HeartbeatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("heartbeat json: %v", err)
	}
	return out
}
