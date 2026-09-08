package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRuleMatchingLogic(t *testing.T) {
	sampleJSON := `{
		"rules": [
			{
				"id": 0,
				"matches": 43,
				"rule": "(lookup key-value store based on 'qname in wire format') && (qtype==A)",
				"action": "spoof in 103.155.191.161"
			},
			{
				"id": 1,
				"matches": 4,
				"rule": "(lookup key-value store based on 'qname in wire format') && (qtype==AAAA)",
				"action": "spoof in 2406:7540:1600:0:192:168:106:81"
			},
			{
				"id": 2,
				"matches": 10,
				"rule": "lookup key-value store based on 'qname in wire format'",
				"action": "return NXDOMAIN"
			},
			{
				"id": 3,
				"matches": 500,
				"rule": "All",
				"action": "pool default"
			}
		],
		"pools": [
			{
				"name": "default",
				"cacheHits": 150,
				"cacheMisses": 50
			}
		],
		"statistics": {
			"queries": 1000,
			"queries-per-second": 25
		}
	}`

	var ov dnsdistServerOverview
	if err := json.Unmarshal([]byte(sampleJSON), &ov); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	var blocked float64
	for _, r := range ov.Rules {
		rLower := strings.ToLower(r.Rule)
		if strings.Contains(rLower, "key-value store") || strings.Contains(rLower, "kvs") {
			blocked += r.Matches
		}
	}

	expectedBlocked := float64(43 + 4 + 10) // 57
	if blocked != expectedBlocked {
		t.Errorf("expected blocked %f, got %f", expectedBlocked, blocked)
	}

	var cacheHits, cacheMisses float64
	for _, p := range ov.Pools {
		cacheHits += p.CacheHits
		cacheMisses += p.CacheMisses
	}
	totalCache := cacheHits + cacheMisses
	cacheHitPct := (cacheHits / totalCache) * 100
	if cacheHitPct != 75.0 {
		t.Errorf("expected cache hit pct 75.0, got %f", cacheHitPct)
	}
}
