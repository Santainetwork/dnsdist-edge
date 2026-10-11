package main

import "testing"

// Kontrak normalizeLocalBlockDomain: domain hanya label a-z0-9- (LDH).
// Karakter pemecah baris, spasi, kutip, dan tanda Lua harus ditolak agar
// satu entri tidak bisa menyuntik baris lain ke local-block.txt.
func TestLocalBlockDomainRejectsInjection(t *testing.T) {
	ok := []string{"example.com", "a-b.example.co.id", "x.y"}
	for _, d := range ok {
		if _, err := normalizeLocalBlockDomain(d); err != nil {
			t.Errorf("harus lolos: %q: %v", d, err)
		}
	}
	bad := []string{
		"evil.com\ngood.com", "evil.com\rx", "a b.com", "a'.com", "a\".com",
		"a;b.com", "a(b).com", "a$(id).com", "-bad.com", "bad-.com", "a..com", "",
	}
	for _, d := range bad {
		if got, err := normalizeLocalBlockDomain(d); err == nil {
			t.Errorf("harus ditolak: %q -> %q", d, got)
		}
	}
}
