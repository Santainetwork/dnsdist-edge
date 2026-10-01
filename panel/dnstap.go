package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	dt "github.com/dnstap/golang-dnstap"
	framestream "github.com/farsightsec/golang-framestream"
	"github.com/miekg/dns"
	"google.golang.org/protobuf/proto"
)

// OtherBucket absorbs unique query keys beyond the configured in-memory capacity
// to prevent OOM when under random subdomain attacks or high domain cardinality.
const OtherBucket = "_other_"

// BlockedItem represents aggregated query statistics for a specific day, domain, and record type.
// Client IP information is strictly discarded prior to aggregation for subscriber privacy.
type BlockedItem struct {
	Day   string `json:"day"`
	QName string `json:"qname"`
	QType string `json:"qtype"`
	Count int64  `json:"count"`
}

type dnstapKey struct {
	day   string
	qname string
	qtype string
}

// DnstapAggregator aggregates DNS queries into (day, qname, qtype) counters.
// It is thread-safe and enforces a bounded memory footprint.
type DnstapAggregator struct {
	mu  sync.Mutex
	cap int
	loc *time.Location
	m   map[dnstapKey]int64
}

// NewDnstapAggregator creates an aggregator with a maximum capacity of distinct keys per window.
func NewDnstapAggregator(capacity int) *DnstapAggregator {
	if capacity <= 0 {
		capacity = 50000
	}
	return &DnstapAggregator{
		cap: capacity,
		loc: time.Local,
		m:   make(map[dnstapKey]int64),
	}
}

// Add records one query observation into the aggregator.
func (a *DnstapAggregator) Add(t time.Time, qname, qtype string) {
	loc := a.loc
	if loc == nil {
		loc = time.Local
	}
	day := t.In(loc).Format("2006-01-02")
	k := dnstapKey{day: day, qname: qname, qtype: qtype}

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.m[k]; !exists && a.cap > 0 && len(a.m) >= a.cap {
		k.qname = OtherBucket
		k.qtype = OtherBucket
	}
	a.m[k]++
}

// Observe parses a decoded DNS message and records its question.
func (a *DnstapAggregator) Observe(t time.Time, m *dns.Msg) {
	if m == nil || len(m.Question) == 0 {
		return
	}
	q := m.Question[0]
	qname := strings.TrimSuffix(strings.ToLower(q.Name), ".")
	qtype := dns.TypeToString[q.Qtype]
	if qtype == "" {
		qtype = "UNKNOWN"
	}
	a.Add(t, qname, qtype)
}

// Take drains and returns the accumulated statistics and resets the aggregation window.
func (a *DnstapAggregator) Take() []BlockedItem {
	a.mu.Lock()
	defer a.mu.Unlock()

	items := make([]BlockedItem, 0, len(a.m))
	for k, count := range a.m {
		items = append(items, BlockedItem{
			Day:   k.day,
			QName: k.qname,
			QType: k.qtype,
			Count: count,
		})
	}
	a.m = make(map[dnstapKey]int64)
	return items
}

// TopBlocked returns the top N blocked domains across all types and days in the current window.
func (a *DnstapAggregator) TopBlocked(n int) []BlockedItem {
	a.mu.Lock()
	defer a.mu.Unlock()

	totals := make(map[string]int64)
	for k, count := range a.m {
		totals[k.qname] += count
	}

	type domainCount struct {
		name  string
		count int64
	}
	sorted := make([]domainCount, 0, len(totals))
	for name, count := range totals {
		sorted = append(sorted, domainCount{name: name, count: count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	if n > len(sorted) || n <= 0 {
		n = len(sorted)
	}

	res := make([]BlockedItem, n)
	for i := 0; i < n; i++ {
		res[i] = BlockedItem{
			QName: sorted[i].name,
			Count: sorted[i].count,
		}
	}
	return res
}

// DnstapServer listens for Framestream dnstap connections from dnsdist.
type DnstapServer struct {
	listener net.Listener
	agg      *DnstapAggregator
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewDnstapServer initializes a dnstap Framestream server on the given network and address.
func NewDnstapServer(network, addr string, agg *DnstapAggregator) (*DnstapServer, error) {
	l, err := net.Listen(network, addr)
	if err != nil {
		return nil, err
	}
	return &DnstapServer{
		listener: l,
		agg:      agg,
		stopCh:   make(chan struct{}),
	}, nil
}

// Addr returns the bound listener network address.
func (s *DnstapServer) Addr() net.Addr {
	return s.listener.Addr()
}

// Start begins accepting Framestream connections in the background.
func (s *DnstapServer) Start() {
	s.wg.Add(1)
	go s.serve()
}

// Stop shuts down the listener and waits for active reader goroutines to finish.
func (s *DnstapServer) Stop() error {
	close(s.stopCh)
	err := s.listener.Close()
	s.wg.Wait()
	return err
}

func (s *DnstapServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stopCh:
				return
			default:
				log.Printf("[dnstap] accept error: %v", err)
				continue
			}
		}

		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			defer c.Close()
			_ = s.handleConn(c)
		}(conn)
	}
}

func (s *DnstapServer) handleConn(c net.Conn) error {
	r, err := dt.NewReader(c, &dt.ReaderOptions{Bidirectional: true, Timeout: 10 * time.Second})
	if err != nil {
		return err
	}

	buf := make([]byte, dt.MaxPayloadSize)
	var dtMsg dt.Dnstap
	var dnsMsg dns.Msg

	for {
		select {
		case <-s.stopCh:
			return nil
		default:
		}

		n, err := r.ReadFrame(buf)
		if err != nil {
			if errors.Is(err, framestream.ErrDataFrameTooLarge) {
				continue
			}
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}

		if proto.Unmarshal(buf[:n], &dtMsg) != nil || dtMsg.Message == nil {
			continue
		}

		t, wire, ok := extractPayload(dtMsg.Message)
		if !ok || len(wire) == 0 {
			continue
		}

		// Privacy boundary: we intentionally DO NOT inspect or retain dtMsg.Message.QueryAddress,
		// QueryPort, ResponseAddress, or ResponsePort. Client IP is dropped here.

		if dnsMsg.Unpack(wire) != nil || len(dnsMsg.Question) == 0 {
			continue
		}

		s.agg.Observe(t, &dnsMsg)
	}
}

func extractPayload(m *dt.Message) (time.Time, []byte, bool) {
	if m == nil {
		return time.Time{}, nil, false
	}
	switch m.GetType() {
	case dt.Message_CLIENT_QUERY:
		return parseTimestamp(m.QueryTimeSec, m.QueryTimeNsec), m.QueryMessage, m.QueryMessage != nil
	case dt.Message_CLIENT_RESPONSE:
		return parseTimestamp(m.ResponseTimeSec, m.ResponseTimeNsec), m.ResponseMessage, m.ResponseMessage != nil
	default:
		return time.Time{}, nil, false
	}
}

func parseTimestamp(sec *uint64, nsec *uint32) time.Time {
	if sec == nil {
		return time.Now()
	}
	var ns int64
	if nsec != nil {
		ns = int64(*nsec)
	}
	return time.Unix(int64(*sec), ns)
}

// handleDnstapTop returns the top blocked domains gathered via dnstap (Protected via JWT)
func handleDnstapTop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}
	top := dnstapAgg.TopBlocked(limit)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":          true,
		"top_blocked": top,
	})
}
