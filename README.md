<<<<<<< HEAD
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
  "log_file": "/var/log/alart-service.log",
  "log_level": "info"
}
```

### 4. Start

```bash
sudo systemctl start alart-service
sudo systemctl status alart-service
```

### 5. View Logs

```bash
# Live logs
sudo journalctl -u alart-service -f

# Or from log file
sudo tail -f /var/log/alart-service.log
```

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

## Architecture

```
alart-service/
├── cmd/alart-service/    # Entrypoint & CLI
│   └── main.go
├── config/               # Configuration loading & validation
│   └── config.go
├── monitor/              # System metrics collection (/proc reader)
│   ├── collector.go
│   └── statfs_linux.go
├── alerter/              # Threshold evaluation & cooldown logic
│   ├── alerter.go
│   └── hostname.go
├── notifier/             # Discord webhook client
│   └── discord.go
├── watcher/              # /etc inotify filesystem monitor
│   ├── etc_watcher.go
│   └── inotify_linux.go
├── deploy/               # Systemd unit & sample config
│   ├── alart-service.service
│   └── config.sample.json
├── install.sh            # One-command installer
├── Makefile              # Build targets
└── go.mod
```

## Uninstall

```bash
make uninstall
# Config preserved at /etc/alart-service/ — remove manually if desired
sudo rm -rf /etc/alart-service
```

## Resource Usage

The service is designed to be extremely lightweight:
- **Memory:** ~5-10 MB RSS
- **CPU:** < 0.1% (wakes up only on check interval)
- **Binary size:** ~3 MB (statically linked, stripped)
- **Zero external dependencies** — reads `/proc` and `/sys` directly

## License

MIT
=======
# alart-service
>>>>>>> 8dbcbb758cdd57ebba714bbc0c80759d2c78ac96
