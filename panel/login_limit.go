package main

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// loginRateLimiter tracks failed login attempts per IP and locks out abusive
// sources. State is in-memory only: ponytail: single-node panel; move to the
// cluster store if panels ever share an auth domain.

const (
	maxLoginFailures = 5
	loginLockWindow  = 15 * time.Minute
)

type loginAttempt struct {
	failures  int
	lockedTil time.Time
}

type loginRateLimiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	entries map[string]*loginAttempt
}

func newLoginRateLimiter(max int, window time.Duration) *loginRateLimiter {
	return &loginRateLimiter{max: max, window: window, entries: map[string]*loginAttempt{}}
}

var loginLimiter = newLoginRateLimiter(maxLoginFailures, loginLockWindow)

func (l *loginRateLimiter) Allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[ip]
	if !ok {
		return true
	}
	if now.Before(e.lockedTil) {
		return false
	}
	if !e.lockedTil.IsZero() && !now.Before(e.lockedTil) {
		// Lockout expired: start fresh.
		delete(l.entries, ip)
		return true
	}
	return true
}

func (l *loginRateLimiter) RecordFailure(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[ip]
	if !ok {
		e = &loginAttempt{}
		l.entries[ip] = e
	}
	if !e.lockedTil.IsZero() && !now.Before(e.lockedTil) {
		e.failures = 0
		e.lockedTil = time.Time{}
	}
	e.failures++
	if e.failures >= l.max {
		e.lockedTil = now.Add(l.window)
	}
}

func (l *loginRateLimiter) ResetFailures(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.entries[ip]; ok && e.lockedTil.IsZero() {
		delete(l.entries, ip)
	}
}

// lockRemaining reports how long the IP stays locked (0 when unlocked).
func (l *loginRateLimiter) lockRemaining(ip string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[ip]
	if !ok {
		return 0
	}
	if remaining := e.lockedTil.Sub(now); remaining > 0 {
		return remaining
	}
	return 0
}

// requestIP extracts the client IP from RemoteAddr (X-Forwarded-For is not
// trusted for auth lockout because it is trivially spoofable on the direct
// listener; ponytail: honor proxy headers only when a trusted-proxy flag exists).
func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
