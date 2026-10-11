package main

import "testing"

// Kontrak validTLSPath: path absolut sederhana lolos; karakter Lua/shell dan ".." ditolak.
func TestValidTLSPathContract(t *testing.T) {
	ok := []string{"/etc/ssl/certs/a.pem", "/var/lib/dnsdist/tls-key_1.key"}
	bad := []string{
		"", "relative/a.pem", "/etc/../shadow", "/a'b.pem", "/a\"b.pem",
		"/a b.pem", "/a$(id).pem", "/a`id`.pem", "/a;b.pem", "/a\nb.pem",
	}
	for _, p := range ok {
		if !validTLSPath(p) {
			t.Errorf("harus lolos: %q", p)
		}
	}
	for _, p := range bad {
		if validTLSPath(p) {
			t.Errorf("harus ditolak: %q", p)
		}
	}
}
