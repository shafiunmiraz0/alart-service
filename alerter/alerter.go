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

// Reload updates the alerter with a new configuration (called on SIGHUP).
func (a *Alerter) Reload(cfg *config.Config) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg = cfg
	a.cooldown = cfg.GetAlertCooldown()
}

// Evaluate checks all metrics against thresholds and sends alerts if needed.
func (a *Alerter) Evaluate(m *monitor.SystemMetrics) {
	hostname := getHostname()

	// CPU
	if m.CPUPercent > a.cfg.Thresholds.CPUPercent {
		a.sendIfCooldown("cpu", notifier.Alert{
			Title:       "🔥 CPU Alert",
			Description: "CPU usage has exceeded the configured threshold.",
			Color:       notifier.ColorDanger,
			Fields: []notifier.Field{
				{Name: "🖥️ Host", Value: fmt.Sprintf("`%s`", hostname), Inline: true},
				{Name: "📊 Usage", Value: fmt.Sprintf("**%.1f%%**", m.CPUPercent), Inline: true},
				{Name: "⚠️ Threshold", Value: fmt.Sprintf("%.1f%%", a.cfg.Thresholds.CPUPercent), Inline: true},
			},
		})
	}

	// RAM
	if m.RAMPercent > a.cfg.Thresholds.RAMPercent {
		usedGB := float64(m.RAMUsed) / (1024 * 1024 * 1024)
		totalGB := float64(m.RAMTotal) / (1024 * 1024 * 1024)
		a.sendIfCooldown("ram", notifier.Alert{
			Title:       "🧠 RAM Alert",
			Description: "Memory usage has exceeded the configured threshold.",
			Color:       notifier.ColorDanger,
			Fields: []notifier.Field{
				{Name: "🖥️ Host", Value: fmt.Sprintf("`%s`", hostname), Inline: true},
				{Name: "📊 Usage", Value: fmt.Sprintf("**%.1f%%** (%.1f/%.1f GB)", m.RAMPercent, usedGB, totalGB), Inline: true},
				{Name: "⚠️ Threshold", Value: fmt.Sprintf("%.1f%%", a.cfg.Thresholds.RAMPercent), Inline: true},
			},
		})
	}

	// Disk usage
	for _, d := range m.Disks {
		if d.Percent > a.cfg.Thresholds.DiskPercent {
			key := fmt.Sprintf("disk_%s", d.MountPoint)
			totalGB := float64(d.Total) / (1024 * 1024 * 1024)
			usedGB := float64(d.Used) / (1024 * 1024 * 1024)
			a.sendIfCooldown(key, notifier.Alert{
				Title:       "💾 Disk Alert",
				Description: fmt.Sprintf("Disk usage on `%s` (`%s`) exceeded threshold.", d.MountPoint, d.Device),
				Color:       notifier.ColorDanger,
				Fields: []notifier.Field{
					{Name: "🖥️ Host", Value: fmt.Sprintf("`%s`", hostname), Inline: true},
					{Name: "📊 Usage", Value: fmt.Sprintf("**%.1f%%** (%.1f/%.1f GB)", d.Percent, usedGB, totalGB), Inline: true},
					{Name: "⚠️ Threshold", Value: fmt.Sprintf("%.1f%%", a.cfg.Thresholds.DiskPercent), Inline: true},
				},
			})
		}
	}

	// Disk I/O
	if m.DiskIOReadMBps > a.cfg.Thresholds.DiskIOReadMBps {
		a.sendIfCooldown("diskio_read", notifier.Alert{
			Title:       "📖 Disk I/O Read Alert",
			Description: "Disk read rate has exceeded the configured threshold.",
			Color:       notifier.ColorWarning,
			Fields: []notifier.Field{
				{Name: "🖥️ Host", Value: fmt.Sprintf("`%s`", hostname), Inline: true},
				{Name: "📊 Rate", Value: fmt.Sprintf("**%.1f MB/s**", m.DiskIOReadMBps), Inline: true},
				{Name: "⚠️ Threshold", Value: fmt.Sprintf("%.1f MB/s", a.cfg.Thresholds.DiskIOReadMBps), Inline: true},
			},
		})
	}
	if m.DiskIOWriteMBps > a.cfg.Thresholds.DiskIOWriteMBps {
		a.sendIfCooldown("diskio_write", notifier.Alert{
			Title:       "✏️ Disk I/O Write Alert",
			Description: "Disk write rate has exceeded the configured threshold.",
			Color:       notifier.ColorWarning,
			Fields: []notifier.Field{
				{Name: "🖥️ Host", Value: fmt.Sprintf("`%s`", hostname), Inline: true},
				{Name: "📊 Rate", Value: fmt.Sprintf("**%.1f MB/s**", m.DiskIOWriteMBps), Inline: true},
				{Name: "⚠️ Threshold", Value: fmt.Sprintf("%.1f MB/s", a.cfg.Thresholds.DiskIOWriteMBps), Inline: true},
			},
		})
	}

	// Network
	if m.NetRxMBps > a.cfg.Thresholds.NetRxMBps {
		a.sendIfCooldown("net_rx", notifier.Alert{
			Title:       "📥 Network RX Alert",
			Description: "Network receive rate has exceeded the configured threshold.",
			Color:       notifier.ColorWarning,
			Fields: []notifier.Field{
				{Name: "🖥️ Host", Value: fmt.Sprintf("`%s`", hostname), Inline: true},
				{Name: "📊 Rate", Value: fmt.Sprintf("**%.1f MB/s**", m.NetRxMBps), Inline: true},
				{Name: "⚠️ Threshold", Value: fmt.Sprintf("%.1f MB/s", a.cfg.Thresholds.NetRxMBps), Inline: true},
			},
		})
	}
	if m.NetTxMBps > a.cfg.Thresholds.NetTxMBps {
		a.sendIfCooldown("net_tx", notifier.Alert{
			Title:       "📤 Network TX Alert",
			Description: "Network transmit rate has exceeded the configured threshold.",
			Color:       notifier.ColorWarning,
			Fields: []notifier.Field{
				{Name: "🖥️ Host", Value: fmt.Sprintf("`%s`", hostname), Inline: true},
				{Name: "📊 Rate", Value: fmt.Sprintf("**%.1f MB/s**", m.NetTxMBps), Inline: true},
				{Name: "⚠️ Threshold", Value: fmt.Sprintf("%.1f MB/s", a.cfg.Thresholds.NetTxMBps), Inline: true},
			},
		})
	}
}

// sendIfCooldown sends an alert only if the cooldown period has elapsed.
func (a *Alerter) sendIfCooldown(key string, alert notifier.Alert) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	if last, ok := a.lastAlert[key]; ok {
		if now.Sub(last) < a.cooldown {
			return // Still in cooldown.
		}
	}

	if err := a.notifier.SendAlert(alert); err != nil {
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
