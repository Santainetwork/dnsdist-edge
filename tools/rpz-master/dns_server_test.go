package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

type testResponseWriter struct {
	remote     net.Addr
	tsigStatus error
	msg        *dns.Msg
	writes     int
}

func (w *testResponseWriter) LocalAddr() net.Addr       { return &net.TCPAddr{} }
func (w *testResponseWriter) RemoteAddr() net.Addr      { return w.remote }
func (w *testResponseWriter) WriteMsg(m *dns.Msg) error { w.msg = m; w.writes++; return nil }
func (w *testResponseWriter) Write([]byte) (int, error) { return 0, nil }
func (w *testResponseWriter) Close() error              { return nil }
func (w *testResponseWriter) TsigStatus() error         { return w.tsigStatus }
func (w *testResponseWriter) TsigTimersOnly(bool)       {}
func (w *testResponseWriter) Hijack()                   {}

func TestTransferACLDefaultsToLoopback(t *testing.T) {
	srv := NewRPZServer(Config{}, &State{})
	if !srv.transferAllowed(net.ParseIP("127.0.0.1")) {
		t.Fatal("loopback denied")
	}
	if srv.transferAllowed(net.ParseIP("192.0.2.1")) {
		t.Fatal("non-loopback allowed")
	}
}

func TestSOAKeepsUnknownSerialAtZero(t *testing.T) {
	soa := NewRPZServer(DefaultConfig(), &State{}).getSOA("rpz.trustpositif.")
	if soa.Serial != 0 {
		t.Fatalf("unknown upstream serial published as %d; want 0", soa.Serial)
	}
}

func TestAXFRScannerErrorOmitsClosingSOA(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "domains.txt")
	if err := os.WriteFile(raw, []byte(strings.Repeat("x", 1024*1024+1)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.RawDomainFile = raw
	state := &State{Serial: 5, TotalDomains: 1}
	srv := NewRPZServer(cfg, state)
	writer := &testResponseWriter{remote: &net.TCPAddr{IP: net.ParseIP("127.0.0.1")}}
	request := new(dns.Msg)
	request.SetAxfr("rpz.trustpositif.")
	srv.handleAXFR(writer, request, "rpz.trustpositif.")
	if writer.writes != 1 {
		t.Fatalf("scanner failure produced %d envelopes; want only opening SOA", writer.writes)
	}
}

func TestAXFRWaitsForActivePublication(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.RawDomainFile = filepath.Join(dir, "domains.txt")
	if err := os.WriteFile(cfg.RawDomainFile, []byte("example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	publicationMu := new(sync.RWMutex)
	srv := NewRPZServerWithMutex(cfg, &State{Serial: 5}, nil, publicationMu)
	request := new(dns.Msg)
	request.SetAxfr("rpz.trustpositif.")
	writer := &testResponseWriter{remote: &net.TCPAddr{IP: net.ParseIP("127.0.0.1")}}
	publicationMu.Lock()
	done := make(chan struct{})
	go func() {
		srv.handleAXFR(writer, request, "rpz.trustpositif.")
		close(done)
	}()
	select {
	case <-done:
		publicationMu.Unlock()
		t.Fatal("AXFR opened snapshot during active publication")
	case <-time.After(20 * time.Millisecond):
	}
	publicationMu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("AXFR remained blocked after publication")
	}
}

func TestTransferACLHonorsConfiguredCIDR(t *testing.T) {
	cfg := Config{TransferACL: []string{"192.0.2.0/24"}}
	srv := NewRPZServer(cfg, &State{})
	if !srv.transferAllowed(net.ParseIP("192.0.2.10")) {
		t.Fatal("configured address denied")
	}
	if srv.transferAllowed(net.ParseIP("127.0.0.1")) {
		t.Fatal("address outside configured ACL allowed")
	}
}

func TestTransferRequiresValidTSIGWhenConfigured(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TSIGKey = "transfer.example."
	srv := NewRPZServer(cfg, &State{})
	request := new(dns.Msg)
	request.SetAxfr("rpz.example.")
	writer := &testResponseWriter{remote: &net.TCPAddr{IP: net.ParseIP("127.0.0.1")}}
	if srv.transferAuthorized(writer, request) {
		t.Fatal("unsigned transfer authorized")
	}
	request.SetTsig("transfer.example.", dns.HmacSHA256, 300, 1)
	if !srv.transferAuthorized(writer, request) {
		t.Fatal("valid signed transfer denied")
	}
	request.SetTsig("transfer.example.", dns.HmacMD5, 300, 1)
	if srv.transferAuthorized(writer, request) {
		t.Fatal("TSIG algorithm downgrade authorized")
	}
	request.SetTsig("transfer.example.", dns.HmacSHA256, 300, 1)
	writer.tsigStatus = errors.New("bad signature")
	if srv.transferAuthorized(writer, request) {
		t.Fatal("invalid signature authorized")
	}
}

func TestIXFRWithNonNewerClientSerialReturnsCurrentSOA(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Zone = "rpz.trustpositif."
	srv := NewRPZServer(cfg, &State{Serial: 10})
	request := new(dns.Msg)
	request.SetIxfr(cfg.Zone, 20, "ns1."+cfg.Zone, "hostmaster."+cfg.Zone)
	writer := &testResponseWriter{remote: &net.TCPAddr{IP: net.ParseIP("127.0.0.1")}}
	srv.ServeDNS(writer, request)
	if writer.msg == nil || writer.msg.Rcode != dns.RcodeSuccess || len(writer.msg.Answer) != 1 {
		t.Fatalf("future serial response = %#v, want one current SOA", writer.msg)
	}
	soa, ok := writer.msg.Answer[0].(*dns.SOA)
	if !ok || soa.Serial != 10 {
		t.Fatalf("future serial answer = %v, want SOA serial 10", writer.msg.Answer[0])
	}
}
