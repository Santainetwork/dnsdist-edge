package main

import (
	"strings"
	"testing"
)

// F5.3 UI: panel harus bisa menampilkan riwayat perubahan penugasan profil
// per node lewat endpoint ?history=<node>.
func TestSmartDistHistoryUIMarkup(t *testing.T) {
	page := string(indexHTML)
	for _, marker := range []string{
		`id="smartdist-history-panel"`,
		"showSmartDistHistory(",
		"/api/cluster/profiles?history=",
		"onclick=\"showSmartDistHistory(",
	} {
		if !strings.Contains(page, marker) {
			t.Fatalf("panel HTML missing SmartDist history marker %q", marker)
		}
	}
}
