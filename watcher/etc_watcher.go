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

	// Try to determine who specifically made the change to this file.
	user := detectFileModifier(filePath)

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

// detectFileModifier identifies the specific user who modified a file.
// Unlike the old approach that listed ALL logged-in users, this uses
// targeted methods to find who actually touched the file:
//  1. ausearch  – queries the Linux audit log for recent writes to the file
//  2. lsof      – finds processes that currently have the file open
//  3. /proc scan – checks /proc/*/fd for open file descriptors pointing to the file
//  4. fallback  – returns "unknown" (no longer dumps all SSH users)
func detectFileModifier(filePath string) string {
	// Method 1: Use auditd's ausearch to find who wrote to this file.
	// This is the most accurate — the audit subsystem records the real UID
	// even through sudo/su. Requires auditd to be running with a watch on /etc.
	if user := getUserFromAuditLog(filePath); user != "" {
		return user
	}

	// Method 2: Use lsof to find who currently has the file open.
	if user := getUserFromLsof(filePath); user != "" {
		return user
	}

	// Method 3: Scan /proc/*/fd to find a process with this file open,
	// then resolve the process owner via loginuid.
	if user := getUserFromProcFd(filePath); user != "" {
		return user
	}

	return "unknown"
}

// getUserFromAuditLog queries ausearch for the MOST RECENT write event on the file.
// We fetch all events (no --just-one, which returns the oldest) and extract the
// auid from the LAST SYSCALL record — that's the actual person who just did it.
func getUserFromAuditLog(filePath string) string {
	// Brief pause so auditd can flush the event to the log before we query.
	time.Sleep(200 * time.Millisecond)

	// Use "-ts recent" (last 10 minutes) — simple and reliable.
	// We take the LAST SYSCALL record below, so stale events are harmless.
	out, err := exec.Command("ausearch", "-f", filePath, "-i", "-ts", "recent").CombinedOutput()
	if len(out) == 0 {
		if err != nil {
			log.Printf("[etc-watcher] ausearch error: %v", err)
		}
		return ""
	}

	output := string(out)
	log.Printf("[etc-watcher] ausearch returned %d bytes for %s", len(out), filePath)

	// Parse output and find the LAST SYSCALL line — that's the most recent event.
	// ausearch output is chronological, so the last matching record is the one
	// that corresponds to the inotify event we just received.
	lines := strings.Split(output, "\n")
	lastAuid := ""
	lastUid := ""

	for _, line := range lines {
		// SYSCALL lines contain both auid= and uid=.
		if !strings.Contains(line, "SYSCALL") {
			continue
		}

		if val := extractAuditValue(line, "auid="); val != "" && val != "unset" && val != "root" {
			lastAuid = val
		}
		if val := extractAuditValue(line, " uid="); val != "" && val != "unset" {
			lastUid = val
		}
	}

	// Prefer auid (original login user, survives sudo/su) over uid.
	if lastAuid != "" {
		return lastAuid
	}
	if lastUid != "" {
		return lastUid
	}

	// If we got output but no SYSCALL lines, log it for debugging.
	if lastAuid == "" && lastUid == "" && len(output) > 0 {
		// Log first 300 chars to see what ausearch actually returned.
		preview := output
		if len(preview) > 300 {
			preview = preview[:300]
		}
		log.Printf("[etc-watcher] ausearch output had no SYSCALL lines: %s", preview)
	}

	return ""
}

// extractAuditValue extracts the value after a key like "auid=" from an audit line.
func extractAuditValue(line, key string) string {
	idx := strings.Index(line, key)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(key):]
	// Value ends at space or end of line.
	end := strings.IndexAny(rest, " \t\n")
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

// getUserFromLsof uses lsof to find which user has the file open.
// Example: lsof /etc/shadow
// Output: COMMAND  PID  USER  FD  TYPE  DEVICE  SIZE/OFF  NODE  NAME
func getUserFromLsof(filePath string) string {
	out, err := exec.Command("lsof", filePath).Output()
	if err != nil || len(out) == 0 {
		return ""
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	seen := make(map[string]bool)
	var users []string

	for _, line := range lines[1:] { // Skip the header line.
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		username := fields[2]
		if username == "root" || username == "" {
			// Root ran the command via sudo; try to resolve original user
			// via loginuid of the PID.
			if len(fields) >= 2 {
				if origUser := resolveLoginUIDByPID(fields[1]); origUser != "" {
					username = origUser
				}
			}
		}
		if username != "" && username != "root" && !seen[username] {
			seen[username] = true
			users = append(users, username)
		}
	}

	if len(users) > 0 {
		return strings.Join(users, ", ")
	}
	return ""
}

// getUserFromProcFd scans /proc/*/fd to find which process has the file open,
// then resolves that PID's loginuid to identify the original user.
func getUserFromProcFd(filePath string) string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return ""
	}

	// Resolve the real path of the target file for comparison.
	targetReal, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		targetReal = filePath
	}

	seen := make(map[string]bool)
	var users []string

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid := entry.Name()
		if len(pid) == 0 || pid[0] < '0' || pid[0] > '9' {
			continue
		}

		fdDir := filepath.Join("/proc", pid, "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}

		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if link == targetReal || link == filePath {
				// This PID has the file open. Resolve its loginuid.
				user := resolveLoginUIDByPID(pid)
				if user != "" && user != "root" && !seen[user] {
					seen[user] = true
					users = append(users, user)
				}
				break
			}
		}
	}

	if len(users) > 0 {
		return strings.Join(users, ", ")
	}
	return ""
}

// resolveLoginUIDByPID reads /proc/<pid>/loginuid and resolves it to a username.
func resolveLoginUIDByPID(pid string) string {
	data, err := os.ReadFile(filepath.Join("/proc", pid, "loginuid"))
	if err != nil {
		return ""
	}
	uidStr := strings.TrimSpace(string(data))
	if uidStr == "4294967295" || uidStr == "0" || uidStr == "" {
		return ""
	}
	return resolveUID(uidStr)
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

