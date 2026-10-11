package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Kontrak: rotasi X-Forwarded-For / X-Real-IP tidak boleh melewati lockout login.
// Limiter memakai RemoteAddr (requestIP), bukan header yang bisa diforge.
func TestLoginLockoutNotBypassedByForwardedHeaders(t *testing.T) {
	dir := t.TempDir()
	resetAuthEnv(t, dir)
	for i := 0; i < maxLoginFailures; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/login", strBody(`{"password":"wrong"}`))
		req.RemoteAddr = "203.0.113.90:1234"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		req.Header.Set("X-Real-IP", fmt.Sprintf("192.0.2.%d", i+1))
		handleLogin(httptest.NewRecorder(), req)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/login", strBody(`{"password":"trust-ng-admin"}`))
	req.RemoteAddr = "203.0.113.90:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.250")
	w := httptest.NewRecorder()
	handleLogin(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("login dengan XFF baru = %d, want 429 (lockout dilewati)", w.Code)
	}
}
