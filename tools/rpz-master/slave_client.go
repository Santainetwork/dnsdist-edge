package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// maxTransferRecords: batas default bila config tidak mengisi
// max_transfer_records. Nilai efektif diambil dari config.
const maxTransferRecords = 10_000_000

type SlaveClient struct {
	cfg   Config
	state *State
}

func NewSlaveClient(cfg Config, st *State) (*SlaveClient, error) {
	if st == nil {
		return nil, errors.New("state is required")
	}
	if cfg.TSIGKey != "" {
		secret, err := cfg.ReadTSIGSecret()
		if err != nil {
			return nil, err
		}
		cfg.TSIGSecret = secret
	}
	return &SlaveClient{cfg: cfg, state: st}, nil
}

func (c *SlaveClient) tsigSecret() map[string]string {
	if c.cfg.TSIGKey == "" || c.cfg.TSIGSecret == "" {
		return nil
	}
	return map[string]string{dns.Fqdn(c.cfg.TSIGKey): c.cfg.TSIGSecret}
}

func (c *SlaveClient) sign(m *dns.Msg) {
	if c.cfg.TSIGKey != "" {
		m.SetTsig(dns.Fqdn(c.cfg.TSIGKey), dns.Fqdn(c.cfg.TSIGAlgorithm), 300, time.Now().Unix())
	}
}

// QueryUpstreamSOA queries the upstream master's SOA serial.
func (c *SlaveClient) QueryUpstreamSOA() (uint32, error) {
	if c.cfg.UpstreamMaster == "" {
		return 0, errors.New("upstream master tidak dikonfigurasi")
	}
	zone := dns.Fqdn(c.cfg.Zone)
	m := new(dns.Msg)
	m.SetQuestion(zone, dns.TypeSOA)
	c.sign(m)
	client := &dns.Client{Timeout: 5 * time.Second, TsigSecret: c.tsigSecret()}
	r, _, err := client.Exchange(m, c.cfg.UpstreamMaster)
	if err != nil {
		return 0, fmt.Errorf("gagal query SOA ke %s: %w", c.cfg.UpstreamMaster, err)
	}
	if r.Truncated || r.Rcode != dns.RcodeSuccess {
		return 0, fmt.Errorf("respons SOA tidak dapat digunakan: truncated=%t rcode=%s", r.Truncated, dns.RcodeToString[r.Rcode])
	}
	for _, answer := range r.Answer {
		soa, ok := answer.(*dns.SOA)
		if ok && strings.EqualFold(soa.Hdr.Name, zone) {
			return soa.Serial, nil
		}
	}
	return 0, errors.New("tidak ada record SOA zone dalam respons")
}

func (c *SlaveClient) transferRequest() *dns.Msg {
	zone := dns.Fqdn(c.cfg.Zone)
	m := new(dns.Msg)
	if c.state.Serial == 0 {
		m.SetAxfr(zone)
	} else {
		m.SetIxfr(zone, c.state.Serial, "ns1."+zone, "hostmaster."+zone)
	}
	c.sign(m)
	return m
}

// SyncIXFR performs AXFR for empty state and IXFR otherwise. Upstreams may
// answer IXFR with a full AXFR, which is detected from the second record and
// streamed to disk instead of being buffered in memory.
func (c *SlaveClient) SyncIXFR(upstreamSerial uint32) (*Delta, error) {
	request := c.transferRequest()
	// miekg/dns terminates IXFR early using a plain uint32 comparison across
	// serial wrap. Request AXFR for that rare transition instead.
	if c.state.Serial != 0 && upstreamSerial < c.state.Serial && serialGreater(upstreamSerial, c.state.Serial) {
		request = new(dns.Msg)
		request.SetAxfr(dns.Fqdn(c.cfg.Zone))
		c.sign(request)
	}
	transfer := &dns.Transfer{
		DialTimeout:  5 * time.Second,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Second,
		TsigSecret:   c.tsigSecret(),
	}
	envelopes, err := transfer.In(request, c.cfg.UpstreamMaster)
	if err != nil {
		return nil, fmt.Errorf("gagal inisiasi transfer: %w", err)
	}

	limit := c.cfg.MaxTransferRecords
	if limit <= 0 {
		limit = maxTransferRecords
	}
	zone := strings.ToLower(dns.Fqdn(c.cfg.Zone))

	// Buffer hanya cukup untuk memutuskan AXFR vs IXFR (2 record pertama).
	var head []dns.RR
	for len(head) < 2 {
		env, ok := <-envelopes
		if !ok {
			break
		}
		if env.Error != nil {
			return nil, fmt.Errorf("error stream transfer: %w", env.Error)
		}
		head = append(head, env.RR...)
	}
	if len(head) == 0 {
		return nil, errors.New("transfer kosong")
	}
	first, ok := head[0].(*dns.SOA)
	if !ok || !strings.EqualFold(first.Hdr.Name, zone) {
		return nil, errors.New("transfer tidak diawali SOA zone")
	}
	if len(head) == 1 {
		if c.state.Serial == first.Serial {
			return nil, nil
		}
		return nil, errors.New("transfer terpotong setelah SOA awal")
	}

	var delta *Delta
	if isIXFRResponse(head) {
		// Respons IXFR: akumulasi delta (biasanya kecil; tetap dibatasi).
		records := head
		for env := range envelopes {
			if env.Error != nil {
				return nil, fmt.Errorf("error stream transfer: %w", env.Error)
			}
			if len(records)+len(env.RR) > limit {
				return nil, fmt.Errorf("transfer melebihi batas %d record", limit)
			}
			records = append(records, env.RR...)
		}
		delta, err = parseTransferRecords(records, c.cfg.Zone, c.state.Serial)
		if err != nil {
			return nil, err
		}
	} else {
		// Respons AXFR (atau IXFR yang dijawab AXFR penuh): stream ke disk.
		delta, err = c.streamAXFRFromEnvelopes(head, envelopes, limit)
		if err != nil {
			return nil, err
		}
	}
	if delta == nil {
		return nil, nil
	}
	if upstreamSerial != 0 && delta.ToSerial != upstreamSerial {
		if delta.StagedRaw != "" {
			_ = os.Remove(delta.StagedRaw)
		}
		return nil, fmt.Errorf("serial transfer %d tidak cocok dengan SOA %d", delta.ToSerial, upstreamSerial)
	}
	return delta, nil
}

// isIXFRResponse distinguishes an IXFR response from a full AXFR one using the
// first two records: an IXFR starts with new-SOA followed by old-SOA, where the
// serials differ. An empty zone transferred by AXFR is [SOA, SOA] with the SAME
// serial (opening SOA repeated as closing SOA) and must be treated as AXFR.
func isIXFRResponse(head []dns.RR) bool {
	if len(head) < 2 {
		return false
	}
	first, ok := head[0].(*dns.SOA)
	if !ok {
		return false
	}
	second, ok := head[1].(*dns.SOA)
	if !ok {
		return false
	}
	return second.Serial != first.Serial
}

// streamAXFRFromEnvelopes writes the owner domains of a full AXFR response to a
// staging file next to raw_domain_file, one normalized domain per line. Memory
// stays bounded by a single envelope chunk, so multi-million-record zones do
// not require RAM proportional to the zone size. head[1:] is the portion
// already buffered for AXFR/IXFR detection; head[0] is the validated opening SOA.
func (c *SlaveClient) streamAXFRFromEnvelopes(head []dns.RR, envelopes <-chan *dns.Envelope, limit int) (*Delta, error) {
	zone := strings.ToLower(dns.Fqdn(c.cfg.Zone))
	firstSerial := head[0].(*dns.SOA).Serial
	rawDir := filepath.Dir(c.cfg.RawDomainFile)
	if err := os.MkdirAll(rawDir, 0o700); err != nil {
		return nil, err
	}
	staged, err := os.CreateTemp(rawDir, ".axfr-stage-*")
	if err != nil {
		return nil, err
	}
	stagedPath := staged.Name()
	w := bufio.NewWriterSize(staged, 1<<20)
	fail := func(err error) (*Delta, error) {
		_ = staged.Close()
		_ = os.Remove(stagedPath)
		return nil, err
	}

	var gotLast bool
	total := 0
	process := func(rr dns.RR) error {
		total++
		if total > limit {
			return fmt.Errorf("transfer melebihi batas %d record", limit)
		}
		owner := strings.ToLower(dns.Fqdn(rr.Header().Name))
		if soa, ok := rr.(*dns.SOA); ok {
			if !strings.EqualFold(soa.Hdr.Name, zone) {
				return errors.New("SOA di luar zone")
			}
			if soa.Serial != firstSerial {
				return errors.New("AXFR tidak ditutup SOA yang cocok")
			}
			gotLast = true
			return nil
		}
		if gotLast {
			return errors.New("record setelah SOA penutup AXFR")
		}
		if !dns.IsSubDomain(zone, owner) {
			return fmt.Errorf("owner record di luar zone RPZ: %s", owner)
		}
		if owner == zone {
			return nil
		}
		switch rr.Header().Rrtype {
		case dns.TypeCNAME, dns.TypeA, dns.TypeAAAA:
		default:
			return nil
		}
		domain := strings.TrimSuffix(strings.TrimSuffix(owner, zone), ".")
		clean, err := normalizePolicyDomain(domain)
		if err != nil {
			return fmt.Errorf("owner RPZ %q: %w", owner, err)
		}
		_, err = w.WriteString(clean + "\n")
		return err
	}

	for _, rr := range head[1:] {
		if err := process(rr); err != nil {
			return fail(err)
		}
	}
	for env := range envelopes {
		if env.Error != nil {
			return fail(fmt.Errorf("error stream transfer: %w", env.Error))
		}
		for _, rr := range env.RR {
			if err := process(rr); err != nil {
				return fail(err)
			}
		}
	}
	if !gotLast {
		return fail(errors.New("AXFR tidak lengkap: SOA penutup tidak ditemukan"))
	}
	if err := w.Flush(); err != nil {
		return fail(err)
	}
	if err := staged.Sync(); err != nil {
		return fail(err)
	}
	if err := staged.Close(); err != nil {
		_ = os.Remove(stagedPath)
		return nil, err
	}
	return &Delta{
		FromSerial: c.state.Serial,
		ToSerial:   firstSerial,
		Timestamp:  time.Now(),
		Full:       true,
		StagedRaw:  stagedPath,
	}, nil
}

func parseTransferRecords(records []dns.RR, zone string, currentSerial uint32) (*Delta, error) {
	zone = strings.ToLower(dns.Fqdn(zone))
	if len(records) == 0 {
		return nil, errors.New("transfer kosong")
	}
	first, ok := records[0].(*dns.SOA)
	if !ok || !strings.EqualFold(first.Hdr.Name, zone) {
		return nil, errors.New("transfer tidak diawali SOA zone")
	}
	if len(records) == 1 {
		if currentSerial == first.Serial {
			return nil, nil
		}
		return nil, errors.New("transfer terpotong setelah SOA awal")
	}

	// An IXFR response starts new-SOA, old-SOA. A full AXFR response starts
	// new-SOA, zone records and ends with the same new-SOA.
	secondSOA, isIXFR := records[1].(*dns.SOA)
	if !isIXFR {
		last, ok := records[len(records)-1].(*dns.SOA)
		if !ok || last.Serial != first.Serial || !strings.EqualFold(last.Hdr.Name, zone) {
			return nil, errors.New("AXFR tidak ditutup SOA yang cocok")
		}
		added, err := policyOwners(records[1:len(records)-1], zone)
		if err != nil {
			return nil, err
		}
		return &Delta{FromSerial: currentSerial, ToSerial: first.Serial, Timestamp: time.Now(), Full: true, Added: added}, nil
	}
	if !strings.EqualFold(secondSOA.Hdr.Name, zone) || secondSOA.Serial != currentSerial {
		return nil, fmt.Errorf("IXFR dimulai dari serial %d, state lokal %d", secondSOA.Serial, currentSerial)
	}
	if !serialGreater(first.Serial, currentSerial) {
		return nil, fmt.Errorf("serial target IXFR %d tidak lebih baru dari state lokal %d", first.Serial, currentSerial)
	}
	changes := make(map[string]bool)
	expectedFrom := currentSerial
	i := 1
	for {
		from, ok := records[i].(*dns.SOA)
		if !ok || !strings.EqualFold(from.Hdr.Name, zone) || from.Serial != expectedFrom {
			return nil, fmt.Errorf("urutan from-SOA IXFR tidak valid pada serial %d", expectedFrom)
		}
		i++
		deleteStart := i
		for i < len(records) {
			if _, ok := records[i].(*dns.SOA); ok {
				break
			}
			i++
		}
		if i >= len(records) {
			return nil, errors.New("IXFR bagian delete terpotong")
		}
		to, ok := records[i].(*dns.SOA)
		if !ok || !strings.EqualFold(to.Hdr.Name, zone) || !serialGreater(to.Serial, from.Serial) {
			return nil, errors.New("to-SOA IXFR tidak valid")
		}
		deleted, err := policyOwners(records[deleteStart:i], zone)
		if err != nil {
			return nil, err
		}
		for _, domain := range deleted {
			changes[domain] = false
		}

		i++
		addStart := i
		for i < len(records) {
			if _, ok := records[i].(*dns.SOA); ok {
				break
			}
			i++
		}
		if i >= len(records) {
			return nil, errors.New("IXFR bagian add terpotong")
		}
		added, err := policyOwners(records[addStart:i], zone)
		if err != nil {
			return nil, err
		}
		for _, domain := range added {
			changes[domain] = true
		}

		next, ok := records[i].(*dns.SOA)
		if !ok || !strings.EqualFold(next.Hdr.Name, zone) || next.Serial != to.Serial {
			return nil, errors.New("SOA penutup/lanjutan IXFR tidak valid")
		}
		if to.Serial == first.Serial {
			i++
			if i != len(records) {
				return nil, errors.New("record setelah SOA penutup IXFR")
			}
			break
		}
		if !serialGreater(first.Serial, to.Serial) {
			return nil, fmt.Errorf("serial antara IXFR %d melewati target %d", to.Serial, first.Serial)
		}
		expectedFrom = to.Serial
	}
	deleted, added := make([]string, 0, len(changes)), make([]string, 0, len(changes))
	for domain, present := range changes {
		if present {
			added = append(added, domain)
		} else {
			deleted = append(deleted, domain)
		}
	}
	sort.Strings(deleted)
	sort.Strings(added)
	return &Delta{FromSerial: currentSerial, ToSerial: first.Serial, Timestamp: time.Now(), Added: added, Deleted: deleted}, nil
}

// serialGreater implements RFC 1982 comparison for 32-bit serials.
func serialGreater(a, b uint32) bool {
	diff := a - b
	return diff != 0 && diff < 1<<31
}

func policyOwners(records []dns.RR, zone string) ([]string, error) {
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		owner := strings.ToLower(dns.Fqdn(record.Header().Name))
		if !dns.IsSubDomain(zone, owner) {
			return nil, fmt.Errorf("owner record di luar zone RPZ: %s", owner)
		}
		if owner == zone {
			continue
		}
		switch record.Header().Rrtype {
		case dns.TypeCNAME, dns.TypeA, dns.TypeAAAA:
		default:
			continue
		}
		domain := strings.TrimSuffix(strings.TrimSuffix(owner, zone), ".")
		clean, err := normalizePolicyDomain(domain)
		if err != nil {
			return nil, fmt.Errorf("owner RPZ %q: %w", owner, err)
		}
		seen[clean] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for domain := range seen {
		out = append(out, domain)
	}
	sort.Strings(out)
	return out, nil
}

func normalizePolicyDomain(value string) (string, error) {
	value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if value == "" || len(value) > 253 {
		return "", errors.New("domain kosong atau terlalu panjang")
	}
	fqdn := dns.Fqdn(value)
	if _, ok := dns.IsDomainName(fqdn); !ok {
		return "", errors.New("domain tidak valid")
	}
	return value, nil
}

// ApplyDeltaToFile writes a complete replacement before atomically publishing it.
// Memory usage is bounded by the incoming delta, not the existing domain count.
func ApplyDeltaToFile(rawFile string, delta *Delta) error {
	if delta == nil {
		return errors.New("delta is required")
	}
	add := make(map[string]struct{}, len(delta.Added))
	for _, value := range delta.Added {
		clean, err := normalizePolicyDomain(value)
		if err != nil {
			return err
		}
		add[clean] = struct{}{}
	}
	deleted := make(map[string]struct{}, len(delta.Deleted))
	for _, value := range delta.Deleted {
		clean, err := normalizePolicyDomain(value)
		if err != nil {
			return err
		}
		deleted[clean] = struct{}{}
	}

	if err := os.MkdirAll(filepath.Dir(rawFile), 0o700); err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(rawFile), ".domains-*")
	if err != nil {
		return err
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	writer := bufio.NewWriter(out)
	writeDomain := func(domain string) error {
		_, err := writer.WriteString(domain + "\n")
		return err
	}

	if !delta.Full {
		input, err := os.Open(rawFile)
		if err != nil {
			_ = out.Close()
			return err
		}
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			clean, err := normalizePolicyDomain(line)
			if err != nil {
				_ = input.Close()
				_ = out.Close()
				return err
			}
			if _, remove := deleted[clean]; remove {
				continue
			}
			if err := writeDomain(clean); err != nil {
				_ = input.Close()
				_ = out.Close()
				return err
			}
			delete(add, clean)
		}
		if err := scanner.Err(); err != nil {
			_ = input.Close()
			_ = out.Close()
			return err
		}
		if err := input.Close(); err != nil {
			_ = out.Close()
			return err
		}
	}

	remaining := make([]string, 0, len(add))
	for domain := range add {
		if _, remove := deleted[domain]; !remove {
			remaining = append(remaining, domain)
		}
	}
	sort.Strings(remaining)
	for _, domain := range remaining {
		if err := writeDomain(domain); err != nil {
			_ = out.Close()
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, rawFile)
}
