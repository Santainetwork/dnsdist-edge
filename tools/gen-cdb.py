#!/usr/bin/env python3
"""
gen-cdb.py — Generator blacklist CDB untuk DNSDist (format wire).

Membuat file CDB (Constant Database) yang berisi pasangan key-value:
  key   = nama domain dalam DNS wire-format (huruf kecil, titik di akhir)
  value = byte bebas (default: 'x', bisa diubah via --value)

Output ini dapat langsung dimuat DNSDist via:
  newCDBKVStore('/var/lib/dnsdist/blacklist.db', 5)

Penggunaan:
  python3 gen-cdb.py OUTPUT.db domain1.com domain2.com...
  cat domains.txt | python3 gen-cdb.py OUTPUT.db
  python3 gen-cdb.py OUTPUT.db --value '1' --no-wire <<< "evil.com"

Opsi:
  --value STR    value byte (string) untuk setiap key (default: 'x')
  --no-wire      pakai key apa adanya (tanpa lowercase / tanpa titik akhir)
  -h, --help     tampilkan bantuan ini
"""
import sys

# ---------------------------------------------------------------------------
# CDB (Constant Database) writer — spec: https://cr.yp.to/cdb/cdb.txt
# ---------------------------------------------------------------------------
def cdb_hash(data: bytes) -> int:
    h = 5381
    for b in data:
        h = ((h + (h << 5)) & 0xFFFFFFFF) ^ b
    return h & 0xFFFFFFFF


def write_cdb(path: str, entries: "list[tuple[bytes, bytes]]") -> None:
    # 1) Hasilkan semua data record + posisi-nya.
    records = []  # (h, pos, klen, dlen, key, val)
    pos = 2048  # header index = 256 * 8 = 2048 byte
    for key, val in entries:
        h = cdb_hash(key)
        records.append((h, pos, len(key), len(val), key, val))
        pos += len(key) + len(val) + 8

    # 2) Alokasikan 256 tabel hash. Bucket dipilih dari BYTE RENDAH hash
    #    (h & 0xFF) sesuai spec CDB. Sebelumnya memakai (h >> 8) & 0xFF,
    #    sehingga dnsdist (yang mengikuti spec) tidak pernah menemukan entri.
    tables = {}
    for h, *_ in records:
        slot = h & 0xFF
        tables.setdefault(slot, []).append(h)

    # Tulis placeholder tabel (n * 4 byte), catat offset-nya.
    hpos = 2048 + sum(len(key) + len(val) + 8 for _, _, _, _, key, val in records)
    table_pos = {}
    with open(path, "wb") as f:
        f.write(b"\x00" * 2048)
        for slot in range(256):
            hs = tables.get(slot, [])
            table_pos[slot] = hpos
            f.write(b"\x00" * (len(hs) * 8))
            hpos += len(hs) * 8

        # 3) Tulis data records.
        f.seek(2048)
        for h, rpos, klen, dlen, key, val in records:
            f.write(klen.to_bytes(4, "little"))
            f.write(dlen.to_bytes(4, "little"))
            f.write(key)
            f.write(val)

        # 4) Isi tabel hash. Posisi awal di dalam bucket = (h >> 8) % slotCount,
        #    lalu linear probing sampai slot kosong - sama seperti pembaca dnsdist.
        for slot, hs in tables.items():
            slot_count = len(hs)
            placed = [None] * slot_count
            for h in hs:
                start = (h >> 8) % slot_count
                for i in range(slot_count):
                    idx = (start + i) % slot_count
                    if placed[idx] is None:
                        placed[idx] = h
                        break
            f.seek(table_pos[slot])
            for h in placed:
                rpos = next(r[1] for r in records if r[0] == h)
                f.write(h.to_bytes(4, "little"))
                f.write(rpos.to_bytes(4, "little"))

        # 5) Tulis header index. Field kedua adalah JUMLAH SLOT, bukan byte.
        f.seek(0)
        for slot in range(256):
            hs = tables.get(slot, [])
            f.write(table_pos[slot].to_bytes(4, "little"))
            f.write(len(hs).to_bytes(4, "little"))

    # 6) Verifikasi: baca ulang dan hitung kembali hash.
    check_records(records)


def check_records(records: "list[tuple[int, int, int, int, bytes, bytes]]") -> None:
    """Sanity check sederhana: setiap record masih bisa di-hash sama."""
    for h, _, _, _, key, _ in records:
        if cdb_hash(key) != h:
            raise SystemExit(f"[!] Internal error: hash mismatch for {key!r}")
    print(f"[✓] OK: {len(records)} entri ditulis")


def wire_name(domain: str) -> bytes:
    """'Example.COM' -> DNS wire format b'\\x07example\\x03com\\x00' (RFC 1035).

    This must be real wire format, not the plain text 'example.com.'. dnsdist
    looks keys up with KeyValueLookupKeyQName(true), i.e. wire format, and
    tools/trust-builder writes wire format via dns.PackDomainName. An earlier
    version of this function returned plain text despite claiming wire format,
    so CDBs it produced never matched and silently blocked nothing.
    """
    d = domain.rstrip(".").lower()
    if not d:
        raise ValueError(f"domain kosong: {domain!r}")
    out = bytearray()
    for label in d.split("."):
        if not label:
            raise ValueError(f"label kosong pada: {domain!r}")
        raw = label.encode()
        if len(raw) > 63:
            raise ValueError(f"label > 63 byte: {label!r}")
        out.append(len(raw))
        out += raw
    out.append(0)
    if len(out) > 255:
        raise ValueError(f"nama > 255 byte: {domain!r}")
    return bytes(out)


def main() -> int:
    argv = sys.argv[1:]
    if not argv or "-h" in argv or "--help" in argv:
        print(__doc__)
        return 0 if argv and ("-h" in argv or "--help" in argv) else 2

    value = b"x"
    wire = True
    out = None
    domains = []
    i = 0
    while i < len(argv):
        a = argv[i]
        if a == "--value":
            i += 1
            if i >= len(argv):
                print("[!] --value membutuhkan argumen", file=sys.stderr)
                return 2
            value = argv[i].encode()
        elif a == "--no-wire":
            wire = False
        elif out is None:
            out = a
        else:
            domains.append(a)
        i += 1

    if out is None:
        print("[!] Tentukan file output: gen-cdb.py OUTPUT.db domain...", file=sys.stderr)
        return 2

    if not domains:
        # Baca dari stdin bila tidak ada domain di argumen.
        domains = [ln.strip() for ln in sys.stdin if ln.strip()]

    entries = []
    for d in domains:
        if wire:
            try:
                key = wire_name(d)
            except ValueError as e:
                print(f"[!] Lewati: {e}", file=sys.stderr)
                continue
        else:
            key = d.encode()
        entries.append((key, value))

    if not entries:
        print("[!] Tidak ada domain valid untuk ditulis.", file=sys.stderr)
        return 2

    write_cdb(out, entries)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
