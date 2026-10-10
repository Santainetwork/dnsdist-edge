package main

import (
	"strings"
	"testing"
)

// UI dua jalur blokir all-in-one: kartu blokir lokal (node ini) memakai
// GET/POST/DELETE /api/localblock, dan kartu blokir central (master) memakai
// GET/POST /api/master/custom-blacklist. Kartu central hanya muncul di mode master.
func TestBlocklistUIMarkup(t *testing.T) {
	page := string(indexHTML)
	for _, marker := range []string{
		// Kartu blokir lokal per node.
		`id="localblock-card"`,
		`id="localblock-input"`,
		`id="localblock-list"`,
		`id="localblock-count"`,
		"addLocalBlock()",
		"removeLocalBlock(",
		"fetchLocalBlock()",
		"/api/localblock",
		// Kartu blokir central (master).
		`id="master-blacklist-card"`,
		`id="master-blacklist-input"`,
		`id="master-blacklist-list"`,
		`id="master-blacklist-count"`,
		"saveMasterBlacklist()",
		"removeMasterBlacklist(",
		"fetchMasterBlacklist()",
		"/api/master/custom-blacklist",
	} {
		if !strings.Contains(page, marker) {
			t.Fatalf("panel HTML missing blocklist UI marker %q", marker)
		}
	}
}

// Kartu central harus digerbangi mode master, memakai penanda cfg.is_master
// yang sudah dipakai fetchConfig() untuk menampilkan navigasi master.
func TestMasterBlacklistCardGatedByMasterMode(t *testing.T) {
	page := string(indexHTML)
	if !strings.Contains(page, "master-blacklist-card") {
		t.Fatal("panel HTML missing master-blacklist-card")
	}
	if !strings.Contains(page, "cfg.is_master") {
		t.Fatal("panel HTML missing cfg.is_master master-mode marker")
	}
}
