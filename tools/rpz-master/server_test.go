package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestRPZMasterServer(t *testing.T) {
	tempDir := t.TempDir()
	rawFile := filepath.Join(tempDir, "domains.txt")
	cdbFile := filepath.Join(tempDir, "test.cdb")
	stateFile := filepath.Join(tempDir, "state.json")

	// 1. Create sample domains
	sampleDomains := "evil1.com\nevil2.org\n# komentar\njudionline.net\n"
	if err := os.WriteFile(rawFile, []byte(sampleDomains), 0644); err != nil {
		t.Fatalf("failed to write sample domains: %v", err)
	}

	cfg := Config{
		Zone:          "rpz.santainetwork.",
		ListenDNS:     "127.0.0.1:25353",
		ListenHTTP:    "127.0.0.1:28088",
		CNAMETarget:   "blockpage.komdigi.go.id.",
		CDBPath:       cdbFile,
		StatePath:     stateFile,
		RawDomainFile: rawFile,
		MaxTransfers:  5,
	}

	state := &State{
		Serial:       2026091201,
		LastUpdate:   time.Now(),
		TotalDomains: 3,
	}

	// 2. Compile CDB
	res, err := CompileDomainsToCDB(rawFile, cdbFile, true)
	if err != nil {
		t.Fatalf("CompileDomainsToCDB failed: %v", err)
	}
	if res.TotalEntries != 3 {
		t.Errorf("Expected 3 entries, got %d", res.TotalEntries)
	}
	state.CDBSHA256 = res.SHA256

	// 3. Start RPZ Server
	rpzSrv := NewRPZServer(cfg, state)
	tcpServer := &dns.Server{
		Addr:    cfg.ListenDNS,
		Net:     "tcp",
		Handler: rpzSrv,
	}
	go func() {
		_ = tcpServer.ListenAndServe()
	}()
	defer tcpServer.Shutdown()

	// 4. Start HTTP Server
	httpSrv := NewHTTPServer(cfg, state, rpzSrv.stateMu)
	if err := httpSrv.Start(); err != nil {
		t.Fatalf("HTTP server start failed: %v", err)
	}
	defer httpSrv.Shutdown(context.Background())
	time.Sleep(150 * time.Millisecond)

	// Test 5: Query SOA
	client := new(dns.Client)
	client.Net = "tcp"
	m := new(dns.Msg)
	m.SetQuestion(cfg.Zone, dns.TypeSOA)

	r, _, err := client.Exchange(m, cfg.ListenDNS)
	if err != nil {
		t.Fatalf("SOA query failed: %v", err)
	}
	if len(r.Answer) == 0 {
		t.Fatalf("Expected SOA answer, got none")
	}
	soa, ok := r.Answer[0].(*dns.SOA)
	if !ok || soa.Serial != 2026091201 {
		t.Errorf("SOA Serial mismatch, got %v", soa)
	}

	// Test 6: AXFR Transfer
	tr := new(dns.Transfer)
	axfrMsg := new(dns.Msg)
	axfrMsg.SetAxfr(cfg.Zone)

	envChan, err := tr.In(axfrMsg, cfg.ListenDNS)
	if err != nil {
		t.Fatalf("AXFR failed: %v", err)
	}

	var receivedRR []dns.RR
	for env := range envChan {
		if env.Error != nil {
			t.Fatalf("AXFR env error: %v", env.Error)
		}
		receivedRR = append(receivedRR, env.RR...)
	}

	// Expected: Initial SOA + 3 CNAMEs + Final SOA = 5 RRs
	if len(receivedRR) != 5 {
		t.Errorf("Expected 5 RRs in AXFR, got %d", len(receivedRR))
	}

	// Test 7: HTTP Manifest endpoint
	resp, err := http.Get("http://" + cfg.ListenHTTP + "/manifest.json")
	if err != nil {
		t.Fatalf("HTTP manifest failed: %v", err)
	}
	defer resp.Body.Close()

	var manifest map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		t.Fatalf("JSON decode manifest failed: %v", err)
	}
	if manifest["status"] != "ok" || manifest["total_domains"].(float64) != 3 {
		t.Errorf("Manifest response mismatch: %v", manifest)
	}
}
