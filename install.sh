#!/usr/bin/env bash
#
# install.sh - Install or update alart-service on a Linux system.
# Usage: sudo ./install.sh
#
set -euo pipefail

BINARY_NAME="alart-service"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/alart-service"
SERVICE_FILE="/etc/systemd/system/alart-service.service"
LOG_FILE="/var/log/alart-service.log"
PID_FILE="/var/run/alart-service.pid"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# Check root.
if [[ $EUID -ne 0 ]]; then
    error "This script must be run as root (use sudo)."
    exit 1
fi

# Check OS.
if [[ "$(uname -s)" != "Linux" ]]; then
    error "This service only runs on Linux."
    exit 1
fi

info "Installing alart-service..."

# Build if binary doesn't exist.
if [[ ! -f "./${BINARY_NAME}" ]]; then
    info "Binary not found, building from source..."
    if ! command -v go &>/dev/null; then
        error "Go is not installed. Please install Go 1.21+ first."
        exit 1
    fi
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o "${BINARY_NAME}" ./cmd/alart-service/
    info "Build complete."
fi

# Stop existing service if running.
if systemctl is-active --quiet alart-service 2>/dev/null; then
    warn "Stopping existing alart-service..."
    systemctl stop alart-service
fi

# Install binary.
info "Installing binary to ${INSTALL_DIR}/${BINARY_NAME}"
cp -f "${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
chmod 755 "${INSTALL_DIR}/${BINARY_NAME}"

# Create 'alart' symlink for short CLI usage (alart -t, alart -s reload).
if [[ -L "${INSTALL_DIR}/alart" ]] || [[ ! -e "${INSTALL_DIR}/alart" ]]; then
    ln -sf "${INSTALL_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/alart"
    info "Created symlink: alart → alart-service"
else
    warn "${INSTALL_DIR}/alart already exists and is not a symlink, skipping"
fi

# Create config directory.
mkdir -p "${CONFIG_DIR}"

# Install config (don't overwrite existing).
if [[ ! -f "${CONFIG_DIR}/config.json" ]]; then
    info "Installing sample configuration to ${CONFIG_DIR}/config.json"
    cp deploy/config.sample.json "${CONFIG_DIR}/config.json"
    chmod 600 "${CONFIG_DIR}/config.json"
    warn "⚠️  Edit ${CONFIG_DIR}/config.json and set your Discord webhook URL!"
else
    info "Configuration already exists at ${CONFIG_DIR}/config.json (not overwriting)"
fi

# Install systemd service.
info "Installing systemd service..."
cp deploy/alart-service.service "${SERVICE_FILE}"
systemctl daemon-reload
systemctl enable alart-service

# Create log file.
touch "${LOG_FILE}"
chmod 644 "${LOG_FILE}"

info ""
info "═══════════════════════════════════════════════════════════"
info "  alart-service installed successfully!"
info "═══════════════════════════════════════════════════════════"
info ""
info "  Commands:"
info "    alart -t              Test config syntax"
info "    alart -s reload       Reload config (no restart)"
info "    alart -s stop         Graceful stop"
info ""
info "  Next steps:"
info "    1. Edit config:   sudo nano ${CONFIG_DIR}/config.json"
info "    2. Test config:   alart -t"
info "    3. Set your Discord webhook URL"
info "    4. Start service: sudo systemctl start alart-service"
info "    5. Check status:  sudo systemctl status alart-service"
info "    6. View logs:     sudo journalctl -u alart-service -f"
info ""
