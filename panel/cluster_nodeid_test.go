package main

import (
	"regexp"
	"testing"
	"time"
)

// Kontrak: ID node dibangkitkan server dan hanya berisi [a-z0-9-].
// UI menyisipkan ID ke atribut onclick JS (deleteClusterNode('...')), jadi
// ID yang memuat kutip akan menjadi injeksi JS. Kontrak ini menjaga asumsi itu.
func TestRegisterNodeIDIsJSSafe(t *testing.T) {
	safe := regexp.MustCompile(`^node-[a-f0-9]+$`)
	cs := &ClusterStore{
		tokens: map[string]time.Time{},
		nodes:  map[string]*NodeRecord{},
	}
	for i := 0; i < 20; i++ {
		tok := "t" + randHex(4)
		cs.tokens[tok] = time.Now().Add(time.Hour)
		rec, err := cs.RegisterNode(RegisterRequest{EnrollToken: tok, Name: "x'); alert(1);//"}, "10.0.0.1")
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		if !safe.MatchString(rec.ID) {
			t.Fatalf("ID tidak aman untuk JS: %q", rec.ID)
		}
	}
}
