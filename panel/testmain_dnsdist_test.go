package main

import (
	"os"
	"testing"
)

// TestMain: gerbang dnsdist (check-config, restart) dinonaktifkan untuk seluruh
// paket tes. Tanpa ini, jalur sync profil memanggil binary dnsdist sungguhan,
// yang tidak ada di lingkungan tes (lokal dan CI). Tes gerbang itu sendiri
// memasang seam-nya sendiri per tes, jadi tidak terpengaruh.
func TestMain(m *testing.M) {
	prevCheck := checkDnsdistConfigFn
	prevRestart := restartDnsdistFn
	checkDnsdistConfigFn = func(string) error { return nil }
	restartDnsdistFn = func() error { return nil }
	code := m.Run()
	checkDnsdistConfigFn = prevCheck
	restartDnsdistFn = prevRestart
	os.Exit(code)
}
