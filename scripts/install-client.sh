#!/usr/bin/env bash
# install-client.sh — Install rtunnel client on macOS or Linux.
#
# On Linux:  installs binary + systemd service
# On macOS:  installs binary + launchd plist (runs at boot with sudo)
#
# Usage:
#   sudo ./scripts/install-client.sh [OPTIONS]
#
# Options:
#   --server URL         Server address (required, e.g., ws://192.168.1.10:8443)
#   --name NAME          Tunnel name (required, e.g., my-macbook)
#   --tun                Enable TUN mode (default: true)
#   --no-tun             Disable TUN mode (SOCKS only, no sudo needed)
#   --expose PORTS       Comma-separated ports to expose (default: 22)
#   --no-auth            Disable SSH authentication
#   --insecure           Skip TLS verification
#   --ssh-key PATH       SSH private key path (default: ssh-agent)
#   --uninstall          Remove rtunnel client
#
set -euo pipefail

BINARY="rtunnel"
OS="$(uname -s)"

# Paths per OS
case "$OS" in
    Linux)
        INSTALL_DIR="/usr/local/bin"
        CONFIG_DIR="/etc/rtunnel"
        SERVICE_NAME="rtunnel-client"
        ;;
    Darwin)
        INSTALL_DIR="/usr/local/bin"
        CONFIG_DIR="/etc/rtunnel"
        PLIST_LABEL="com.rtunnel.client"
        PLIST_PATH="/Library/LaunchDaemons/${PLIST_LABEL}.plist"
        ;;
    *)
        echo "Unsupported OS: $OS" >&2
        exit 1
        ;;
esac

# Defaults
SERVER=""
NAME=""
TUN_MODE=true
EXPOSE="22"
NO_AUTH=""
INSECURE=""
SSH_KEY=""
UNINSTALL=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --server)     SERVER="$2"; shift 2 ;;
        --name)       NAME="$2"; shift 2 ;;
        --tun)        TUN_MODE=true; shift ;;
        --no-tun)     TUN_MODE=false; shift ;;
        --expose)     EXPOSE="$2"; shift 2 ;;
        --no-auth)    NO_AUTH="--no-auth"; shift ;;
        --insecure)   INSECURE="--insecure"; shift ;;
        --ssh-key)    SSH_KEY="$2"; shift 2 ;;
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
    echo "Removing rtunnel client..."
    case "$OS" in
        Linux)
            systemctl stop "${SERVICE_NAME}" 2>/dev/null || true
            systemctl disable "${SERVICE_NAME}" 2>/dev/null || true
            rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
            systemctl daemon-reload
            ;;
        Darwin)
            launchctl bootout system "${PLIST_PATH}" 2>/dev/null || true
            rm -f "${PLIST_PATH}"
            ;;
    esac
    rm -f "${INSTALL_DIR}/${BINARY}"
    rm -rf "${CONFIG_DIR}"
    echo "Done. rtunnel client uninstalled."
    exit 0
fi

# Validate required args
if [[ -z "$SERVER" || -z "$NAME" ]]; then
    echo "Error: --server and --name are required." >&2
    echo "Usage: sudo $0 --server ws://192.168.1.10:8443 --name my-machine" >&2
    exit 1
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
mkdir -p "${CONFIG_DIR}"

# ─── Generate YAML config ─────────────────────────────────────────────────────
CONFIG_FILE="${CONFIG_DIR}/rtunnel.yaml"
echo "==> Writing config to ${CONFIG_FILE}"

# Build expose list as YAML array
EXPOSE_YAML=""
IFS=',' read -ra PORTS <<< "$EXPOSE"
for port in "${PORTS[@]}"; do
    EXPOSE_YAML="${EXPOSE_YAML}\n    - ${port}"
done

cat > "${CONFIG_FILE}" <<YAML
client:
  server: "${SERVER}"
  name: "${NAME}"
  expose:$(echo -e "${EXPOSE_YAML}")
  reconnect: true
  reconnect_interval: "5s"
YAML

if $TUN_MODE; then
    echo "  tun: true" >> "${CONFIG_FILE}"
fi
if [[ -n "$NO_AUTH" ]]; then
    echo "  no_auth: true" >> "${CONFIG_FILE}"
fi
if [[ -n "$INSECURE" ]]; then
    echo "  insecure: true" >> "${CONFIG_FILE}"
fi
if [[ -n "$SSH_KEY" ]]; then
    echo "  ssh_key: \"${SSH_KEY}\"" >> "${CONFIG_FILE}"
fi

chmod 644 "${CONFIG_FILE}"

# ─── Install service per OS ───────────────────────────────────────────────────
case "$OS" in
    Linux)
        SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
        echo "==> Creating systemd service: ${SERVICE_NAME}"
        cat > "${SERVICE_FILE}" <<EOF
[Unit]
Description=rtunnel client (reverse tunnel to ${SERVER})
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/${BINARY} client --config ${CONFIG_FILE}
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
        echo "==> rtunnel client installed and running!"
        echo "    Status:  systemctl status ${SERVICE_NAME}"
        echo "    Logs:    journalctl -u ${SERVICE_NAME} -f"
        ;;

    Darwin)
        echo "==> Creating launchd service: ${PLIST_LABEL}"
        cat > "${PLIST_PATH}" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${PLIST_LABEL}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${INSTALL_DIR}/${BINARY}</string>
        <string>client</string>
        <string>--config</string>
        <string>${CONFIG_FILE}</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/rtunnel-client.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/rtunnel-client.log</string>
</dict>
</plist>
EOF
        # Load the service
        launchctl bootout system "${PLIST_PATH}" 2>/dev/null || true
        launchctl bootstrap system "${PLIST_PATH}"
        echo ""
        echo "==> rtunnel client installed and running!"
        echo "    Plist:   ${PLIST_PATH}"
        echo "    Logs:    /var/log/rtunnel-client.log"
        echo "    Stop:    sudo launchctl bootout system ${PLIST_PATH}"
        echo "    Start:   sudo launchctl bootstrap system ${PLIST_PATH}"
        ;;
esac

echo ""
echo "    Binary:  ${INSTALL_DIR}/${BINARY}"
echo "    Config:  ${CONFIG_FILE}"
echo "    Server:  ${SERVER}"
echo "    Name:    ${NAME}"
echo "    TUN:     ${TUN_MODE}"
echo "    Expose:  ${EXPOSE}"
echo ""
echo "    To change settings, edit ${CONFIG_FILE} and restart the service."
