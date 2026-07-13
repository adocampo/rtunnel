# rtunnel

Reverse network tunnel — expose private machines to your network.

## Overview

`rtunnel` creates reverse network tunnels. A machine inside a private network
(WSL, Docker container, cloud VM, macOS behind firewall) initiates an outbound WebSocket
connection to your server, giving you transparent IP-level access via a TUN interface
or a SOCKS5 proxy.

## Quick Start

### 1. Start the server (Linux, requires root/CAP_NET_ADMIN)

```bash
sudo rtunnel server \
  --mode tun \
  --listen 192.168.1.10:8443 \
  --no-auth \
  -v
```

This creates a `rtun0` interface with IP `10.99.0.1/16` and listens for client connections.

Production flags:
```bash
sudo rtunnel server \
  --mode tun \
  --listen 192.168.1.10:8443 \
  --tls-cert /etc/rtunnel/cert.pem \
  --tls-key /etc/rtunnel/key.pem \
  --authorized-keys ~/.ssh/authorized_keys \
  -v
```

Alternatively, grant capabilities instead of running as root:
```bash
sudo setcap cap_net_admin+ep ./bin/rtunnel
./bin/rtunnel server --mode tun --listen 192.168.1.10:8443 ...
```

### 2. Start the client (Linux, macOS, WSL, Docker)

```bash
sudo rtunnel client \
  --server ws://192.168.1.10:8443 \
  --name my-machine \
  --tun \
  --expose 22 \
  --no-auth \
  -v
```

This creates a local TUN interface (`utun` on macOS, `tun` on Linux) and assigns
an IP from the server's pool (e.g., `10.99.0.2/16`).

Production flags:
```bash
sudo rtunnel client \
  --server wss://192.168.1.10:8443 \
  --name my-machine \
  --tun \
  --expose 22,80,443 \
  -v
```

#### Exposing additional ports

The `--expose` flag controls which local ports are accessible through the tunnel.
Specify a comma-separated list of ports:

```bash
# Expose SSH only
--expose 22

# Expose SSH + HTTP + HTTPS
--expose 22,80,443

# Expose SSH + LMStudio API (default port 1234)
--expose 22,1234
```

After connecting, remote machines can reach these services via the tunnel IP:
```bash
# SSH into the client machine
ssh user@10.99.0.2

# Query LMStudio API running on the client
curl http://10.99.0.2:1234/v1/models
```

> **Note:** `sudo` is required on the client when using `--tun` (creates a network
> interface). Without `--tun`, the client runs unprivileged and only supports SOCKS
> mode forwarding.

### 3. Configure LAN routing

For other machines on your network to reach tunnel clients (e.g., `10.99.0.2`),
they need a route to `10.99.0.0/16` via the server's LAN IP.

#### Option A: Static route on the router (recommended)

Add a single route on your router so all LAN devices automatically know how to
reach the tunnel network.

| Network/Host IP | Netmask     | Gateway       | Metric | Interface |
|-----------------|-------------|---------------|--------|-----------|
| 10.99.0.0       | 255.255.0.0 | 192.168.1.10  | 1      | LAN       |

Example on an ASUS router (Asuswrt-Merlin):

1. Go to **LAN → Route**
2. Set *Enable static routes* to **Yes**
3. Add the route as shown above
4. Click **Apply**

#### Option B: Static route per machine (CLI)

Linux:
```bash
sudo ip route add 10.99.0.0/16 via 192.168.1.10
```

macOS:
```bash
sudo route -n add -net 10.99.0.0/16 192.168.1.10
```

Windows (cmd as Administrator):
```cmd
route add 10.99.0.0 mask 255.255.0.0 192.168.1.10
```

#### Server-side forwarding (required)

The server must forward packets between its LAN interface and the tunnel interface.
With Docker's default iptables (policy DROP on FORWARD), add:

```bash
sudo iptables -I FORWARD 1 -i br0 -o rtun0 -j ACCEPT
sudo iptables -I FORWARD 1 -i rtun0 -o br0 -j ACCEPT
```

Replace `br0` with your LAN interface name (`eth0`, `enp6s0`, etc.).

### 4. Verify connectivity

From the server:
```bash
ping 10.99.0.2          # may fail if client has ICMP firewall
ssh user@10.99.0.2      # TCP should work
```

From any LAN machine (after routing is set up):
```bash
ssh user@10.99.0.2
```

## Multiple Clients

Multiple clients can connect simultaneously. The server assigns each one a
unique IP from the pool:

```
Client "wsl"       → 10.99.0.2
Client "macbook"   → 10.99.0.3
Client "docker-ci" → 10.99.0.4
```

All clients can see each other automatically — traffic between them is routed
through the server's TUN interface:

```
macbook (10.99.0.3) → utun → WebSocket → server rtun0 → WebSocket → tun → wsl (10.99.0.2)
```

No extra configuration needed. Just start additional clients with different
`--name` values:

```bash
# On machine A
sudo rtunnel client --server ws://192.168.1.10:8443 --name laptop --tun --expose 22 --no-auth

# On machine B
sudo rtunnel client --server ws://192.168.1.10:8443 --name desktop --tun --expose 22,1234 --no-auth
```

From any client or LAN machine:
```bash
ssh user@10.99.0.2    # reach machine A
ssh user@10.99.0.3    # reach machine B
```

## Static IP Assignment

By default, the server remembers which IP it assigned to each client name
("sticky IPs"). On reconnection, the same client always gets the same IP —
no configuration needed.

If you want to explicitly request a specific IP:

```bash
sudo rtunnel client --server ws://192.168.1.10:8443 --name my-machine --tun --request-ip 10.99.0.50
```

Or in the YAML config:
```yaml
client:
  server: "ws://192.168.1.100:8444"
  name: "my-machine"
  request_ip: "10.99.0.50"
  tun: true
  expose:
    - 22
```

IP assignment priority:
1. Client-requested IP (`--request-ip`) — if available in the pool
2. Sticky IP — last IP used by this `--name` (server remembers across reconnects)
3. Next available from pool — sequential allocation

## Status & Health Check

Check the full state of the tunnel with a single command:

```bash
rtunnel status
```

Output:
```
rtunnel status
────────────────────────────────────────────────────────────
  Config:    /etc/rtunnel/rtunnel.yaml ✓
  Server:    ws://192.168.1.100:8444
  Name:      mac-client
  Expose:    [22 1234]
  TUN:       true

  Service:
    com.rtunnel.client: running ✓

  TUN Interface:
    utun4: 10.99.0.2/16 ✓

  Server:    http://192.168.1.100:8444 ✓

  Connected Clients: 1
  ID    NAME                  TUNNEL IP           REMOTE ADDR               EXPOSE PORTS
  ──    ────                  ─────────           ───────────               ────────────
  1     mac-client            10.99.0.2           192.168.1.155:49882       22, 1234
```

The status command:
- Validates config file (detects missing fields, wrong mode)
- Checks service state (systemd on Linux, launchd on macOS)
- Shows TUN interface and assigned IP
- Tests server connectivity
- Lists all connected clients with their IPs and exposed ports

Reads the server address from the config file automatically — no flags needed.

## Updating

To update an existing installation (preserves configuration):

```bash
cd /path/to/rtunnel
make update
```

This pulls the latest code, rebuilds the binary, and restarts the service.
The YAML config is never touched during updates.

## Use Cases

- **WSL**: Access your WSL instance from any machine on your LAN via SSH
- **macOS**: Expose a Mac behind a corporate firewall to your home network
- **Docker**: SSH into containers without `docker exec`
- **Remote VMs**: Access machines behind NAT/firewalls
- **IoT/Edge**: Reach devices that can only make outbound connections

## Installation

```bash
# From source
make build

# Or cross-compile for all platforms
make release

# Or install via go
go install github.com/malevolent/rtunnel@latest
```

## Architecture

```
[LAN machines]                [Server (Linux)]                [Client (any OS)]
    │                              │                                │
    │  route 10.99.0.0/16    ┌─────┴─────┐                        │
    ├─────── via srv ───────►│  rtun0     │◄── WebSocket ──────────┤
    │                        │ 10.99.0.1  │                   utun/tun
    │                        └────────────┘                  10.99.0.2
    │
    └──► ssh user@10.99.0.2
```

- **Server** creates `rtun0`, assigns IPs, routes packets to/from clients
- **Client** creates a local TUN device and forwards IP packets over WebSocket
- **LAN machines** reach clients via static route through the server

## Modes

### TUN Mode (default, requires root on both sides)

Creates a real network interface on server and client. The client gets a routable
IP visible to the entire LAN (with proper routing configured).

Supported platforms: Linux, macOS (utun), WSL2.

### SOCKS5 Mode (unprivileged fallback)

No TUN interfaces created. The server starts a local SOCKS5 proxy that forwards
TCP connections through the tunnel. Useful when root is not available.

```bash
# Server
rtunnel server --mode socks --listen :8443 --socks-listen 127.0.0.1:1080 --no-auth

# Client (no sudo needed)
rtunnel client --server ws://server:8443 --name my-machine --no-auth

# Use the proxy
curl --socks5-hostname 127.0.0.1:1080 http://target:80
ssh -o ProxyCommand='nc -x 127.0.0.1:1080 %h %p' user@target
```

## Authentication

Uses SSH keys via `ssh-agent`. The server checks the client's public key
against `~/.ssh/authorized_keys` (or a configured path).

For testing, use `--no-auth` on both server and client.

## Configuration

Instead of passing flags on every invocation, you can use a YAML config file.
rtunnel automatically looks for `rtunnel.yaml` in these paths (in order):

1. `./rtunnel.yaml` (current directory)
2. `~/.config/rtunnel/rtunnel.yaml`
3. `/etc/rtunnel/rtunnel.yaml`

Or specify a custom path with `--config /path/to/config.yaml`.

Server example (`/etc/rtunnel/rtunnel.yaml`):
```yaml
server:
  listen: "192.168.1.100:8444"
  mode: "tun"
  ip_pool: "10.99.0.0/16"
  authorized_keys: "/etc/rtunnel/authorized_keys"
```

Client example (`/etc/rtunnel/rtunnel.yaml`):
```yaml
client:
  server: "ws://192.168.1.100:8444"
  name: "mac-client"
  expose:
    - 22
    - 1234
  reconnect: true
  reconnect_interval: "5s"
  insecure: false
```

Then just run `rtunnel server` or `rtunnel client` with no extra flags.

CLI flags take priority over the YAML file. Environment variables with the
`RTUNNEL_` prefix also work (e.g., `RTUNNEL_SERVER_LISTEN=:8444`).

See `rtunnel.example.yaml` for a full reference with all available options.

## Building

```bash
make build          # local binary
make release        # cross-compile all platforms
make docker         # Docker image for client
make test           # run tests
make update         # pull + rebuild + restart service (preserves config)
```

## Platform Notes

### macOS

- TUN mode uses the native `utun` interface (no third-party kext needed)
- Requires `sudo` for interface creation
- Corporate MDM may block incoming ICMP (Stealth Mode); TCP still works
- The client **automatically configures `pf` redirect rules** for all exposed
  ports on each connection, so incoming traffic on the tunnel bypasses the
  Application Firewall — no manual setup needed
- If needed manually:
  ```bash
  echo 'rdr on utunX proto tcp from any to 10.99.0.2 port 22 -> 127.0.0.1 port 22' \
    | sudo pfctl -a "com.apple/rtunnel" -f -
  ```

### Linux / WSL

- TUN mode uses `/dev/net/tun` (standard kernel TUN/TAP)
- Requires `root` or `CAP_NET_ADMIN`
- No firewall workarounds needed in most cases

## License

MIT
