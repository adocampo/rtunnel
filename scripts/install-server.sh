#!/usr/bin/env bash
# install-server.sh — Install rtunnel server on Linux with systemd service.
#
# Usage:
#   sudo ./scripts/install-server.sh [OPTIONS]
#
# Options:
#   --listen ADDR        Listen address (default: 0.0.0.0:8443)
#   --mode MODE          tun or socks (default: tun)
#   --ip-pool CIDR       Tunnel IP pool (default: 10.99.0.0/16)
#   --no-auth            Disable SSH authentication
#   --tls-cert PATH      TLS certificate file
#   --tls-key PATH       TLS key file
#   --uninstall          Remove rtunnel server
#
set -euo pipefail

INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/rtunnel"
SERVICE_NAME="rtunnel-server"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
BINARY="rtunnel"

# Defaults
LISTEN="0.0.0.0:8443"
MODE="tun"
IP_POOL="10.99.0.0/16"
NO_AUTH=""
TLS_CERT=""
TLS_KEY=""
UNINSTALL=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --listen)     LISTEN="$2"; shift 2 ;;
        --mode)       MODE="$2"; shift 2 ;;
        --ip-pool)    IP_POOL="$2"; shift 2 ;;
        --no-auth)    NO_AUTH="--no-auth"; shift ;;
        --tls-cert)   TLS_CERT="$2"; shift 2 ;;
        --tls-key)    TLS_KEY="$2"; shift 2 ;;
        --uninstall)  UNINSTALL=true; shift ;;
        *)            echo "Unknown option: $1"; exit 1 ;;
    esac
done

if [[ "$(id -u)" -ne 0 ]]; then
    echo "Error: must run as root (sudo)." >&2
    exit 1
fi

# ─── Uninstall ────────────────────────────────────────────────────────────────
if $UNINSTALL; then
    echo "Stopping and removing ${SERVICE_NAME}..."
    systemctl stop "${SERVICE_NAME}" 2>/dev/null || true
    systemctl disable "${SERVICE_NAME}" 2>/dev/null || true
    rm -f "${SERVICE_FILE}"
    rm -f "${INSTALL_DIR}/${BINARY}"
    rm -rf "${CONFIG_DIR}"
    systemctl daemon-reload
    echo "Done. rtunnel server uninstalled."
    exit 0
fi

# ─── Build ────────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

echo "==> Building rtunnel..."
cd "$PROJECT_DIR"
CGO_ENABLED=0 go build -ldflags "-s -w" -o "bin/${BINARY}" .

# ─── Install binary ──────────────────────────────────────────────────────────
echo "==> Installing binary to ${INSTALL_DIR}/${BINARY}"
install -m 0755 "bin/${BINARY}" "${INSTALL_DIR}/${BINARY}"

# ─── Config directory ─────────────────────────────────────────────────────────
mkdir -p "${CONFIG_DIR}"

# ─── Generate YAML config ─────────────────────────────────────────────────────
CONFIG_FILE="${CONFIG_DIR}/rtunnel.yaml"
echo "==> Writing config to ${CONFIG_FILE}"

cat > "${CONFIG_FILE}" <<YAML
server:
  listen: "${LISTEN}"
  mode: "${MODE}"
  ip_pool: "${IP_POOL}"
YAML

if [[ -n "$NO_AUTH" ]]; then
    echo "  no_auth: true" >> "${CONFIG_FILE}"
else
    echo "  authorized_keys: \"${CONFIG_DIR}/authorized_keys\"" >> "${CONFIG_FILE}"
    if [[ ! -f "${CONFIG_DIR}/authorized_keys" ]]; then
        touch "${CONFIG_DIR}/authorized_keys"
        chmod 600 "${CONFIG_DIR}/authorized_keys"
        echo "    Created ${CONFIG_DIR}/authorized_keys (add client public keys here)"
    fi
fi

if [[ -n "$TLS_CERT" && -n "$TLS_KEY" ]]; then
    cat >> "${CONFIG_FILE}" <<YAML
  tls:
    cert: "${TLS_CERT}"
    key: "${TLS_KEY}"
YAML
fi

chmod 600 "${CONFIG_FILE}"

# ─── Systemd service ─────────────────────────────────────────────────────────
echo "==> Creating systemd service: ${SERVICE_NAME}"
cat > "${SERVICE_FILE}" <<EOF
[Unit]
Description=rtunnel server (reverse TUN/SOCKS tunnel)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/${BINARY} server --config ${CONFIG_FILE}
Restart=on-failure
RestartSec=5
AmbientCapabilities=CAP_NET_ADMIN
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "${SERVICE_NAME}"
systemctl restart "${SERVICE_NAME}"

echo ""
echo "==> rtunnel server installed and running!"
echo "    Binary:  ${INSTALL_DIR}/${BINARY}"
echo "    Service: ${SERVICE_NAME}"
echo "    Config:  ${CONFIG_DIR}/"
echo ""
echo "    Status:  systemctl status ${SERVICE_NAME}"
echo "    Logs:    journalctl -u ${SERVICE_NAME} -f"
echo ""
echo "    Config:  ${CONFIG_FILE}"
echo ""
echo "    Listening on ${LISTEN} (mode=${MODE}, pool=${IP_POOL})"
echo ""
echo "    To change settings, edit ${CONFIG_FILE} and run:"
echo "    sudo systemctl restart ${SERVICE_NAME}"
