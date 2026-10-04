package main

import (
	"crypto/subtle"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cdbConfiguredToken returns the configured shared secret, trimmed.
func cdbConfiguredToken() string { return strings.TrimSpace(*flagCDBToken) }

// cdbNoTokenWarn logs the unauthenticated-mode warning only once.
var cdbNoTokenWarn sync.Once

// ─── CDB publisher rate limiting ─────────────────────────────────────────────
//
// Policy (per operator request):
//   - A request carrying the correct X-CDB-Token is a trusted edge node and is
//     never rate limited.
//   - A request WITHOUT a token (or with a wrong one) is still allowed to
//     download, so a fresh edge can bootstrap before the token is configured.
//     It is throttled hard per client IP so the endpoint cannot be used to
//     flood the master (DoS amplification via repeated large CDB downloads).
//
// Throttling is a token bucket per IP: a small burst is permitted, then the
// refill rate limits sustained abuse. Exceeding it yields 429 with Retry-After.
//
// State is in-memory only. ponytail: single-node panel; if panels ever share an
// auth domain this should move to the cluster store.

const (
	// Untrusted clients may burst this many requests...
	cdbUntrustedBurst = 3
	// ...then refill at this rate (one request per window).
	cdbUntrustedRefillEvery = 30 * time.Second
	// Hard ceiling on tracked IPs so a spoofed-source flood cannot grow the map
	// without bound. ponytail: simple eviction, not an LRU.
	cdbUntrustedMaxTracked = 4096
)

type cdbBucket struct {
	tokens   float64
	lastSeen time.Time
}

type cdbRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*cdbBucket
}

func newCDBRateLimiter() *cdbRateLimiter {
	return &cdbRateLimiter{buckets: make(map[string]*cdbBucket)}
}

// Allow reports whether an untrusted request from ip may proceed, and how long
// to wait when it may not.
func (l *cdbRateLimiter) Allow(ip string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[ip]
	if !ok {
		if len(l.buckets) >= cdbUntrustedMaxTracked {
			l.evictOldestLocked()
		}
		l.buckets[ip] = &cdbBucket{tokens: cdbUntrustedBurst - 1, lastSeen: now}
		return true, 0
	}

	// Refill based on elapsed time.
	elapsed := now.Sub(b.lastSeen)
	if elapsed > 0 {
		b.tokens += elapsed.Seconds() / cdbUntrustedRefillEvery.Seconds()
		if b.tokens > cdbUntrustedBurst {
			b.tokens = cdbUntrustedBurst
		}
		b.lastSeen = now
	}

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}

	// Time until one token is available.
	need := 1 - b.tokens
	wait := time.Duration(need * float64(cdbUntrustedRefillEvery))
	return false, wait
}

// evictOldestLocked drops the least-recently-seen bucket. Caller holds the lock.
func (l *cdbRateLimiter) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	first := true
	for k, v := range l.buckets {
		if first || v.lastSeen.Before(oldest) {
			oldestKey, oldest, first = k, v.lastSeen, false
		}
	}
	if oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}

// Forget drops an IP's bucket. Used when a request proves itself trusted so the
// IP is not penalised for earlier unauthenticated traffic.
func (l *cdbRateLimiter) Forget(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, ip)
}

var cdbLimiter = newCDBRateLimiter()

// cdbAuthorize enforces the CDB publisher access policy.
//
// Returns true when the request may proceed. Trusted requests (valid token, or
// no token configured at all) are never throttled. Untrusted requests are
// permitted but rate limited per IP; when the limit is hit this writes 429 and
// returns false.
func cdbAuthorize(w http.ResponseWriter, r *http.Request) bool {
	token := cdbConfiguredToken()
	got := strings.TrimSpace(r.Header.Get("X-CDB-Token"))

	// Trusted path: exact token match (constant time), or no token configured.
	if token == "" {
		cdbNoTokenWarn.Do(func() {
			log.Printf("[cdb-publisher] WARNING: no --cdb-token/PANEL_CDB_TOKEN configured; /cdb/* is unauthenticated (rate limited per IP)")
		})
		return true
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1 {
		// A valid token proves this is a real edge; clear any throttle history.
		cdbLimiter.Forget(requestIP(r))
		return true
	}

	// Untrusted: allow, but throttle hard so the endpoint cannot be flooded.
	ip := requestIP(r)
	ok, wait := cdbLimiter.Allow(ip, time.Now())
	if !ok {
		secs := int(wait.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		w.Header().Set("X-CDB-Rate-Limited", "1")
		jsonErr(w, http.StatusTooManyRequests,
			"rate limited: provide X-CDB-Token for unrestricted CDB access")
		return false
	}

	// Allowed unauthenticated. Advertise that the response was throttled so
	// operators can see misconfigured edges in logs and headers.
	w.Header().Set("X-CDB-Untrusted", "1")
	return true
}

// resetCDBLimiter clears all throttle state. Used by tests so they do not leak
// buckets between cases.
func resetCDBLimiter() {
	cdbLimiter.mu.Lock()
	defer cdbLimiter.mu.Unlock()
	cdbLimiter.buckets = make(map[string]*cdbBucket)
}
