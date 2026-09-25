package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigAndValidation(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.SourceMode != "feeds" {
		t.Fatalf("default source mode = %q, want feeds", cfg.SourceMode)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	if len(cfg.TransferACL) == 0 {
		t.Fatal("default transfer ACL must not be unrestricted")
	}
}

func TestConfigValidationRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"source mode", func(c *Config) { c.SourceMode = "other" }},
		{"interval", func(c *Config) { c.CheckInterval = "0s" }},
		{"listen address", func(c *Config) { c.ListenDNS = "not-an-address" }},
		{"zone", func(c *Config) { c.Zone = "bad..zone" }},
		{"upstream address", func(c *Config) { c.UpstreamMaster = "example.com" }},
		{"transfer ACL", func(c *Config) { c.TransferACL = []string{"not-a-network"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.edit(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() succeeded, want error")
			}
		})
	}
}

func TestConfigValidationRequiresProtectedTSIGSecretFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("dGVzdA==\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.TSIGKey = "transfer.example."
	cfg.TSIGSecretFile = path
	if err := cfg.Validate(); err != nil {
		t.Fatalf("protected TSIG secret rejected: %v", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("world-readable TSIG secret accepted")
	}
}

func TestDaemonRejectsFeedsMode(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.ValidateDaemon(); err == nil {
		t.Fatal("daemon accepted feeds source mode")
	}
	cfg.SourceMode = "rpz-slave"
	cfg.UpstreamMaster = "127.0.0.1:53"
	if err := cfg.ValidateDaemon(); err != nil {
		t.Fatalf("daemon rejected rpz-slave: %v", err)
	}
}
