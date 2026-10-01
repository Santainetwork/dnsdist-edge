package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	dt "github.com/dnstap/golang-dnstap"
	"github.com/miekg/dns"
)

func makeDnstapFrame(t *testing.T, typ dt.Message_Type, name string, qtype uint16, at time.Time, clientIP string) *dt.Dnstap {
	m := new(dns.Msg)
	m.SetQuestion(name, qtype)
	wire, err := m.Pack()
	if err != nil {
		t.Fatal(err)
	}
	sec, nsec := uint64(at.Unix()), uint32(0)
	msg := &dt.Message{
		Type: &typ,
	}

	// Attach a sensitive client IP on the wire to test privacy dropping
	if clientIP != "" {
		ip := net.ParseIP(clientIP).To4()
		port := uint32(54321)
		msg.QueryAddress = ip
		msg.QueryPort = &port
	}

	if typ == dt.Message_CLIENT_QUERY {
		msg.QueryMessage, msg.QueryTimeSec, msg.QueryTimeNsec = wire, &sec, &nsec
	} else {
		msg.ResponseMessage, msg.ResponseTimeSec, msg.ResponseTimeNsec = wire, &sec, &nsec
	}
	kind := dt.Dnstap_MESSAGE
	return &dt.Dnstap{
		Type:     &kind,
		Identity: []byte("edge-01"),
		Message:  msg,
	}
}

func TestDnstapAggregatorAddAndTake(t *testing.T) {
	agg := NewDnstapAggregator(100)
	day := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	agg.loc = time.UTC

	// Add queries
	agg.Add(day, "judi-online.com", "A")
	agg.Add(day, "judi-online.com", "A")
	agg.Add(day, "judi-online.com", "AAAA")
	agg.Add(day, "porn.example", "HTTPS")

	items := agg.Take()
	if len(items) != 3 {
		t.Fatalf("expected 3 distinct items, got %d", len(items))
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].QName+items[i].QType < items[j].QName+items[j].QType
	})

	if items[0].QName != "judi-online.com" || items[0].QType != "A" || items[0].Count != 2 {
		t.Fatalf("unexpected item 0: %+v", items[0])
	}
	if items[1].QName != "judi-online.com" || items[1].QType != "AAAA" || items[1].Count != 1 {
		t.Fatalf("unexpected item 1: %+v", items[1])
	}

	// Verify Take cleared the aggregator
	if len(agg.Take()) != 0 {
		t.Fatal("Take must start a fresh window")
	}
}

func TestDnstapAggregatorBoundedCapacity(t *testing.T) {
	// Set cap to 2 items
	agg := NewDnstapAggregator(2)
	day := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	agg.loc = time.UTC

	agg.Add(day, "domain1.com", "A")
	agg.Add(day, "domain2.com", "A")
	// Overflow items
	agg.Add(day, "overflow-1.com", "A")
	agg.Add(day, "overflow-2.com", "A")

	items := agg.Take()
	if len(items) != 3 { // domain1, domain2, and _other_
		t.Fatalf("expected 3 items (2 normal + 1 _other_), got %d: %+v", len(items), items)
	}

	var otherCount int64
	for _, it := range items {
		if it.QName == OtherBucket {
			otherCount = it.Count
		}
	}
	if otherCount != 2 {
		t.Fatalf("expected 2 overflow items in %s bucket, got %d", OtherBucket, otherCount)
	}
}

func TestDnstapAggregatorTopBlocked(t *testing.T) {
	agg := NewDnstapAggregator(100)
	day := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	agg.loc = time.UTC

	for i := 0; i < 10; i++ {
		agg.Add(day, "top1.com", "A")
	}
	for i := 0; i < 5; i++ {
		agg.Add(day, "top2.com", "A")
	}
	for i := 0; i < 2; i++ {
		agg.Add(day, "top3.com", "A")
	}

	top := agg.TopBlocked(2)
	if len(top) != 2 {
		t.Fatalf("expected 2 top items, got %d", len(top))
	}
	if top[0].QName != "top1.com" || top[0].Count != 10 {
		t.Fatalf("unexpected rank 1: %+v", top[0])
	}
	if top[1].QName != "top2.com" || top[1].Count != 5 {
		t.Fatalf("unexpected rank 2: %+v", top[1])
	}
}

func TestDnstapServerStreamingAndPrivacy(t *testing.T) {
	agg := NewDnstapAggregator(50)
	agg.loc = time.UTC

	server, err := NewDnstapServer("tcp", "127.0.0.1:0", agg)
	if err != nil {
		t.Fatalf("failed to start dnstap server: %v", err)
	}
	server.Start()
	defer server.Stop()

	// Connect client to dnstap framestream server
	conn, err := net.Dial("tcp", server.Addr().String())
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}

	w, err := dt.NewWriter(conn, &dt.WriterOptions{Bidirectional: true, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("framestream writer: %v", err)
	}
	enc := dt.NewEncoder(w)

	day := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	// Send 3 frames with subscriber IP addresses
	f1 := makeDnstapFrame(t, dt.Message_CLIENT_QUERY, "test-stream-1.example.org.", dns.TypeA, day, "192.168.100.50")
	f2 := makeDnstapFrame(t, dt.Message_CLIENT_QUERY, "test-stream-1.example.org.", dns.TypeA, day, "192.168.100.51")
	f3 := makeDnstapFrame(t, dt.Message_CLIENT_RESPONSE, "test-stream-2.example.org.", dns.TypeHTTPS, day, "10.0.0.1")

	for _, f := range []*dt.Dnstap{f1, f2, f3} {
		if err := enc.Encode(f); err != nil {
			t.Fatalf("encode frame error: %v", err)
		}
	}

	// Close framestream cleanly
	if err := w.Close(); err != nil {
		t.Fatalf("close writer error: %v", err)
	}
	_ = conn.Close()

	// Wait briefly for server reader to ingest
	deadline := time.Now().Add(2 * time.Second)
	var items []BlockedItem
	for time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		items = agg.Take()
		if len(items) == 2 {
			break
		}
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 aggregated items, got %d: %+v", len(items), items)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].QName < items[j].QName
	})

	if items[0].QName != "test-stream-1.example.org" || items[0].Count != 2 {
		t.Fatalf("item 0 mismatch: %+v", items[0])
	}
	if items[1].QName != "test-stream-2.example.org" || items[1].Count != 1 {
		t.Fatalf("item 1 mismatch: %+v", items[1])
	}
}

func TestHandleDnstapTopEndpoint(t *testing.T) {
	// Seed global dnstapAgg
	now := time.Now()
	dnstapAgg.Add(now, "judibola.com", "A")
	dnstapAgg.Add(now, "judibola.com", "A")
	dnstapAgg.Add(now, "slotgacor.example", "A")

	// 1. Method not allowed
	reqPost := httptest.NewRequest(http.MethodPost, "/api/dnstap/top", nil)
	recPost := httptest.NewRecorder()
	handleDnstapTop(recPost, reqPost)
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", recPost.Code)
	}

	// 2. GET top blocked
	req := httptest.NewRequest(http.MethodGet, "/api/dnstap/top?limit=5", nil)
	rec := httptest.NewRecorder()
	handleDnstapTop(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var res struct {
		OK         bool          `json:"ok"`
		TopBlocked []BlockedItem `json:"top_blocked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.OK || len(res.TopBlocked) == 0 {
		t.Fatalf("expected non-empty top blocked, got %+v", res)
	}
	if res.TopBlocked[0].QName != "judibola.com" || res.TopBlocked[0].Count < 2 {
		t.Fatalf("top rank mismatch: %+v", res.TopBlocked[0])
	}
}
