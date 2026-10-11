package main

import (
	"regexp"
	"strings"
	"testing"
)

// Kontrak UI: tombol tidak lagi menyisipkan data ke string JS di onclick.
// Data dibawa lewat data-arg (escape HTML) dan dipanggil oleh satu delegasi
// klik dengan allowlist fungsi. Mencegah kelas injeksi JS seperti di task-012.
func TestUIButtonsNoInlineDataInOnclick(t *testing.T) {
	page := string(indexHTML)
	inline := regexp.MustCompile(`onclick="[A-Za-z_]+\('\$\{`)
	if m := inline.FindAllString(page, -1); len(m) > 0 {
		t.Fatalf("masih ada %d onclick dengan interpolasi data: %v", len(m), m)
	}
	for _, marker := range []string{"data-act=", "data-arg=", "ACTION_ALLOW"} {
		if !strings.Contains(page, marker) {
			t.Fatalf("delegasi UI tidak lengkap: marker %q hilang", marker)
		}
	}
}
