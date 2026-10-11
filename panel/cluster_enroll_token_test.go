package main

import (
	"testing"
	"time"
)

// Kontrak token enroll: sekali pakai, dan kedaluwarsa ditolak.
func TestEnrollTokenOneTimeAndExpiry(t *testing.T) {
	cs := &ClusterStore{tokens: make(map[string]time.Time)}
	cs.tokens["live"] = time.Now().Add(time.Hour)
	cs.tokens["dead"] = time.Now().Add(-time.Second)

	if !cs.ValidateAndConsumeToken("live") {
		t.Fatal("token segar harus diterima sekali")
	}
	if cs.ValidateAndConsumeToken("live") {
		t.Fatal("token yang sudah dipakai tidak boleh diterima lagi")
	}
	if cs.ValidateAndConsumeToken("dead") {
		t.Fatal("token kedaluwarsa tidak boleh diterima")
	}
	if cs.ValidateAndConsumeToken("unknown") {
		t.Fatal("token tak dikenal tidak boleh diterima")
	}
}
