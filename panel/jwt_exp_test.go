package main

import (
	"testing"
	"time"
)

// Regression for the JWT exp handling (audit finding F3). Before the fix a
// token whose "exp" was missing or not numeric verified successfully and was
// treated as never-expiring. exp must now be present, numeric, and in the future.
//
// Self-contained on purpose: depends only on jwtSign/jwtVerify/jwtSecret, not
// on helpers in other test files, so it builds from a clean HEAD.
func TestJWTExpRequired(t *testing.T) {
	old := jwtSecret
	jwtSecret = []byte("0123456789abcdef0123456789abcdef")
	t.Cleanup(func() { jwtSecret = old })

	reject := map[string]map[string]any{
		"missing exp": {"sub": "admin"},
		"exp null":    {"sub": "admin", "exp": nil},
		"exp string":  {"sub": "admin", "exp": "2001-01-01"},
		"exp expired": {"sub": "admin", "exp": time.Now().Add(-time.Hour).Unix()},
	}
	for name, claims := range reject {
		if _, err := jwtVerify(jwtSign(claims)); err == nil {
			t.Errorf("%s: token accepted, want rejection", name)
		}
	}

	valid := jwtSign(map[string]any{"sub": "admin", "exp": time.Now().Add(time.Hour).Unix()})
	if _, err := jwtVerify(valid); err != nil {
		t.Errorf("valid token rejected: %v", err)
	}
}
