package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoginRateLimiterLocksAfterFailures(t *testing.T) {
	rl := newLoginRateLimiter(5, 15*time.Minute)
	ip := "203.0.113.10"

	for i := 0; i < 5; i++ {
		if !rl.Allow(ip, time.Now()) {
			t.Fatalf("request %d should be allowed before lockout", i+1)
		}
		rl.RecordFailure(ip, time.Now())
	}
	if rl.Allow(ip, time.Now()) {
		t.Fatal("6th attempt must be locked out")
	}

	// After lockout expiry, access is restored.
	later := time.Now().Add(15*time.Minute + time.Second)
	if !rl.Allow(ip, later) {
		t.Fatal("lockout must expire after its window")
	}
}

func TestLoginRateLimiterIndependentPerIP(t *testing.T) {
	rl := newLoginRateLimiter(3, 10*time.Minute)
	a, b := "198.51.100.1", "198.51.100.2"
	for i := 0; i < 3; i++ {
		rl.Allow(a, time.Now())
		rl.RecordFailure(a, time.Now())
	}
	if rl.Allow(a, time.Now()) {
		t.Fatal("ip a must be locked")
	}
	if !rl.Allow(b, time.Now()) {
		t.Fatal("ip b must not be affected by ip a failures")
	}
}

func TestLoginRateLimiterSuccessResetsFailures(t *testing.T) {
	rl := newLoginRateLimiter(3, 10*time.Minute)
	ip := "192.0.2.7"
	for i := 0; i < 2; i++ {
		rl.Allow(ip, time.Now())
		rl.RecordFailure(ip, time.Now())
	}
	rl.ResetFailures(ip)
	for i := 0; i < 3; i++ {
		if !rl.Allow(ip, time.Now()) {
			t.Fatalf("attempt %d after reset should be allowed", i+1)
		}
		rl.RecordFailure(ip, time.Now())
	}
	if rl.Allow(ip, time.Now()) {
		t.Fatal("must lock after three failures post-reset")
	}
}

func TestLoginRateLimiterUnlockedIPHasNoRemainingLock(t *testing.T) {
	rl := newLoginRateLimiter(2, time.Minute)
	if got := rl.lockRemaining("192.0.2.9", time.Now()); got != 0 {
		t.Fatalf("unknown ip lock remaining = %v, want 0", got)
	}
	rl.Allow("192.0.2.9", time.Now())
	rl.RecordFailure("192.0.2.9", time.Now())
	rl.Allow("192.0.2.9", time.Now())
	rl.RecordFailure("192.0.2.9", time.Now())
	if got := rl.lockRemaining("192.0.2.9", time.Now()); got <= 0 {
		t.Fatalf("locked ip must report positive remaining time, got %v", got)
	}
}

func TestHandleLoginLocksOutRepeatedFailures(t *testing.T) {
	dir := t.TempDir()
	resetAuthEnv(t, dir)
	rr := func() *httptest.ResponseRecorder { return httptest.NewRecorder() }
	for i := 0; i < maxLoginFailures; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/login", strBody(`{"password":"wrong"}`))
		req.RemoteAddr = "203.0.113.77:1234"
		handleLogin(rr(), req)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/login", strBody(`{"password":"trust-ng-admin"}`))
	req.RemoteAddr = "203.0.113.77:1234"
	w := httptest.NewRecorder()
	handleLogin(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("locked-out correct-password login = %d, want 429", w.Code)
	}
}
