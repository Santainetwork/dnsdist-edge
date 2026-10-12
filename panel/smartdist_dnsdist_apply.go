package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Gerbang apply profil dnsdist (docs/PROFILE-V1-DESIGN.md §4-§6).
//   1. generate ke .new, validasi panel (dalam generator)
//   2. check-config dnsdist harus lulus; file lama tidak disentuh bila gagal
//   3. simpan salinan lama ke .lkg, rename .new -> aktif (atomik)
//   4. restart hanya bila isi berubah; gagal -> kembalikan .lkg, restart lagi,
//      dan error tetap dikembalikan (hash tidak boleh naik di pemanggil).

var dnsdistProfilePath = "/etc/dnsdist/dnsdist-profile.lua"

// dnsdistBaseConf: dnsdist.conf yang memuat profil. Check-config dijalankan
// terhadapnya agar hook dofile ikut diuji.
var dnsdistBaseConf = "/etc/dnsdist/dnsdist.conf"

// checkDnsdistConfigFn: seam agar tes tidak memanggil dnsdist sungguhan.
// Menguji profileFile (.new) dalam sandbox: conf sementara memuat baseline
// dnsdist.conf asli lalu dofile profileFile. Tanpa ini check-config menguji
// profil LAMA, bukan yang akan diterapkan.
var checkDnsdistConfigFn = func(profileFile string) error {
	dir, err := os.MkdirTemp("", "dnsdist-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	conf := filepath.Join(dir, "check.conf")
	src := fmt.Sprintf("dofile(%s)\ndofile(%s)\n", luaQuote(profileFile), luaQuote(dnsdistBaseConf))
	if err := os.WriteFile(conf, []byte(src), 0o600); err != nil {
		return err
	}
	out, err := exec.Command("dnsdist", "--check-config", "-C", conf).CombinedOutput()
	if err != nil {
		return fmt.Errorf("check-config gagal: %v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// applyDnsdistProfile menerapkan section dnsdist dari body profil. Error = file
// aktif tidak berubah (atau sudah dikembalikan dari .lkg).
func applyDnsdistProfile(body []byte) error {
	lua, err := generateDnsdistLua(body)
	if err != nil {
		return err
	}
	old, readErr := os.ReadFile(dnsdistProfilePath)
	if readErr == nil && string(old) == lua {
		return nil // isi sama: tidak tulis, tidak restart
	}

	newPath := dnsdistProfilePath + ".new"
	lkgPath := dnsdistProfilePath + ".lkg"
	if err := os.WriteFile(newPath, []byte(lua), 0o644); err != nil {
		return fmt.Errorf("tulis .new gagal: %w", err)
	}
	if err := checkDnsdistConfigFn(newPath); err != nil {
		_ = os.Remove(newPath)
		return err
	}

	if readErr == nil {
		if err := os.WriteFile(lkgPath, old, 0o644); err != nil {
			_ = os.Remove(newPath)
			return fmt.Errorf("simpan .lkg gagal: %w", err)
		}
	}
	if err := os.Rename(newPath, dnsdistProfilePath); err != nil {
		return fmt.Errorf("rename gagal: %w", err)
	}

	if err := restartDnsdistFn(); err != nil {
		// Kembalikan profil lama dan restart. Jika tidak ada .lkg (node baru),
		// hapus profil agar dnsdist.conf memakai baseline saat restart berikutnya.
		if readErr == nil {
			if rerr := os.Rename(lkgPath, dnsdistProfilePath); rerr != nil {
				return fmt.Errorf("restart gagal (%v), rollback rename gagal: %w", err, rerr)
			}
		} else {
			_ = os.Remove(dnsdistProfilePath)
		}
		if rerr := restartDnsdistFn(); rerr != nil {
			return fmt.Errorf("restart gagal (%v), restart setelah rollback juga gagal: %w", err, rerr)
		}
		return fmt.Errorf("restart gagal, profil dikembalikan: %w", err)
	}
	return nil
}
