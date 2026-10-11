package main

import (
	"net/http/httptest"
	"testing"
)

// Kontrak: X-Forwarded-For / X-Real-IP hanya dipercaya bila request datang dari
// proxy tepercaya (daftar CIDR). Default kosong = tidak ada header yang dipercaya.
func TestClientIPTrustsForwardedOnlyFromTrustedProxy(t *testing.T) {
	prev := trustedProxyNets
	t.Cleanup(func() { trustedProxyNets = prev })

	// Default: tanpa proxy tepercaya, XFF diabaikan.
	trustedProxyNets = nil
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "198.51.100.7:1234"
	r.Header.Set("X-Forwarded-For", "10.9.9.9")
	if got := clientIP(r); got != "198.51.100.7" {
		t.Fatalf("tanpa trusted proxy XFF harus diabaikan, got %q", got)
	}

	// Dengan proxy 198.51.100.0/24 tepercaya, XFF dipakai.
	if err := setTrustedProxies("198.51.100.0/24"); err != nil {
		t.Fatal(err)
	}
	if got := clientIP(r); got != "10.9.9.9" {
		t.Fatalf("dari trusted proxy XFF harus dipakai, got %q", got)
	}

	// Request dari luar daftar tetap mengabaikan XFF (anti-spoof).
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "203.0.113.5:1234"
	r2.Header.Set("X-Forwarded-For", "10.9.9.9")
	if got := clientIP(r2); got != "203.0.113.5" {
		t.Fatalf("XFF dari non-proxy harus diabaikan, got %q", got)
	}
}

func TestSetTrustedProxiesRejectsGarbage(t *testing.T) {
	prev := trustedProxyNets
	t.Cleanup(func() { trustedProxyNets = prev })
	if err := setTrustedProxies("not-a-cidr"); err == nil {
		t.Fatal("CIDR tidak valid harus ditolak")
	}
}
