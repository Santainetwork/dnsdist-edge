package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Kontrak: body endpoint admin (dibungkus auth) juga dibatasi maxJSONBody.
// Tanpa cap di auth(), decoder bisa dipaksa membaca body tak terbatas.
func TestAuthWrapperCapsBody(t *testing.T) {
	tok := jwtSign(map[string]any{"sub": "admin", "exp": time.Now().Add(time.Hour).Unix()})
	cr := &countingReader{left: 64 << 20}
	req := httptest.NewRequest(http.MethodPost, "/api/settings",
		io.MultiReader(strings.NewReader(`{"block_mode":"`), cr))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	auth(handleSettings)(rr, req)
	if cr.n > maxJSONBody+1024 {
		t.Fatalf("body admin dibaca %d byte, melebihi cap %d", cr.n, maxJSONBody)
	}
	if rr.Code == http.StatusOK {
		t.Fatalf("body raksasa tidak boleh lolos")
	}
}
