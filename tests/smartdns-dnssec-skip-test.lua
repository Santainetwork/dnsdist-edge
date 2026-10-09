-- Unit test: DNSSEC skip for SmartDist alias rewrite.
-- Run: lua5.1 tests/smartdns-dnssec-skip-test.lua
-- Contract: a response that carries an RRSIG in its Answer section must NOT be rewritten.
local PLUGIN = arg[1] or "addons/smartdns-plugin.lua"
local fails = 0
local function check(name, cond)
  if cond then print("PASS " .. name) else print("FAIL " .. name); fails = fails + 1 end
end

-- Minimal dnsdist stubs so the plugin loads outside dnsdist.
function warnlog() end
function infolog() end
function errlog() end
function addAction() end
function addResponseAction() end
function addCacheHitResponseAction() end
function newServer() return {} end
function getPool() return {} end

-- Load the pure helper the plugin exposes for this check.
dofile(PLUGIN)
check("helper smartdns_has_rrsig exists", type(smartdns_has_rrsig) == "function")
if type(smartdns_has_rrsig) == "function" then
  check("signed answer -> true",   smartdns_has_rrsig({ {qtype = 1}, {qtype = 46} }) == true)
  check("unsigned answer -> false", smartdns_has_rrsig({ {qtype = 1}, {qtype = 1} }) == false)
  check("empty answer -> false",    smartdns_has_rrsig({}) == false)
end
os.exit(fails == 0 and 0 or 1)
