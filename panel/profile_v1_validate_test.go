package main

import "testing"

// Kontrak profil v1 (docs/PROFILE-V1-DESIGN.md §2-§3). Tes merah dulu.

func TestDnsdistSectionValidAccepted(t *testing.T) {
	raw := `{"id":"cfg-a","name":"A","smartdist":{"enabled":false,"rules":[]},` +
		`"dnsdist":{"block_mode":"adguard","sinkhole_v4":[],"sinkhole_v6":[],` +
		`"acl":["127.0.0.0/8","10.0.0.0/8"],` +
		`"upstreams":[{"name":"cf-primary","address":"1.1.1.1:53","pool":"default"}],` +
		`"ecs_v4":24,"ecs_v6":56,"max_qps_per_ip":100,"tcp_max_conns":50,"tcp_max_queries":100}}`
	if _, _, err := parseSmartDistProfile([]byte(raw)); err != nil {
		t.Fatalf("profil valid ditolak: %v", err)
	}
}

func TestDnsdistSectionRejectsBadValues(t *testing.T) {
	base := func(dn string) string {
		return `{"id":"cfg-a","name":"A","smartdist":{"enabled":false,"rules":[]},"dnsdist":` + dn + `}`
	}
	cases := map[string]string{
		"block_mode tak dikenal": `{"block_mode":"hack"}`,
		"ecs_v4 > 32":            `{"ecs_v4":99}`,
		"ecs_v4 negatif":         `{"ecs_v4":-1}`,
		"ecs_v6 > 128":           `{"ecs_v6":129}`,
		"acl bukan CIDR":         `{"acl":["not-an-ip"]}`,
		"acl CIDR rusak":         `{"acl":["10.0.0.0/99"]}`,
		"sinkhole bukan IP":      `{"sinkhole_v4":["x.y"]}`,
		"sinkhole v6 di v4":      `{"sinkhole_v4":["2001:db8::1"]}`,
		"upstream address rusak": `{"upstreams":[{"name":"a","address":"1.1.1.1:99999","pool":"default"}]}`,
		"upstream name kosong":   `{"upstreams":[{"name":"","address":"1.1.1.1:53","pool":"default"}]}`,
		"upstream name injeksi":  `{"upstreams":[{"name":"a\"b","address":"1.1.1.1:53","pool":"default"}]}`,
		"qps nol":                `{"max_qps_per_ip":0}`,
		"tcp_max_conns negatif":  `{"tcp_max_conns":-5}`,
		"field tak dikenal":      `{"hack":1}`,
	}
	for name, dn := range cases {
		if _, _, err := parseSmartDistProfile([]byte(base(dn))); err == nil {
			t.Errorf("%s: harus ditolak", name)
		}
	}
}

func TestDnsdistSectionAbsentIsLegacy(t *testing.T) {
	raw := `{"id":"cfg-a","name":"A","smartdist":{"enabled":true,"rules":[]}}`
	p, _, err := parseSmartDistProfile([]byte(raw))
	if err != nil {
		t.Fatalf("profil lama ditolak: %v", err)
	}
	if p.Dnsdist != nil {
		t.Fatalf("section dnsdist harus nil bila absen")
	}
}

func TestDnsdistSectionInjectionStaysLiteral(t *testing.T) {
	// Nilai string yang mengandung kutip, backslash, dan panggilan Lua tidak boleh
	// lolos menjadi kode. Ditolak oleh validasi, atau di-escape di generator.
	raw := `{"id":"cfg-a","name":"A","smartdist":{"enabled":false,"rules":[]},` +
		`"dnsdist":{"upstreams":[{"name":"a","address":"1.1.1.1:53\"); os.execute('x') --","pool":"default"}]}}`
	if _, _, err := parseSmartDistProfile([]byte(raw)); err == nil {
		t.Fatal("address berisi payload Lua harus ditolak")
	}
}
