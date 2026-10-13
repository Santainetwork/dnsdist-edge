package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/miekg/dns"
)

// Uji e2e jalur IXFR delta setelah refactor streaming: master melayani IXFR
// (newSOA, oldSOA, deletes, newSOA, adds, newSOA) -> slave harus menerapkan
// delta ke raw file + CDB, dan menyimpan delta (bukan Full) ke state.
func TestSyncAppliesIXFRDeltaEndToEnd(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpConn, err := net.ListenPacket("udp", tcpListener.Addr().String())
	if err != nil {
		_ = tcpListener.Close()
		t.Fatal(err)
	}
	zone := "rpz.trustpositif."
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		soaNew := transferSOA(zone, 8)
		soaOld := transferSOA(zone, 7)
		if r.Question[0].Qtype == dns.TypeIXFR {
			response := new(dns.Msg)
			response.SetReply(r)
			response.Authoritative = true
			response.Answer = []dns.RR{
				soaNew, soaOld,
				transferCNAME("old.example."+zone, "block.example."),
				soaNew,
				transferCNAME("new.example."+zone, "block.example."),
				soaNew,
			}
			_ = w.WriteMsg(response)
			return
		}
		response := new(dns.Msg)
		response.SetReply(r)
		response.Answer = []dns.RR{soaNew}
		_ = w.WriteMsg(response)
	})
	tcpServer := &dns.Server{Listener: tcpListener, Net: "tcp", Handler: handler}
	udpServer := &dns.Server{PacketConn: udpConn, Net: "udp", Handler: handler}
	go func() { _ = tcpServer.ActivateAndServe() }()
	go func() { _ = udpServer.ActivateAndServe() }()
	defer tcpServer.Shutdown()
	defer udpServer.Shutdown()

	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.SourceMode = "rpz-slave"
	cfg.Zone = zone
	cfg.UpstreamMaster = tcpListener.Addr().String()
	cfg.RawDomainFile = filepath.Join(dir, "domains.txt")
	cfg.CDBPath = filepath.Join(dir, "trust.db")
	cfg.StatePath = filepath.Join(dir, "state.json")
	if err := os.WriteFile(cfg.RawDomainFile, []byte("old.example\nkeep.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := &State{Serial: 7}
	if err := NewApp(cfg, state).Sync(); err != nil {
		t.Fatal(err)
	}
	if state.Serial != 8 {
		t.Fatalf("serial = %d, want 8", state.Serial)
	}
	got, err := os.ReadFile(cfg.RawDomainFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "keep.example\nnew.example\n"
	if string(got) != want {
		t.Fatalf("raw file setelah IXFR = %q, want %q", got, want)
	}
	if len(state.Deltas) != 1 || state.Deltas[0].Full {
		t.Fatalf("delta IXFR tidak disimpan: %+v", state.Deltas)
	}
	if _, err := os.Stat(cfg.CDBPath); err != nil {
		t.Fatalf("CDB tidak dibuat: %v", err)
	}
}

// Upstream yang menjawab IXFR dengan AXFR penuh harus tetap diproses (jalur
// streamAXFRFromEnvelopes), dan hasilnya delta Full dengan staging terpakai.
func TestSyncHandlesAXFRAnsweredToIXFR(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpConn, err := net.ListenPacket("udp", tcpListener.Addr().String())
	if err != nil {
		_ = tcpListener.Close()
		t.Fatal(err)
	}
	zone := "rpz.trustpositif."
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		soa := transferSOA(zone, 9)
		if r.Question[0].Qtype == dns.TypeIXFR {
			// Jawab dengan AXFR penuh (tanpa old-SOA di posisi kedua).
			response := new(dns.Msg)
			response.SetReply(r)
			response.Authoritative = true
			response.Answer = []dns.RR{
				soa,
				transferCNAME("full1.example."+zone, "block.example."),
				transferCNAME("full2.example."+zone, "block.example."),
				soa,
			}
			_ = w.WriteMsg(response)
			return
		}
		response := new(dns.Msg)
		response.SetReply(r)
		response.Answer = []dns.RR{soa}
		_ = w.WriteMsg(response)
	})
	tcpServer := &dns.Server{Listener: tcpListener, Net: "tcp", Handler: handler}
	udpServer := &dns.Server{PacketConn: udpConn, Net: "udp", Handler: handler}
	go func() { _ = tcpServer.ActivateAndServe() }()
	go func() { _ = udpServer.ActivateAndServe() }()
	defer tcpServer.Shutdown()
	defer udpServer.Shutdown()

	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.SourceMode = "rpz-slave"
	cfg.Zone = zone
	cfg.UpstreamMaster = tcpListener.Addr().String()
	cfg.RawDomainFile = filepath.Join(dir, "domains.txt")
	cfg.CDBPath = filepath.Join(dir, "trust.db")
	cfg.StatePath = filepath.Join(dir, "state.json")
	if err := os.WriteFile(cfg.RawDomainFile, []byte("stale.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := &State{Serial: 7}
	if err := NewApp(cfg, state).Sync(); err != nil {
		t.Fatal(err)
	}
	if state.Serial != 9 {
		t.Fatalf("serial = %d, want 9", state.Serial)
	}
	got, err := os.ReadFile(cfg.RawDomainFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "full1.example\nfull2.example\n"
	if string(got) != want {
		t.Fatalf("raw file = %q, want %q", got, want)
	}
}
