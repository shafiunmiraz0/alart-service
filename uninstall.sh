#!/usr/bin/env bash
#
# uninstall.sh - Completely remove alart-service from the system.
# Usage: sudo ./uninstall.sh [--purge]
#
#   --purge    Also remove config files and logs (default: keep them)
#
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }
step()  { echo -e "${CYAN}  →${NC} $*"; }

PURGE=false
for arg in "$@"; do
    case "$arg" in
        --purge) PURGE=true ;;
        -h|--help)
            echo "Usage: sudo ./uninstall.sh [--purge]"
            echo ""
            echo "  --purge    Also remove config files (/etc/alart-service) and logs"
            echo "             Without --purge, config and logs are preserved."
            exit 0
            ;;
        *)
            error "Unknown option: $arg"
            echo "Usage: sudo ./uninstall.sh [--purge]"
            exit 1
            ;;
    esac
done

# Check root.
if [[ $EUID -ne 0 ]]; then
    error "This script must be run as root (use sudo)."
    exit 1
fi

echo ""
echo -e "${RED}═══════════════════════════════════════════════════════════${NC}"
echo -e "${RED}  Uninstalling alart-service${NC}"
echo -e "${RED}═══════════════════════════════════════════════════════════${NC}"
echo ""

# --- Step 1: Stop the service ---
if systemctl is-active --quiet alart-service 2>/dev/null; then
    info "Stopping alart-service..."
    systemctl stop alart-service
    step "Service stopped"
else
    info "Service is not running"
fi

# --- Step 2: Disable the service ---
if systemctl is-enabled --quiet alart-service 2>/dev/null; then
    info "Disabling alart-service..."
    systemctl disable alart-service
    step "Service disabled"
else
    info "Service is not enabled"
fi

# --- Step 3: Remove systemd unit file ---
if [[ -f /etc/systemd/system/alart-service.service ]]; then
    info "Removing systemd service file..."
    rm -f /etc/systemd/system/alart-service.service
    systemctl daemon-reload
    step "Removed /etc/systemd/system/alart-service.service"
fi

# --- Step 4: Remove binary ---
if [[ -f /usr/local/bin/alart-service ]]; then
    info "Removing binary..."
    rm -f /usr/local/bin/alart-service
    step "Removed /usr/local/bin/alart-service"
fi

# --- Step 5: Remove symlink ---
if [[ -L /usr/local/bin/alart ]]; then
    info "Removing alart symlink..."
    rm -f /usr/local/bin/alart
    step "Removed /usr/local/bin/alart"
fi

# --- Step 6: Remove PID file ---
if [[ -f /var/run/alart-service.pid ]]; then
    info "Removing PID file..."
    rm -f /var/run/alart-service.pid
    step "Removed /var/run/alart-service.pid"
fi

# --- Step 7: Config and logs (only with --purge) ---
if [[ "$PURGE" == true ]]; then
    if [[ -d /etc/alart-service ]]; then
        info "Purging configuration..."
        rm -rf /etc/alart-service
        step "Removed /etc/alart-service/"
    fi
    if [[ -f /var/log/alart-service.log ]]; then
        info "Purging log file..."
        rm -f /var/log/alart-service.log
        step "Removed /var/log/alart-service.log"
    fi
else
    echo ""
    if [[ -d /etc/alart-service ]]; then
        warn "Config preserved at /etc/alart-service/"
        warn "  To remove: sudo rm -rf /etc/alart-service"
    fi
    if [[ -f /var/log/alart-service.log ]]; then
        warn "Log file preserved at /var/log/alart-service.log"
        warn "  To remove: sudo rm -f /var/log/alart-service.log"
    fi
    warn "Re-run with --purge to remove config and logs too"
fi

echo ""
echo -e "${GREEN}═══════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  alart-service uninstalled successfully!${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════${NC}"
echo ""
