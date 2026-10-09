-- Integration test: DNSSEC skip guards the alias rewrite path.
-- Run: lua5.1 tests/smartdns-dnssec-skip-integration.lua
-- Stubs a DNS packet overlay and drives the LuaResponseAction callback that
-- smartdns_ip_rules_alias registers, once with an RRSIG and once without.
local PLUGIN = arg[1] or "addons/smartdns-plugin.lua"
local fails = 0
local function check(name, cond)
  if cond then print("PASS " .. name) else print("FAIL " .. name); fails = fails + 1 end
end

-- Capture the response callback the plugin registers.
local captured = nil
function addResponseAction(rule, action) captured = action end
function addAction() end
function addCacheHitResponseAction() end
function warnlog() end
function infolog(m) end
function errlog(m) print("errlog: " .. tostring(m)) end
function newServer() return {} end
function getPool() return {} end
function newCA(ip) return ip end
local nmg = { match = function() return true end }
function newNetmaskGroup() return nmg end

-- dnsdist enum stubs used by the plugin.
DNSQType = { A = 1, AAAA = 28 }
DNSSection = { Answer = 1 }
DNSResponseAction = { None = 0 }
function AllRule() return {} end
function LuaResponseAction(fn) return fn end
function SuffixMatchNodeRule() return {} end
function SpoofCNAMEAction() return {} end
function newSuffixMatchNode() return { add = function() end, check = function() return false end } end
function newDNSName(s) return s end

dofile(PLUGIN)
smartdns_nmg["test-set"] = nmg
-- Enable SmartDist for this node: write a profile flag pointing at a temp file.
local prof = os.tmpname()
local pf = assert(io.open(prof, "w")); pf:write("SMARTDIST_ENABLED = true\n"); pf:close()
smartdns_profile_path = prof
smartdns_ip_rules_alias("test-set", { "10.0.0.9" })

check("response action captured", type(captured) == "function")
if type(captured) ~= "function" then os.exit(1) end

-- Build a stub driver around a fake packet with one A record (4-byte rdata).
local function make_overlay(types)
  return {
    getRecordsCountInSection = function() return #types end,
    getRecord = function(_, i)
      return { type = types[i + 1], contentLength = 4, contentOffset = 40 }
    end,
  }
end
-- Fake "10.0.0.1" rdata bytes at offset 40.
local rdata = string.rep(" ", 40) .. string.char(10, 0, 0, 1)
local state = { set = false }
local dr = {
  qtype = DNSQType.A,
  qname = "x.example.",
  getContent = function() return rdata end,
  setContent = function(_, c) state.set = true end,
}

-- Case 1: answer carries RRSIG (type 46) -> must NOT rewrite.
function newDNSPacketOverlay() return make_overlay({1, 46}) end
captured(dr)
check("signed answer not rewritten", state.set == false)

-- Case 2: unsigned answer -> rewrite happens.
state.set = false
function newDNSPacketOverlay() return make_overlay({1}) end
captured(dr)
check("unsigned answer rewritten", state.set == true)

-- Case 3: profile disabled -> no rewrite even for an unsigned answer.
state.set = false
local pf2 = assert(io.open(prof, "w")); pf2:write("SMARTDIST_ENABLED = false\n"); pf2:close()
captured(dr)
check("profile disabled -> not rewritten", state.set == false)

os.remove(prof)
os.exit(fails == 0 and 0 or 1)
