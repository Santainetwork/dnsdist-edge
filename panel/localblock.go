package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Daftar blokir lokal per node. Dibaca dnsdist lewat smartdns_domain_set
// (SuffixMatchNode), BUKAN digabung ke blacklist.db, karena update-blacklist.sh
// mengganti blacklist.db via symlink dan akan menimpa perubahan lokal.

var localBlockLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

var errLocalBlockInvalid = errors.New("domain tidak valid")

// normalizeLocalBlockDomain: lowercase, trim, buang titik akhir, validasi label.
func normalizeLocalBlockDomain(in string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(in))
	d = strings.TrimSuffix(d, ".")
	if d == "" || len(d) > 253 {
		return "", fmt.Errorf("%w: %q", errLocalBlockInvalid, in)
	}
	for _, label := range strings.Split(d, ".") {
		if !localBlockLabelRe.MatchString(label) {
			return "", fmt.Errorf("%w: %q", errLocalBlockInvalid, in)
		}
	}
	return d, nil
}

type localBlockStore struct {
	mu      sync.Mutex
	path    string
	domains map[string]struct{}
}

func newLocalBlockStore(path string) *localBlockStore {
	s := &localBlockStore{path: path, domains: map[string]struct{}{}}
	if b, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if d := strings.TrimSpace(line); d != "" {
				s.domains[d] = struct{}{}
			}
		}
	}
	return s
}

// Add menambah banyak domain sekaligus. Semua divalidasi dulu; satu saja
// tidak valid -> tidak ada yang berubah.
func (s *localBlockStore) Add(in []string) error {
	norm := make([]string, 0, len(in))
	for _, raw := range in {
		d, err := normalizeLocalBlockDomain(raw)
		if err != nil {
			return err
		}
		norm = append(norm, d)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]struct{}, len(s.domains)+len(norm))
	for d := range s.domains {
		next[d] = struct{}{}
	}
	for _, d := range norm {
		next[d] = struct{}{}
	}
	if err := s.writeLocked(next); err != nil {
		return err
	}
	s.domains = next
	return nil
}

func (s *localBlockStore) Remove(domain string) error {
	d, err := normalizeLocalBlockDomain(domain)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]struct{}, len(s.domains))
	for k := range s.domains {
		if k != d {
			next[k] = struct{}{}
		}
	}
	if err := s.writeLocked(next); err != nil {
		return err
	}
	s.domains = next
	return nil
}

func (s *localBlockStore) List() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.domains))
	for d := range s.domains {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func (s *localBlockStore) writeLocked(set map[string]struct{}) error {
	keys := make([]string, 0, len(set))
	for d := range set {
		keys = append(keys, d)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, d := range keys {
		b.WriteString(d)
		b.WriteString("\n")
	}
	return atomicWriteString(s.path, b.String(), 0o644)
}
