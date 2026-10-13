package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// periodicSync harus: (a) memanggil Sync secara berkala, (b) berhenti saat
// context dibatalkan (tidak ada goroutine bocor / sync setelah cancel).
func TestPeriodicSyncTicksAndStops(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpConn, err := net.ListenPacket("udp", tcpListener.Addr().String())
	if err != nil {
		_ = tcpListener.Close()
		t.Fatal(err)
	}
	var soaQueries atomic.Int32
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		soaQueries.Add(1)
		response := new(dns.Msg)
		response.SetReply(r)
		response.Answer = []dns.RR{transferSOA("rpz.trustpositif.", 3)}
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
	if err := os.WriteFile(cfg.RawDomainFile, []byte("x.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp(cfg, &State{Serial: 3})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		app.periodicSync(ctx, 20*time.Millisecond)
		close(done)
	}()

	// Tunggu minimal 2 tick (query SOA ke upstream).
	deadline := time.Now().Add(2 * time.Second)
	for soaQueries.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if soaQueries.Load() < 2 {
		cancel()
		t.Fatalf("periodicSync tidak memicu sync berkala (queries=%d)", soaQueries.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("periodicSync tidak berhenti setelah cancel")
	}
	// Setelah cancel, tidak boleh ada query tambahan.
	after := soaQueries.Load()
	time.Sleep(80 * time.Millisecond)
	if got := soaQueries.Load(); got != after {
		t.Fatalf("masih ada sync setelah cancel: %d -> %d", after, got)
	}
}
