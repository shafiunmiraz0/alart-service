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
        info "Go is not installed. Attempting to install..."
        if command -v apt-get &>/dev/null; then
            apt-get update >/dev/null 2>&1
            apt-get install -y golang-go >/dev/null 2>&1 || { error "Failed to install golang-go via apt."; exit 1; }
        elif command -v yum &>/dev/null; then
            yum install -y golang >/dev/null 2>&1 || { error "Failed to install golang via yum."; exit 1; }
        elif command -v dnf &>/dev/null; then
            dnf install -y golang >/dev/null 2>&1 || { error "Failed to install golang via dnf."; exit 1; }
        else
            error "No supported package manager found. Please install Go 1.21+ manually."
            exit 1
        fi
        info "Go installed successfully."
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

# Create state directory (for boot/reboot detection).
mkdir -p /var/lib/alart-service
info "State directory created at /var/lib/alart-service/"

# Set up auditd for /etc file-change attribution.
# This allows the service to identify exactly WHO modified files in /etc,
# rather than listing all logged-in users.
info "Setting up audit rules for /etc monitoring..."
if ! command -v auditctl &>/dev/null; then
    info "Installing auditd..."
    if command -v apt-get &>/dev/null; then
        apt-get install -y auditd audispd-plugins >/dev/null 2>&1 || warn "Could not install auditd (apt)"
    elif command -v yum &>/dev/null; then
        yum install -y audit >/dev/null 2>&1 || warn "Could not install auditd (yum)"
    elif command -v dnf &>/dev/null; then
        dnf install -y audit >/dev/null 2>&1 || warn "Could not install auditd (dnf)"
    else
        warn "Could not install auditd automatically. Install it manually for accurate user attribution."
    fi
fi

if command -v auditctl &>/dev/null; then
    # Remove old file-watch rule if it exists (from previous installs).
    auditctl -W /etc -p wa -k alart-etc-monitor 2>/dev/null || true

    # Use a syscall-based rule with dir= filter — this is GUARANTEED to
    # recursively monitor all subdirectories under /etc (unlike -w which
    # may only watch the top-level directory on some kernels).
    AUDIT_RULE="-a always,exit -F dir=/etc -F perm=wa -k alart-etc-monitor"
    AUDIT_RULES_FILE="/etc/audit/rules.d/alart-etc.rules"

    # Add rule to live kernel.
    auditctl ${AUDIT_RULE} 2>/dev/null || true

    # Start building the persistent rules file.
    echo "${AUDIT_RULE}" > "${AUDIT_RULES_FILE}"

    # Also add audit rules for symlink TARGETS under /etc.
    # Example: /etc/openresty → /usr/local/openresty/nginx
    # Without this, auditd records events under the real path but our
    # ausearch queries for /etc/... would miss them.
    for link in /etc/*/; do
        if [[ -L "${link%/}" ]]; then
            real_target=$(readlink -f "${link%/}")
            if [[ -d "${real_target}" ]]; then
                SYMLINK_RULE="-a always,exit -F dir=${real_target} -F perm=wa -k alart-etc-monitor"
                auditctl ${SYMLINK_RULE} 2>/dev/null || true
                echo "${SYMLINK_RULE}" >> "${AUDIT_RULES_FILE}"
                info "Added audit rule for symlink target: ${link%/} → ${real_target}"
            fi
        fi
    done

    chmod 640 "${AUDIT_RULES_FILE}"

    # Restart auditd to pick up the rules.
    systemctl restart auditd 2>/dev/null || service auditd restart 2>/dev/null || true
    info "Audit rule installed: watching /etc recursively for write/attribute changes"
else
    warn "auditd not available. /etc user detection will use fallback methods (lsof, /proc)."
fi

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
