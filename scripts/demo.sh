#!/bin/bash
# Quick manual test: starts server + client + SOCKS proxy + echo server
# then demonstrates connecting through the tunnel.
#
# Usage: ./scripts/demo.sh
#
# Requires: go, nc (netcat), curl or similar SOCKS-capable client
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"

echo "=== Building rtunnel ==="
CGO_ENABLED=0 go build -o bin/rtunnel .

echo ""
echo "=== Starting echo server on :9999 ==="
# Simple echo server using bash
(while true; do
    echo "HELLO from the private machine!" | nc -l -p 9999 -q 1 2>/dev/null || true
done) &
ECHO_PID=$!

echo "=== Starting rtunnel server (no TLS, mode=socks) ==="
bin/rtunnel server --listen :8443 --mode socks --authorized-keys /dev/null -v &
SERVER_PID=$!
sleep 1

echo "=== Starting rtunnel client ==="
bin/rtunnel client --server ws://127.0.0.1:8443 --name demo --expose 9999 --insecure -v &
CLIENT_PID=$!
sleep 1

echo ""
echo "============================================"
echo " rtunnel demo running!"
echo ""
echo " - Echo server:    127.0.0.1:9999"
echo " - rtunnel server: 127.0.0.1:8443"
echo " - Tunnel name:    demo"
echo ""
echo " To test with curl through SOCKS5:"
echo "   curl --socks5 127.0.0.1:1080 http://10.99.0.2:9999"
echo ""
echo " To test with nc through SOCKS5 (ncat):"
echo "   ncat --proxy 127.0.0.1:1080 --proxy-type socks5 10.99.0.2 9999"
echo ""
echo " To SSH through SOCKS5:"
echo "   ssh -o ProxyCommand='ncat --proxy 127.0.0.1:1080 --proxy-type socks5 %h %p' user@10.99.0.2"
echo ""
echo " Press Ctrl+C to stop"
echo "============================================"

cleanup() {
    echo ""
    echo "Stopping..."
    kill $ECHO_PID $SERVER_PID $CLIENT_PID 2>/dev/null || true
    wait 2>/dev/null
    echo "Done."
}
trap cleanup EXIT INT TERM

wait
