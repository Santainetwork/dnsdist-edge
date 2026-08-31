#!/bin/bash
# ============================================================
# Trust-NG ASN DB Loader & Compiler (Sweep-line Optimized)
# Mengkompilasi ipinfo_lite.csv -> /etc/dnsdist/asn-db.bin
# ============================================================

set -e

# --- Config & Paths ---
INPUT_CSV="ipinfo_lite.csv"
OUTPUT_BIN="/etc/dnsdist/asn-db.bin"

# Deteksi user dnsdist
if id -u _dnsdist >/dev/null 2>&1; then
    DNSDIST_USER="_dnsdist"
elif id -u dnsdist >/dev/null 2>&1; then
    DNSDIST_USER="dnsdist"
else
    DNSDIST_USER="root"
fi

# Cari input file
if [ ! -f "$INPUT_CSV" ]; then
    if [ -f "/etc/dnsdist/$INPUT_CSV" ]; then
        INPUT_CSV="/etc/dnsdist/$INPUT_CSV"
    else
        echo "Error: ipinfo_lite.csv tidak ditemukan di direktori saat ini atau /etc/dnsdist/"
        exit 1
    fi
fi

echo "[*] Ditemukan: $INPUT_CSV"
echo "[*] Mengompilasi database ASN ke $OUTPUT_BIN..."

# Jalankan compiler Python (di-embed)
python3 - << 'EOF' "$INPUT_CSV" "$OUTPUT_BIN"
import csv
import sys
import struct
import os

input_path = sys.argv[1]
output_path = sys.argv[2]

def ip_to_int(ip):
    parts = ip.split('.')
    return (int(parts[0]) << 24) + (int(parts[1]) << 16) + (int(parts[2]) << 8) + int(parts[3])

def cidr_to_range(cidr):
    ip, mask_str = cidr.split('/')
    mask = int(mask_str)
    ip_int = ip_to_int(ip)
    
    host_bits = 32 - mask
    mask_int = 0xffffffff if host_bits == 0 else ~( (1 << host_bits) - 1 ) & 0xffffffff
    
    start_ip = ip_int & mask_int
    end_ip = start_ip | (~mask_int & 0xffffffff)
    return start_ip, end_ip, mask

raw_entries = []
unique_names = {}
name_block = bytearray()

private_ranges = [
    ("127.0.0.0/8", 127, "Localhost Loopback"),
    ("10.0.0.0/8", 64512, "Private LAN"),
    ("172.16.0.0/12", 64512, "Private LAN"),
    ("192.168.0.0/16", 64512, "Private LAN"),
]

for cidr, asn, name in private_ranges:
    start_ip, end_ip, mask = cidr_to_range(cidr)
    name_bytes = name.encode('utf-8') + b'\x00'
    if name not in unique_names:
        unique_names[name] = len(name_block)
        name_block.extend(name_bytes)
    offset = unique_names[name]
    raw_entries.append((start_ip, end_ip, mask, asn, offset))

count = 0
with open(input_path, "r", encoding="utf-8", newline="") as f:
    reader = csv.DictReader(f)
    for row in reader:
        net = row.get("network", "")
        if not net or ":" in net:
            continue
        
        asn_str = row.get("asn", "")
        if not asn_str or not asn_str.startswith("AS"):
            continue
        
        try:
            asn = int(asn_str[2:])
        except ValueError:
            continue

        try:
            start_ip, end_ip, mask = cidr_to_range(net)
        except Exception:
            continue
            
        name = row.get("as_name", "")
        if not name:
            name = asn_str
            
        name_bytes = name.encode('utf-8') + b'\x00'
        if name not in unique_names:
            unique_names[name] = len(name_block)
            name_block.extend(name_bytes)
            
        offset = unique_names[name]
        raw_entries.append((start_ip, end_ip, mask, asn, offset))
        count += 1

print(f"Flattening intervals via Sweep-line (total input ranges: {len(raw_entries)})...")

# Sweep-line algorithm untuk de-overlapping
events = []
for start, end, mask, asn, name_off in raw_entries:
    events.append((start, 1, mask, asn, name_off))
    events.append((end + 1, -1, mask, asn, name_off))

# Sort: IP ascending. Jika IP sama, event END (-1) didahulukan
events.sort(key=lambda x: (x[0], x[1]))

flat_entries = []
active = []
last_ip = None

for ip, etype, mask, asn, name_off in events:
    if last_ip is not None and ip > last_ip and active:
        # LPM (longest prefix match) -> ambil mask paling spesifik (terbesar)
        best = max(active, key=lambda x: x[0])
        flat_entries.append((last_ip, ip - 1, best[0], best[1], best[2]))
        
    if etype == 1:
        active.append((mask, asn, name_off))
    else:
        # Hapus interval yang selesai dari active
        for item in active:
            if item[0] == mask and item[1] == asn and item[2] == name_off:
                active.remove(item)
                break
    last_ip = ip

print(f"Writing binary output containing {len(flat_entries)} disjoint entries...")
os.makedirs(os.path.dirname(output_path), exist_ok=True)
with open(output_path, "wb") as f:
    f.write(b"TASB")
    f.write(struct.pack(">I", len(flat_entries)))
    f.write(struct.pack(">I", len(name_block)))
    
    for start_ip, end_ip, mask, asn, offset in flat_entries:
        f.write(struct.pack(">IIIBI", start_ip, end_ip, asn, mask, offset))
        
    f.write(name_block)

print(f"[✓] Berhasil mengompilasi {len(flat_entries)} disjoint entri.")
EOF

# Set owner file ke user dnsdist
echo "[*] Setting ownership ke ${DNSDIST_USER}:${DNSDIST_USER}..."
chown "${DNSDIST_USER}:${DNSDIST_USER}" "$OUTPUT_BIN"
chmod 644 "$OUTPUT_BIN"

# Reload dnsdist jika berjalan
if systemctl is-active --quiet dnsdist 2>/dev/null; then
    echo "[*] DNSdist aktif, me-restart dnsdist..."
    systemctl restart dnsdist
    echo "[✓] DNSdist direstart."
else
    echo "[!] DNSdist service mati atau tidak terdeteksi. Restart manual jika diperlukan."
fi

echo "[✓] Selesai!"
