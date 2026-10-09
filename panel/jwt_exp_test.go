package main

import (
	"testing"
	"time"
)

// Regression for the JWT exp handling (audit finding F3). Before the fix a
// token whose "exp" was missing or not numeric verified successfully and was
// treated as never-expiring. exp must now be present, numeric, and in the future.
func TestJWTExpRequired(t *testing.T) {
	setJWTSecret(t, []byte("0123456789abcdef0123456789abcdef"))

	reject := map[string]map[string]any{
		"missing exp":      {"sub": "admin"},
		"exp null":         {"sub": "admin", "exp": nil},
		"exp string":       {"sub": "admin", "exp": "2001-01-01"},
		"exp expired":      {"sub": "admin", "exp": time.Now().Add(-time.Hour).Unix()},
	}
	for name, claims := range reject {
		tok := mustSignJWT(t, claims)
		if _, err := jwtVerify(tok); err == nil {
			t.Errorf("%s: token accepted, want rejection", name)
		}
	}

	// Valid future exp still verifies.
	tok := mustSignJWT(t, map[string]any{"sub": "admin", "exp": time.Now().Add(time.Hour).Unix()})
	if _, err := jwtVerify(tok); err != nil {
		t.Errorf("valid token rejected: %v", err)
	}
}
