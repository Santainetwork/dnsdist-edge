package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Password hashing format: pbkdf2$<iterations>$<salt-b64>$<hash-b64>
// Legacy plaintext (no "pbkdf2$" prefix) is accepted by verifyPassword so
// existing installs migrate to a hash on first successful login.

const (
	pbkdf2Iterations = 600_000
	pbkdf2KeyLen     = 32
	pbkdf2HashPrefix = "pbkdf2$"
)

func hashPassword(password string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		// crypto/rand failing is unrecoverable; a fixed salt would be worse.
		panic(fmt.Sprintf("hashPassword: rand: %v", err))
	}
	return hashPasswordWithSalt(password, salt, pbkdf2Iterations)
}

func hashPasswordWithSalt(password string, salt []byte, iterations int) string {
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, pbkdf2KeyLen)
	if err != nil {
		panic(fmt.Sprintf("hashPassword: pbkdf2: %v", err))
	}
	return fmt.Sprintf("%s%d$%s$%s",
		pbkdf2HashPrefix, iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

// verifyPassword checks a candidate against a stored value. Stored values in
// legacy plaintext form compare directly (migration path); hashed values use
// PBKDF2 with constant-time comparison.
func verifyPassword(stored, candidate string) bool {
	if !strings.HasPrefix(stored, pbkdf2HashPrefix) {
		return subtle.ConstantTimeCompare([]byte(stored), []byte(candidate)) == 1
	}
	parts := strings.Split(stored, "$")
	if len(parts) != 4 {
		return false
	}
	var iterations int
	if _, err := fmt.Sscanf(parts[1], "%d", &iterations); err != nil || iterations <= 0 || iterations > 10_000_000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, candidate, salt, iterations, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(want, got) == 1
}

func isHashedPassword(stored string) bool {
	return strings.HasPrefix(stored, pbkdf2HashPrefix)
}

// migratePlaintextPassword upgrades a plaintext password file to a PBKDF2 hash
// after a successful login. Already-hashed files are left untouched.
func migratePlaintextPassword(path, candidate string) error {
	stored, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	current := strings.TrimSpace(string(stored))
	if current == "" || isHashedPassword(current) {
		return nil
	}
	if !verifyPassword(current, candidate) {
		return errors.New("password mismatch during migration")
	}
	return atomicWriteString(path, hashPassword(candidate)+"\n", 0o600)
}
