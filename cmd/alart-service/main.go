package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alart-service/alerter"
	"github.com/alart-service/config"
	"github.com/alart-service/monitor"
	"github.com/alart-service/notifier"
	"github.com/alart-service/watcher"
)

const (
	defaultConfigPath = "/etc/alart-service/config.json"
	pidFilePath       = "/var/run/alart-service.pid"
	version           = "1.0.0"
)

func main() {
	var (
		configPath  string
		genConfig   bool
		showVersion bool
		testConfig  bool
		signalCmd   string
	)

	flag.StringVar(&configPath, "config", defaultConfigPath, "Path to configuration file")
	flag.BoolVar(&genConfig, "gen-config", false, "Generate a default configuration file and exit")
	flag.BoolVar(&showVersion, "version", false, "Show version and exit")
	flag.BoolVar(&testConfig, "t", false, "Test configuration file syntax and exit")
	flag.StringVar(&signalCmd, "s", "", "Send signal to running process: reload, stop, reopen")
	flag.Parse()

	// --- Mode: version ---
	if showVersion {
		fmt.Printf("alart-service v%s\n", version)
		os.Exit(0)
	}

	// --- Mode: generate config ---
	if genConfig {
		if err := config.GenerateDefault(configPath); err != nil {
			log.Fatalf("Failed to generate config: %v", err)
		}
		fmt.Printf("Default configuration written to %s\n", configPath)
		fmt.Println("Edit the file and set your discord_webhook_url before starting the service.")
		os.Exit(0)
	}

	// --- Mode: test config (alart -t) ---
	if testConfig {
		runTestConfig(configPath)
		return
	}

	// --- Mode: send signal (alart -s reload|stop) ---
	if signalCmd != "" {
		runSignalCommand(signalCmd)
		return
	}

	// --- Mode: run as daemon ---
	runDaemon(configPath)
}

// runTestConfig validates the configuration file and prints results like nginx -t.
func runTestConfig(configPath string) {
	fmt.Printf("alart-service: testing configuration file %s\n", configPath)

	// Step 1: Check file exists and is readable.
	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Printf("alart-service: [ERROR] cannot read %s (%v)\n", configPath, err)
		fmt.Printf("alart-service: configuration file %s test failed\n", configPath)
		os.Exit(1)
	}

	// Step 2: Check JSON syntax.
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		// Try to provide a helpful line/column hint.
		if syntaxErr, ok := err.(*json.SyntaxError); ok {
			line, col := findLineCol(data, syntaxErr.Offset)
			fmt.Printf("alart-service: [ERROR] JSON syntax error in %s at line %d, column %d:\n", configPath, line, col)
			fmt.Printf("  → %s\n", syntaxErr.Error())
		} else {
			fmt.Printf("alart-service: [ERROR] JSON parse error in %s:\n", configPath)
			fmt.Printf("  → %s\n", err.Error())
		}
		fmt.Printf("alart-service: configuration file %s test failed\n", configPath)
		os.Exit(1)
	}

	// Step 3: Unmarshal into config struct.
	cfg := config.DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		fmt.Printf("alart-service: [ERROR] invalid config structure in %s:\n", configPath)
		if ute, ok := err.(*json.UnmarshalTypeError); ok {
			fmt.Printf("  → field %q expects %s, got %s\n", ute.Field, ute.Type, ute.Value)
		} else {
			fmt.Printf("  → %s\n", err.Error())
		}
		fmt.Printf("alart-service: configuration file %s test failed\n", configPath)
		os.Exit(1)
	}

	// Step 4: Validate logic (thresholds, durations, webhook).
	var warnings []string
	var errors []string

	// Validate webhook URL.
	if cfg.DiscordWebhookURL == "" {
		errors = append(errors, "discord_webhook_url is required but empty")
	} else if cfg.DiscordWebhookURL == "https://discord.com/api/webhooks/YOUR_WEBHOOK_ID/YOUR_WEBHOOK_TOKEN" {
		warnings = append(warnings, "discord_webhook_url is still set to the placeholder — update it before starting")
	} else if !strings.HasPrefix(cfg.DiscordWebhookURL, "https://discord.com/api/webhooks/") &&
		!strings.HasPrefix(cfg.DiscordWebhookURL, "https://discordapp.com/api/webhooks/") {
		warnings = append(warnings, fmt.Sprintf("discord_webhook_url %q doesn't look like a valid Discord webhook URL", cfg.DiscordWebhookURL))
	}

	// Validate durations.
	if _, err := time.ParseDuration(cfg.CheckInterval); err != nil {
		errors = append(errors, fmt.Sprintf("check_interval %q is not a valid duration: %v", cfg.CheckInterval, err))
	}
	if _, err := time.ParseDuration(cfg.AlertCooldown); err != nil {
		errors = append(errors, fmt.Sprintf("alert_cooldown %q is not a valid duration: %v", cfg.AlertCooldown, err))
	}

	// Validate thresholds.
	if cfg.Thresholds.CPUPercent <= 0 || cfg.Thresholds.CPUPercent > 100 {
		errors = append(errors, fmt.Sprintf("thresholds.cpu_percent must be 0-100, got %.1f", cfg.Thresholds.CPUPercent))
	}
	if cfg.Thresholds.RAMPercent <= 0 || cfg.Thresholds.RAMPercent > 100 {
		errors = append(errors, fmt.Sprintf("thresholds.ram_percent must be 0-100, got %.1f", cfg.Thresholds.RAMPercent))
	}
	if cfg.Thresholds.DiskPercent <= 0 || cfg.Thresholds.DiskPercent > 100 {
		errors = append(errors, fmt.Sprintf("thresholds.disk_percent must be 0-100, got %.1f", cfg.Thresholds.DiskPercent))
	}
	if cfg.Thresholds.DiskIOReadMBps < 0 {
		errors = append(errors, fmt.Sprintf("thresholds.disk_io_read_mbps must be >= 0, got %.1f", cfg.Thresholds.DiskIOReadMBps))
	}
	if cfg.Thresholds.DiskIOWriteMBps < 0 {
		errors = append(errors, fmt.Sprintf("thresholds.disk_io_write_mbps must be >= 0, got %.1f", cfg.Thresholds.DiskIOWriteMBps))
	}
	if cfg.Thresholds.NetRxMBps < 0 {
		errors = append(errors, fmt.Sprintf("thresholds.net_rx_mbps must be >= 0, got %.1f", cfg.Thresholds.NetRxMBps))
	}
	if cfg.Thresholds.NetTxMBps < 0 {
		errors = append(errors, fmt.Sprintf("thresholds.net_tx_mbps must be >= 0, got %.1f", cfg.Thresholds.NetTxMBps))
	}

	// Validate log level.
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if cfg.LogLevel != "" && !validLevels[cfg.LogLevel] {
		warnings = append(warnings, fmt.Sprintf("log_level %q is not one of: debug, info, warn, error", cfg.LogLevel))
	}

	// Print warnings.
	for _, w := range warnings {
		fmt.Printf("alart-service: [WARN]  %s\n", w)
	}

	// Print errors.
	if len(errors) > 0 {
		for _, e := range errors {
			fmt.Printf("alart-service: [ERROR] %s\n", e)
		}
		fmt.Printf("alart-service: configuration file %s test failed\n", configPath)
		os.Exit(1)
	}

	// All good.
	fmt.Printf("alart-service: the configuration file %s syntax is ok\n", configPath)
	fmt.Printf("alart-service: configuration file %s test is successful\n", configPath)
}

// runSignalCommand sends a signal to the running alart-service process.
func runSignalCommand(cmd string) {
	switch strings.ToLower(cmd) {
	case "reload":
		sendSignalToProcess(syscall.SIGHUP, "reload")
	case "stop":
		sendSignalToProcess(syscall.SIGTERM, "stop")
	case "reopen":
		sendSignalToProcess(syscall.SIGUSR1, "reopen")
	default:
		fmt.Printf("alart-service: unknown signal command %q\n", cmd)
		fmt.Println("  Available: reload, stop, reopen")
		os.Exit(1)
	}
}

// sendSignalToProcess reads the PID file and sends the given signal.
func sendSignalToProcess(sig syscall.Signal, action string) {
	data, err := os.ReadFile(pidFilePath)
	if err != nil {
		fmt.Printf("alart-service: [ERROR] cannot read PID file %s\n", pidFilePath)
		fmt.Println("  Is alart-service running?")
		fmt.Printf("  Hint: check with 'systemctl status alart-service'\n")
		os.Exit(1)
	}

	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		fmt.Printf("alart-service: [ERROR] invalid PID in %s: %q\n", pidFilePath, pidStr)
		os.Exit(1)
	}

	// Check if process is actually running.
	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Printf("alart-service: [ERROR] process %d not found\n", pid)
		os.Exit(1)
	}

	// On Linux, FindProcess always succeeds. Send signal 0 to check.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		fmt.Printf("alart-service: [ERROR] process %d is not running (%v)\n", pid, err)
		fmt.Printf("  Stale PID file? Remove it: sudo rm %s\n", pidFilePath)
		os.Exit(1)
	}

	if err := proc.Signal(sig); err != nil {
		fmt.Printf("alart-service: [ERROR] failed to send signal to process %d: %v\n", pid, err)
		os.Exit(1)
	}

	fmt.Printf("alart-service: signal %q sent to process %d (PID: %d)\n", action, pid, pid)
}

// runDaemon starts the main service loop.
func runDaemon(configPath string) {
	// Load configuration.
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Setup logging.
	setupLogging(cfg)

	// Write PID file.
	if err := writePIDFile(); err != nil {
		log.Printf("[WARN] Failed to write PID file: %v", err)
	}
	defer removePIDFile()

	log.Printf("alart-service v%s starting (PID: %d)", version, os.Getpid())
	log.Printf("Config: %s", configPath)
	log.Printf("Check interval: %s | Alert cooldown: %s", cfg.CheckInterval, cfg.AlertCooldown)
	log.Printf("Thresholds — CPU: %.0f%% | RAM: %.0f%% | Disk: %.0f%%",
		cfg.Thresholds.CPUPercent, cfg.Thresholds.RAMPercent, cfg.Thresholds.DiskPercent)

	// Initialize components.
	discord := notifier.NewDiscord(cfg.DiscordWebhookURL)
	collector := monitor.NewCollector()
	alert := alerter.New(cfg, discord)

	// Send startup notification.
	hostname, _ := os.Hostname()
	startupMsg := fmt.Sprintf("✅ **alart-service started**\n🖥️ Host: `%s`\n📊 Monitoring: CPU, RAM, Disk, I/O, Network\n🔐 /etc Monitor: %v\n⏰ %s",
		hostname, cfg.EtcMonitor.Enabled, time.Now().Format("2006-01-02 15:04:05 MST"))
	if err := discord.Send(startupMsg); err != nil {
		log.Printf("[WARN] Failed to send startup notification: %v", err)
	}

	// Context for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start /etc watcher in a goroutine.
	etcWatcher := watcher.New(&cfg.EtcMonitor, discord)
	go func() {
		if err := etcWatcher.Start(); err != nil {
			log.Printf("[ERROR] /etc watcher failed: %v", err)
		}
	}()

	// Start the metrics collection loop.
	go metricsLoop(ctx, cfg, collector, alert)

	// Listen for signals: SIGINT/SIGTERM for shutdown, SIGHUP for reload.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGUSR1)

	for {
		sig := <-sigCh

		switch sig {
		case syscall.SIGHUP:
			// --- Config reload ---
			log.Println("[RELOAD] Received SIGHUP, reloading configuration...")

			newCfg, err := config.Load(configPath)
			if err != nil {
				log.Printf("[RELOAD] ERROR: failed to load config: %v", err)
				log.Println("[RELOAD] Keeping current configuration")
				continue
			}

			// Apply changes.
			cfg = newCfg

			// Update Discord notifier.
			discord.UpdateWebhookURL(cfg.DiscordWebhookURL)

			// Update alerter with new config.
			alert.Reload(cfg)

			// Restart the /etc watcher with new config.
			etcWatcher.Stop()
			etcWatcher = watcher.New(&cfg.EtcMonitor, discord)
			go func() {
				if err := etcWatcher.Start(); err != nil {
					log.Printf("[ERROR] /etc watcher failed after reload: %v", err)
				}
			}()

			// Restart metrics loop with new interval.
			cancel()
			ctx, cancel = context.WithCancel(context.Background())
			go metricsLoop(ctx, cfg, collector, alert)

			log.Printf("[RELOAD] Configuration reloaded successfully")
			log.Printf("[RELOAD] Check interval: %s | Alert cooldown: %s", cfg.CheckInterval, cfg.AlertCooldown)
			log.Printf("[RELOAD] Thresholds — CPU: %.0f%% | RAM: %.0f%% | Disk: %.0f%%",
				cfg.Thresholds.CPUPercent, cfg.Thresholds.RAMPercent, cfg.Thresholds.DiskPercent)

			// Notify via Discord.
			reloadMsg := fmt.Sprintf("🔄 **alart-service config reloaded**\n🖥️ Host: `%s`\n⏰ %s",
				hostname, time.Now().Format("2006-01-02 15:04:05 MST"))
			_ = discord.Send(reloadMsg)

		case syscall.SIGUSR1:
			// Reopen log file (useful for log rotation).
			log.Println("[REOPEN] Received SIGUSR1, reopening log file...")
			setupLogging(cfg)
			log.Println("[REOPEN] Log file reopened")

		case syscall.SIGINT, syscall.SIGTERM:
			log.Printf("Received signal %v, shutting down...", sig)

			// Graceful shutdown.
			cancel()
			etcWatcher.Stop()

			// Send shutdown notification.
			shutdownMsg := fmt.Sprintf("🛑 **alart-service stopped**\n🖥️ Host: `%s`\n⏰ %s",
				hostname, time.Now().Format("2006-01-02 15:04:05 MST"))
			_ = discord.Send(shutdownMsg)

			log.Println("alart-service stopped")
			return
		}
	}
}

// metricsLoop periodically collects system metrics and evaluates alerts.
func metricsLoop(ctx context.Context, cfg *config.Config, collector *monitor.Collector, alert *alerter.Alerter) {
	interval := cfg.GetCheckInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Do an initial collection to seed delta calculations.
	if _, err := collector.Collect(); err != nil {
		log.Printf("[WARN] Initial collection failed: %v", err)
	}

	log.Println("Metrics collection loop started")

	for {
		select {
		case <-ctx.Done():
			log.Println("Metrics loop stopping")
			return
		case <-ticker.C:
			metrics, err := collector.Collect()
			if err != nil {
				log.Printf("[ERROR] Failed to collect metrics: %v", err)
				continue
			}

			log.Printf("[METRICS] CPU: %.1f%% | RAM: %.1f%% | Net RX: %.1f MB/s TX: %.1f MB/s | DiskIO R: %.1f W: %.1f MB/s",
				metrics.CPUPercent, metrics.RAMPercent,
				metrics.NetRxMBps, metrics.NetTxMBps,
				metrics.DiskIOReadMBps, metrics.DiskIOWriteMBps)

			alert.Evaluate(metrics)
		}
	}
}

// setupLogging configures the log output.
func setupLogging(cfg *config.Config) {
	if cfg.LogFile == "" || cfg.LogFile == "stdout" {
		log.SetOutput(os.Stdout)
		return
	}

	f, err := os.OpenFile(cfg.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[WARN] Cannot open log file %s: %v, falling back to stdout", cfg.LogFile, err)
		log.SetOutput(os.Stdout)
		return
	}

	log.SetOutput(f)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
}

// writePIDFile writes the current process ID to the PID file.
func writePIDFile() error {
	pid := os.Getpid()
	return os.WriteFile(pidFilePath, []byte(strconv.Itoa(pid)+"\n"), 0644)
}

// removePIDFile removes the PID file on shutdown.
func removePIDFile() {
	_ = os.Remove(pidFilePath)
}

// findLineCol converts a byte offset into a line and column number for error reporting.
func findLineCol(data []byte, offset int64) (line, col int) {
	line = 1
	col = 1
	for i := int64(0); i < offset && i < int64(len(data)); i++ {
		if data[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return
}
