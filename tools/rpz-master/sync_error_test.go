package main

import (
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/miekg/dns"
)

// startTransferServer menjalankan server DNS uji yang menjawab sesuai handler.
func startTransferServer(t *testing.T, handler dns.HandlerFunc) string {
	t.Helper()
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpConn, err := net.ListenPacket("udp", tcpListener.Addr().String())
	if err != nil {
		_ = tcpListener.Close()
		t.Fatal(err)
	}
	tcpServer := &dns.Server{Listener: tcpListener, Net: "tcp", Handler: handler}
	udpServer := &dns.Server{PacketConn: udpConn, Net: "udp", Handler: handler}
	go func() { _ = tcpServer.ActivateAndServe() }()
	go func() { _ = udpServer.ActivateAndServe() }()
	t.Cleanup(func() { _ = tcpServer.Shutdown(); _ = udpServer.Shutdown() })
	return tcpListener.Addr().String()
}

func syncConfig(t *testing.T, upstream string) (Config, *State) {
	t.Helper()
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.SourceMode = "rpz-slave"
	cfg.Zone = "rpz.trustpositif."
	cfg.UpstreamMaster = upstream
	cfg.RawDomainFile = filepath.Join(dir, "domains.txt")
	cfg.CDBPath = filepath.Join(dir, "trust.db")
	cfg.StatePath = filepath.Join(dir, "state.json")
	return cfg, &State{}
}

// Transfer tidak diawali SOA zone -> error jelas (bukan diam-diam sukses).
func TestSyncRejectsTransferNotStartingWithZoneSOA(t *testing.T) {
	zone := "rpz.trustpositif."
	addr := startTransferServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(r)
		soa := transferSOA(zone, 5)
		if r.Question[0].Qtype == dns.TypeAXFR {
			response.Answer = []dns.RR{transferCNAME("x.example."+zone, "block.example."), soa}
		} else {
			response.Answer = []dns.RR{soa}
		}
		_ = w.WriteMsg(response)
	})
	cfg, state := syncConfig(t, addr)
	if err := NewApp(cfg, state).Sync(); err == nil {
		t.Fatal("transfer tanpa SOA awal diterima")
	}
}

// SOA upstream menandakan serial X tapi transfer membawa serial Y -> error
// (mencegah state tidak konsisten).
func TestSyncRejectsSerialMismatch(t *testing.T) {
	zone := "rpz.trustpositif."
	addr := startTransferServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(r)
		if r.Question[0].Qtype == dns.TypeAXFR {
			// SOA pembuka serial 5, tapi SOA penutup serial 6 -> tidak konsisten.
			response.Answer = []dns.RR{
				transferSOA(zone, 5),
				transferCNAME("x.example."+zone, "block.example."),
				transferSOA(zone, 6),
			}
		} else {
			response.Answer = []dns.RR{transferSOA(zone, 5)}
		}
		_ = w.WriteMsg(response)
	})
	cfg, state := syncConfig(t, addr)
	err := NewApp(cfg, state).Sync()
	if err == nil {
		t.Fatal("AXFR dengan SOA penutup serial beda diterima")
	}
	if state.Serial != 0 {
		t.Fatalf("state berubah walau transfer ditolak: serial=%d", state.Serial)
	}
	if _, statErr := os.Stat(cfg.RawDomainFile); statErr == nil {
		t.Fatal("raw file ditulis walau transfer ditolak")
	}
}

// Transfer kosong (server menutup tanpa record) -> error, bukan panic.
func TestSyncRejectsEmptyTransfer(t *testing.T) {
	addr := startTransferServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(r)
		// Tidak ada Answer sama sekali.
		_ = w.WriteMsg(response)
	})
	cfg, state := syncConfig(t, addr)
	if err := NewApp(cfg, state).Sync(); err == nil {
		t.Fatal("transfer kosong diterima")
	}
}

// SOA pembuka ada tapi untuk ZONE BERBEDA -> harus ditolak (mencegah transfer
// zona asing masuk ke basis lokal).
func TestSyncRejectsForeignZoneSOA(t *testing.T) {
	addr := startTransferServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(r)
		foreign := transferSOA("other.zone.", 5)
		if r.Question[0].Qtype == dns.TypeAXFR {
			response.Answer = []dns.RR{foreign, transferCNAME("x.other.zone.", "block.example."), foreign}
		} else {
			response.Answer = []dns.RR{transferSOA("rpz.trustpositif.", 5)}
		}
		_ = w.WriteMsg(response)
	})
	cfg, state := syncConfig(t, addr)
	if err := NewApp(cfg, state).Sync(); err == nil {
		t.Fatal("transfer zona asing diterima")
	}
	if state.Serial != 0 {
		t.Fatalf("state berubah walau zona asing: serial=%d", state.Serial)
	}
}

// Kasus licik: SOA pembuka zone ASING, tapi record dan SOA penutup di zone
// kita. Tanpa validasi zone pada SOA pembuka, transfer ini akan lolos dan
// menulis domain dari server yang tidak berhak. Harus ditolak.
func TestSyncRejectsForeignOpeningSOAWithLocalRecords(t *testing.T) {
	zone := "rpz.trustpositif."
	addr := startTransferServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(r)
		if r.Question[0].Qtype == dns.TypeAXFR {
			response.Answer = []dns.RR{
				transferSOA("evil.zone.", 5),                      // pembuka zona asing
				transferCNAME("injected."+zone, "block.example."), // record zone kita
				transferSOA(zone, 5),                              // penutup zone kita
			}
		} else {
			response.Answer = []dns.RR{transferSOA(zone, 5)}
		}
		_ = w.WriteMsg(response)
	})
	cfg, state := syncConfig(t, addr)
	if err := NewApp(cfg, state).Sync(); err == nil {
		t.Fatal("transfer dengan SOA pembuka zona asing DITERIMA (bahaya)")
	}
	if state.Serial != 0 {
		t.Fatalf("state berubah: serial=%d", state.Serial)
	}
	if _, err := os.Stat(cfg.RawDomainFile); err == nil {
		t.Fatal("raw file ditulis walau transfer ditolak")
	}
}

// Serial wrap RFC 1982: state lokal hampir max, upstream kecil tapi "lebih
// baru" secara wrap -> harus minta AXFR (bukan IXFR yang salah hitung).
func TestSyncSerialWrapRequestsAXFR(t *testing.T) {
	zone := "rpz.trustpositif."
	var lastQtype atomic.Uint32
	addr := startTransferServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(r)
		soa := transferSOA(zone, 5)
		lastQtype.Store(uint32(r.Question[0].Qtype))
		if r.Question[0].Qtype == dns.TypeAXFR {
			response.Answer = []dns.RR{soa, transferCNAME("x."+zone, "b."), soa}
		} else {
			response.Answer = []dns.RR{soa}
		}
		_ = w.WriteMsg(response)
	})
	cfg, state := syncConfig(t, addr)
	// raw file HARUS ada, kalau tidak Sync() mereset snapshot.Serial ke 0
	// dan cabang wrap tidak pernah diuji.
	if err := os.WriteFile(cfg.RawDomainFile, []byte("stale.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state.Serial = ^uint32(0) - 1 // hampir max; upstream 5 adalah "lebih baru" via wrap
	if err := NewApp(cfg, state).Sync(); err != nil {
		t.Fatal(err)
	}
	if got := uint16(lastQtype.Load()); got != dns.TypeAXFR {
		t.Fatalf("qtype transfer = %s, want AXFR (serial wrap harus paksa AXFR)", dns.TypeToString[got])
	}
	if state.Serial != 5 {
		t.Fatalf("serial = %d, want 5", state.Serial)
	}
}

// Upstream tidak dapat dihubungi -> error, state tidak berubah.
func TestSyncUpstreamUnreachable(t *testing.T) {
	cfg, state := syncConfig(t, "127.0.0.1:1") // port mati
	if err := NewApp(cfg, state).Sync(); err == nil {
		t.Fatal("upstream mati diterima")
	}
	if state.Serial != 0 {
		t.Fatalf("state berubah: serial=%d", state.Serial)
	}
}
