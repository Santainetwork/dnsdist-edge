-- ============================================================
--  Trust-NG DNSDist Edge — Top Stats Module
--  top-stats.lua
--
--  Fitur:
--    • Top Queries   — domain paling banyak di-query
--    • Top Blocked   — domain paling banyak di-blokir
--    • Top ASN       — Autonomous System Number klien terbanyak
--    • Top Blocked ASN — ASN yang paling sering kena blokir
--
--  Akses via API bawaan dnsdist (webserver port 8083):
--    GET /api/v1/top-queries?n=50
--    GET /api/v1/top-blocked?n=50
--    GET /api/v1/top-asn?n=50
--    GET /api/v1/top-blocked-asn?n=50
--    GET /api/v1/top-stats          (ringkasan semua)
--
--  Autentikasi: header  X-API-Key: <key>
--
--  Integrasi: tambahkan di dnsdist.conf SETELAH setup KVS
--  dan SEBELUM filtering rules:
--    dofile('/etc/dnsdist/top-stats.lua')
--
--  Kompatibel: dnsdist >= 1.6  (Debian bookworm = 1.7.3)
-- ============================================================

-- ============================================================
-- [A] KONFIGURASI
-- ============================================================
local CONFIG = {
  -- Batas maksimum entri unik per tabel (cegah memory leak)
  maxEntries       = 20000,

  -- Setiap berapa query dilakukan cleanup (buang entri kecil)
  cleanupInterval  = 50000,

  -- Jumlah default hasil top-N jika parameter ?n tidak diberikan
  defaultTopN      = 50,

  -- API key — HARUS sama dengan setWebserverConfig({apiKey=...})
  -- di dnsdist.conf. Kosongkan ('') untuk menonaktifkan auth.
  apiKey           = 'trust-ng-apikey-changeme',

  -- Path database ASN. Format:
  --   .bin  : binary compact hasil kompilasi csv2bin.py (Recommended, cepat)
  --   .csv  : text CSV (subnet,asn_number,asn_name) - format lama
  asnDBPath        = '/etc/dnsdist/asn-db.bin',

  -- Interval reload ASN database (dalam jumlah query)
  asnReloadInterval = 500000,
}

-- ============================================================
-- [B] JSON ENCODER (minimal, tanpa dependensi eksternal)
-- ============================================================
local function json_escape(s)
  s = s:gsub('\\', '\\\\')
  s = s:gsub('"',  '\\"')
  s = s:gsub('\n', '\\n')
  s = s:gsub('\r', '\\r')
  s = s:gsub('\t', '\\t')
  -- buang karakter kontrol
  s = s:gsub('[%c]', '')
  return s
end

local function json_encode(val)
  local t = type(val)
  if t == 'string' then
    return '"' .. json_escape(val) .. '"'
  elseif t == 'number' then
    if val ~= val then return 'null' end           -- NaN
    if val == math.huge then return '1e999' end
    if val == -math.huge then return '-1e999' end
    return string.format('%.17g', val)
  elseif t == 'boolean' then
    return val and 'true' or 'false'
  elseif t == 'nil' then
    return 'null'
  elseif t == 'table' then
    -- cek apakah array (integer keys 1..n)
    local is_array = true
    local n = 0
    for k, _ in pairs(val) do
      n = n + 1
      if type(k) ~= 'number' or k ~= math.floor(k) or k < 1 then
        is_array = false
        break
      end
    end
    if is_array and n == #val then
      local parts = {}
      for i = 1, #val do
        parts[i] = json_encode(val[i])
      end
      return '[' .. table.concat(parts, ',') .. ']'
    else
      local parts = {}
      for k, v in pairs(val) do
        parts[#parts + 1] = '"' .. json_escape(tostring(k)) .. '":' .. json_encode(v)
      end
      return '{' .. table.concat(parts, ',') .. '}'
    end
  end
  return 'null'
end

-- ============================================================
-- [C] TABEL STATISTIK (in-memory)
-- ============================================================
local stats = {
  queries      = {},   -- qname (string) -> count (number)
  blocked      = {},   -- qname (string) -> count (number)
  asn          = {},   -- "AS12345 NAME" -> count
  blocked_asn  = {},   -- "AS12345 NAME" -> count
  clients      = {},   -- client_ip (string) -> count (number)
  total_queries  = 0,
  total_blocked  = 0,
  start_time     = os.time(),
}

-- Counter internal untuk trigger cleanup & ASN reload
local _query_counter   = 0
local _asn_load_counter = 0

-- ============================================================
-- [D] ASN DATABASE (longest-prefix match, in-memory)
-- ============================================================
-- Struktur: array of { net_int, mask_bits, asn, name }
-- Di-sort descending by mask_bits agar longest match duluan.
-- database state
local asn_loaded  = false
local is_bin_db   = false

-- Variabel data biner (jika format .bin)
local bin_data = ""
local names_block = ""
local entry_count = 0

-- Variabel format CSV lama
local asn_entries = {}

--- Konversi IPv4 string -> integer 32-bit
local function ipv4_to_int(ip)
  local a, b, c, d = ip:match('^(%d+)%.(%d+)%.(%d+)%.(%d+)$')
  if not a then return nil end
  a, b, c, d = tonumber(a), tonumber(b), tonumber(c), tonumber(d)
  if a > 255 or b > 255 or c > 255 or d > 255 then return nil end
  return a * 16777216 + b * 65536 + c * 256 + d
end

--- Parse satu baris CSV: subnet,asn_number,asn_name
local function parse_asn_line(line)
  if line:match('^%s*#') or line:match('^%s*$') then return nil end
  local subnet, asn_num, asn_name = line:match('^%s*([^,]+)%s*,%s*(%d+)%s*,%s*(.-)%s*$')
  if not subnet then return nil end

  local ip_part, mask_str = subnet:match('^(.+)/(%d+)$')
  if not ip_part then
    ip_part = subnet
    mask_str = '32'
  end
  local mask = tonumber(mask_str)
  if not mask or mask < 0 or mask > 32 then return nil end

  local net_int = ipv4_to_int(ip_part)
  if not net_int then return nil end

  local host_bits = 32 - mask
  net_int = math.floor(net_int / (2 ^ host_bits)) * (2 ^ host_bits)

  return {
    net_int   = net_int,
    mask_bits = mask,
    host_bits = host_bits,
    asn       = tonumber(asn_num),
    name      = asn_name or ('AS' .. asn_num),
  }
end

-- Helper membaca uint32 big endian dari file handle
local function read_uint32_be(f)
  local bytes = f:read(4)
  if not bytes or #bytes < 4 then return nil end
  local b1, b2, b3, b4 = string.byte(bytes, 1, 4)
  return b1 * 16777216 + b2 * 65536 + b3 * 256 + b4
end

-- Helper mengambil start_ip dari entri ke-mid di bin_data
local function get_bin_start_ip(mid)
  local offset = (mid - 1) * 17 + 1
  local b1, b2, b3, b4 = string.byte(bin_data, offset, offset + 3)
  return b1 * 16777216 + b2 * 65536 + b3 * 256 + b4
end

-- Helper mengambil detail lengkap entri biner ke-i
local function get_bin_entry(i)
  local offset = (i - 1) * 17 + 1
  local b = { string.byte(bin_data, offset, offset + 16) }
  local start_ip = b[1] * 16777216 + b[2] * 65536 + b[3] * 256 + b[4]
  local end_ip   = b[5] * 16777216 + b[6] * 65536 + b[7] * 256 + b[8]
  local asn      = b[9] * 16777216 + b[10] * 65536 + b[11] * 256 + b[12]
  local mask     = b[13]
  local name_off = b[14] * 16777216 + b[15] * 65536 + b[16] * 256 + b[17]
  return start_ip, end_ip, asn, mask, name_off
end

-- Helper mengambil string dari names_block
local function get_bin_name(offset)
  local start = offset + 1
  local end_idx = string.find(names_block, "\0", start, true)
  if not end_idx then return "Unknown" end
  return string.sub(names_block, start, end_idx - 1)
end

--- Muat / reload database ASN
function load_asn_db()
  if CONFIG.asnDBPath == '' then return end
  
  -- Cek tipe file (bin atau csv)
  if string.sub(CONFIG.asnDBPath, -4) == '.bin' then
    local f = io.open(CONFIG.asnDBPath, 'rb')
    if not f then
      print('[top-stats] Error opening binary file: ' .. CONFIG.asnDBPath)
      return
    end
    
    local magic = f:read(4)
    if magic ~= "TASB" then
      print('[top-stats] Invalid binary magic header in ' .. CONFIG.asnDBPath)
      f:close()
      return
    end
    
    local count = read_uint32_be(f)
    local str_size = read_uint32_be(f)
    if not count or not str_size then
      print('[top-stats] Invalid header data')
      f:close()
      return
    end
    
    bin_data = f:read(17 * count)
    names_block = f:read(str_size)
    f:close()
    
    if not bin_data or #bin_data < (17 * count) then
      print('[top-stats] Truncated binary data')
      return
    end
    
    entry_count = count
    is_bin_db = true
    asn_loaded = true
  else
    -- Fallback ke parser CSV lama
    local f = io.open(CONFIG.asnDBPath, 'r')
    if not f then return end

    local entries = {}
    for line in f:lines() do
      local e = parse_asn_line(line)
      if e then entries[#entries + 1] = e end
    end
    f:close()

    table.sort(entries, function(a, b)
      if a.mask_bits ~= b.mask_bits then return a.mask_bits > b.mask_bits end
      return a.net_int < b.net_int
    end)

    asn_entries = entries
    is_bin_db = false
    asn_loaded = true
  end
end

--- Lookup ASN dari IP string. Return: label "AS12345 Name" atau nil
function lookup_asn(ip)
  if not asn_loaded then return nil end
  local ip_int = ipv4_to_int(ip)
  if not ip_int then return nil end

  if is_bin_db then
    if entry_count == 0 then return nil end
    -- Binary search: cari index terakhir di mana start_ip <= ip_int
    local low, high = 1, entry_count
    local ans_idx = nil
    while low <= high do
      local mid = math.floor((low + high) / 2)
      local start_ip = get_bin_start_ip(mid)
      if start_ip <= ip_int then
        ans_idx = mid
        low = mid + 1
      else
        high = mid - 1
      end
    end
    if not ans_idx then return nil end

    -- Karena data sudah disjoint (sweep-line flattened), cukup 1x cek
    local start_ip, end_ip, asn, mask, name_off = get_bin_entry(ans_idx)
    if ip_int >= start_ip and ip_int <= end_ip then
      local name = get_bin_name(name_off)
      return 'AS' .. asn .. ' ' .. name
    end
    return nil
  else
    -- Fallback CSV linear scan
    if #asn_entries == 0 then return nil end
    for _, e in ipairs(asn_entries) do
      local shifted = math.floor(ip_int / (2 ^ e.host_bits))
      local net_shifted = math.floor(e.net_int / (2 ^ e.host_bits))
      if shifted == net_shifted then
        return 'AS' .. e.asn .. ' ' .. e.name
      end
    end
    return nil
  end
end

-- Muat pertama kali saat config di-load
load_asn_db()

-- ============================================================
-- [E] FUNGSI CLEANUP (cegah tabel membengkak)
-- ============================================================
--- Buang entri di luar top-N, sisakan maxEntries/2
local function trim_table(tbl, keep)
  local arr = {}
  for k, v in pairs(tbl) do
    arr[#arr + 1] = { k = k, v = v }
  end
  if #arr <= keep then return end

  table.sort(arr, function(a, b) return a.v > b.v end)

  -- hapus semua, lalu isi ulang dengan top keep
  for k in pairs(tbl) do tbl[k] = nil end
  for i = 1, keep do
    tbl[arr[i].k] = arr[i].v
  end
end

local function maybe_cleanup()
  _query_counter = _query_counter + 1
  if _query_counter % CONFIG.cleanupInterval ~= 0 then return end

  local keep = math.floor(CONFIG.maxEntries / 2)
  trim_table(stats.queries, keep)
  trim_table(stats.blocked, keep)
  trim_table(stats.asn, keep)
  trim_table(stats.blocked_asn, keep)
  trim_table(stats.clients, keep)
end

--- Reload ASN DB secara berkala
local function maybe_reload_asn()
  if CONFIG.asnDBPath == '' then return end
  _asn_load_counter = _asn_load_counter + 1
  if _asn_load_counter % CONFIG.asnReloadInterval ~= 0 then return end
  load_asn_db()
end

-- ============================================================
-- [F] FUNGSI HELPER: ambil top-N dari tabel
-- ============================================================
local function get_top_n(tbl, n)
  local arr = {}
  for k, v in pairs(tbl) do
    arr[#arr + 1] = { name = k, count = v }
  end
  table.sort(arr, function(a, b)
    if a.count ~= b.count then return a.count > b.count end
    return a.name < b.name
  end)
  local result = {}
  for i = 1, math.min(n, #arr) do
    result[i] = { rank = i, name = arr[i].name, count = arr[i].count }
  end
  return result
end

-- ============================================================
-- [F.5] RIPE STAT CACHE + RESOLVER
-- ============================================================
-- Cache LRU sederhana untuk hasil lookup RIPE Stat API per IP client.
-- Lookup hanya dilakukan saat endpoint /top-clients?resolve=1 dipanggil,
-- TIDAK mempengaruhi proses query DNS normal.

local ripe_cache = {}
local ripe_cache_list = {}
local ripe_cache_size = 5000  -- batas entri cache (LRU eviction)

local function ripe_cache_push(ip, label)
  if ripe_cache[ip] then
    ripe_cache[ip] = label
    return
  end
  if #ripe_cache_list >= ripe_cache_size then
    local oldest = table.remove(ripe_cache_list, 1)
    ripe_cache[oldest] = nil
  end
  ripe_cache[ip] = label
  table.insert(ripe_cache_list, ip)
end

-- Coba load cjson untuk parsing JSON (fallback ke regex jika tidak ada)
local has_cjson, cjson_safe = pcall(require, 'cjson.safe')
if not has_cjson then
  has_cjson, cjson_safe = pcall(require, 'cjson')
end

--- Resolve IP via RIPE Stat API (realtime, dengan cache & timeout)
--- Returns: string label berisi "ASxxxxx | prefix | country | organization"
--- atau nil jika gagal. Untuk IP private, return 'Private IP' langsung.
function resolve_ip_via_ripe(ip)
  -- Skip IPv6
  if ip:find(':') then return 'IPv6 (unresolved)' end

  -- Skip IP private/loopback (langsung label tanpa HTTP call)
  local a, b, c = ip:match('^(%d+)%.(%d+)%.(%d+)%.')
  if a and b and c then
    a, b, c = tonumber(a), tonumber(b), tonumber(c)
    if a == 127 or a == 10 or (a == 172 and b >= 16 and b <= 31) or (a == 192 and b == 168) then
      return 'Private Network'
    end
  end

  if ripe_cache[ip] then return ripe_cache[ip] end

  -- Fetch network info dari RIPE Stat
  local cmd = 'curl -s --connect-timeout 2 -m 4 -A "Mozilla/5.0" "https://stat.ripe.net/data/network-info/data.json?resource=' .. ip .. '" 2>/dev/null'
  local f = io.popen(cmd)
  if not f then return nil end
  local out = f:read('*all')
  f:close()

  if not out or out == '' then return nil end

  local asn = nil
  local prefix = nil
  local country = nil
  local holder = nil

  if has_cjson and cjson_safe then
    local ok, data = pcall(cjson_safe.decode, out)
    if ok and data and data.data then
      prefix = data.data.prefix
      country = data.data.country
      if data.data.asns and #data.data.asns > 0 then
        asn = data.data.asns[1]
      end
    end
    -- Fetch holder via RIPE as-overview (best-effort, ignore error)
    if asn then
      local cmd2 = 'curl -s --connect-timeout 1 -m 2 -A "Mozilla/5.0" "https://stat.ripe.net/data/as-overview/data.json?resource=AS' .. asn .. '" 2>/dev/null'
      local f2 = io.popen(cmd2)
      if f2 then
        local out2 = f2:read('*all')
        f2:close()
        if out2 and out2 ~= '' then
          local ok2, data2 = pcall(cjson_safe.decode, out2)
          if ok2 and data2 and data2.data and data2.data.holder then
            holder = data2.data.holder
          end
        end
      end
    end
  else
    -- Fallback regex parsing (anti cjson missing)
    -- Pola mendukung integer atau string dalam array asns
    asn = out:match('"asns"%s*:%s*%[%s*"?(%d+)"?%s*%]')
       or out:match('"asns"%s*:%s*%[%s*"?(%d+)"?%s*,')
    prefix = out:match('"prefix"%s*:%s*"([^"]+)"')
    country = out:match('"country"%s*:%s*"([^"]+)"')
    if asn then
      local cmd2 = 'curl -s --connect-timeout 1 -m 2 -A "Mozilla/5.0" "https://stat.ripe.net/data/as-overview/data.json?resource=AS' .. asn .. '" 2>/dev/null'
      local f2 = io.popen(cmd2)
      if f2 then
        local out2 = f2:read('*all')
        f2:close()
        if out2 and out2 ~= '' then
          holder = out2:match('"holder"%s*:%s*"([^"]+)"')
        end
      end
    end
  end

  -- Compose label
  local parts = {}
  if asn then table.insert(parts, 'AS' .. asn) end
  if prefix then table.insert(parts, prefix) end
  if country then table.insert(parts, country) end
  if holder and holder ~= '' then
    -- Bersihkan trailing koma / quote
    holder = holder:gsub(',.*$', ''):gsub('"%s*$', ''):gsub('^%s*"', ''):gsub('%s+$', '')
    table.insert(parts, holder)
  end
  local label = table.concat(parts, ' | ')
  if label == '' then return nil end

  ripe_cache_push(ip, label)
  return label
end

--- Top source clients (IP) + resolved ASN/org/country on-the-fly
--- @param n integer jumlah top clients
--- @param resolve boolean jika true, lakukan lookup RIPE Stat + cache untuk data akurat
function get_top_clients(n, resolve)
  local arr = {}
  for k, v in pairs(stats.clients) do
    arr[#arr + 1] = { ip = k, count = v }
  end
  table.sort(arr, function(a, b)
    if a.count ~= b.count then return a.count > b.count end
    return a.ip < b.ip
  end)
  local result = {}
  for i = 1, math.min(n, #arr) do
    local ip = arr[i].ip
    local label
    if resolve then
      label = resolve_ip_via_ripe(ip)
    end
    if not label or label == '' then
      label = lookup_asn(ip)
    end
    if not label or label == '' then
      label = 'Unknown'
    end
    result[i] = {
      rank  = i,
      ip    = ip,
      asn   = label,
      count = arr[i].count,
    }
  end
  return result
end

-- ============================================================
-- [G] RULES: TRACKING (dipanggil dari dnsdist.conf)
-- ============================================================

--- Track SEMUA query yang masuk.
--- Pasang SEBELUM semua rule lain.
--- Return DNSAction.None agar processing lanjut.
function topstats_track_query(dq)
  local qname = dq.qname:toString()
  stats.queries[qname] = (stats.queries[qname] or 0) + 1
  stats.total_queries = stats.total_queries + 1

  -- Client IP tracking (Top Source)
  local ip = dq.remoteaddr:toString()
  stats.clients[ip] = (stats.clients[ip] or 0) + 1

  -- ASN tracking
  local asn_label = lookup_asn(ip)
  if asn_label then
    stats.asn[asn_label] = (stats.asn[asn_label] or 0) + 1
  end

  maybe_cleanup()
  maybe_reload_asn()
  return DNSAction.None
end

--- Track query yang COCOK dengan blacklist (akan di-blokir).
--- Pasang SEBELUM rule blokir aktual (SpoofAction / RCodeAction).
--- Return DNSAction.None agar rule blokir di bawahnya tetap jalan.
function topstats_track_blocked(dq)
  local qname = dq.qname:toString()
  stats.blocked[qname] = (stats.blocked[qname] or 0) + 1
  stats.total_blocked = stats.total_blocked + 1
  
  -- Debug log untuk testing
  print("[TOP-STATS DEBUG] Blocked query detected:", qname, "from", dq.remoteaddr:toString())

  -- ASN tracking untuk blocked
  local ip = dq.remoteaddr:toString()
  local asn_label = lookup_asn(ip)
  if asn_label then
    stats.blocked_asn[asn_label] = (stats.blocked_asn[asn_label] or 0) + 1
  end

  return DNSAction.None
end

-- ============================================================
-- [H] DAFTAR RULES KE DNSDIST
-- ============================================================
-- Rule 1: track semua query (paling awal)
addAction(AllRule(), LuaAction(topstats_track_query))

-- Rule 2: track query yang akan di-blokir
-- Menggunakan kvsRule yang sudah didefinisikan di dnsdist.conf.
-- Jika kvsRule belum ada (misal module di-load sebelum KVS),
-- skip rule ini agar tidak error.
if kvsRule then
  addAction(kvsRule, LuaAction(topstats_track_blocked))
  print("[top-stats] WARNING: Blocked tracking rule added using kvsRule")
else
  print("[top-stats] ERROR: kvsRule not found! Blocked tracking SKIPPED!")
end

-- ============================================================
-- [I] WEB API HANDLERS (registerWebHandler)
-- ============================================================

--- Cek API key. Return true jika diizinkan.
local function check_auth(req)
  if CONFIG.apiKey == '' then return true end
  -- cek header X-API-Key
  if req.headers then
    local key = req.headers['x-api-key'] or req.headers['X-API-Key']
    if key == CONFIG.apiKey then return true end
    -- cek Authorization: Bearer <key>
    local auth = req.headers['authorization'] or req.headers['Authorization']
    if auth and auth:match('^Bearer%s+(.+)$') == CONFIG.apiKey then return true end
  end
  return false
end

--- Parse query parameter ?n=XX dari URL path
local function parse_topn(req)
  local n = CONFIG.defaultTopN
  if req.getvars and req.getvars.n then
    local v = tonumber(req.getvars.n)
    if v and v > 0 then n = math.min(v, 1000) end
  end
  return n
end

--- Parse query parameter ?resolve=1 dari URL path
local function parse_resolve(req)
  if req.getvars and req.getvars.resolve then
    local r = tostring(req.getvars.resolve)
    return r == '1' or r == 'true' or r == 'yes'
  end
  return false
end

--- Kirim response JSON
local function send_json(resp, data, status)
  resp.status = status or 200
  resp.headers = { ['Content-Type'] = 'application/json; charset=utf-8' }
  resp.body = json_encode(data)
end

--- Kirim error 401
local function send_unauthorized(resp)
  send_json(resp, {
    status = 'error',
    error  = 'Unauthorized. Provide X-API-Key header.',
  }, 401)
end

-- ---------- /api/v1/top-queries ----------
registerWebHandler('/api/v1/top-queries', function(req, resp)
  if not check_auth(req) then send_unauthorized(resp) return end
  local n = parse_topn(req)
  send_json(resp, {
    status        = 'ok',
    endpoint      = 'top-queries',
    total_queries = stats.total_queries,
    top           = get_top_n(stats.queries, n),
  })
end)

-- ---------- /api/v1/top-blocked ----------
registerWebHandler('/api/v1/top-blocked', function(req, resp)
  if not check_auth(req) then send_unauthorized(resp) return end
  local n = parse_topn(req)
  send_json(resp, {
    status        = 'ok',
    endpoint      = 'top-blocked',
    total_blocked = stats.total_blocked,
    top           = get_top_n(stats.blocked, n),
  })
end)

-- ---------- /api/v1/top-asn ----------
registerWebHandler('/api/v1/top-asn', function(req, resp)
  if not check_auth(req) then send_unauthorized(resp) return end
  local n = parse_topn(req)
  send_json(resp, {
    status        = 'ok',
    endpoint      = 'top-asn',
    asn_db_loaded = asn_loaded,
    total_queries = stats.total_queries,
    top           = get_top_n(stats.asn, n),
  })
end)

-- ---------- /api/v1/top-blocked-asn ----------
registerWebHandler('/api/v1/top-blocked-asn', function(req, resp)
  if not check_auth(req) then send_unauthorized(resp) return end
  local n = parse_topn(req)
  send_json(resp, {
    status        = 'ok',
    endpoint      = 'top-blocked-asn',
    asn_db_loaded = asn_loaded,
    total_blocked = stats.total_blocked,
    top           = get_top_n(stats.blocked_asn, n),
  })
end)

-- ---------- /api/v1/top-clients (Top Source IP + ASN) ----------
registerWebHandler('/api/v1/top-clients', function(req, resp)
  if not check_auth(req) then send_unauthorized(resp) return end
  local n = parse_topn(req)
  local resolve = parse_resolve(req)
  send_json(resp, {
    status        = 'ok',
    endpoint      = 'top-clients',
    asn_db_loaded = asn_loaded,
    total_queries = stats.total_queries,
    top           = get_top_clients(n, resolve),
  })
end)

-- ---------- /api/v1/top-stats (ringkasan semua) ----------
registerWebHandler('/api/v1/top-stats', function(req, resp)
  if not check_auth(req) then send_unauthorized(resp) return end
  local n = parse_topn(req)
  local resolve = parse_resolve(req)
  local uptime = os.time() - stats.start_time
  send_json(resp, {
    status        = 'ok',
    endpoint      = 'top-stats',
    uptime_seconds = uptime,
    total_queries  = stats.total_queries,
    total_blocked  = stats.total_blocked,
    qps_avg        = (uptime > 0) and (stats.total_queries / uptime) or 0,
    block_rate_pct = (stats.total_queries > 0)
                       and (stats.total_blocked / stats.total_queries * 100) or 0,
    asn_db_loaded  = asn_loaded,
    top_queries    = get_top_n(stats.queries, n),
    top_blocked    = get_top_n(stats.blocked, n),
    top_asn        = get_top_n(stats.asn, n),
    top_blocked_asn = get_top_n(stats.blocked_asn, n),
    top_clients    = get_top_clients(n, resolve),
  })
end)

-- ============================================================
-- [J] LOG STARTUP
-- ============================================================
print('[top-stats] Module loaded. Endpoints:')
print('[top-stats]   GET /api/v1/top-queries?n=50')
print('[top-stats]   GET /api/v1/top-blocked?n=50')
print('[top-stats]   GET /api/v1/top-asn?n=50')
print('[top-stats]   GET /api/v1/top-blocked-asn?n=50')
print('[top-stats]   GET /api/v1/top-clients?n=50')
print('[top-stats]   GET /api/v1/top-stats?n=50')
if asn_loaded then
  if is_bin_db then
    print('[top-stats]   ASN database: ' .. entry_count .. ' entries loaded (binary)')
  else
    print('[top-stats]   ASN database: ' .. #asn_entries .. ' entries loaded (csv)')
  end
else
  print('[top-stats]   ASN database: not loaded (file: ' .. (CONFIG.asnDBPath ~= '' and CONFIG.asnDBPath or 'disabled') .. ')')
end
