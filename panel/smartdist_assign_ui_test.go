package main

import (
	"strings"
	"testing"
)

// UI bulk assign profil SmartDist: panel harus memuat daftar node (checkbox),
// dropdown profil, dan tombol Terapkan yang memanggil endpoint bulk.
func TestSmartDistAssignUIMarkup(t *testing.T) {
	page := string(indexHTML)
	for _, marker := range []string{
		`id="smartdist-assign-tbody"`,
		`id="smartdist-assign-profile"`,
		`id="smartdist-assign-apply"`,
		"applySmartDistAssign()",
		"/api/cluster/profiles",
		"/api/cluster/profile/assign",
		"node_ids",
		"assigned",
	} {
		if !strings.Contains(page, marker) {
			t.Fatalf("panel HTML missing SmartDist bulk assign marker %q", marker)
		}
	}
}
