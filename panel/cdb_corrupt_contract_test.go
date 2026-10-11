package main

import (
	"encoding/binary"
	"testing"
)

// Kontrak: CDB korup tidak boleh panic dan tidak boleh membaca di luar buffer.
// Kasus "inloop" lolos guard header (tabel valid) tetapi slot berisi hash cocok
// dengan tpos di luar file, sehingga hanya guard di dalam loop yang menahannya.
func TestCDBContainsDomainCorruptNoPanic(t *testing.T) {
	const domain = "a.example.com"
	key, ok := wireDomainKey(domain)
	if !ok {
		t.Fatal("wireDomainKey gagal untuk domain uji")
	}
	var h uint32 = 5381
	for _, c := range key {
		h = ((h + (h << 5)) ^ uint32(c))
	}

	cases := map[string][]byte{}
	cases["zeros"] = make([]byte, 2048)
	cases["short"] = make([]byte, 2047)

	big := make([]byte, 2048)
	for i := 0; i < 256; i++ {
		binary.LittleEndian.PutUint32(big[i*8:], 0xFFFFFFF0)
		binary.LittleEndian.PutUint32(big[i*8+4:], 0xFFFFFFFF)
	}
	cases["bigptr"] = big

	// Tabel valid di offset 2048, satu slot dengan hash cocok, tpos di luar file.
	inloop := make([]byte, 2048+8)
	idx := int(h&0xff) * 8
	binary.LittleEndian.PutUint32(inloop[idx:], 2048)
	binary.LittleEndian.PutUint32(inloop[idx+4:], 1)
	binary.LittleEndian.PutUint32(inloop[2048:], h)
	binary.LittleEndian.PutUint32(inloop[2052:], 0x7FFFFFF0)
	cases["inloop"] = inloop

	for name, data := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: PANIC %v", name, r)
				}
			}()
			if cdbContainsDomain(data, domain) {
				t.Errorf("%s: true untuk data korup", name)
			}
		}()
	}
}
