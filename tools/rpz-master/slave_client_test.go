package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func transferSOA(zone string, serial uint32) *dns.SOA {
	return &dns.SOA{
		Hdr:    dns.RR_Header{Name: zone, Rrtype: dns.TypeSOA, Class: dns.ClassINET},
		Ns:     "ns1." + zone,
		Mbox:   "hostmaster." + zone,
		Serial: serial,
	}
}

func transferCNAME(owner, target string) *dns.CNAME {
	return &dns.CNAME{
		Hdr:    dns.RR_Header{Name: owner, Rrtype: dns.TypeCNAME, Class: dns.ClassINET},
		Target: target,
	}
}

func TestTransferRequestSelectsAXFRForEmptyState(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SourceMode = "rpz-slave"
	cfg.Zone = "rpz.example."
	cfg.UpstreamMaster = "127.0.0.1:53"

	client, err := NewSlaveClient(cfg, &State{})
	if err != nil {
		t.Fatal(err)
	}
	if got := client.transferRequest().Question[0].Qtype; got != dns.TypeAXFR {
		t.Fatalf("empty state request type = %s, want AXFR", dns.TypeToString[got])
	}

	client.state.Serial = 42
	if got := client.transferRequest().Question[0].Qtype; got != dns.TypeIXFR {
		t.Fatalf("existing state request type = %s, want IXFR", dns.TypeToString[got])
	}
}

func TestParseTransferRecordsRejectsOutOfZoneOwner(t *testing.T) {
	zone := "rpz.example."
	records := []dns.RR{
		transferSOA(zone, 2),
		transferCNAME("outside.example.", "block.example."),
		transferSOA(zone, 2),
	}
	if _, err := parseTransferRecords(records, zone, 0); err == nil {
		t.Fatal("out-of-zone AXFR owner accepted")
	}
}

func TestParseTransferRecordsRecognizesFullAXFR(t *testing.T) {
	zone := "rpz.example."
	records := []dns.RR{
		transferSOA(zone, 7),
		transferCNAME("evil.example."+zone, "block.example."),
		transferSOA(zone, 7),
	}
	delta, err := parseTransferRecords(records, zone, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !delta.Full || delta.FromSerial != 0 || delta.ToSerial != 7 {
		t.Fatalf("AXFR delta = %+v", delta)
	}
	if len(delta.Added) != 1 || delta.Added[0] != "evil.example" {
		t.Fatalf("AXFR domains = %q", delta.Added)
	}
}

func TestParseTransferRecordsParsesIXFRSequence(t *testing.T) {
	zone := "rpz.example."
	records := []dns.RR{
		transferSOA(zone, 8),
		transferSOA(zone, 7),
		transferCNAME("old.example."+zone, "block.example."),
		transferSOA(zone, 8),
		transferCNAME("new.example."+zone, "block.example."),
		transferSOA(zone, 8),
	}
	delta, err := parseTransferRecords(records, zone, 7)
	if err != nil {
		t.Fatal(err)
	}
	if delta.Full || delta.FromSerial != 7 || delta.ToSerial != 8 {
		t.Fatalf("IXFR delta = %+v", delta)
	}
	if strings.Join(delta.Deleted, ",") != "old.example" || strings.Join(delta.Added, ",") != "new.example" {
		t.Fatalf("IXFR changes = -%q +%q", delta.Deleted, delta.Added)
	}
}

func TestParseTransferRecordsCombinesMultipleIXFRSequences(t *testing.T) {
	zone := "rpz.example."
	records := []dns.RR{
		transferSOA(zone, 9),
		transferSOA(zone, 7),
		transferCNAME("removed.example."+zone, "block.example."),
		transferSOA(zone, 8),
		transferCNAME("transient.example."+zone, "block.example."),
		transferSOA(zone, 8),
		transferCNAME("transient.example."+zone, "block.example."),
		transferSOA(zone, 9),
		transferCNAME("final.example."+zone, "block.example."),
		transferSOA(zone, 9),
	}
	delta, err := parseTransferRecords(records, zone, 7)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(delta.Deleted, ",") != "removed.example,transient.example" {
		t.Fatalf("combined deletes = %q", delta.Deleted)
	}
	if strings.Join(delta.Added, ",") != "final.example" {
		t.Fatalf("combined additions = %q", delta.Added)
	}
}

func TestSerialGreaterHandlesRFC1982Wrap(t *testing.T) {
	if !serialGreater(1, ^uint32(0)) {
		t.Fatal("wrapped serial 1 should be newer than max uint32")
	}
	if serialGreater(^uint32(0), 1) {
		t.Fatal("max uint32 should be older than wrapped serial 1")
	}
}

func TestApplyDeltaToFileCreatesInitialSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "domains.txt")
	delta := &Delta{FromSerial: 0, ToSerial: 1, Timestamp: time.Now(), Full: true, Added: []string{"B.example.", "a.example"}}
	if err := ApplyDeltaToFile(path, delta); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a.example\nb.example\n" {
		t.Fatalf("snapshot = %q", got)
	}
}

func TestApplyDeltaFailurePreservesRawFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "domains.txt")
	original := []byte("keep.example\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	tooLong := strings.Repeat("x", 1<<20+1)
	if err := ApplyDeltaToFile(path, &Delta{Added: []string{tooLong}}); err == nil {
		t.Fatal("oversized domain accepted")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("raw file changed after failure: %q", got)
	}
}
