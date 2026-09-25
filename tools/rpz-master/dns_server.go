package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/miekg/dns"
)

type RPZServer struct {
	cfg           Config
	state         *State
	stateMu       *sync.RWMutex
	publicationMu *sync.RWMutex
	semaphore     chan struct{}
	transferACL   []*net.IPNet
}

func NewRPZServer(cfg Config, st *State) *RPZServer {
	return NewRPZServerWithMutex(cfg, st, nil, nil)
}

func NewRPZServerWithMutex(cfg Config, st *State, stateMu, publicationMu *sync.RWMutex) *RPZServer {
	maxTransfers := cfg.MaxTransfers
	if maxTransfers <= 0 {
		maxTransfers = 10
	}
	acl := cfg.TransferACL
	if len(acl) == 0 {
		acl = []string{"127.0.0.0/8", "::1/128"}
	}
	transferACL := make([]*net.IPNet, 0, len(acl))
	for _, cidr := range acl {
		if _, network, err := net.ParseCIDR(cidr); err == nil {
			transferACL = append(transferACL, network)
		}
	}
	if stateMu == nil {
		stateMu = new(sync.RWMutex)
	}
	if publicationMu == nil {
		publicationMu = new(sync.RWMutex)
	}
	return &RPZServer{
		cfg:           cfg,
		state:         st,
		stateMu:       stateMu,
		publicationMu: publicationMu,
		semaphore:     make(chan struct{}, maxTransfers),
		transferACL:   transferACL,
	}
}

func (s *RPZServer) transferAllowed(ip net.IP) bool {
	for _, network := range s.transferACL {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *RPZServer) transferAuthorized(w dns.ResponseWriter, r *dns.Msg) bool {
	var ip net.IP
	switch addr := w.RemoteAddr().(type) {
	case *net.TCPAddr:
		ip = addr.IP
	case *net.UDPAddr:
		ip = addr.IP
	default:
		host, _, err := net.SplitHostPort(w.RemoteAddr().String())
		if err == nil {
			ip = net.ParseIP(host)
		}
	}
	if !s.transferAllowed(ip) {
		return false
	}
	if s.cfg.TSIGKey == "" {
		return true
	}
	tsig := r.IsTsig()
	return tsig != nil &&
		strings.EqualFold(tsig.Hdr.Name, dns.Fqdn(s.cfg.TSIGKey)) &&
		strings.EqualFold(tsig.Algorithm, dns.Fqdn(s.cfg.TSIGAlgorithm)) &&
		w.TsigStatus() == nil
}

func refuseTransfer(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetRcode(r, dns.RcodeRefused)
	_ = w.WriteMsg(m)
}

func (s *RPZServer) getSOA(zone string) *dns.SOA {
	s.stateMu.RLock()
	serial := s.state.Serial
	s.stateMu.RUnlock()

	return &dns.SOA{
		Hdr:     dns.RR_Header{Name: zone, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 300},
		Ns:      "ns1." + zone,
		Mbox:    "hostmaster." + zone,
		Serial:  serial,
		Refresh: 3600,
		Retry:   600,
		Expire:  86400,
		Minttl:  300,
	}
}

func (s *RPZServer) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	if len(r.Question) == 0 {
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeFormatError)
		_ = w.WriteMsg(m)
		return
	}

	q := r.Question[0]
	zone := strings.ToLower(q.Name)
	cfgZone := strings.ToLower(s.cfg.Zone)
	if !strings.HasSuffix(cfgZone, ".") {
		cfgZone += "."
	}

	// Validate zone matching
	if zone != cfgZone {
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeRefused)
		_ = w.WriteMsg(m)
		return
	}

	switch q.Qtype {
	case dns.TypeSOA:
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		m.Answer = append(m.Answer, s.getSOA(cfgZone))
		_ = w.WriteMsg(m)

	case dns.TypeAXFR:
		if !s.transferAuthorized(w, r) {
			refuseTransfer(w, r)
			return
		}
		s.handleAXFR(w, r, cfgZone)

	case dns.TypeIXFR:
		if !s.transferAuthorized(w, r) {
			refuseTransfer(w, r)
			return
		}
		s.handleIXFR(w, r, cfgZone)

	default:
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeNotImplemented)
		_ = w.WriteMsg(m)
	}
}

func (s *RPZServer) handleAXFR(w dns.ResponseWriter, r *dns.Msg, zone string) {
	// Acquire transfer concurrency slot
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	default:
		log.Printf("[warn] Max concurrent transfers (%d) reached. Refusing AXFR to %s", s.cfg.MaxTransfers, w.RemoteAddr())
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeServerFailure)
		_ = w.WriteMsg(m)
		return
	}

	remoteAddr := w.RemoteAddr().String()
	log.Printf("[axfr] Starting streaming AXFR for %s to %s", zone, remoteAddr)

	s.publicationMu.RLock()
	s.stateMu.RLock()
	stateSnapshot := *s.state
	s.stateMu.RUnlock()
	pinnedSOA := &dns.SOA{
		Hdr:     dns.RR_Header{Name: zone, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 300},
		Ns:      "ns1." + zone,
		Mbox:    "hostmaster." + zone,
		Serial:  stateSnapshot.Serial,
		Refresh: 3600,
		Retry:   600,
		Expire:  86400,
		Minttl:  300,
	}

	rawFile := s.cfg.RawDomainFile
	f, err := os.Open(rawFile)
	s.publicationMu.RUnlock()
	if err != nil {
		log.Printf("[error] Failed to open raw domains for AXFR: %v", err)
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeServerFailure)
		_ = w.WriteMsg(m)
		return
	}
	defer f.Close()

	ch := make(chan *dns.Envelope, 50)
	producerErr := make(chan error, 1)
	tr := new(dns.Transfer)

	go func() {
		defer close(ch)
		defer close(producerErr)
		soa := pinnedSOA

		// 1. Initial SOA
		ch <- &dns.Envelope{RR: []dns.RR{soa}}

		scanner := bufio.NewScanner(f)
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		const chunkSize = 200
		chunk := make([]dns.RR, 0, chunkSize)
		cnameTarget := s.cfg.CNAMETarget
		if !strings.HasSuffix(cnameTarget, ".") {
			cnameTarget += "."
		}

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.ToLower(line)
			line = strings.TrimSuffix(line, ".")

			rrName := fmt.Sprintf("%s.%s", line, zone)
			rr := &dns.CNAME{
				Hdr:    dns.RR_Header{Name: rrName, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 300},
				Target: cnameTarget,
			}
			chunk = append(chunk, rr)

			if len(chunk) >= chunkSize {
				ch <- &dns.Envelope{RR: chunk}
				chunk = make([]dns.RR, 0, chunkSize)
			}
		}

		if len(chunk) > 0 {
			ch <- &dns.Envelope{RR: chunk}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("[axfr] Failed to scan raw domains for %s: %v", remoteAddr, err)
			producerErr <- err
			return
		}

		// 3. Final SOA
		ch <- &dns.Envelope{RR: []dns.RR{soa}}
	}()

	if err := tr.Out(w, r, ch); err != nil {
		log.Printf("[axfr] Transfer out error to %s: %v", remoteAddr, err)
	} else if err := <-producerErr; err != nil {
		log.Printf("[axfr] Transfer incomplete to %s: %v", remoteAddr, err)
	} else {
		log.Printf("[axfr] Completed streaming AXFR to %s", remoteAddr)
	}
}

func (s *RPZServer) handleIXFR(w dns.ResponseWriter, r *dns.Msg, zone string) {
	// Extract client's SOA serial from authority section if present
	var clientSerial uint32
	for _, ns := range r.Ns {
		if soa, ok := ns.(*dns.SOA); ok {
			clientSerial = soa.Serial
			break
		}
	}

	s.stateMu.RLock()
	currentSerial := s.state.Serial
	var matchedDelta *Delta
	for i := len(s.state.Deltas) - 1; i >= 0; i-- {
		d := s.state.Deltas[i]
		if d.FromSerial == clientSerial && d.ToSerial == currentSerial {
			matchedDelta = &d
			break
		}
	}
	s.stateMu.RUnlock()

	// RFC 1995: no transfer when the server serial is not newer.
	if clientSerial != 0 && !serialGreater(currentSerial, clientSerial) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		m.Answer = append(m.Answer, s.getSOA(zone))
		_ = w.WriteMsg(m)
		return
	}

	// If we don't have the delta, fallback to full AXFR
	if matchedDelta == nil {
		log.Printf("[ixfr] Client serial %d not in delta cache (current: %d). Falling back to AXFR", clientSerial, currentSerial)
		s.handleAXFR(w, r, zone)
		return
	}

	// Concurrency slot
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	default:
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeServerFailure)
		_ = w.WriteMsg(m)
		return
	}

	log.Printf("[ixfr] Streaming IXFR delta (%d -> %d) to %s", clientSerial, currentSerial, w.RemoteAddr())

	ch := make(chan *dns.Envelope, 20)
	tr := new(dns.Transfer)

	go func() {
		defer close(ch)
		newSOA := s.getSOA(zone)
		newSOA.Serial = matchedDelta.ToSerial
		oldSOA := &dns.SOA{
			Hdr:     dns.RR_Header{Name: zone, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 300},
			Ns:      "ns1." + zone,
			Mbox:    "hostmaster." + zone,
			Serial:  clientSerial,
			Refresh: 3600,
			Retry:   600,
			Expire:  86400,
			Minttl:  300,
		}

		cnameTarget := s.cfg.CNAMETarget
		if !strings.HasSuffix(cnameTarget, ".") {
			cnameTarget += "."
		}

		// Sequence: newSOA -> oldSOA -> deletes -> newSOA -> adds -> newSOA
		ch <- &dns.Envelope{RR: []dns.RR{newSOA, oldSOA}}

		// Deleted records
		for _, dom := range matchedDelta.Deleted {
			rrName := fmt.Sprintf("%s.%s", strings.TrimSuffix(dom, "."), zone)
			rr := &dns.CNAME{
				Hdr:    dns.RR_Header{Name: rrName, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 300},
				Target: cnameTarget,
			}
			ch <- &dns.Envelope{RR: []dns.RR{rr}}
		}

		// Middle newSOA
		ch <- &dns.Envelope{RR: []dns.RR{newSOA}}

		// Added records
		for _, dom := range matchedDelta.Added {
			rrName := fmt.Sprintf("%s.%s", strings.TrimSuffix(dom, "."), zone)
			rr := &dns.CNAME{
				Hdr:    dns.RR_Header{Name: rrName, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 300},
				Target: cnameTarget,
			}
			ch <- &dns.Envelope{RR: []dns.RR{rr}}
		}

		// Final newSOA
		ch <- &dns.Envelope{RR: []dns.RR{newSOA}}
	}()

	if err := tr.Out(w, r, ch); err != nil {
		log.Printf("[ixfr] Out error: %v", err)
	}
}
