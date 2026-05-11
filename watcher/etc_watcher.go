package watcher

import (
	"fmt"
	"log"
	"os"
	"os/exec"
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

// detectUser finds the real login user(s) who are currently logged into the system.
// Since the service runs as root under systemd, we can't use $USER.
// Instead, we use 'who' to find active login sessions — this shows the original
// SSH user even after 'sudo su'.
func detectUser() string {
	// Method 1: Parse 'who' output for active login sessions.
	// This is the most reliable way to find who SSH'd in.
	if users := getActiveLoginUsers(); len(users) > 0 {
		return strings.Join(users, ", ")
	}

	// Method 2: Scan /proc/*/loginuid for non-root login UIDs.
	// The Linux kernel tracks the original login UID (audit UID) which
	// persists through sudo/su.
	if users := getLoginUIDs(); len(users) > 0 {
		return strings.Join(users, ", ")
	}

	return "unknown"
}

// getActiveLoginUsers parses 'who' output to find logged-in users.
// 'who' reads /var/run/utmp and shows the original login user, not the
// effective user after sudo/su.
// Example output: "shafiun  pts/0  2026-05-11 17:00 (192.168.1.5)"
func getActiveLoginUsers() []string {
	out, err := exec.Command("who").Output()
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var users []string

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		username := fields[0]

		// Skip system users / root (we want the real login user).
		if username == "root" || username == "" {
			continue
		}

		if !seen[username] {
			seen[username] = true
			users = append(users, username)
		}
	}

	return users
}

// getLoginUIDs scans /proc/*/loginuid to find non-root login UIDs.
// The loginuid is set at login and never changes, even after sudo su.
// UID 4294967295 (0xFFFFFFFF) means "not set" (kernel default).
func getLoginUIDs() []string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var users []string

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Only look at numeric PID directories.
		name := entry.Name()
		if len(name) == 0 || name[0] < '0' || name[0] > '9' {
			continue
		}

		data, err := os.ReadFile(filepath.Join("/proc", name, "loginuid"))
		if err != nil {
			continue
		}

		uidStr := strings.TrimSpace(string(data))
		// Skip unset (4294967295) and root (0).
		if uidStr == "4294967295" || uidStr == "0" || uidStr == "" {
			continue
		}

		if !seen[uidStr] {
			seen[uidStr] = true
			username := resolveUID(uidStr)
			users = append(users, username)
		}
	}

	return users
}

// resolveUID converts a numeric UID to a username by reading /etc/passwd.
func resolveUID(uid string) string {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return "uid:" + uid
	}

	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 4)
		if len(parts) >= 3 && parts[2] == uid {
			return parts[0]
		}
	}

	return "uid:" + uid
}

