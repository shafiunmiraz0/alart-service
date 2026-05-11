package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/alart-service/certmon"
)

// Config holds all configuration for the alert service.
type Config struct {
	// Discord webhook URL for sending alerts.
	DiscordWebhookURL string `json:"discord_webhook_url"`

	// Custom avatar URL for the Discord bot (optional, defaults to project logo).
	DiscordAvatarURL string `json:"discord_avatar_url,omitempty"`

	// How often to check system metrics (e.g. "30s", "1m", "5m").
	CheckInterval string `json:"check_interval"`

	// Cooldown period between repeated alerts for the same metric (e.g. "5m", "15m").
	// Prevents alert spam when a metric stays above threshold.
	AlertCooldown string `json:"alert_cooldown"`

	// Thresholds for various system metrics.
	Thresholds ThresholdConfig `json:"thresholds"`

	// /etc directory monitoring settings.
	EtcMonitor EtcMonitorConfig `json:"etc_monitor"`

	// Kubernetes certificate expiration monitoring (optional, opt-in).
	// If this section is absent from config.json, the feature is completely disabled.
	K8sCertMonitor *certmon.K8sCertMonitorConfig `json:"k8s_cert_monitor,omitempty"`

	// Logging configuration.
	LogFile  string `json:"log_file"`
	LogLevel string `json:"log_level"` // "debug", "info", "warn", "error"
}

// ThresholdConfig defines alert threshold values.
type ThresholdConfig struct {
	// CPU usage percentage (0-100). Alert when usage exceeds this value.
	CPUPercent float64 `json:"cpu_percent"`

	// RAM usage percentage (0-100). Alert when usage exceeds this value.
	RAMPercent float64 `json:"ram_percent"`

	// Disk usage percentage (0-100). Alert when any partition exceeds this value.
	DiskPercent float64 `json:"disk_percent"`

	// Disk I/O read rate in MB/s. Alert when exceeded.
	DiskIOReadMBps float64 `json:"disk_io_read_mbps"`

	// Disk I/O write rate in MB/s. Alert when exceeded.
	DiskIOWriteMBps float64 `json:"disk_io_write_mbps"`

	// Network bandwidth received in MB/s. Alert when exceeded.
	NetRxMBps float64 `json:"net_rx_mbps"`

	// Network bandwidth transmitted in MB/s. Alert when exceeded.
	NetTxMBps float64 `json:"net_tx_mbps"`
}

// EtcMonitorConfig controls the /etc filesystem watcher.
type EtcMonitorConfig struct {
	// Enable or disable /etc directory monitoring.
	Enabled bool `json:"enabled"`

	// Watch subdirectories recursively.
	Recursive bool `json:"recursive"`

	// Specific subdirectories or files within /etc to watch.
	// If empty, watches the entire /etc directory.
	WatchPaths []string `json:"watch_paths"`

	// File patterns to ignore (glob patterns, e.g. "*.swp", "*.tmp").
	IgnorePatterns []string `json:"ignore_patterns"`
}

// DefaultConfig returns a sensible default configuration.
func DefaultConfig() *Config {
	return &Config{
		DiscordWebhookURL: "",
		CheckInterval:     "30s",
		AlertCooldown:     "5m",
		Thresholds: ThresholdConfig{
			CPUPercent:      85.0,
			RAMPercent:      85.0,
			DiskPercent:     90.0,
			DiskIOReadMBps:  500.0,
			DiskIOWriteMBps: 300.0,
			NetRxMBps:       100.0,
			NetTxMBps:       100.0,
		},
		EtcMonitor: EtcMonitorConfig{
			Enabled:        true,
			Recursive:      true,
			WatchPaths:     []string{},
			IgnorePatterns: []string{"*.swp", "*.tmp", "*~"},
		},
		LogFile:  "/var/log/alart-service.log",
		LogLevel: "info",
	}
}

// Load reads and parses the configuration file from the given path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// Validate checks the configuration for logical errors.
func (c *Config) Validate() error {
	if c.DiscordWebhookURL == "" {
		return fmt.Errorf("discord_webhook_url is required")
	}

	if _, err := time.ParseDuration(c.CheckInterval); err != nil {
		return fmt.Errorf("invalid check_interval %q: %w", c.CheckInterval, err)
	}

	if _, err := time.ParseDuration(c.AlertCooldown); err != nil {
		return fmt.Errorf("invalid alert_cooldown %q: %w", c.AlertCooldown, err)
	}

	if c.Thresholds.CPUPercent <= 0 || c.Thresholds.CPUPercent > 100 {
		return fmt.Errorf("cpu_percent must be between 0 and 100, got %.1f", c.Thresholds.CPUPercent)
	}

	if c.Thresholds.RAMPercent <= 0 || c.Thresholds.RAMPercent > 100 {
		return fmt.Errorf("ram_percent must be between 0 and 100, got %.1f", c.Thresholds.RAMPercent)
	}

	if c.Thresholds.DiskPercent <= 0 || c.Thresholds.DiskPercent > 100 {
		return fmt.Errorf("disk_percent must be between 0 and 100, got %.1f", c.Thresholds.DiskPercent)
	}

	return nil
}

// GetCheckInterval returns the parsed check interval duration.
func (c *Config) GetCheckInterval() time.Duration {
	d, _ := time.ParseDuration(c.CheckInterval)
	return d
}

// GetAlertCooldown returns the parsed alert cooldown duration.
func (c *Config) GetAlertCooldown() time.Duration {
	d, _ := time.ParseDuration(c.AlertCooldown)
	return d
}

// GenerateDefault writes a default configuration file to the given path.
func GenerateDefault(path string) error {
	cfg := DefaultConfig()
	cfg.DiscordWebhookURL = "https://discord.com/api/webhooks/YOUR_WEBHOOK_ID/YOUR_WEBHOOK_TOKEN"

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal default config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write default config to %s: %w", path, err)
	}

	return nil
}
