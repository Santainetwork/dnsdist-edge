package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/miekg/dns"
)

// Edge case: AXFR zone kosong (hanya SOA pembuka+penutup, tanpa record lain).
// Deteksi AXFR vs IXFR dari 2 record pertama ambigu ([SOA,SOA]). Pastikan
// perilakunya tidak crash dan pesan errornya jelas (bukan panic).
func TestSyncEmptyZoneAXFRBehavior(t *testing.T) {
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
		soa := transferSOA(zone, 5)
		response := new(dns.Msg)
		response.SetReply(r)
		response.Authoritative = true
		if r.Question[0].Qtype == dns.TypeAXFR {
			// Zone kosong: SOA, SOA.
			response.Answer = []dns.RR{soa, soa}
		} else {
			response.Answer = []dns.RR{soa}
		}
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
	state := &State{}
	err = NewApp(cfg, state).Sync()
	// Zona kosong adalah AXFR yang sah ([SOA,SOA]): harus sukses, 0 domain.
	if err != nil {
		t.Fatalf("AXFR zona kosong gagal: %v", err)
	}
	if state.Serial != 5 {
		t.Fatalf("serial = %d, want 5", state.Serial)
	}
	if state.TotalDomains != 0 {
		t.Fatalf("total = %d, want 0", state.TotalDomains)
	}
	if data, rerr := os.ReadFile(cfg.RawDomainFile); rerr != nil || len(data) != 0 {
		t.Fatalf("raw file zona kosong: data=%q err=%v (want kosong)", data, rerr)
	}
	if _, serr := os.Stat(cfg.CDBPath); serr != nil {
		t.Fatalf("CDB tidak dibuat: %v", serr)
	}
}
