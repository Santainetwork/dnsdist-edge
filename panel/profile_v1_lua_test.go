package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func intp(v int) *int { return &v }

// Generator profil v1 -> dnsdist-profile.lua. Tes merah dulu.

func TestDnsdistLuaDisabledWhenNoSection(t *testing.T) {
	out, err := generateDnsdistLua([]byte(`{"id":"a","name":"A","smartdist":{"enabled":false,"rules":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "PROFILE_ENABLED = false") {
		t.Errorf("tanpa section harus nonaktif:\n%s", out)
	}
}

func TestDnsdistLuaEmitsOverrides(t *testing.T) {
	d := &DnsdistSettings{
		BlockMode:     "adguard",
		SinkholeV4:    []string{"10.0.0.1"},
		ACL:           []string{"10.0.0.0/8"},
		Upstreams:     []DnsdistUpstream{{Name: "cf1", Address: "1.1.1.1:53", Pool: "default"}},
		ECSv4:         intp(24),
		ECSv6:         intp(56),
		MaxQPSPerIP:   intp(100),
		TCPMaxConns:   intp(50),
		TCPMaxQueries: intp(100),
	}
	raw := mustDoc(t, d)
	out, err := generateDnsdistLua(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"PROFILE_ENABLED = true",
		`PROFILE_BLOCK_MODE = "adguard"`,
		`PROFILE_SINKHOLE_V4 = {"10.0.0.1"}`,
		`PROFILE_ACL = {"10.0.0.0/8"}`,
		`PROFILE_UPSTREAMS = {{name="cf1", address="1.1.1.1:53", pool="default"}}`,
		"PROFILE_ECS_V4 = 24",
		"PROFILE_ECS_V6 = 56",
		"PROFILE_MAX_QPS_PER_IP = 100",
		"PROFILE_TCP_MAX_CONNS = 50",
		"PROFILE_TCP_MAX_QUERIES = 100",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("hilang %q\n%s", want, out)
		}
	}
}

func TestDnsdistLuaFieldAbsentStaysNil(t *testing.T) {
	// Field kosong tidak boleh menimpa baseline: harus nil di Lua.
	out, err := generateDnsdistLua(mustDoc(t, &DnsdistSettings{ECSv4: intp(24)}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "PROFILE_BLOCK_MODE = ") {
		t.Errorf("block_mode kosong tidak boleh diemit:\n%s", out)
	}
	if !strings.Contains(out, "PROFILE_ECS_V4 = 24") {
		t.Errorf("ecs_v4 hilang:\n%s", out)
	}
}

func TestDnsdistLuaStringsEscaped(t *testing.T) {
	// Generator harus memakai luaQuote untuk setiap string. Setiap kutip di
	// dalam literal harus didahului backslash, agar tidak menutup string.
	lit := luaQuote(`a"); os.execute("x") --`)
	inner := lit[1 : len(lit)-1]
	for i := 0; i < len(inner); i++ {
		if inner[i] == '"' && (i == 0 || inner[i-1] != '\\') {
			t.Fatalf("kutip tidak di-escape di posisi %d: %s", i, lit)
		}
	}
	if !strings.HasPrefix(lit, `"`) || !strings.HasSuffix(lit, `"`) {
		t.Fatalf("literal tidak berkutip ganda: %s", lit)
	}
}

// Runtime check: output harus bisa di-load Lua 5.1 dan menghasilkan nilai literal yang benar.
func TestDnsdistLuaLoadsInLua51(t *testing.T) {
	lua, err := exec.LookPath("lua5.1")
	if err != nil {
		t.Skip("lua5.1 not installed")
	}
	out, err := generateDnsdistLua(mustDoc(t, &DnsdistSettings{
		BlockMode:   "adguard",
		ECSv4:       intp(24),
		ACL:         []string{"10.0.0.0/8", "::1/128"},
		Upstreams:   []DnsdistUpstream{{Name: "cf1", Address: "[2606:4700:4700::1111]:53", Pool: "default"}},
		MaxQPSPerIP: intp(100),
	}))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pf := filepath.Join(dir, "p.lua")
	if err := os.WriteFile(pf, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	chk := filepath.Join(dir, "chk.lua")
	src := "dofile(arg[1])\nprint(PROFILE_ENABLED, PROFILE_BLOCK_MODE, PROFILE_ECS_V4, #PROFILE_ACL, PROFILE_UPSTREAMS[1].address, PROFILE_MAX_QPS_PER_IP)\n"
	if err := os.WriteFile(chk, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := exec.Command(lua, chk, pf).CombinedOutput()
	if err != nil {
		t.Fatalf("lua gagal load: %v\n%s", err, got)
	}
	want := "true\tadguard\t24\t2\t[2606:4700:4700::1111]:53\t100\n"
	if string(got) != want {
		t.Fatalf("hasil lua = %q, want %q", got, want)
	}
}

func TestDnsdistLuaEmptyListsKeepBaseline(t *testing.T) {
	// Daftar kosong tidak boleh menimpa baseline: {} akan menolak semua query
	// (ACL) atau menghapus semua upstream. Catatan: JSON omitempty menghapus
	// array kosong saat marshal, jadi jalur JSON tidak pernah membawa non-nil
	// kosong. Tes ini memanggil generator langsung dengan slice non-nil kosong,
	// yaitu satu-satunya jalur yang bisa mengunci bug != nil.
	d := &DnsdistSettings{ACL: []string{}, Upstreams: []DnsdistUpstream{}}
	out, err := generateDnsdistLuaFromStruct(&SmartDistProfile{ID: "a", Name: "A", Dnsdist: d})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "PROFILE_ACL") {
		t.Errorf("ACL kosong tidak boleh diemit:\n%s", out)
	}
	if strings.Contains(out, "PROFILE_UPSTREAMS") {
		t.Errorf("upstream kosong tidak boleh diemit:\n%s", out)
	}
}

func mustDoc(t *testing.T, d *DnsdistSettings) []byte {
	t.Helper()
	doc := SmartDistProfile{ID: "cfg-a", Name: "A", Smartdist: SmartDistSettings{Rules: []SmartDistRuleSpec{}}, Dnsdist: d}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
