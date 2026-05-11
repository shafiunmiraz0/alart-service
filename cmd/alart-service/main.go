package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
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
	version           = "1.0.0"
)

func main() {
	var (
		configPath  string
		genConfig   bool
		showVersion bool
	)

	flag.StringVar(&configPath, "config", defaultConfigPath, "Path to configuration file")
	flag.BoolVar(&genConfig, "gen-config", false, "Generate a default configuration file and exit")
	flag.BoolVar(&showVersion, "version", false, "Show version and exit")
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

	// Load configuration.
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Setup logging.
	setupLogging(cfg)

	log.Printf("alart-service v%s starting", version)
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

	// Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	log.Printf("Received signal %v, shutting down...", sig)

	// Graceful shutdown.
	cancel()
	etcWatcher.Stop()

	// Send shutdown notification.
	shutdownMsg := fmt.Sprintf("🛑 **alart-service stopped**\n🖥️ Host: `%s`\n⏰ %s",
		hostname, time.Now().Format("2006-01-02 15:04:05 MST"))
	_ = discord.Send(shutdownMsg)

	log.Println("alart-service stopped")
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
