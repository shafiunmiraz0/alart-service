package alerter

import (
	"fmt"
	"sync"
	"time"

	"github.com/alart-service/config"
	"github.com/alart-service/monitor"
	"github.com/alart-service/notifier"
)

// Alerter evaluates system metrics against configured thresholds and sends alerts.
type Alerter struct {
	cfg      *config.Config
	notifier *notifier.Discord
	cooldown time.Duration

	// Track last alert time per metric to implement cooldown.
	mu        sync.Mutex
	lastAlert map[string]time.Time
}

// New creates a new Alerter.
func New(cfg *config.Config, discord *notifier.Discord) *Alerter {
	return &Alerter{
		cfg:       cfg,
		notifier:  discord,
		cooldown:  cfg.GetAlertCooldown(),
		lastAlert: make(map[string]time.Time),
	}
}

// Evaluate checks all metrics against thresholds and sends alerts if needed.
func (a *Alerter) Evaluate(m *monitor.SystemMetrics) {
	// CPU
	if m.CPUPercent > a.cfg.Thresholds.CPUPercent {
		a.sendIfCooldown("cpu", fmt.Sprintf(
			"🔥 **CPU Alert**\nUsage: **%.1f%%** (threshold: %.1f%%)",
			m.CPUPercent, a.cfg.Thresholds.CPUPercent,
		))
	}

	// RAM
	if m.RAMPercent > a.cfg.Thresholds.RAMPercent {
		usedGB := float64(m.RAMUsed) / (1024 * 1024 * 1024)
		totalGB := float64(m.RAMTotal) / (1024 * 1024 * 1024)
		a.sendIfCooldown("ram", fmt.Sprintf(
			"🧠 **RAM Alert**\nUsage: **%.1f%%** (%.1f / %.1f GB)\nThreshold: %.1f%%",
			m.RAMPercent, usedGB, totalGB, a.cfg.Thresholds.RAMPercent,
		))
	}

	// Disk usage
	for _, d := range m.Disks {
		if d.Percent > a.cfg.Thresholds.DiskPercent {
			key := fmt.Sprintf("disk_%s", d.MountPoint)
			totalGB := float64(d.Total) / (1024 * 1024 * 1024)
			usedGB := float64(d.Used) / (1024 * 1024 * 1024)
			a.sendIfCooldown(key, fmt.Sprintf(
				"💾 **Disk Alert** — `%s` (`%s`)\nUsage: **%.1f%%** (%.1f / %.1f GB)\nThreshold: %.1f%%",
				d.MountPoint, d.Device, d.Percent, usedGB, totalGB, a.cfg.Thresholds.DiskPercent,
			))
		}
	}

	// Disk I/O
	if m.DiskIOReadMBps > a.cfg.Thresholds.DiskIOReadMBps {
		a.sendIfCooldown("diskio_read", fmt.Sprintf(
			"📖 **Disk I/O Read Alert**\nRate: **%.1f MB/s** (threshold: %.1f MB/s)",
			m.DiskIOReadMBps, a.cfg.Thresholds.DiskIOReadMBps,
		))
	}
	if m.DiskIOWriteMBps > a.cfg.Thresholds.DiskIOWriteMBps {
		a.sendIfCooldown("diskio_write", fmt.Sprintf(
			"✏️ **Disk I/O Write Alert**\nRate: **%.1f MB/s** (threshold: %.1f MB/s)",
			m.DiskIOWriteMBps, a.cfg.Thresholds.DiskIOWriteMBps,
		))
	}

	// Network
	if m.NetRxMBps > a.cfg.Thresholds.NetRxMBps {
		a.sendIfCooldown("net_rx", fmt.Sprintf(
			"📥 **Network RX Alert**\nRate: **%.1f MB/s** (threshold: %.1f MB/s)",
			m.NetRxMBps, a.cfg.Thresholds.NetRxMBps,
		))
	}
	if m.NetTxMBps > a.cfg.Thresholds.NetTxMBps {
		a.sendIfCooldown("net_tx", fmt.Sprintf(
			"📤 **Network TX Alert**\nRate: **%.1f MB/s** (threshold: %.1f MB/s)",
			m.NetTxMBps, a.cfg.Thresholds.NetTxMBps,
		))
	}
}

// sendIfCooldown sends an alert only if the cooldown period has elapsed.
func (a *Alerter) sendIfCooldown(key, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	if last, ok := a.lastAlert[key]; ok {
		if now.Sub(last) < a.cooldown {
			return // Still in cooldown.
		}
	}

	hostname := getHostname()
	fullMessage := fmt.Sprintf("🖥️ **Host:** `%s`\n%s\n⏰ %s",
		hostname, message, now.Format("2006-01-02 15:04:05 MST"))

	if err := a.notifier.Send(fullMessage); err != nil {
		fmt.Printf("[WARN] failed to send discord alert: %v\n", err)
	} else {
		a.lastAlert[key] = now
	}
}

func getHostname() string {
	name, err := getHostnameOS()
	if err != nil {
		return "unknown"
	}
	return name
}
