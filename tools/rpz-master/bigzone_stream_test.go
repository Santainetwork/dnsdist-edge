package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/miekg/dns"
)

// countingWriter menghitung envelope dan CNAME yang dikirim master.
type countingWriter struct {
	cnames int
	envs   int
}

func (w *countingWriter) LocalAddr() net.Addr  { return &net.TCPAddr{} }
func (w *countingWriter) RemoteAddr() net.Addr { return &net.TCPAddr{IP: net.ParseIP("127.0.0.1")} }
func (w *countingWriter) WriteMsg(m *dns.Msg) error {
	w.envs++
	for _, rr := range m.Answer {
		if _, ok := rr.(*dns.CNAME); ok {
			w.cnames++
		}
	}
	return nil
}
func (w *countingWriter) Write([]byte) (int, error) { return 0, nil }
func (w *countingWriter) Close() error              { return nil }
func (w *countingWriter) TsigStatus() error         { return nil }
func (w *countingWriter) TsigTimersOnly(bool)       {}
func (w *countingWriter) Hijack()                   {}

// Tes merah: AXFR master mengirim seluruh domain dari file besar.
func TestAXFRStreamsLargeZone(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "domains.txt")
	f, err := os.Create(raw)
	if err != nil {
		t.Fatal(err)
	}
	const total = 50_000
	for i := 0; i < total; i++ {
		fmt.Fprintf(f, "domain-%07d.example\n", i)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.RawDomainFile = raw
	cfg.Zone = "rpz.test."
	cfg.CNAMETarget = "block.example."
	srv := NewRPZServer(cfg, &State{Serial: 1, TotalDomains: total})

	collector := &countingWriter{}
	request := new(dns.Msg)
	request.SetAxfr(cfg.Zone)
	srv.handleAXFR(collector, request, cfg.Zone)

	if collector.cnames != total {
		t.Fatalf("AXFR mengirim %d CNAME, want %d", collector.cnames, total)
	}
}

// Tes merah: full AXFR dari upstream harus ditulis streaming ke file staging,
// bukan diakumulasi sebagai daftar domain di memori. State tidak boleh
// menyimpan daftar penuh (state.json tetap kecil).
func TestSyncFullAXFRStreamsToStagingWithoutHugeState(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpConn, err := net.ListenPacket("udp", tcpListener.Addr().String())
	if err != nil {
		_ = tcpListener.Close()
		t.Fatal(err)
	}
	const total = 20_000
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		soa := transferSOA("rpz.trustpositif.", 3)
		if r.Question[0].Qtype == dns.TypeAXFR {
			ch := make(chan *dns.Envelope, 8)
			go func() {
				defer close(ch)
				ch <- &dns.Envelope{RR: []dns.RR{soa}}
				chunk := make([]dns.RR, 0, 200)
				for i := 0; i < total; i++ {
					chunk = append(chunk, transferCNAME(fmt.Sprintf("d%07d.example.rpz.trustpositif.", i), "block.example."))
					if len(chunk) == 200 {
						ch <- &dns.Envelope{RR: chunk}
						chunk = make([]dns.RR, 0, 200)
					}
				}
				if len(chunk) > 0 {
					ch <- &dns.Envelope{RR: chunk}
				}
				ch <- &dns.Envelope{RR: []dns.RR{soa}}
			}()
			tr := new(dns.Transfer)
			_ = tr.Out(w, r, ch)
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
	cfg.UpstreamMaster = tcpListener.Addr().String()
	cfg.RawDomainFile = filepath.Join(dir, "domains.txt")
	cfg.CDBPath = filepath.Join(dir, "trust.db")
	cfg.StatePath = filepath.Join(dir, "state.json")
	state := &State{}
	if err := NewApp(cfg, state).Sync(); err != nil {
		t.Fatal(err)
	}

	if state.Serial != 3 {
		t.Fatalf("serial = %d, want 3", state.Serial)
	}
	if state.TotalDomains != total {
		t.Fatalf("total = %d, want %d", state.TotalDomains, total)
	}
	// State tidak boleh menyimpan daftar penuh domain.
	if len(state.Deltas) != 0 {
		t.Fatalf("state menyimpan %d delta penuh; want 0", len(state.Deltas))
	}
	raw, err := os.ReadFile(cfg.RawDomainFile)
	if err != nil {
		t.Fatal(err)
	}
	if lines := countLines(raw); lines != total {
		t.Fatalf("raw file %d baris, want %d", lines, total)
	}
	if _, err := os.Stat(cfg.CDBPath); err != nil {
		t.Fatalf("CDB tidak ada: %v", err)
	}
}

func countLines(b []byte) int {
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}
