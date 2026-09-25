package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"
)

type Config struct {
	SourceMode      string   `json:"source_mode" yaml:"source_mode"`
	Zone            string   `json:"zone" yaml:"zone"`
	ListenDNS       string   `json:"listen_dns" yaml:"listen_dns"`
	ListenHTTP      string   `json:"listen_http" yaml:"listen_http"`
	CNAMETarget     string   `json:"cname_target" yaml:"cname_target"`
	CDBPath         string   `json:"cdb_path" yaml:"cdb_path"`
	StatePath       string   `json:"state_path" yaml:"state_path"`
	SourceDomainURL string   `json:"source_domain_url" yaml:"source_domain_url"`
	UpstreamMaster  string   `json:"upstream_master" yaml:"upstream_master"`
	TSIGKey         string   `json:"tsig_key" yaml:"tsig_key"`
	TSIGSecret      string   `json:"tsig_secret" yaml:"tsig_secret"`
	TSIGSecretFile  string   `json:"tsig_secret_file" yaml:"tsig_secret_file"`
	TSIGAlgorithm   string   `json:"tsig_algorithm" yaml:"tsig_algorithm"`
	CheckInterval   string   `json:"check_interval" yaml:"check_interval"`
	MaxTransfers    int      `json:"max_transfers" yaml:"max_transfers"`
	RawDomainFile   string   `json:"raw_domain_file" yaml:"raw_domain_file"`
	TransferACL     []string `json:"transfer_acl" yaml:"transfer_acl"`
}

func DefaultConfig() Config {
	return Config{
		SourceMode:      "feeds",
		Zone:            "rpz.trustpositif.",
		ListenDNS:       "127.0.0.1:5353",
		ListenHTTP:      "127.0.0.1:8088",
		CNAMETarget:     "blockpage.komdigi.go.id.",
		CDBPath:         "/var/lib/dnsdist/trustpositif.cdb",
		StatePath:       "/var/lib/dnsdist/rpz-master-state.json",
		SourceDomainURL: "https://trustpositif.komdigi.go.id/assets/db/domains_isp",
		UpstreamMaster:  "",
		TSIGKey:         "",
		TSIGSecret:      "",
		TSIGAlgorithm:   "hmac-sha256.",
		CheckInterval:   "15m",
		MaxTransfers:    10,
		RawDomainFile:   "/var/lib/dnsdist/domains_isp.txt",
		TransferACL:     []string{"127.0.0.0/8", "::1/128"},
	}
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.SourceMode != "feeds" && c.SourceMode != "rpz-slave" {
		return fmt.Errorf("source_mode must be feeds or rpz-slave")
	}
	zone := dns.Fqdn(c.Zone)
	if _, ok := dns.IsDomainName(zone); c.Zone == "" || !ok {
		return fmt.Errorf("invalid zone %q", c.Zone)
	}
	if _, ok := dns.IsDomainName(dns.Fqdn(c.CNAMETarget)); c.CNAMETarget == "" || !ok {
		return fmt.Errorf("invalid cname_target %q", c.CNAMETarget)
	}
	if err := validateAddress("listen_dns", c.ListenDNS, false); err != nil {
		return err
	}
	if err := validateAddress("listen_http", c.ListenHTTP, true); err != nil {
		return err
	}
	if err := validateAddress("upstream_master", c.UpstreamMaster, true); err != nil {
		return err
	}
	interval, err := time.ParseDuration(c.CheckInterval)
	if err != nil || interval <= 0 {
		return fmt.Errorf("invalid check_interval %q", c.CheckInterval)
	}
	if c.MaxTransfers <= 0 {
		return fmt.Errorf("max_transfers must be positive")
	}
	if c.CDBPath == "" || c.StatePath == "" || c.RawDomainFile == "" {
		return errors.New("cdb_path, state_path, and raw_domain_file are required")
	}
	if c.SourceDomainURL != "" {
		u, err := url.ParseRequestURI(c.SourceDomainURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("invalid source_domain_url %q", c.SourceDomainURL)
		}
	}
	if len(c.TransferACL) == 0 {
		return errors.New("transfer_acl must not be empty")
	}
	for _, prefix := range c.TransferACL {
		if _, _, err := net.ParseCIDR(prefix); err != nil {
			return fmt.Errorf("invalid transfer_acl entry %q: %w", prefix, err)
		}
	}
	if c.TSIGSecret != "" {
		return errors.New("tsig_secret is not supported; use tsig_secret_file")
	}
	if (c.TSIGKey == "") != (c.TSIGSecretFile == "") {
		return errors.New("tsig_key and tsig_secret_file must be configured together")
	}
	if c.TSIGKey != "" {
		if _, ok := dns.IsDomainName(dns.Fqdn(c.TSIGKey)); !ok {
			return fmt.Errorf("invalid tsig_key %q", c.TSIGKey)
		}
		if _, err := c.ReadTSIGSecret(); err != nil {
			return err
		}
	}
	return nil
}

func (c Config) ValidateDaemon() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.SourceMode != "rpz-slave" {
		return fmt.Errorf("rpz-master daemon requires source_mode rpz-slave")
	}
	if c.UpstreamMaster == "" {
		return errors.New("upstream_master is required in rpz-slave mode")
	}
	return nil
}

func (c Config) ReadTSIGSecret() (string, error) {
	info, err := os.Stat(c.TSIGSecretFile)
	if err != nil {
		return "", fmt.Errorf("stat tsig_secret_file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("tsig_secret_file must be regular and inaccessible to group and others")
	}
	data, err := os.ReadFile(c.TSIGSecretFile)
	if err != nil {
		return "", fmt.Errorf("read tsig_secret_file: %w", err)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", errors.New("tsig_secret_file is empty")
	}
	if _, err := base64.StdEncoding.DecodeString(secret); err != nil {
		return "", fmt.Errorf("tsig_secret_file is not base64: %w", err)
	}
	return secret, nil
}

func validateAddress(name, address string, allowEmpty bool) error {
	if address == "" && allowEmpty {
		return nil
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid %s %q: %w", name, address, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid %s port %q", name, portText)
	}
	if host != "" && net.ParseIP(host) == nil {
		if _, ok := dns.IsDomainName(dns.Fqdn(host)); !ok {
			return fmt.Errorf("invalid %s host %q", name, host)
		}
	}
	return nil
}

type Delta struct {
	FromSerial uint32    `json:"from_serial"`
	ToSerial   uint32    `json:"to_serial"`
	Timestamp  time.Time `json:"timestamp"`
	Full       bool      `json:"full,omitempty"`
	Added      []string  `json:"added"`
	Deleted    []string  `json:"deleted"`
}

type State struct {
	Serial       uint32    `json:"serial"`
	LastUpdate   time.Time `json:"last_update"`
	TotalDomains int       `json:"total_domains"`
	CDBSHA256    string    `json:"cdb_sha256"`
	Deltas       []Delta   `json:"deltas,omitempty"`
}

func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{Serial: 0, TotalDomains: 0}, nil
		}
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	tmp := tmpFile.Name()
	defer os.Remove(tmp)
	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
