package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// captureWriter merekam semua envelope yang dikirim Transfer.Out.
type captureWriter struct {
	msgs []*dns.Msg
}

func (w *captureWriter) LocalAddr() net.Addr  { return &net.TCPAddr{} }
func (w *captureWriter) RemoteAddr() net.Addr { return &net.TCPAddr{IP: net.ParseIP("127.0.0.1")} }
func (w *captureWriter) WriteMsg(m *dns.Msg) error {
	w.msgs = append(w.msgs, m.Copy())
	return nil
}
func (w *captureWriter) Write([]byte) (int, error) { return 0, nil }
func (w *captureWriter) Close() error              { return nil }
func (w *captureWriter) TsigStatus() error         { return nil }
func (w *captureWriter) TsigTimersOnly(bool)       {}
func (w *captureWriter) Hijack()                   {}

func ixfrRequest(zone string, clientSerial uint32) *dns.Msg {
	m := new(dns.Msg)
	m.SetIxfr(zone, clientSerial, "ns1."+zone, "hostmaster."+zone)
	return m
}

func writeDomainsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "domains.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Jalur sukses: delta ada di cache -> master mengirim urutan IXFR lengkap
// (newSOA, oldSOA, deletes, newSOA, adds, newSOA).
func TestHandleIXFRServesCachedDelta(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Zone = "rpz.example."
	cfg.CNAMETarget = "block.example."
	state := &State{
		Serial: 8,
		Deltas: []Delta{{
			FromSerial: 7,
			ToSerial:   8,
			Timestamp:  time.Now(),
			Added:      []string{"new.example"},
			Deleted:    []string{"old.example"},
		}},
	}
	srv := NewRPZServer(cfg, state)
	writer := &captureWriter{}
	srv.handleIXFR(writer, ixfrRequest(cfg.Zone, 7), cfg.Zone)

	if len(writer.msgs) == 0 {
		t.Fatal("IXFR tidak mengirim apa pun")
	}
	var gotNewSOA, gotOldSOA int
	cnames := map[string]string{}
	for _, m := range writer.msgs {
		for _, rr := range m.Answer {
			switch v := rr.(type) {
			case *dns.SOA:
				if v.Serial == 8 {
					gotNewSOA++
				} else if v.Serial == 7 {
					gotOldSOA++
				}
			case *dns.CNAME:
				cnames[v.Hdr.Name] = v.Target
			}
		}
	}
	if gotOldSOA != 1 {
		t.Fatalf("old-SOA (serial 7) = %d, want 1", gotOldSOA)
	}
	if gotNewSOA < 3 {
		t.Fatalf("new-SOA (serial 8) = %d, want >=3 (awal/mid/akhir)", gotNewSOA)
	}
	if _, ok := cnames["old.example.rpz.example."]; !ok {
		t.Fatalf("CNAME delete tidak dikirim: %v", cnames)
	}
	if _, ok := cnames["new.example.rpz.example."]; !ok {
		t.Fatalf("CNAME add tidak dikirim: %v", cnames)
	}
}

// Delta tidak ada di cache -> fallback AXFR penuh (bukan error/diam).
func TestHandleIXFRFallsBackToAXFRWithoutDelta(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Zone = "rpz.example."
	cfg.RawDomainFile = writeDomainsFile(t, "fallback.example\n")
	cfg.CNAMETarget = "block.example."
	srv := NewRPZServer(cfg, &State{Serial: 9})
	writer := &captureWriter{}
	srv.handleIXFR(writer, ixfrRequest(cfg.Zone, 2), cfg.Zone)

	if len(writer.msgs) == 0 {
		t.Fatal("fallback AXFR tidak mengirim apa pun")
	}
	first := writer.msgs[0]
	if len(first.Answer) == 0 {
		t.Fatal("pesan pertama kosong")
	}
	if _, ok := first.Answer[0].(*dns.SOA); !ok {
		t.Fatalf("fallback tidak diawali SOA: %v", first.Answer[0])
	}
	var sawCNAME bool
	for _, m := range writer.msgs {
		for _, rr := range m.Answer {
			if _, ok := rr.(*dns.CNAME); ok {
				sawCNAME = true
			}
		}
	}
	if !sawCNAME {
		t.Fatal("fallback AXFR tidak berisi CNAME domain")
	}
}

// Serial klien sudah terbaru -> balas satu SOA saat ini, tanpa transfer.
func TestHandleIXFRCurrentSerialRepliesSOAOnly(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Zone = "rpz.example."
	srv := NewRPZServer(cfg, &State{Serial: 10})
	writer := &captureWriter{}
	srv.handleIXFR(writer, ixfrRequest(cfg.Zone, 10), cfg.Zone)
	if len(writer.msgs) != 1 {
		t.Fatalf("kirim %d pesan, want 1", len(writer.msgs))
	}
	if len(writer.msgs[0].Answer) != 1 {
		t.Fatalf("answer %d RR, want 1 SOA", len(writer.msgs[0].Answer))
	}
	soa, ok := writer.msgs[0].Answer[0].(*dns.SOA)
	if !ok || soa.Serial != 10 {
		t.Fatalf("bukan SOA serial 10: %v", writer.msgs[0].Answer[0])
	}
}
