package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const dnsdistTestDoc = `{"id":"cfg-a","name":"A","smartdist":{"enabled":false,"rules":[]},` +
	`"dnsdist":{"max_qps_per_ip":200}}`

// Gerbang apply profil dnsdist (docs §4-§6): validasi check-config, rename
// atomik, restart hanya bila berubah, rollback .lkg bila restart gagal.

func setupDnsdistApply(t *testing.T) (dir string, restore func()) {
	t.Helper()
	dir = t.TempDir()
	prevPath := dnsdistProfilePath
	dnsdistProfilePath = filepath.Join(dir, "dnsdist-profile.lua")
	prevCheck := checkDnsdistConfigFn
	prevRestart := restartDnsdistFn
	return dir, func() {
		dnsdistProfilePath = prevPath
		checkDnsdistConfigFn = prevCheck
		restartDnsdistFn = prevRestart
	}
}

func TestDnsdistApplyWritesAndRestarts(t *testing.T) {
	_, restore := setupDnsdistApply(t)
	defer restore()
	var restarts atomic.Int32
	checkDnsdistConfigFn = func(string) error { return nil }
	restartDnsdistFn = func() error { restarts.Add(1); return nil }

	if err := applyDnsdistProfile([]byte(dnsdistTestDoc)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	b, err := os.ReadFile(dnsdistProfilePath)
	if err != nil {
		t.Fatalf("file tidak ditulis: %v", err)
	}
	if !strings.Contains(string(b), "PROFILE_MAX_QPS_PER_IP = 200") {
		t.Errorf("isi salah:\n%s", b)
	}
	if restarts.Load() != 1 {
		t.Errorf("restart = %d, want 1", restarts.Load())
	}
}

func TestDnsdistApplyCheckFailKeepsOldFile(t *testing.T) {
	_, restore := setupDnsdistApply(t)
	defer restore()
	old := "-- lama\nPROFILE_ENABLED = false\n"
	if err := os.WriteFile(dnsdistProfilePath, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	var restarts atomic.Int32
	checkDnsdistConfigFn = func(string) error { return errors.New("syntax") }
	restartDnsdistFn = func() error { restarts.Add(1); return nil }

	if err := applyDnsdistProfile([]byte(dnsdistTestDoc)); err == nil {
		t.Fatal("check-config gagal harus error")
	}
	b, _ := os.ReadFile(dnsdistProfilePath)
	if string(b) != old {
		t.Errorf("file lama tertimpa setelah check gagal:\n%s", b)
	}
	if restarts.Load() != 0 {
		t.Errorf("restart dipanggil padahal check gagal")
	}
}

func TestDnsdistApplyRestartFailRollsBack(t *testing.T) {
	_, restore := setupDnsdistApply(t)
	defer restore()
	old := "-- lama\nPROFILE_ENABLED = false\n"
	if err := os.WriteFile(dnsdistProfilePath, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	checkDnsdistConfigFn = func(string) error { return nil }
	var calls atomic.Int32
	restartDnsdistFn = func() error {
		n := calls.Add(1)
		if n == 1 {
			return errors.New("systemctl gagal")
		}
		return nil // restart kedua (setelah rollback) berhasil
	}

	if err := applyDnsdistProfile([]byte(dnsdistTestDoc)); err == nil {
		t.Fatal("restart gagal harus error")
	}
	b, _ := os.ReadFile(dnsdistProfilePath)
	if string(b) != old {
		t.Errorf("rollback gagal: file = %q, want %q", b, old)
	}
	if calls.Load() != 2 {
		t.Errorf("restart calls = %d, want 2 (gagal lalu rollback)", calls.Load())
	}
}

func TestDnsdistApplyUnchangedNoRestart(t *testing.T) {
	_, restore := setupDnsdistApply(t)
	defer restore()
	var restarts atomic.Int32
	checkDnsdistConfigFn = func(string) error { return nil }
	restartDnsdistFn = func() error { restarts.Add(1); return nil }

	if err := applyDnsdistProfile([]byte(dnsdistTestDoc)); err != nil {
		t.Fatal(err)
	}
	if err := applyDnsdistProfile([]byte(dnsdistTestDoc)); err != nil {
		t.Fatal(err)
	}
	if restarts.Load() != 1 {
		t.Errorf("profil sama dua kali: restart = %d, want 1", restarts.Load())
	}
}

func TestDnsdistApplyInvalidProfileRejectedBeforeWrite(t *testing.T) {
	_, restore := setupDnsdistApply(t)
	defer restore()
	var restarts atomic.Int32
	checkDnsdistConfigFn = func(string) error { return nil }
	restartDnsdistFn = func() error { restarts.Add(1); return nil }

	bad := `{"id":"cfg-a","name":"A","smartdist":{"rules":[]},"dnsdist":{"ecs_v4":99}}`
	if err := applyDnsdistProfile([]byte(bad)); err == nil {
		t.Fatal("profil tak valid harus ditolak")
	}
	if _, err := os.Stat(dnsdistProfilePath); err == nil {
		t.Error("profil tak valid tetap menulis file")
	}
	if restarts.Load() != 0 {
		t.Error("profil tak valid tetap restart")
	}
}
