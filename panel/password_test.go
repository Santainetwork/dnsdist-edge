package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePassFile(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "panel.password")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHashPasswordRoundTrips(t *testing.T) {
	h := hashPassword("s3cret-pass")
	if !strings.HasPrefix(h, "pbkdf2$") {
		t.Fatalf("hash must be pbkdf2 format, got %q", h)
	}
	if !verifyPassword(h, "s3cret-pass") {
		t.Fatal("correct password rejected")
	}
	if verifyPassword(h, "wrong-pass") {
		t.Fatal("wrong password accepted")
	}
	// Deterministic format check: 4 fields.
	parts := strings.Split(h, "$")
	if len(parts) != 4 {
		t.Fatalf("hash format must be pbkdf2$iter$salt$hash, got %d fields", len(parts))
	}
}

func TestVerifyPasswordAcceptsLegacyPlaintext(t *testing.T) {
	if !verifyPassword("legacy-plain", "legacy-plain") {
		t.Fatal("legacy plaintext must still verify for migration")
	}
	if verifyPassword("legacy-plain", "other") {
		t.Fatal("legacy plaintext mismatch must fail")
	}
}

func TestHashIsSalted(t *testing.T) {
	a := hashPassword("same-password")
	b := hashPassword("same-password")
	if a == b {
		t.Fatal("two hashes of same password must differ (salt)")
	}
	if !verifyPassword(a, "same-password") || !verifyPassword(b, "same-password") {
		t.Fatal("both salted hashes must verify")
	}
}

func TestLoginMigratesPlaintextToHash(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "panel.secret")
	path := writePassFile(t, dir, "old-plain\n")
	*flagSecret = secretPath

	if err := migratePlaintextPassword(path, "old-plain"); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	data, _ := os.ReadFile(path)
	stored := strings.TrimSpace(string(data))
	if !strings.HasPrefix(stored, "pbkdf2$") {
		t.Fatalf("password file not migrated, still: %q", stored)
	}
	if !verifyPassword(stored, "old-plain") {
		t.Fatal("migrated hash must verify original password")
	}
	// Second migration must be a no-op (already hashed).
	if err := migratePlaintextPassword(path, "old-plain"); err != nil {
		t.Fatalf("re-migration failed: %v", err)
	}
	data2, _ := os.ReadFile(path)
	if strings.TrimSpace(string(data2)) != stored {
		t.Fatal("re-migration changed the hash")
	}
}
