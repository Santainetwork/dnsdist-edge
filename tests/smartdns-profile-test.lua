-- Test: plugin reads the node-local SmartDist profile flag and honours it.
-- Node profile file is a tiny Lua file written by the agent:
--   SMARTDIST_ENABLED = true|false
-- Run: lua5.1 tests/smartdns-profile-test.lua
local PLUGIN = arg[1] or "addons/smartdns-plugin.lua"
local fails = 0
local function check(name, cond)
  if cond then print("PASS " .. name) else print("FAIL " .. name); fails = fails + 1 end
end

function warnlog() end
function infolog() end
function errlog() end
function addAction() end
function addResponseAction() end
function addCacheHitResponseAction() end
function newServer() return {} end
function getPool() return {} end
DNSQType = { A = 1, AAAA = 28 }
DNSSection = { Answer = 1 }
DNSResponseAction = { None = 0 }
function AllRule() return {} end
function LuaResponseAction(fn) return fn end
function SuffixMatchNodeRule() return {} end
function SpoofCNAMEAction() return {} end
function newSuffixMatchNode() return { add = function() end, check = function() return false end } end
function newDNSName(s) return s end
function newNetmaskGroup() return { addMask = function() end, match = function() return false end } end
function newCA(ip) return ip end

dofile(PLUGIN)

check("profile flag reader exists", type(smartdns_profile_enabled_from_file) == "function")
if type(smartdns_profile_enabled_from_file) == "function" then
  local tmp = os.tmpname()
  local function write(s) local f = assert(io.open(tmp, "w")); f:write(s); f:close() end

  write("SMARTDIST_ENABLED = false\n")
  check("file enabled=false -> disabled", smartdns_profile_enabled_from_file(tmp) == false)

  write("SMARTDIST_ENABLED = true\n")
  check("file enabled=true -> enabled", smartdns_profile_enabled_from_file(tmp) == true)

  -- Real profiles carry rule lines, not just the flag. Reader must still find
  -- the flag even though the rule functions are stubbed no-ops.
  write("SMARTDIST_ENABLED = true\nsmartdns_ip_set(\"s\", \"/x\")\nsmartdns_ip_rules_alias(\"s\", {\"9.9.9.9\"})\n")
  check("profile with rules -> enabled", smartdns_profile_enabled_from_file(tmp) == true)

  write("this is not lua ((\n")
  check("broken file -> disabled (fail closed)", smartdns_profile_enabled_from_file(tmp) == false)

  -- Flag must be a real line start, not a token inside a comment/string/rule call.
  -- These lock the shape: a future "simplify to substring match" would break them.
  write("-- SMARTDIST_ENABLED = true\n")
  check("flag inside a comment -> disabled", smartdns_profile_enabled_from_file(tmp) == false)
  write("local s = \"SMARTDIST_ENABLED = true\"\n")
  check("flag inside a string -> disabled", smartdns_profile_enabled_from_file(tmp) == false)
  write("smartdns_ip_set(\"SMARTDIST_ENABLED = true\", \"/x\")\n")
  check("flag inside a rule call -> disabled", smartdns_profile_enabled_from_file(tmp) == false)

  -- Only lowercase true enables; anything else fails closed.
  write("SMARTDIST_ENABLED = TRUE\n")
  check("uppercase TRUE -> disabled (fail closed)", smartdns_profile_enabled_from_file(tmp) == false)
  write("SMARTDIST_ENABLED = truex\n")
  check("truex -> disabled (fail closed)", smartdns_profile_enabled_from_file(tmp) == false)

  os.remove(tmp)
  check("missing file -> disabled (fail closed)", smartdns_profile_enabled_from_file(tmp) == false)
end
os.exit(fails == 0 and 0 or 1)
