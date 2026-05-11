package watcher

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alart-service/config"
	"github.com/alart-service/notifier"
)

// EtcWatcher monitors the /etc directory for file access and modifications
// using inotify via the fsnotify library (or polling as fallback).
// Since we want to detect "whoever enters /etc", we use the audit-style
// approach: we watch for any file create/write/remove/rename/chmod events.
type EtcWatcher struct {
	cfg      *config.EtcMonitorConfig
	discord  *notifier.Discord
	stopCh   chan struct{}
	hostname string
}

// New creates a new EtcWatcher.
func New(cfg *config.EtcMonitorConfig, discord *notifier.Discord) *EtcWatcher {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	return &EtcWatcher{
		cfg:      cfg,
		discord:  discord,
		stopCh:   make(chan struct{}),
		hostname: hostname,
	}
}

// Start begins watching /etc. This is a blocking call — run in a goroutine.
func (w *EtcWatcher) Start() error {
	if !w.cfg.Enabled {
		log.Println("[etc-watcher] disabled in configuration")
		return nil
	}

	log.Println("[etc-watcher] starting /etc directory monitor")

	// Determine which paths to watch.
	watchPaths := w.cfg.WatchPaths
	if len(watchPaths) == 0 {
		watchPaths = []string{"/etc"}
	}

	// Use inotifywait-based approach via Go's native inotify support.
	return w.watchWithInotify(watchPaths)
}

// Stop signals the watcher to stop.
func (w *EtcWatcher) Stop() {
	close(w.stopCh)
}

// shouldIgnore checks if a file path matches any ignore patterns.
func (w *EtcWatcher) shouldIgnore(path string) bool {
	base := filepath.Base(path)
	for _, pattern := range w.cfg.IgnorePatterns {
		if matched, _ := filepath.Match(pattern, base); matched {
			return true
		}
	}
	return false
}

// notifyFileEvent sends a Discord alert about a file event in /etc.
func (w *EtcWatcher) notifyFileEvent(eventType, filePath string) {
	if w.shouldIgnore(filePath) {
		return
	}

	// Try to get file size for context.
	sizeInfo := "—"
	if stat, err := os.Stat(filePath); err == nil {
		sizeInfo = fmt.Sprintf("%d bytes", stat.Size())
	}

	// Try to determine who made the change using /proc.
	user := detectUser()

	alert := notifier.Alert{
		Title:       "🔐 /etc Monitor Alert",
		Description: fmt.Sprintf("A filesystem event was detected in `/etc`."),
		Color:       notifier.ColorSecurity,
		Fields: []notifier.Field{
			{Name: "🖥️ Host", Value: fmt.Sprintf("`%s`", w.hostname), Inline: true},
			{Name: "📁 Event", Value: fmt.Sprintf("**%s**", eventType), Inline: true},
			{Name: "👤 User", Value: fmt.Sprintf("`%s`", user), Inline: true},
			{Name: "📄 Path", Value: fmt.Sprintf("`%s`", filePath), Inline: false},
			{Name: "📊 Size", Value: sizeInfo, Inline: true},
			{Name: "⏰ Time", Value: time.Now().Format("2006-01-02 15:04:05 MST"), Inline: true},
		},
	}

	if err := w.discord.SendAlert(alert); err != nil {
		log.Printf("[etc-watcher] failed to send alert: %v", err)
	}
}

// detectUser tries to find the current user from environment.
func detectUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("LOGNAME"); u != "" {
		return u
	}
	// Try reading /proc/self/status for process UID.
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fmt.Sprintf("uid:%s", fields[1])
			}
		}
	}
	return "unknown"
}
