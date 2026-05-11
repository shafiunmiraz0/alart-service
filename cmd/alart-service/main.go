package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
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
	stateFilePath     = "/var/lib/alart-service/state.json"
	version           = "1.0.0"
)

// serviceState tracks boot info for detecting reboots.
type serviceState struct {
	LastBootID    string `json:"last_boot_id"`
	LastShutdown  string `json:"last_shutdown"`
	CleanShutdown bool   `json:"clean_shutdown"`
}

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

	if showVersion {
		fmt.Printf("alart-service v%s\n", version)
		os.Exit(0)
	}

	if genConfig {
		if err := config.GenerateDefault(configPath); err != nil {
			log.Fatalf("Failed to generate config: %v", err)
		}
		fmt.Printf("Default configuration written to %s\n", configPath)
		fmt.Println("Edit the file and set your discord_webhook_url before starting the service.")
		os.Exit(0)
	}

	if testConfig {
		runTestConfig(configPath)
		return
	}

	if signalCmd != "" {
		runSignalCommand(signalCmd)
		return
	}

	runDaemon(configPath)
}

// =============================================================================
// Config Test (alart -t)
// =============================================================================

func runTestConfig(configPath string) {
	fmt.Printf("alart-service: testing configuration file %s\n", configPath)

	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Printf("alart-service: [ERROR] cannot read %s (%v)\n", configPath, err)
		fmt.Printf("alart-service: configuration file %s test failed\n", configPath)
		os.Exit(1)
	}

	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
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

	cfg := config.DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		fmt.Printf("alart-service: [ERROR] invalid config structure in %s:\n", configPath)
		fmt.Printf("  → %s\n", err.Error())
		fmt.Printf("alart-service: configuration file %s test failed\n", configPath)
		os.Exit(1)
	}

	var warnings, errors []string

	if cfg.DiscordWebhookURL == "" {
		errors = append(errors, "discord_webhook_url is required but empty")
	} else if cfg.DiscordWebhookURL == "https://discord.com/api/webhooks/YOUR_WEBHOOK_ID/YOUR_WEBHOOK_TOKEN" {
		warnings = append(warnings, "discord_webhook_url is still set to the placeholder")
	}

	if _, err := time.ParseDuration(cfg.CheckInterval); err != nil {
		errors = append(errors, fmt.Sprintf("check_interval %q is invalid: %v", cfg.CheckInterval, err))
	}
	if _, err := time.ParseDuration(cfg.AlertCooldown); err != nil {
		errors = append(errors, fmt.Sprintf("alert_cooldown %q is invalid: %v", cfg.AlertCooldown, err))
	}
	if cfg.Thresholds.CPUPercent <= 0 || cfg.Thresholds.CPUPercent > 100 {
		errors = append(errors, fmt.Sprintf("cpu_percent must be 0-100, got %.1f", cfg.Thresholds.CPUPercent))
	}
	if cfg.Thresholds.RAMPercent <= 0 || cfg.Thresholds.RAMPercent > 100 {
		errors = append(errors, fmt.Sprintf("ram_percent must be 0-100, got %.1f", cfg.Thresholds.RAMPercent))
	}
	if cfg.Thresholds.DiskPercent <= 0 || cfg.Thresholds.DiskPercent > 100 {
		errors = append(errors, fmt.Sprintf("disk_percent must be 0-100, got %.1f", cfg.Thresholds.DiskPercent))
	}

	for _, w := range warnings {
		fmt.Printf("alart-service: [WARN]  %s\n", w)
	}
	if len(errors) > 0 {
		for _, e := range errors {
			fmt.Printf("alart-service: [ERROR] %s\n", e)
		}
		fmt.Printf("alart-service: configuration file %s test failed\n", configPath)
		os.Exit(1)
	}

	fmt.Printf("alart-service: the configuration file %s syntax is ok\n", configPath)
	fmt.Printf("alart-service: configuration file %s test is successful\n", configPath)
}

// =============================================================================
// Signal Commands (alart -s reload|stop|reopen)
// =============================================================================

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

func sendSignalToProcess(sig syscall.Signal, action string) {
	data, err := os.ReadFile(pidFilePath)
	if err != nil {
		fmt.Printf("alart-service: [ERROR] cannot read PID file %s\n", pidFilePath)
		fmt.Println("  Is alart-service running? Check: systemctl status alart-service")
		os.Exit(1)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		fmt.Printf("alart-service: [ERROR] invalid PID in %s\n", pidFilePath)
		os.Exit(1)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Printf("alart-service: [ERROR] process %d not found\n", pid)
		os.Exit(1)
	}

	if err := proc.Signal(syscall.Signal(0)); err != nil {
		fmt.Printf("alart-service: [ERROR] process %d is not running (%v)\n", pid, err)
		fmt.Printf("  Stale PID file? Remove it: sudo rm %s\n", pidFilePath)
		os.Exit(1)
	}

	if err := proc.Signal(sig); err != nil {
		fmt.Printf("alart-service: [ERROR] failed to send signal to process %d: %v\n", pid, err)
		os.Exit(1)
	}

	fmt.Printf("alart-service: signal %q sent to process %d\n", action, pid)
}

// =============================================================================
// Daemon
// =============================================================================

func runDaemon(configPath string) {
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	setupLogging(cfg)

	if err := writePIDFile(); err != nil {
		log.Printf("[WARN] Failed to write PID file: %v", err)
	}
	defer removePIDFile()

	hostname, _ := os.Hostname()

	log.Printf("alart-service v%s starting (PID: %d)", version, os.Getpid())
	log.Printf("Config: %s", configPath)
	log.Printf("Check interval: %s | Alert cooldown: %s", cfg.CheckInterval, cfg.AlertCooldown)

	// Initialize Discord notifier.
	discord := notifier.NewDiscord(cfg.DiscordWebhookURL, cfg.DiscordAvatarURL)
	collector := monitor.NewCollector()
	alert := alerter.New(cfg, discord)

	// --- VM Boot/Reboot Detection ---
	sendStartupAlert(discord, hostname)

	// Context for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start /etc watcher.
	etcWatcher := watcher.New(&cfg.EtcMonitor, discord)
	go func() {
		if err := etcWatcher.Start(); err != nil {
			log.Printf("[ERROR] /etc watcher failed: %v", err)
		}
	}()

	// Start metrics loop.
	go metricsLoop(ctx, cfg, collector, alert)

	// Listen for signals.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGUSR1)

	for {
		sig := <-sigCh

		switch sig {
		case syscall.SIGHUP:
			log.Println("[RELOAD] Received SIGHUP, reloading configuration...")

			newCfg, err := config.Load(configPath)
			if err != nil {
				log.Printf("[RELOAD] ERROR: %v — keeping current config", err)
				continue
			}

			cfg = newCfg
			discord.UpdateWebhookURL(cfg.DiscordWebhookURL)
			discord.UpdateAvatarURL(cfg.DiscordAvatarURL)
			alert.Reload(cfg)

			etcWatcher.Stop()
			etcWatcher = watcher.New(&cfg.EtcMonitor, discord)
			go func() {
				if err := etcWatcher.Start(); err != nil {
					log.Printf("[ERROR] /etc watcher failed after reload: %v", err)
				}
			}()

			cancel()
			ctx, cancel = context.WithCancel(context.Background())
			go metricsLoop(ctx, cfg, collector, alert)

			log.Printf("[RELOAD] Configuration reloaded successfully")

			_ = discord.SendAlert(notifier.Alert{
				Title:       "🔄 Configuration Reloaded",
				Description: "The service configuration has been reloaded successfully.",
				Color:       notifier.ColorInfo,
				Fields: []notifier.Field{
					{Name: "🖥️ Host", Value: fmt.Sprintf("`%s`", hostname), Inline: true},
					{Name: "⏱️ Interval", Value: cfg.CheckInterval, Inline: true},
					{Name: "⏳ Cooldown", Value: cfg.AlertCooldown, Inline: true},
				},
			})

		case syscall.SIGUSR1:
			log.Println("[REOPEN] Reopening log file...")
			setupLogging(cfg)
			log.Println("[REOPEN] Log file reopened")

		case syscall.SIGINT, syscall.SIGTERM:
			log.Printf("Received signal %v, shutting down...", sig)

			cancel()
			etcWatcher.Stop()

			// Mark clean shutdown in state file.
			saveState(&serviceState{
				LastBootID:    readBootID(),
				LastShutdown:  time.Now().UTC().Format(time.RFC3339),
				CleanShutdown: true,
			})

			_ = discord.SendAlert(notifier.Alert{
				Title:       "🛑 Service Stopped",
				Description: "alart-service is shutting down gracefully.",
				Color:       notifier.ColorCritical,
				Fields: []notifier.Field{
					{Name: "🖥️ Host", Value: fmt.Sprintf("`%s`", hostname), Inline: true},
					{Name: "📋 Signal", Value: fmt.Sprintf("`%s`", sig.String()), Inline: true},
					{Name: "⏰ Time", Value: time.Now().Format("2006-01-02 15:04:05 MST"), Inline: true},
				},
			})

			log.Println("alart-service stopped")
			return
		}
	}
}

// =============================================================================
// VM Boot / Reboot Detection
// =============================================================================

func sendStartupAlert(discord *notifier.Discord, hostname string) {
	bootID := readBootID()
	prevState := loadState()
	uptimeStr := readUptimeString()

	var title, description string
	var color int

	switch {
	case prevState == nil:
		// First time ever.
		title = "✅ Service Started"
		description = "alart-service is running for the first time on this host."
		color = notifier.ColorSuccess

	case prevState.LastBootID != bootID && prevState.CleanShutdown:
		// Different boot, was cleanly shut down → clean reboot.
		title = "🔄 VM Rebooted (Clean)"
		description = "The system was rebooted after a clean shutdown."
		color = notifier.ColorWarning

	case prevState.LastBootID != bootID && !prevState.CleanShutdown:
		// Different boot, NOT cleanly shut down → unexpected reboot/crash.
		title = "⚠️ VM Rebooted (Unexpected!)"
		description = "The system rebooted **without a clean shutdown** — possible crash, power loss, or forced restart."
		color = notifier.ColorCritical

	case prevState.LastBootID == bootID:
		// Same boot → service restarted (not a reboot).
		title = "✅ Service Restarted"
		description = "The service was restarted within the same boot session."
		color = notifier.ColorSuccess
	}

	fields := []notifier.Field{
		{Name: "🖥️ Host", Value: fmt.Sprintf("`%s`", hostname), Inline: true},
		{Name: "⏱️ Uptime", Value: uptimeStr, Inline: true},
		{Name: "🔖 Version", Value: fmt.Sprintf("`v%s`", version), Inline: true},
	}

	if prevState != nil && prevState.LastShutdown != "" {
		fields = append(fields, notifier.Field{
			Name: "🕐 Last Shutdown", Value: prevState.LastShutdown, Inline: false,
		})
	}

	if err := discord.SendAlert(notifier.Alert{
		Title:       title,
		Description: description,
		Color:       color,
		Fields:      fields,
	}); err != nil {
		log.Printf("[WARN] Failed to send startup notification: %v", err)
	}

	// Mark as NOT cleanly shut down (will be overwritten on clean exit).
	saveState(&serviceState{
		LastBootID:    bootID,
		LastShutdown:  "",
		CleanShutdown: false,
	})
}

func readBootID() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}

func readUptimeString() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "unknown"
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return "unknown"
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "unknown"
	}
	d := time.Duration(secs * float64(time.Second))
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func loadState() *serviceState {
	data, err := os.ReadFile(stateFilePath)
	if err != nil {
		return nil
	}
	var s serviceState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	return &s
}

func saveState(s *serviceState) {
	_ = os.MkdirAll(filepath.Dir(stateFilePath), 0755)
	data, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(stateFilePath, data, 0644)
}

// =============================================================================
// Metrics Loop
// =============================================================================

func metricsLoop(ctx context.Context, cfg *config.Config, collector *monitor.Collector, alert *alerter.Alerter) {
	interval := cfg.GetCheckInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

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

			log.Printf("[METRICS] CPU: %.1f%% | RAM: %.1f%% | Net RX: %.1f TX: %.1f MB/s",
				metrics.CPUPercent, metrics.RAMPercent,
				metrics.NetRxMBps, metrics.NetTxMBps)

			alert.Evaluate(metrics)
		}
	}
}

// =============================================================================
// Helpers
// =============================================================================

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

func writePIDFile() error {
	return os.WriteFile(pidFilePath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0644)
}

func removePIDFile() {
	_ = os.Remove(pidFilePath)
}

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
