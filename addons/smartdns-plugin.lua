-- ============================================================================
-- SMARTDNS COMPATIBILITY PLUGIN (vendored dari repo SmartDist milik sendiri)
-- Menyediakan fungsi: smartdns_ip_set, smartdns_domain_set,
--                     smartdns_cname, smartdns_ip_rules_alias
-- Speed Check: Asynchronous TCP probing untuk semua domain (ala SmartDNS)
--
-- Catatan integrasi dnsdist-edge: pemuatan bersifat opsional dari
-- setup/dnsdist.conf [5.6] (aman bila file tidak ada — modul dilewati).
-- Tanpa lua-socket, speed check otomatis nonaktif (fitur ip-set/domain-set/
-- CNAME/alias tetap jalan). Tidak ada kredensial/ACL bawaan di file ini.
-- ============================================================================

-- Variabel penampung untuk NetmaskGroup (ip-set) dan SuffixMatchNode (domain-set)
smartdns_nmg = {}
smartdns_smn = {}

-- ============================================================================
-- SPEED CHECK MODULE (Asynchronous, All Domains)
-- Membutuhkan: lua-socket (apt install lua-socket / yum install lua-socket)
-- Jika tidak terinstal, speed check dinonaktifkan secara otomatis (graceful)
-- ============================================================================
local _has_socket, _socket = pcall(require, "socket")

-- Cache hasil speed check: { domain = { ip, expires_at } }
local _speed_cache = {}
-- Antrian probing: { { domain, ips[], timestamp } }
local _probe_queue = {}
local _probe_queue_set = {} -- O(1) lookup
-- Sesi socket aktif (non-blocking): { { sock, ip, domain, start_time } }
local _active_sockets = {}

-- Konfigurasi Speed Check (dapat di-override di dnsdist.conf sebelum dofile())
SPEEDCHECK_ENABLED     = true       -- Aktifkan/nonaktifkan fitur ini
SPEEDCHECK_MODE        = "fastest-ip" -- Kompat dgn response-mode SmartDNS:
                                      --   "first-ping"      = pasang ping pertama yg berhasil (overwrite semua)
                                      --   "fastest-ip"      = reorder, IP tercepat di urutan #1 (default)
                                      --   "fastest-response" = overwrite semua record dgn IP tercepat (round-robin)
SPEEDCHECK_TIMEOUT_MS  = 3000       -- Timeout TCP connect (ms); harus > interval
                                     -- maintenance() dnsdist (~1 detik), kalau tidak socket
                                     -- dibuang sebelum sempat menang.
SPEEDCHECK_PORTS       = {80, 443}  -- Port yang diuji
SPEEDCHECK_MAX_IPS     = 6          -- Maks IP yang dites per domain
SPEEDCHECK_MAX_RESPONSE= 2          -- Maksimal jumlah IP Juara yang dikembalikan (digunakan oleh fastest-response)
SPEEDCHECK_CACHE_TTL   = 300        -- Detik menyimpan hasil di memori
SPEEDCHECK_QUEUE_LIMIT = 200        -- Maks antrian domain sekaligus
SPEEDCHECK_MAX_SOCKETS = 120        -- BUG FIX #3: Batas maksimal socket TCP aktif (6 IP x 2 port x 10 domain)

-- Pengecualian speed-check per domain (ala domain-rules -speed-check-mode none/tcp:port)
--   SPEEDCHECK_SKIP  = {"local.example.com", ".internal"}
--   SPEEDCHECK_PORTS_OVERRIDE = { ["api.example.com"] = {443} }
SPEEDCHECK_SKIP        = {}         -- Prefix akhiran ".x" = subdomain; tanpa titik = exact/domain itu sendiri
SPEEDCHECK_PORTS_OVERRIDE = {}      -- Kunci = domain (exact), nilai = daftar port pengganti

-- Konfigurasi Parallel Upstream Aggregation
-- Secara default SmartDist menggunakan newServer() yang sudah dikonfigurasi,
-- tetapi HANYA memilih server yang CEPAT (latency di bawah threshold).
-- Jika ingin menentukan upstream secara manual, isi array ini:
--   PARALLEL_UPSTREAMS = {"8.8.8.8", "1.1.1.1"}
-- Jika kosong, akan auto-detect dari newServer() dengan filter latency.
PARALLEL_UPSTREAMS             = {}    -- Kosong = auto dari newServer()
PARALLEL_UPSTREAM_MAX_LATENCY  = 300   -- Skip upstream jika latency > ms ini
PARALLEL_UPSTREAM_REFRESH_SEC  = 30    -- Seberapa sering rebuild list upstream (detik)
PARALLEL_MAX_UPSTREAMS         = 5     -- Maks jumlah upstream yang dipakai sekaligus
SPEEDCHECK_MAX_PAR_SOCKETS    = 30    -- Batas socket UDP parallel
local _parallel_queue      = {}        -- Antrian domain yang perlu diresolve paralel
local _parallel_queue_set  = {}        -- Hash map O(1) untuk duplikat
local _parallel_sockets    = {}        -- Socket UDP aktif untuk query DNS
local _active_upstreams    = {}        -- List upstream aktif setelah filter latency
local _last_upstream_refresh = 0       -- Timestamp refresh terakhir

-- Cek apakah domain masuk daftar skip speed-check (prefix "." = subdomain, exact jika tanpa titik)
local function _speedcheck_skipped(domain)
    if not SPEEDCHECK_SKIP or #SPEEDCHECK_SKIP == 0 then return false end
    for _, pat in ipairs(SPEEDCHECK_SKIP) do
        if pat:sub(1, 1) == "." then
            local sub = pat:sub(2)
            if domain == sub or domain:sub(-#sub - 1) == "." .. sub then return true end
        elseif domain == pat then
            return true
        end
    end
    return false
end

-- Ambil daftar port untuk sebuah domain (override per-domain, else global)
local function _speedcheck_ports(domain)
    if SPEEDCHECK_PORTS_OVERRIDE and SPEEDCHECK_PORTS_OVERRIDE[domain] then
        return SPEEDCHECK_PORTS_OVERRIDE[domain]
    end
    return SPEEDCHECK_PORTS
end

if not _has_socket then
    warnlog("[SmartDist] lua-socket tidak ditemukan! Speed Check DINONAKTIFKAN.")
    warnlog("[SmartDist] Install: apt-get install lua-socket  (atau yum install lua-socket)")
    SPEEDCHECK_ENABLED = false
else
    infolog("[SmartDist] lua-socket OK. Speed Check AKTIF untuk semua domain.")
end

-- Helper: Generate Raw DNS A Query Packet (UDP)
local function _build_dns_a_query(domain)
    local id = math.random(0, 65535)
    local packet = string.char(math.floor(id / 256), id % 256) ..
                   string.char(0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00)
    for part in domain:gmatch("[^%.]+") do
        packet = packet .. string.char(#part) .. part
    end
    packet = packet .. string.char(0, 0, 1, 0, 1) -- QTYPE A(1), QCLASS IN(1)
    return packet, id
end

-- Helper: waktu sekarang dalam detik
local function _now()
    if _has_socket then return _socket.gettime() end
    return os.time()
end

-- Ambil cached IP tercepat untuk sebuah domain (nil jika belum ada/expired)
local function _get_fastest(domain)
    local entry = _speed_cache[domain]
    if entry and _now() < entry.expires_at then
        return entry.ips
    end
    _speed_cache[domain] = nil
    return nil
end

-- Tambahkan domain ke antrian probing jika belum ada
local function _enqueue(domain, ips)
    if not SPEEDCHECK_ENABLED then return end
    if _speedcheck_skipped(domain) then return end
    if #_probe_queue >= SPEEDCHECK_QUEUE_LIMIT then return end
    
    -- Cek duplikat O(1)
    if _probe_queue_set[domain] then return end

    -- Batasi jumlah IP
    local limited = {}
    for i = 1, math.min(#ips, SPEEDCHECK_MAX_IPS) do
        limited[i] = ips[i]
    end
    
    table.insert(_probe_queue, { domain = domain, ips = limited })
    _probe_queue_set[domain] = true
end

-- Fungsi maintenance() dijalankan DnsDist setiap ~1 detik
-- Ini adalah "background worker" kita
function maintenance()
    if not SPEEDCHECK_ENABLED then return end
    -- maintenance() juga bisa dipanggil di luar dnsdist (test harness):
    -- keluar aman bila API dnsdist tidak tersedia.
    if type(getServers) ~= "function" then return end

    local timeout_sec = SPEEDCHECK_TIMEOUT_MS / 1000
    local now = _now()

    -- [0] SMART UPSTREAM REFRESH: Rebuild list upstream dari newServer() secara berkala.
    -- Hanya upstream yang UP dan latency-nya di bawah threshold yang dipilih.
    if (now - _last_upstream_refresh) >= (PARALLEL_UPSTREAM_REFRESH_SEC or 30) then
        _last_upstream_refresh = now
        local max_lat = PARALLEL_UPSTREAM_MAX_LATENCY or 300
        local max_up  = PARALLEL_MAX_UPSTREAMS or 5

        -- Jika user sudah set manual, pakai itu
        if PARALLEL_UPSTREAMS and #PARALLEL_UPSTREAMS > 0 then
            _active_upstreams = PARALLEL_UPSTREAMS
        else
            -- Auto dari newServer(): ambil semua yang UP, sort by latency, ambil top N
            local candidates = {}
            local ok, srvs = pcall(getServers)
            if ok and srvs then
                for _, s in ipairs(srvs) do
                    local lat = s:getLatency()
                    local up  = s:isUp()
                    if up and lat > 0 and lat <= max_lat then
                        local name = s:getName()
                        -- Ekstrak IP saja (hilangkan port :53)
                        local ip = name:match("^%[?([^%]%[]+)%]?:%d+$") or name:match("^([^:]+)")
                        if ip then
                            table.insert(candidates, { ip = ip, lat = lat })
                        end
                    end
                end
            end
            -- Sort ascending by latency
            table.sort(candidates, function(a, b) return a.lat < b.lat end)
            -- Ambil hanya top N
            _active_upstreams = {}
            for i = 1, math.min(#candidates, max_up) do
                table.insert(_active_upstreams, candidates[i].ip)
            end
            if #_active_upstreams > 0 then
                local names = table.concat(_active_upstreams, ", ")
                infolog(string.format("[SmartDist] Parallel: %d upstream aktif (lat <=%.0fms): %s",
                    #_active_upstreams, max_lat, names))
            end
        end
    end

    -- [1] Periksa socket aktif: siapa yang sudah konek?
    local still_active = {}
    for _, sess in ipairs(_active_sockets) do
        if (now - sess.start_time) > timeout_sec then
            -- Timeout: buang
            pcall(function() sess.sock:close() end)
        else
            local res, err = sess.sock:connect(sess.ip, sess.port)
            -- luasocket non-blocking: 1 = langsung konek, 0/nil+"already connected" = selesai,
            -- nil+"timeout"/"Operation already in progress" = masih menunggu
            if res == 1 or res == 0 or (not err) or err == "already connected" then
                -- MENANG! IP ini cepat! Masukkan ke array IP pemenang.
                local latency_ms = math.floor((now - sess.start_time) * 1000)
                local entry = _speed_cache[sess.domain]
                if not entry then
                    entry = { ips = {}, expires_at = now + SPEEDCHECK_CACHE_TTL }
                    _speed_cache[sess.domain] = entry
                end
                
                local is_dup = false
                for _, existing_ip in ipairs(entry.ips) do
                    if existing_ip == sess.ip then is_dup = true break end
                end
                
                if not is_dup and #entry.ips < (SPEEDCHECK_MAX_RESPONSE or 2) then
                    table.insert(entry.ips, sess.ip)
                    infolog(string.format("[SmartDist] Fastest Rank #%d for %s -> %s (%dms)",
                        #entry.ips, sess.domain, sess.ip, latency_ms))
                end
                pcall(function() sess.sock:close() end)
            else
                -- Masih menunggu (TIMEOUT atau belum selesai)
                still_active[#still_active + 1] = sess
            end
        end
    end
    _active_sockets = still_active

    -- [2] DRAIN BATCH: Ambil item dari antrian, buka socket jika masih ada slot
    -- BUG FIX #3: Jangan buka socket baru jika sudah melebihi batas
    if #_active_sockets < (SPEEDCHECK_MAX_SOCKETS or 120) then
        local drain_count = math.min(5, #_probe_queue)
        for i = 1, drain_count do
            local item = table.remove(_probe_queue, 1)
            if item then
                local ports = _speedcheck_ports(item.domain)
                for _, ip in ipairs(item.ips) do
                    for _, port in ipairs(ports) do
                        local ok, sock = pcall(function()
                            local s = _socket.tcp()
                            s:settimeout(0)
                            s:connect(ip, port)
                            return s
                        end)
                        if ok and sock then
                            table.insert(_active_sockets, {
                                sock = sock,
                                ip = ip,
                                port = port,
                                domain = item.domain,
                                start_time = now
                            })
                        end
                    end
                end
                -- BUG FIX #2: Hapus dari set SETELAH semua socket domain ini dibuka
                _probe_queue_set[item.domain] = nil
            end
        end
    end

    -- [3] PARALLEL UPSTREAM AGGREGATION: Cek response UDP DNS
    -- Hanya berjalan jika ada upstream aktif (setelah filter latency)
    if #_active_upstreams > 0 then
        local still_parallel = {}
        for _, sess in ipairs(_parallel_sockets) do
            if (now - sess.start_time) > 1 then
                pcall(function() sess.sock:close() end)
            else
                local data, err = sess.sock:receive()
                if data then
                    pcall(function()
                        local overlay = newDNSPacketOverlay(data)
                        local count = overlay:getRecordsCountInSection(DNSSection.Answer)
                        local ips = {}
                        for i = 0, count - 1 do
                            local rec = overlay:getRecord(i)
                            if rec.type == DNSQType.A and rec.contentLength == 4 then
                                local ip_str = string.format("%d.%d.%d.%d",
                                    data:byte(rec.contentOffset + 1), data:byte(rec.contentOffset + 2),
                                    data:byte(rec.contentOffset + 3), data:byte(rec.contentOffset + 4))
                                ips[#ips + 1] = ip_str
                            end
                        end
                        if #ips > 0 then
                            _enqueue(sess.domain, ips)
                        end
                    end)
                    pcall(function() sess.sock:close() end)
                else
                    still_parallel[#still_parallel + 1] = sess
                end
            end
        end
        _parallel_sockets = still_parallel

        -- [4] Kirim UDP Query ke upstream aktif (max 1 domain per tick, max cap socket)
        if #_parallel_queue > 0 and #_parallel_sockets < (SPEEDCHECK_MAX_PAR_SOCKETS or 30) then
            local domain = table.remove(_parallel_queue, 1)
            if domain then
                _parallel_queue_set[domain] = nil
                local query_pkt = _build_dns_a_query(domain)
                for _, upstream in ipairs(_active_upstreams) do
                    local ok, sock = pcall(function()
                        local s = _socket.udp()
                        s:settimeout(0)
                        s:setpeername(upstream, 53)
                        s:send(query_pkt)
                        return s
                    end)
                    if ok and sock then
                        table.insert(_parallel_sockets, {
                            sock = sock,
                            domain = domain,
                            upstream = upstream,
                            start_time = now
                        })
                    end
                end
            end
        end
    end
end

-- ============================================================================
-- HELPER: Konversi IPv6 string ke 16 byte
-- ============================================================================
function _ipv6_to_bytes(str)
    local left, right
    local groups = {}

    if str:find("::") then
        left, right = str:match("^(.-)::(.-)$")
        local lg, rg = {}, {}
        if left ~= "" then
            for g in left:gmatch("[^:]+") do lg[#lg + 1] = g end
        end
        if right ~= "" then
            for g in right:gmatch("[^:]+") do rg[#rg + 1] = g end
        end
        local missing = 8 - #lg - #rg
        for _, g in ipairs(lg) do groups[#groups + 1] = g end
        for _ = 1, missing do groups[#groups + 1] = "0" end
        for _, g in ipairs(rg) do groups[#groups + 1] = g end
    else
        for g in str:gmatch("[^:]+") do groups[#groups + 1] = g end
    end

    local bytes = {}
    for _, g in ipairs(groups) do
        local val = tonumber(g, 16)
        bytes[#bytes + 1] = math.floor(val / 256)
        bytes[#bytes + 1] = val % 256
    end
    return bytes
end

-- ============================================================================
-- HELPER: Baca 16 byte IPv6 dari paket sebagai string
-- ============================================================================
function _bytes_to_ipv6(pkt, offset)
    local groups = {}
    for i = 0, 7 do
        local val = pkt:byte(offset + i * 2 + 1) * 256 + pkt:byte(offset + i * 2 + 2)
        groups[#groups + 1] = string.format("%x", val)
    end
    return table.concat(groups, ":")
end

-- ============================================================================
-- Meniru fungsi: ip-set -name [nama] -file [file]
-- ============================================================================
function smartdns_ip_set(name, filepath)
    smartdns_nmg[name] = newNMG()
    local file = io.open(filepath, "r")
    if file then
        for line in file:lines() do
            local mask = line:match("^%s*(.-)%s*$")
            if mask ~= "" and not mask:find("^#") then 
                smartdns_nmg[name]:addMask(mask) 
            end
        end
        file:close()
        infolog("smartdns: Loaded ip-set '" .. name .. "' from " .. filepath)
    else
        errlog("smartdns: Gagal membuka file ip-set untuk " .. name .. ": " .. filepath)
    end
end

-- ============================================================================
-- Meniru fungsi: domain-set -name [nama] -file [file]
-- ============================================================================
function smartdns_domain_set(name, filepath)
    smartdns_smn[name] = newSuffixMatchNode()
    local file = io.open(filepath, "r")
    if file then
        for line in file:lines() do
            local domain = line:match("^%s*(.-)%s*$")
            if domain ~= "" and not domain:find("^#") then 
                -- Hapus prefix *. jika user menuliskannya secara manual
                domain = domain:gsub("^%*%.", "")
                smartdns_smn[name]:add(newDNSName(domain)) 
            end
        end
        file:close()
        infolog("smartdns: Loaded domain-set '" .. name .. "' from " .. filepath)
    else
        errlog("smartdns: Gagal membuka file domain-set untuk " .. name .. ": " .. filepath)
    end
end

-- ============================================================================
-- Meniru fungsi: cname /domain/target
-- ============================================================================
function smartdns_cname(domain_pattern, target_cname)
    -- Menghapus prefix wildcard "-." atau "." khas SmartDNS jika disertakan oleh user
    local clean_domain = domain_pattern
    if clean_domain:sub(1, 2) == "-." then
        clean_domain = clean_domain:sub(3)
    elseif clean_domain:sub(1, 1) == "." then
        clean_domain = clean_domain:sub(2)
    end
    local smn = newSuffixMatchNode()
    smn:add(newDNSName(clean_domain))
    addAction(SuffixMatchNodeRule(smn), SpoofCNAMEAction(target_cname), {name="CNAME: " .. target_cname})
    infolog("smartdns: CNAME rule added: *." .. clean_domain .. " -> " .. target_cname)
end

-- ============================================================================
-- Meniru fungsi: ip-rules ip-set:[ip_set] -ip-alias [target_ip(s)]
--
-- target_ips bisa berupa:
--   - String tunggal:  "172.64.87.224"
--   - Tabel/array:     {"172.64.52.159", "172.64.87.224"}
--
-- Semantics SmartDNS (src/dns_server/answer.c + ip_rule.c): ip-alias dicek PER
-- RECORD; record yang cocok ip-set diganti dari daftar alias, record lain utuh.
-- (SmartDNS menaruh SEMUA alias di setiap record yang cocok, lalu dedup lewat
-- ip_map. Lihat ponytail di hook untuk model yang dipakai di sini.)
--
-- Otomatis mendeteksi IPv4 (A record) atau IPv6 (AAAA record) dari format IP.
-- Hanya alias dengan famili yang sama dengan record yang dipakai (cek
-- addr_len di _dns_server_process_ip_alias) — alias beda famili dilewati.
-- ============================================================================
function smartdns_ip_rules_alias(ip_set_name, target_ips, exclude_domain_set)
    -- Pastikan ip-set sudah dideklarasikan
    if not smartdns_nmg[ip_set_name] then
        errlog("smartdns: Error - ip-set '" .. ip_set_name .. "' belum dibuat.")
        return
    end

    -- Normalisasi: jika string tunggal, bungkus dalam tabel
    if type(target_ips) == "string" then
        target_ips = {target_ips}
    end

    -- Deteksi famili dari target pertama (untuk memilih qtype hook)
    local is_ipv6 = target_ips[1]:find(":") ~= nil
    local dns_qtype = is_ipv6 and DNSQType.AAAA or DNSQType.A
    local record_len = is_ipv6 and 16 or 4

    -- Pre-parse SEMUA alias ke byte array. Alias beda famili dibuang
    -- (SmartDNS: addr_len harus sama, kalau tidak -> continue/skip).
    local alias_list = {}
    for _, tip in ipairs(target_ips) do
        local tip_v6 = tip:find(":") ~= nil
        if tip_v6 == is_ipv6 then
            local bytes
            if tip_v6 then
                bytes = _ipv6_to_bytes(tip)
            else
                bytes = {}
                for octet in tip:gmatch("%d+") do
                    bytes[#bytes + 1] = tonumber(octet)
                end
            end
            if #bytes == record_len then
                alias_list[#alias_list + 1] = bytes
            end
        end
    end

    if #alias_list == 0 then
        errlog("smartdns: ip-rules alias -ip-set " .. ip_set_name .. ": tidak ada alias yang cocok famili.")
        return
    end

    local ip_type_str = is_ipv6 and "IPv6/AAAA" or "IPv4/A"
    infolog("smartdns: ip-rules alias [" .. ip_type_str .. "] ip-set:" .. ip_set_name 
            .. " -> " .. table.concat(target_ips, ", "))

    -- Semantics SmartDNS asli (src/dns_server/answer.c + src/dns_server/ip_rule.c):
    -- ip-alias dicek per RECORD. Record yang cocok ip-set diisi dari daftar
    -- alias; record yang tidak cocok dibiarkan utuh. Alias beda famili di-skip.
    --
    -- ponytail: dnsdist tidak bisa menumbuhkan/menyusutkan jumlah RR tanpa
    -- membangun ulang paket + ANCOUNT. Jadi record yang cocok diisi alias
    -- secara round-robin (bukan menaruh seluruh daftar di tiap record).
    -- Hasil sama dengan SmartDNS saat jumlah record cocok == jumlah alias;
    -- beda hanya saat jumlahnya tidak sama (bisa ada alias berulang).
    -- Butuh persis ala ip_map (dedup + jumlah RR dinamis)? Bangun ulang paket
    -- dan sesuaikan test ALIAS_RR/ALIAS_PARTIAL.
    addResponseAction(AllRule(), LuaResponseAction(function(dr)
        -- Filter berdasarkan tipe query (A untuk IPv4, AAAA untuk IPv6)
        if dr.qtype ~= dns_qtype then
            return DNSResponseAction.None, ""
        end

        -- EXCLUDE LOGIC: domain di daftar exclude tidak di-alias
        if exclude_domain_set and smartdns_smn[exclude_domain_set] then
            if smartdns_smn[exclude_domain_set]:check(dr.qname) then
                return DNSResponseAction.None, ""
            end
        end

        -- Parse paket response
        local pkt = dr:getContent()
        local overlay = newDNSPacketOverlay(pkt)
        local record_count = overlay:getRecordsCountInSection(DNSSection.Answer)

        -- Kumpulkan offset record target yang cocok ip-set (urutan paket)
        local matched_offsets = {}
        for i = 0, record_count - 1 do
            local rec = overlay:getRecord(i)
            if rec.type == dns_qtype and rec.contentLength == record_len then
                local ip_str
                if is_ipv6 then
                    ip_str = _bytes_to_ipv6(pkt, rec.contentOffset)
                else
                    ip_str = string.format("%d.%d.%d.%d",
                        pkt:byte(rec.contentOffset + 1),
                        pkt:byte(rec.contentOffset + 2),
                        pkt:byte(rec.contentOffset + 3),
                        pkt:byte(rec.contentOffset + 4))
                end

                local addr = newCA(ip_str)
                if addr and smartdns_nmg[ip_set_name]:match(addr) then
                    matched_offsets[#matched_offsets + 1] = rec.contentOffset
                end
            end
        end

        if #matched_offsets == 0 then
            return DNSResponseAction.None, ""
        end

        -- Bangun daftar IP hasil: record tak cocok tetap; record cocok diganti
        -- alias bergilir (round-robin), supaya multi-record tidak semua jadi
        -- alias yang sama. Set IP asli tidak pernah bocor ke respons.
        -- (SmartDNS men-dedup lewat ip_map; di sini jumlah RR tetap, lihat
        -- ponytail di atas.)
        local pkt_bytes = {pkt:byte(1, #pkt)}
        local matched_n = 0

        for i = 0, record_count - 1 do
            local rec = overlay:getRecord(i)
            if rec.type == dns_qtype and rec.contentLength == record_len then
                if matched_offsets[matched_n + 1] == rec.contentOffset then
                    matched_n = matched_n + 1
                    local target_bytes = alias_list[((matched_n - 1) % #alias_list) + 1]
                    for b = 1, record_len do
                        pkt_bytes[rec.contentOffset + b] = target_bytes[b]
                    end
                end
            end
        end

        -- Rebuild paket dari byte array
        local parts = {}
        for _, b in ipairs(pkt_bytes) do
            parts[#parts + 1] = string.char(b)
        end
        dr:setContent(table.concat(parts))

        return DNSResponseAction.None, ""
    end), {name="IP Alias: " .. ip_set_name .. " (" .. (is_ipv6 and "IPv6" or "IPv4") .. ")"})
end

-- ============================================================================
-- SPEED CHECK: Global Response Hook untuk SEMUA Domain
-- Panggil fungsi ini SATU KALI di bagian akhir dnsdist.conf Anda:
--   smartdns_enable_speedcheck()
-- ============================================================================
function smartdns_enable_speedcheck()
    if not SPEEDCHECK_ENABLED then
        warnlog("[SmartDist] Speed Check tidak aktif (lua-socket tidak tersedia).")
        return
    end

    -- Info parallel aggregation
    if PARALLEL_UPSTREAMS and #PARALLEL_UPSTREAMS > 0 then
        infolog(string.format("[SmartDist] Parallel aggregation: %d upstream manual ditentukan.", #PARALLEL_UPSTREAMS))
        _active_upstreams = PARALLEL_UPSTREAMS
    else
        infolog(string.format("[SmartDist] Parallel aggregation: auto-select newServer() lat<=%.0fms, top %d.",
            PARALLEL_UPSTREAM_MAX_LATENCY or 300, PARALLEL_MAX_UPSTREAMS or 5))
    end

    addResponseAction(AllRule(), LuaResponseAction(function(dr)
        if dr.qtype ~= DNSQType.A and dr.qtype ~= DNSQType.AAAA then
            return DNSResponseAction.None, ""
        end

        local domain = dr.qname:toString():gsub("%.$", "")

        -- BUG FIX #1: FAST PATH — cek hal-hal murah SEBELUM parsing paket yang mahal.
        -- Jika domain sudah ada di antrian probe, tidak ada yang perlu dilakukan.
        if _probe_queue_set[domain] then
            return DNSResponseAction.None, ""
        end

        -- Jika ada cache hit, langsung masuk ke rewrite tanpa parse dulu.
        local fastest_ips = _get_fastest(domain)

        -- Jika tidak ada cache dan tidak ada di antrian, kita perlu parse paket
        -- untuk mengumpulkan IP-IP yang akan di-probe.
        if not fastest_ips then
            local pkt_raw = dr:getContent()
            local overlay = newDNSPacketOverlay(pkt_raw)
            local record_count = overlay:getRecordsCountInSection(DNSSection.Answer)
            local record_len = (dr.qtype == DNSQType.AAAA) and 16 or 4
            local ips = {}
            for i = 0, record_count - 1 do
                local rec = overlay:getRecord(i)
                if rec.type == dr.qtype and rec.contentLength == record_len then
                    local ip_str
                    if dr.qtype == DNSQType.AAAA then
                        ip_str = _bytes_to_ipv6(pkt_raw, rec.contentOffset)
                    else
                        ip_str = string.format("%d.%d.%d.%d",
                            pkt_raw:byte(rec.contentOffset + 1),
                            pkt_raw:byte(rec.contentOffset + 2),
                            pkt_raw:byte(rec.contentOffset + 3),
                            pkt_raw:byte(rec.contentOffset + 4))
                    end
                    if ip_str then ips[#ips + 1] = ip_str end
                end
            end

            if #ips < 2 then return DNSResponseAction.None, "" end

            -- Masukkan ke antrian probe background
            _enqueue(domain, ips)

            -- Masukkan ke antrian parallel upstream juga
            if PARALLEL_UPSTREAMS and #PARALLEL_UPSTREAMS > 0 then
                if #_parallel_queue < SPEEDCHECK_QUEUE_LIMIT and not _parallel_queue_set[domain] then
                    table.insert(_parallel_queue, domain)
                    _parallel_queue_set[domain] = true
                end
            end

            -- Untuk query pertama, kembalikan paket asli tanpa diubah
            return DNSResponseAction.None, ""
        end

        -- Ada cache hit: lakukan rewrite paket
        local pkt = dr:getContent()
        local overlay = newDNSPacketOverlay(pkt)
        local record_count = overlay:getRecordsCountInSection(DNSSection.Answer)
        local record_len = (dr.qtype == DNSQType.AAAA) and 16 or 4

        if record_count == 0 then return DNSResponseAction.None, "" end

        -- Convert fastest IPs ke byte arrays
        local pkt_bytes = {pkt:byte(1, #pkt)}
        local target_bytes_list = {}
        for _, fip in ipairs(fastest_ips) do
            local tbytes = {}
            if dr.qtype == DNSQType.AAAA then
                tbytes = _ipv6_to_bytes(fip)
            else
                for octet in fip:gmatch("%d+") do
                    tbytes[#tbytes + 1] = tonumber(octet)
                end
            end
            table.insert(target_bytes_list, tbytes)
        end

        if SPEEDCHECK_MODE == "fastest-ip" then
            -- Mode fastest-ip: Tukar IP tercepat #1 ke urutan paling atas
            local first_rec_offset = nil
            local fastest_rec_offset = nil
            local top_target = target_bytes_list[1]

            for i = 0, record_count - 1 do
                local rec = overlay:getRecord(i)
                if rec.type == dr.qtype and rec.contentLength == record_len then
                    if not first_rec_offset then first_rec_offset = rec.contentOffset end
                    local is_match = true
                    for b = 1, record_len do
                        if pkt_bytes[rec.contentOffset + b] ~= top_target[b] then
                            is_match = false break
                        end
                    end
                    if is_match then fastest_rec_offset = rec.contentOffset end
                end
            end

            if first_rec_offset and fastest_rec_offset and first_rec_offset ~= fastest_rec_offset then
                for b = 1, record_len do
                    local temp = pkt_bytes[first_rec_offset + b]
                    pkt_bytes[first_rec_offset + b] = pkt_bytes[fastest_rec_offset + b]
                    pkt_bytes[fastest_rec_offset + b] = temp
                end
            end
        else
            -- Mode fastest-response: Overwrite record dengan top N IP (Round-Robin)
            local fip_index = 1
            for i = 0, record_count - 1 do
                local rec = overlay:getRecord(i)
                if rec.type == dr.qtype and rec.contentLength == record_len then
                    local current_target = target_bytes_list[fip_index]
                    for b = 1, record_len do
                        pkt_bytes[rec.contentOffset + b] = current_target[b]
                    end
                    fip_index = fip_index + 1
                    if fip_index > #target_bytes_list then fip_index = 1 end
                end
            end
        end

        local parts = {}
        for _, b in ipairs(pkt_bytes) do parts[#parts + 1] = string.char(b) end
        dr:setContent(table.concat(parts))

        return DNSResponseAction.None, ""
    end), {name="SmartDist Speed Check Hook"})

    infolog("[SmartDist] Speed Check hook aktif untuk SEMUA domain.")
end
