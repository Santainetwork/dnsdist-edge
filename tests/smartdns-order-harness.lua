-- Harness: stub dnsdist API, load plugin, record addAction registration order.
-- Usage: lua5.1 tests/smartdns-order-harness.lua <plugin_path>
-- Prints one line per registered action in registration order.
local plugin = arg[1] or "addons/smartdns-plugin.lua"
local order = {}
local function rec(kind, ...) order[#order + 1] = kind end
function addAction(rule, action, opts) rec("addAction") end
function addResponseAction(rule, action) rec("addResponseAction") end
function addCacheHitResponseAction(rule, action) rec("addCacheHit") end
function SuffixMatchNodeRule(smn) return {t = "smn"} end
function SpoofCNAMEAction(t) return {t = "cname", v = t} end
function newSuffixMatchNode() return {add = function() end, t = "smn"} end
function newDNSName(s) return s end
function newNetmaskGroup() return {addMask = function() end} end
function newDNSPacketOverlay(d) return {} end
function errlog(m) io.stderr:write("ERR " .. m .. "\n") end
function warnlog(m) end
function infolog(m) end
function getPool() return {} end
function newServer() return {} end
dofile(plugin)
smartdns_cname("-.example.com", "target.example.net.")
for i, k in ipairs(order) do print(i, k) end
