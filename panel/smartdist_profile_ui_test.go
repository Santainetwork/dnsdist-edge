package main

import (
	"strings"
	"testing"
)

// UI buat/hapus profil SmartDist (PRD F2.1): panel harus punya form JSON untuk
// membuat profil, tombol hapus, dan memanggil endpoint CRUD yang sudah ada.
func TestSmartDistProfileUIMarkup(t *testing.T) {
	page := string(indexHTML)
	for _, marker := range []string{
		`id="smartdist-profile-json"`,
		`id="smartdist-profile-save"`,
		"saveSmartDistProfile()",
		"onclick=\"deleteSmartDistProfile(",
		"onclick=\"saveSmartDistProfile()\"",
		"api('/api/cluster/profiles?id=' + encodeURIComponent(id), 'DELETE')",
		"/api/cluster/profiles",
		"n.profile_sync === 'synced'",
		"<th>Profil</th>",
	} {
		if !strings.Contains(page, marker) {
			t.Fatalf("panel HTML missing SmartDist profile CRUD marker %q", marker)
		}
	}
}
