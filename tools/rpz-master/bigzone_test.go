package main

import (
	"net"
	"testing"
)

// Batas transfer masuk harus bisa dikonfigurasi; default harus di atas 10 juta
// agar zona RPZ nasional tidak tertolak.
func TestConfigMaxTransferRecordsDefaultAndOverride(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxTransferRecords <= 10_000_000 {
		t.Fatalf("default max_transfer_records = %d, want > 10jt", cfg.MaxTransferRecords)
	}
	cfg.MaxTransferRecords = 12_345_678
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid override rejected: %v", err)
	}
	cfg.MaxTransferRecords = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("zero max_transfer_records accepted")
	}
	cfg.MaxTransferRecords = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("negative max_transfer_records accepted")
	}
}

// Allowlist slave: beberapa CIDR harus valid dan dihormati.
func TestTransferACLMultipleCIDRs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TransferACL = []string{"192.0.2.0/24", "2001:db8::/32"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("multi-CIDR ACL rejected: %v", err)
	}
	srv := NewRPZServer(cfg, &State{})
	if !srv.transferAllowed(net.ParseIP("192.0.2.9")) {
		t.Fatal("IPv4 rekanan ditolak")
	}
	if !srv.transferAllowed(net.ParseIP("2001:db8::1")) {
		t.Fatal("IPv6 rekanan ditolak")
	}
	if srv.transferAllowed(net.ParseIP("198.51.100.9")) {
		t.Fatal("IP di luar allowlist diterima")
	}
}
