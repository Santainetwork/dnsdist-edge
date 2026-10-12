package main

import "testing"

// Node uji di host yang sama tidak boleh menimpa /etc/dnsdist. Dua flag mengatur
// path profil dan mematikan restart. Default tetap produksi.
func TestDnsdistProfileFlagsDefaultsAreProduction(t *testing.T) {
	if got := *flagDnsdistProfilePath; got != "/etc/dnsdist/dnsdist-profile.lua" {
		t.Fatalf("default path = %q", got)
	}
	if !*flagDnsdistRestart {
		t.Fatal("default restart harus aktif")
	}
}

func TestApplyDnsdistProfileHonorsRestartFlag(t *testing.T) {
	_, restore := setupDnsdistApply(t)
	defer restore()
	prev := *flagDnsdistRestart
	*flagDnsdistRestart = false
	defer func() { *flagDnsdistRestart = prev }()
	restarted := false
	restartDnsdistFn = func() error { restarted = true; return nil }
	checkDnsdistConfigFn = func(string) error { return nil }

	if err := applyDnsdistProfile([]byte(dnsdistTestDoc)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if restarted {
		t.Fatal("restart dipanggil padahal -dnsdist-restart=false")
	}
}
