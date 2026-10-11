package main

import "testing"

// Validasi upstream di panel harus menerima semua format yang setup-edge.sh
// terima (IPv4, IPv4:port, [IPv6]:port, IPv6 bare). Jika tidak, fix F6.2 akan
// menjadi regresi untuk konfigurasi yang sah.
func TestValidIPAcceptsScriptFormats(t *testing.T) {
	ok := []string{"1.1.1.1", "8.8.4.4:53", "[2606:4700:4700::1111]:53", "2606:4700:4700::1111", "2001:db8::1"}
	for _, s := range ok {
		if !validIP(s) {
			t.Errorf("validIP(%q) = false, setup-edge.sh menerimanya", s)
		}
	}
	bad := []string{"1.1.1.1;id", "$(id)", "8.8.8.8`id`", "example.com", "",
		"1.1.1.1:99999", "1.1.1.1:0", "1.1.1.1: 53", "1.1.1.1:+53", "1.1.1.1:", "1.1.1.1:-1", "[::1]:99999"}
	for _, s := range bad {
		if validIP(s) {
			t.Errorf("validIP(%q) = true, harus false", s)
		}
	}
}
