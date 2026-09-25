package main

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestAppSerializesUpdates(t *testing.T) {
	app := NewApp(DefaultConfig(), &State{})
	var active atomic.Int32
	var maximum atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := app.withUpdate(func() error {
				n := active.Add(1)
				if n > maximum.Load() {
					maximum.Store(n)
				}
				time.Sleep(10 * time.Millisecond)
				active.Add(-1)
				return nil
			}); err != nil {
				t.Errorf("withUpdate() error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if maximum.Load() != 1 {
		t.Fatalf("maximum concurrent updates = %d, want 1", maximum.Load())
	}
}

func TestRunSyncReturnsErrors(t *testing.T) {
	cfg := DefaultConfig()
	app := NewApp(cfg, &State{})
	if err := app.Sync(); err == nil {
		t.Fatal("Sync() succeeded without upstream")
	}
}

func TestSyncTreatsWrappedUpstreamSerialAsNewer(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpConn, err := net.ListenPacket("udp", tcpListener.Addr().String())
	if err != nil {
		_ = tcpListener.Close()
		t.Fatal(err)
	}
	var sawAXFR atomic.Bool
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(r)
		soa := transferSOA("rpz.trustpositif.", 1)
		if r.Question[0].Qtype == dns.TypeAXFR {
			sawAXFR.Store(true)
			response.Answer = []dns.RR{soa, transferCNAME("blocked.example.rpz.trustpositif.", "block.example."), soa}
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
	cfg.UpstreamMaster = tcpListener.Addr().String()
	cfg.RawDomainFile = filepath.Join(dir, "domains.txt")
	cfg.CDBPath = filepath.Join(dir, "trust.db")
	cfg.StatePath = filepath.Join(dir, "state.json")
	state := &State{Serial: ^uint32(0)}
	if err := NewApp(cfg, state).Sync(); err != nil {
		t.Fatal(err)
	}
	if !sawAXFR.Load() || state.Serial != 1 {
		t.Fatalf("wrapped sync: saw AXFR=%t serial=%d", sawAXFR.Load(), state.Serial)
	}
}

func TestSyncRecoversMissingRawSnapshotAtCurrentSerial(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpConn, err := net.ListenPacket("udp", tcpListener.Addr().String())
	if err != nil {
		_ = tcpListener.Close()
		t.Fatal(err)
	}
	var sawAXFR atomic.Bool
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(r)
		soa := transferSOA("rpz.trustpositif.", 7)
		if r.Question[0].Qtype == dns.TypeAXFR {
			sawAXFR.Store(true)
			response.Answer = []dns.RR{soa, transferCNAME("blocked.example.rpz.trustpositif.", "block.example."), soa}
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
	cfg.UpstreamMaster = tcpListener.Addr().String()
	cfg.RawDomainFile = filepath.Join(dir, "domains.txt")
	cfg.CDBPath = filepath.Join(dir, "trust.db")
	cfg.StatePath = filepath.Join(dir, "state.json")
	if err := NewApp(cfg, &State{Serial: 7}).Sync(); err != nil {
		t.Fatal(err)
	}
	if !sawAXFR.Load() {
		t.Fatal("missing raw snapshot did not force AXFR")
	}
}

func TestBuildCDBKeepsRPZSlaveSerialUnknownUntilTransfer(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.SourceMode = "rpz-slave"
	cfg.RawDomainFile = filepath.Join(dir, "domains.txt")
	cfg.CDBPath = filepath.Join(dir, "trust.cdb")
	cfg.StatePath = filepath.Join(dir, "state.json")
	if err := os.WriteFile(cfg.RawDomainFile, []byte("example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	state := &State{}
	if err := NewApp(cfg, state).BuildCDB(); err != nil {
		t.Fatal(err)
	}
	if state.Serial != 0 {
		t.Fatalf("local build assigned upstream serial %d; want unknown (0)", state.Serial)
	}
}

func TestBootstrapKeepsRPZSlaveSerialUnknownUntilTransfer(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.SourceMode = "rpz-slave"
	cfg.RawDomainFile = filepath.Join(dir, "domains.txt")
	cfg.CDBPath = filepath.Join(dir, "trust.cdb")
	cfg.StatePath = filepath.Join(dir, "state.json")
	if err := os.WriteFile(cfg.RawDomainFile, []byte("example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	state := &State{}
	if err := NewApp(cfg, state).Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if state.Serial != 0 {
		t.Fatalf("bootstrap assigned upstream serial %d; want unknown (0)", state.Serial)
	}
}

func TestVersionAliases(t *testing.T) {
	for _, alias := range []string{"-v", "-version"} {
		t.Run(alias, func(t *testing.T) {
			options, err := parseOptions([]string{alias}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if !options.showVersion {
				t.Fatalf("%s did not enable version output", alias)
			}
		})
	}
}

func TestParseOptionsReturnsFlagError(t *testing.T) {
	_, err := parseOptions([]string{"-unknown"}, io.Discard)
	if err == nil {
		t.Fatal("parseOptions() accepted unknown flag")
	}
}

func TestAppServeStopsWhenContextCanceled(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.ListenDNS = "127.0.0.1:0"
	cfg.ListenHTTP = "127.0.0.1:0"
	cfg.RawDomainFile = filepath.Join(dir, "domains.txt")
	cfg.CDBPath = filepath.Join(dir, "trust.cdb")
	cfg.StatePath = filepath.Join(dir, "state.json")
	if err := os.WriteFile(cfg.RawDomainFile, []byte("example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- NewApp(cfg, &State{}).Serve(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve() did not shut down")
	}
}

func TestServeBuildsMissingCDBAfterFailedInitialTransfer(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.SourceMode = "rpz-slave"
	cfg.UpstreamMaster = "127.0.0.1:1"
	cfg.ListenDNS = "127.0.0.1:0"
	cfg.ListenHTTP = ""
	cfg.RawDomainFile = filepath.Join(dir, "domains.txt")
	cfg.CDBPath = filepath.Join(dir, "trust.db")
	cfg.StatePath = filepath.Join(dir, "state.json")
	if err := os.WriteFile(cfg.RawDomainFile, []byte("example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewApp(cfg, &State{}).Serve(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.CDBPath); err != nil {
		t.Fatalf("missing CDB was not rebuilt: %v", err)
	}
}
