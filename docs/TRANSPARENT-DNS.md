# Transparent DNS on a Trust-NG Edge

Optional MikroTik force-DNS support. The helper is deliberately opt-in: it does not change the host unless `--apply` is explicit.

## Topology

```text
MikroTik force-DNS -> Edge public :53 -> dnsdist

Edge sebagai gateway -> nft TPROXY :53 -> dnsdist-tproxy
                    -> PROXYv2 -> dnsdist 127.0.0.1:5353
```

Pilih salah satu jalur untuk setiap subnet. Jika MikroTik sudah mengubah tujuan DNS ke IP Edge, gunakan mode `off`; TPROXY tidak diperlukan. Gunakan mode `tproxy` hanya ketika paket DNS klien benar-benar dirutekan melewati Edge sebagai gateway.

The nftables chain matches only inbound UDP/TCP port 53 on the selected LAN interface and source IPv4 CIDR. It returns for locally-destined Edge addresses before interception. It never matches the loopback listener, so the `127.0.0.1:5353` path cannot loop through TPROXY.

The helper owns one nftables table, `inet trust_ng_tproxy`, one fwmark route rule, one local route, one sysctl drop-in, one systemd unit, and one dnsdist include. It does not flush or edit unrelated firewall rules. The sysctl drop-in records required persistent values, but the helper never changes live sysctls. Existing values must already be `ip_forward=1` and interface `rp_filter=0` before apply.

## Commands

Plan only, default mode off:

```bash
sudo addons/dnsdist-tproxy.sh
sudo addons/dnsdist-tproxy.sh --mode tproxy --interface eth1 --subnet 192.168.88.0/24
```

Diagnostics:

```bash
sudo addons/dnsdist-tproxy.sh --mode auto --interface eth1 --subnet 192.168.88.0/24
sudo addons/dnsdist-tproxy.sh status
```

`auto` is read-only. It **cannot detect MikroTik rules remotely** and **never enables** TPROXY. Inspect MikroTik directly:

```routeros
/ip firewall nat print detail
/ip dns print
```

Apply:

```bash
sudo addons/dnsdist-tproxy.sh \
  --mode tproxy \
  --interface eth1 \
  --subnet 192.168.88.0/24 \
  --apply
```

Before apply, add the include hook to the main configuration and prepare the kernel prerequisites:

```lua
local tproxyFile = '/etc/dnsdist/tproxy.conf'
local tpf = io.open(tproxyFile, 'r')
if tpf then
  tpf:close()
  dofile(tproxyFile)
end
```

```bash
sudo sysctl -w net.ipv4.ip_forward=1
sudo sysctl -w net.ipv4.conf.eth1.rp_filter=0
```

The helper requires `/usr/local/bin/dnsdist-tproxy`. Its unit listens on `0.0.0.0:53`, forwarding to `127.0.0.1:5353` with PROXY protocol v2 and source `127.0.0.2`. While enabled, the guarded include makes dnsdist release public port 53 and bind only the isolated backend. Removing TPROXY restores the public dnsdist listeners. This preserves the original UDP source port in replies. It runs `dnsdist --check-config` before restarting dnsdist, then validates nftables with `nft -c` before replacing the owned table.

Disable and remove only owned state:

```bash
sudo addons/dnsdist-tproxy.sh --mode off --apply
```

Without `--apply`, `off` prints a plan and does nothing.

## dnsdist include

The generated `/etc/dnsdist/tproxy.conf` contains:

```lua
addLocal('127.0.0.1:5353')
setProxyProtocolACL({'127.0.0.2/32'})
```

Include it from the main dnsdist configuration, after the existing public listeners. Keep the existence guard so `--mode off --apply` can remove the include safely:

```lua
local tproxyFile = '/etc/dnsdist/tproxy.conf'
local tpf = io.open(tproxyFile, 'r')
if tpf then
  tpf:close()
  dofile(tproxyFile)
end
```

`setProxyProtocolACL()` requires PROXYv2 from `127.0.0.2`. While TPROXY is enabled, dnsdist binds only the isolated `127.0.0.1:5353` listener and the proxy owns public port 53. Mode `off` removes the include, then dnsdist restores its normal public IPv4/IPv6 port 53 listeners.

## Kernel and routing details

The owned configuration:

- requires IPv4 forwarding already enabled;
- requires `net.ipv4.conf.<interface>.rp_filter = 0` already set for the selected interface;
- marks intercepted packets `0x1`;
- routes marked packets through local table `130` with `local 0.0.0.0/0 dev lo`;
- uses rule preference `13001`;
- intercepts UDP and TCP DNS only.

The helper canonicalizes the supplied IPv4 CIDR before rendering nftables. Interface names, octets, prefixes, mode values, and required arguments are validated before apply.

## Status and troubleshooting

```bash
sudo addons/dnsdist-tproxy.sh status
sudo dnsdist -c 'showStats()'
sudo dnsdist -c 'showServers()'
sudo tcpdump -ni eth1 'udp port 53 or tcp port 53'
sudo nft list table inet trust_ng_tproxy
ip rule show
ip route show table 130
journalctl -u trust-ng-dnsdist-tproxy.service
```

Evidence that MikroTik force-DNS is active:

1. Client packet capture shows DNS arriving on the selected Edge interface even when the client targets another resolver.
2. nftables counters increase for the owned TPROXY rules.
3. `dnsdist` query and backend counters increase through the isolated listener.
4. MikroTik `/ip firewall nat print stats` shows redirect hits.

No packets or counters means verify the interface, source subnet, MikroTik redirect, and whether clients use encrypted DNS. Do not infer remote MikroTik state from `auto`; remote detection is unavailable by design.

## Safety

- `--apply` is mandatory for changes.
- `auto` never mutates.
- Apply refuses missing dnsdist include hooks or kernel prerequisites.
- Apply validates the new nftables rules before replacing an existing owned table.
- `off` removes only exact owned objects and files.
- `off` does not change live sysctl values.
- Existing non-owned unit, sysctl, or include files are not overwritten or removed.
- The helper does not modify `setup/setup-edge.sh`, the main dnsdist configuration, or unrelated nftables tables.
