package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// countingReader menghasilkan n byte spasi lalu EOF, dan menghitung yang dibaca.
type countingReader struct{ left, n int }

func (c *countingReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, io.EOF
	}
	k := min(len(p), c.left)
	for i := 0; i < k; i++ {
		p[i] = ' '
	}
	c.left -= k
	c.n += k
	return k, nil
}

// Kontrak: body login tidak boleh dibaca melebihi maxJSONBody (anti-DoS memori).
func TestLoginBodyCapped(t *testing.T) {
	cr := &countingReader{left: 64 << 20}
	req := httptest.NewRequest(http.MethodPost, "/api/login",
		io.MultiReader(strings.NewReader(`{"password":"x"`), cr))
	rr := httptest.NewRecorder()
	handleLogin(rr, req)
	if cr.n > maxJSONBody+1024 {
		t.Fatalf("body dibaca %d byte, melebihi cap %d", cr.n, maxJSONBody)
	}
	if rr.Code == http.StatusOK {
		t.Fatalf("body raksasa tidak boleh lolos")
	}
}
