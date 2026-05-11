# 🚨 alart-service

A **lightweight, zero-dependency** Linux system monitoring service that sends threshold alerts to Discord via webhooks.

Built in pure Go — reads directly from `/proc` and `/sys`, no cgo, no external libraries.

## Features

| Feature | Description |
|---|---|
| **CPU Monitoring** | Alerts when CPU usage exceeds threshold |
| **RAM Monitoring** | Tracks real memory usage (excludes buffers/cache) |
| **Disk Usage** | Monitors all mounted partitions |
| **Disk I/O** | Tracks read/write rates in MB/s |
| **Network Bandwidth** | Monitors RX/TX rates across all interfaces |
| **/etc Watcher** | Real-time inotify alerts on file changes in `/etc` |
| **Alert Cooldown** | Prevents spam with configurable cooldown per metric |
| **Config Test** | `alart -t` validates config syntax like nginx |
| **Live Reload** | `alart -s reload` applies config changes without restart |
| **VM Reboot Detection** | Alerts on unexpected reboots vs clean restarts |
| **K8s Cert Monitor** | Opt-in: alerts when Kubernetes certificates approach expiry |
| **Systemd Ready** | Ships with unit file, runs as a proper service |

## Quick Start

### 1. Build

```bash
# AMD64 (most servers)
make build

# ARM64 (Raspberry Pi, etc.)
make build-arm
```

### 2. Install

```bash
sudo make install
```

### 3. Configure

Edit `/etc/alart-service/config.json` and set your Discord webhook URL:

```bash
sudo nano /etc/alart-service/config.json
```

```json
{
  "discord_webhook_url": "https://discord.com/api/webhooks/YOUR_ID/YOUR_TOKEN",
  "discord_avatar_url": "",
  "check_interval": "30s",
  "alert_cooldown": "5m",
  "thresholds": {
    "cpu_percent": 85.0,
    "ram_percent": 85.0,
    "disk_percent": 90.0,
    "disk_io_read_mbps": 500.0,
    "disk_io_write_mbps": 300.0,
    "net_rx_mbps": 100.0,
    "net_tx_mbps": 100.0
  },
  "etc_monitor": {
    "enabled": true,
    "recursive": true,
    "watch_paths": [],
    "ignore_patterns": ["*.swp", "*.tmp", "*~"]
  },
  "k8s_cert_monitor": {
    "check_interval": "6h",
    "cert_paths": ["/etc/kubernetes/pki"],
    "warning_days": [30, 14, 7, 1]
  },
  "log_file": "/var/log/alart-service.log",
  "log_level": "info"
}
```

> **Note:** `k8s_cert_monitor` is **optional** — remove the entire block to disable K8s cert checking. `discord_avatar_url` is also optional — leave empty to use the default logo.

### 4. Test Config

```bash
# Validate syntax (like nginx -t)
alart -t

# Output on success:
# alart-service: the configuration file /etc/alart-service/config.json syntax is ok
# alart-service: configuration file /etc/alart-service/config.json test is successful

# Output on error:
# alart-service: [ERROR] JSON syntax error in /etc/alart-service/config.json at line 5, column 12:
#   → invalid character '}' looking for beginning of value
# alart-service: configuration file /etc/alart-service/config.json test failed
```

### 5. Start

```bash
sudo systemctl start alart-service
sudo systemctl enable alart-service
sudo systemctl status alart-service
```

### 6. Reload Config (no restart)

After editing the config file, apply changes without restarting:

```bash
# Option 1: Using the alart CLI
alart -s reload

# Option 2: Using systemctl
sudo systemctl reload alart-service
```

### 7. View Logs

```bash
# Live logs
sudo journalctl -u alart-service -f

# Or from log file
sudo tail -f /var/log/alart-service.log
```

## CLI Reference

| Command | Description |
|---|---|
| `alart -t` | Test config file syntax and validate all values |
| `alart -t -config /path/to/config.json` | Test a specific config file |
| `alart -s reload` | Reload config without restart (sends SIGHUP) |
| `alart -s stop` | Graceful shutdown (sends SIGTERM) |
| `alart -s reopen` | Reopen log file (for log rotation, sends SIGUSR1) |
| `alart -version` | Show version |
| `alart -gen-config -config ./config.json` | Generate default config file |

## Configuration Reference

| Key | Type | Default | Description |
|---|---|---|---|
| `discord_webhook_url` | string | *required* | Discord webhook URL |
| `check_interval` | string | `"30s"` | How often to check metrics |
| `alert_cooldown` | string | `"5m"` | Minimum time between repeated alerts |
| `thresholds.cpu_percent` | float | `85.0` | CPU usage alert threshold (%) |
| `thresholds.ram_percent` | float | `85.0` | RAM usage alert threshold (%) |
| `thresholds.disk_percent` | float | `90.0` | Disk usage alert threshold (%) |
| `thresholds.disk_io_read_mbps` | float | `500.0` | Disk read rate threshold (MB/s) |
| `thresholds.disk_io_write_mbps` | float | `300.0` | Disk write rate threshold (MB/s) |
| `thresholds.net_rx_mbps` | float | `100.0` | Network receive threshold (MB/s) |
| `thresholds.net_tx_mbps` | float | `100.0` | Network transmit threshold (MB/s) |
| `etc_monitor.enabled` | bool | `true` | Enable /etc filesystem monitoring |
| `etc_monitor.recursive` | bool | `true` | Watch subdirectories recursively |
| `etc_monitor.watch_paths` | []string | `[]` | Specific paths (empty = all /etc) |
| `etc_monitor.ignore_patterns` | []string | `["*.swp","*.tmp","*~"]` | Glob patterns to ignore |
| `log_file` | string | `"/var/log/alart-service.log"` | Log file path (`"stdout"` for console) |
| `log_level` | string | `"info"` | Log verbosity |

### K8s Certificate Monitoring (Opt-in)

This feature is **disabled by default**. To enable it, add the `k8s_cert_monitor` section to your config:

```json
{
  "discord_webhook_url": "...",
  "check_interval": "30s",
  "alert_cooldown": "5m",
  "thresholds": { ... },
  "etc_monitor": { ... },

  "k8s_cert_monitor": {
    "check_interval": "6h",
    "cert_paths": ["/etc/kubernetes/pki"],
    "warning_days": [30, 14, 7, 1]
  },

  "log_file": "/var/log/alart-service.log",
  "log_level": "info"
}
```

**If `k8s_cert_monitor` is not present in config.json, the feature is completely off.** No scanning, no alerts, no overhead.

| Key | Type | Default | Description |
|---|---|---|---|
| `k8s_cert_monitor.check_interval` | string | `"6h"` | How often to scan certificates |
| `k8s_cert_monitor.cert_paths` | []string | `["/etc/kubernetes/pki"]` | Directories/files to scan for .crt/.pem |
| `k8s_cert_monitor.warning_days` | []int | `[30, 14, 7, 1]` | Alert at these day thresholds before expiry |

## Typical Workflow

```bash
# 1. Edit config
sudo nano /etc/alart-service/config.json

# 2. Test your changes
alart -t

# 3. Apply changes (no downtime)
alart -s reload

# 4. Verify
sudo journalctl -u alart-service -n 5
```

## Discord Alert Examples

**System metric alert:**
```
🖥️ Host: web-server-01
🔥 CPU Alert
Usage: 92.3% (threshold: 85.0%)
⏰ 2026-05-11 09:15:30 UTC
```

**/etc change alert:**
```
🔐 /etc Monitor Alert
🖥️ Host: web-server-01
📁 Event: MODIFIED
📄 Path: /etc/passwd
👤 User: root
⏰ 2026-05-11 09:20:45 UTC
```

**Config reload notification:**
```
🔄 alart-service config reloaded
🖥️ Host: web-server-01
⏰ 2026-05-11 09:25:00 UTC
```

## Architecture

```
alart-service/
├── cmd/alart-service/    # Entrypoint & CLI (-t, -s reload)
│   └── main.go
├── config/               # Configuration loading & validation
│   └── config.go
├── monitor/              # System metrics collection (/proc reader)
│   ├── collector.go
│   └── statfs_linux.go
├── alerter/              # Threshold evaluation & cooldown logic
│   ├── alerter.go
│   └── hostname.go
├── certmon/              # K8s certificate expiration monitor (opt-in)
│   └── certmon.go
├── notifier/             # Discord webhook client (rich embeds)
│   └── discord.go
├── watcher/              # /etc inotify filesystem monitor
│   ├── etc_watcher.go
│   └── inotify_linux.go
├── assets/               # Logo and static assets
│   └── logo.png
├── deploy/               # Systemd unit & sample config
│   ├── alart-service.service
│   └── config.sample.json
├── install.sh            # One-command installer
├── uninstall.sh          # Clean uninstaller (--purge for full removal)
├── Makefile              # Build targets
└── go.mod
```

## Uninstall

```bash
# Remove service but keep config and logs
sudo bash uninstall.sh

# Or via make
make uninstall

# Remove everything including config and logs
sudo bash uninstall.sh --purge

# Or via make
make purge
```

## Resource Usage

The service is designed to be extremely lightweight:
- **Memory:** ~5-10 MB RSS
- **CPU:** < 0.1% (wakes up only on check interval)
- **Binary size:** ~3 MB (statically linked, stripped)
- **Zero external dependencies** — reads `/proc` and `/sys` directly

## License

MIT
