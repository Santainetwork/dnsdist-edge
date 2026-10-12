package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Agent: section dnsdist ikut diterapkan dari body yang sama. Hash naik hanya
// bila SmartDist DAN dnsdist sukses (F5.2: synced = semua penugasan diterapkan).

func agentWithDnsdistSandbox(t *testing.T, body string, hits *atomic.Int32) *EdgeClusterAgent {
	t.Helper()
	a := newSmartDistAgent(t, body, hits)
	dir := t.TempDir()
	prevPath := dnsdistProfilePath
	dnsdistProfilePath = filepath.Join(dir, "dnsdist-profile.lua")
	t.Cleanup(func() { dnsdistProfilePath = prevPath })
	prevCheck := checkDnsdistConfigFn
	checkDnsdistConfigFn = func(string) error { return nil }
	t.Cleanup(func() { checkDnsdistConfigFn = prevCheck })
	prevRestart := restartDnsdistFn
	restartDnsdistFn = func() error { return nil }
	t.Cleanup(func() { restartDnsdistFn = prevRestart })
	return a
}

func TestAgentAppliesDnsdistSectionFromSameBody(t *testing.T) {
	body := `{"id":"cfg-a","name":"A","smartdist":{"enabled":false,"rules":[]},` +
		`"dnsdist":{"max_qps_per_ip":321}}`
	var hits atomic.Int32
	a := agentWithDnsdistSandbox(t, body, &hits)
	want := smartDistHash(body)

	if err := a.syncSmartDistProfile("cfg-a", want); err != nil {
		t.Fatalf("sync: %v", err)
	}
	b, err := os.ReadFile(dnsdistProfilePath)
	if err != nil {
		t.Fatalf("dnsdist-profile.lua tidak ditulis: %v", err)
	}
	if !strings.Contains(string(b), "PROFILE_MAX_QPS_PER_IP = 321") {
		t.Errorf("isi dnsdist-profile.lua salah:\n%s", b)
	}
	if a.state.ProfileHash != want {
		t.Errorf("hash = %q, want %q", a.state.ProfileHash, want)
	}
}

func TestAgentDnsdistFailKeepsHashSoRetry(t *testing.T) {
	body := `{"id":"cfg-a","name":"A","smartdist":{"enabled":false,"rules":[]},` +
		`"dnsdist":{"max_qps_per_ip":321}}`
	var hits atomic.Int32
	a := agentWithDnsdistSandbox(t, body, &hits)
	checkDnsdistConfigFn = func(string) error { return errors.New("config rusak") }
	want := smartDistHash(body)

	if err := a.syncSmartDistProfile("cfg-a", want); err == nil {
		t.Fatal("apply dnsdist gagal harus error")
	}
	if a.state.ProfileHash == want {
		t.Fatal("hash naik padahal dnsdist gagal: heartbeat berikutnya tidak akan retry")
	}
}

func TestAgentLegacyProfileNoDnsdistSectionStillWorks(t *testing.T) {
	// Profil lama tanpa section dnsdist: SmartDist tetap berhasil, dnsdist-profile.lua
	// ditulis dengan PROFILE_ENABLED = false (baseline dipakai).
	var hits atomic.Int32
	a := agentWithDnsdistSandbox(t, smartDistTestProfile, &hits)
	want := smartDistHash(smartDistTestProfile)

	if err := a.syncSmartDistProfile("cfg-a", want); err != nil {
		t.Fatalf("profil lama gagal: %v", err)
	}
	b, _ := os.ReadFile(dnsdistProfilePath)
	if !strings.Contains(string(b), "PROFILE_ENABLED = false") {
		t.Errorf("profil lama harus PROFILE_ENABLED=false:\n%s", b)
	}
}
