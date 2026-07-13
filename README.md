# rtunnel

Reverse network tunnel — expose private machines to your network.

## Overview

`rtunnel` creates reverse network tunnels. A machine inside a private network
(WSL, Docker container, cloud VM, Firewalled machine) initiates an outbound WebSocket connection
to your machine, giving you transparent IP-level access via a TUN interface
or a SOCKS5 proxy.

## Quick Start

### On your machine (server)

```bash
rtunnel server --listen :8443 --authorized-keys ~/.ssh/authorized_keys
```

### On the private machine (client)

```bash
rtunnel client --server wss://your-pc:8443 --name wsl --expose 22,80
```

### Result

Your machine now has a route to the private machine. From any machine on your LAN:
```bash
ssh user@10.99.0.2   # IP assigned by rtunnel
```

## Use Cases

- **WSL**: Access your WSL instance from any machine on your LAN via SSH
- **Docker**: SSH into containers without `docker exec`
- **Remote VMs**: Access machines behind NAT/firewalls
- **IoT/Edge**: Reach devices that can only make outbound connections

## Installation

```bash
# From source
go install github.com/malevolent/rtunnel@latest

# Or download a binary from releases
```

## Architecture

```
[Your Machine]                        [Private Machine]
rtunnel server ←── WSS/HTTPS ──── rtunnel client
    │                                    │
 tun0 (10.99.0.1)                    proxies to localhost:22
  + proxy ARP                        (no root needed)
    │
[LAN] → can reach 10.99.0.2
```

The **client** is ultra-lightweight:
- Static binary, no dependencies
- No TUN device needed on the client side
- No root/admin privileges required
- Runs in Docker containers (FROM scratch)

The **server** creates the network interfaces:
- TUN device with assigned IP
- Proxy ARP for LAN visibility (optional)
- Route management

## Modes

### TUN Mode (default, requires root on server)
Creates a real network interface. The private machine gets a routable IP.
Other machines on your LAN can reach it (via proxy ARP).

### SOCKS5 Mode (unprivileged fallback)
Starts a local SOCKS5 proxy. Connect with:
```bash
ssh -o ProxyCommand='nc -x 127.0.0.1:1080 %h %p' user@remote
```

## Authentication

Uses SSH keys via `ssh-agent`. The server checks the client's public key
against `~/.ssh/authorized_keys` (or a configured path).

## Configuration

CLI flags or YAML config file. See `rtunnel.example.yaml`.

## Building

```bash
make build          # local binary
make release        # cross-compile all platforms
make docker         # Docker image for client
make test           # run tests
```

## License

MIT
