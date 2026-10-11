package main

import "testing"

// Kontrak VerifyNodeKey: hanya node+key benar yang lolos; fail-closed.
func TestVerifyNodeKeyContract(t *testing.T) {
	cs := &ClusterStore{nodes: map[string]*NodeRecord{
		"n1": {ID: "n1", Key: "secret-key-1"},
	}}

	if !cs.VerifyNodeKey("n1", "secret-key-1") {
		t.Fatal("key benar harus lolos")
	}
	if cs.VerifyNodeKey("n1", "secret-key-2") {
		t.Fatal("key salah harus ditolak")
	}
	if cs.VerifyNodeKey("n1", "") {
		t.Fatal("key kosong harus ditolak")
	}
	if cs.VerifyNodeKey("", "") {
		t.Fatal("node kosong harus ditolak")
	}
	if cs.VerifyNodeKey("n-unknown", "secret-key-1") {
		t.Fatal("node tak dikenal harus ditolak")
	}
	var nilStore *ClusterStore
	if nilStore.VerifyNodeKey("n1", "secret-key-1") {
		t.Fatal("store nil harus fail-closed")
	}
}