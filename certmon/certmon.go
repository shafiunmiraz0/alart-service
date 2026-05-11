package certmon

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alart-service/notifier"
)

// K8sCertMonitorConfig controls the Kubernetes certificate expiration monitoring.
// This is opt-in: if the key is absent from the config JSON, the feature is disabled.
type K8sCertMonitorConfig struct {
	// How often to check certificate expiration (e.g. "6h", "12h", "24h").
	CheckInterval string `json:"check_interval"`

	// Directories or files to scan for certificates.
	// Default: ["/etc/kubernetes/pki"]
	CertPaths []string `json:"cert_paths"`

	// Days before expiry to trigger warnings.
	// Alerts are sent at each threshold as it's crossed.
	// Default: [30, 14, 7, 1]
	WarningDays []int `json:"warning_days"`
}

// CertInfo holds parsed certificate metadata.
type CertInfo struct {
	Path      string
	Subject   string
	Issuer    string
	NotAfter  time.Time
	DaysLeft  int
	IsCA      bool
}

// CertMonitor checks K8s certificate expiration dates.
type CertMonitor struct {
	cfg      *K8sCertMonitorConfig
	discord  *notifier.Discord
	hostname string
	stopCh   chan struct{}

	// Track which (cert, threshold) pairs we already alerted on.
	mu      sync.Mutex
	alerted map[string]bool
}

// New creates a new CertMonitor. Returns nil if config is nil (feature disabled).
func New(cfg *K8sCertMonitorConfig, discord *notifier.Discord) *CertMonitor {
	if cfg == nil {
		return nil
	}

	// Apply defaults.
	if cfg.CheckInterval == "" {
		cfg.CheckInterval = "6h"
	}
	if len(cfg.CertPaths) == 0 {
		cfg.CertPaths = []string{"/etc/kubernetes/pki"}
	}
	if len(cfg.WarningDays) == 0 {
		cfg.WarningDays = []int{30, 14, 7, 1}
	}

	// Sort warning days descending.
	sort.Sort(sort.Reverse(sort.IntSlice(cfg.WarningDays)))

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	return &CertMonitor{
		cfg:      cfg,
		discord:  discord,
		hostname: hostname,
		stopCh:   make(chan struct{}),
		alerted:  make(map[string]bool),
	}
}

// Start begins the certificate monitoring loop. Blocks — run in a goroutine.
func (cm *CertMonitor) Start() {
	if cm == nil {
		return
	}

	interval, err := time.ParseDuration(cm.cfg.CheckInterval)
	if err != nil {
		interval = 6 * time.Hour
	}

	log.Printf("[cert-monitor] started — checking every %s across %v", interval, cm.cfg.CertPaths)
	log.Printf("[cert-monitor] warning thresholds: %v days", cm.cfg.WarningDays)

	// Do an initial check immediately.
	cm.checkAll()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-cm.stopCh:
			log.Println("[cert-monitor] stopped")
			return
		case <-ticker.C:
			cm.checkAll()
		}
	}
}

// Stop signals the monitor to stop.
func (cm *CertMonitor) Stop() {
	if cm == nil {
		return
	}
	close(cm.stopCh)
}

// checkAll scans all configured cert paths and checks expiration.
func (cm *CertMonitor) checkAll() {
	var certs []CertInfo

	for _, p := range cm.cfg.CertPaths {
		found, err := cm.scanPath(p)
		if err != nil {
			log.Printf("[cert-monitor] error scanning %s: %v", p, err)
			continue
		}
		certs = append(certs, found...)
	}

	if len(certs) == 0 {
		log.Println("[cert-monitor] no certificates found in configured paths")
		return
	}

	log.Printf("[cert-monitor] scanned %d certificates", len(certs))

	now := time.Now()
	expiredCount := 0
	warningCount := 0

	for _, cert := range certs {
		daysLeft := int(cert.NotAfter.Sub(now).Hours() / 24)
		cert.DaysLeft = daysLeft

		if daysLeft < 0 {
			// Already expired!
			expiredCount++
			cm.sendAlertIfNew(cert, 0, true)
			continue
		}

		// Check against warning thresholds.
		for _, threshold := range cm.cfg.WarningDays {
			if daysLeft <= threshold {
				warningCount++
				cm.sendAlertIfNew(cert, threshold, false)
				break // Only alert at the most urgent threshold.
			}
		}
	}

	if expiredCount > 0 {
		log.Printf("[cert-monitor] ⚠️  %d certificates EXPIRED", expiredCount)
	}
	if warningCount > 0 {
		log.Printf("[cert-monitor] ⚠️  %d certificates approaching expiry", warningCount)
	}
}

// scanPath scans a file or directory for .crt/.pem files.
func (cm *CertMonitor) scanPath(path string) ([]CertInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	var certs []CertInfo

	if !info.IsDir() {
		// Single file.
		parsed, err := parseCertFile(path)
		if err == nil {
			certs = append(certs, parsed...)
		}
		return certs, nil
	}

	// Walk directory.
	err = filepath.Walk(path, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip inaccessible.
		}
		if fi.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".crt" || ext == ".pem" || ext == ".cert" {
			parsed, err := parseCertFile(p)
			if err == nil {
				certs = append(certs, parsed...)
			}
		}
		return nil
	})

	return certs, err
}

// parseCertFile reads a PEM file and extracts certificate info.
func parseCertFile(path string) ([]CertInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var certs []CertInfo
	rest := data

	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}

		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}

		certs = append(certs, CertInfo{
			Path:     path,
			Subject:  cert.Subject.CommonName,
			Issuer:   cert.Issuer.CommonName,
			NotAfter: cert.NotAfter,
			IsCA:     cert.IsCA,
		})
	}

	if len(certs) == 0 {
		return nil, fmt.Errorf("no certificates found in %s", path)
	}

	return certs, nil
}

// sendAlertIfNew sends a Discord alert if we haven't already alerted for this cert+threshold.
func (cm *CertMonitor) sendAlertIfNew(cert CertInfo, threshold int, expired bool) {
	// Build a unique key: cert path + subject + threshold.
	key := fmt.Sprintf("%s|%s|%d", cert.Path, cert.Subject, threshold)

	cm.mu.Lock()
	if cm.alerted[key] {
		cm.mu.Unlock()
		return
	}
	cm.alerted[key] = true
	cm.mu.Unlock()

	var title, description string
	var color int

	certType := "Certificate"
	if cert.IsCA {
		certType = "CA Certificate"
	}

	if expired {
		title = "🚨 K8s Certificate EXPIRED"
		description = fmt.Sprintf("A Kubernetes %s has **already expired**! Cluster operations may be affected.", certType)
		color = notifier.ColorCritical
	} else {
		title = "⏳ K8s Certificate Expiring Soon"
		description = fmt.Sprintf("A Kubernetes %s will expire in **%d days**.", certType, cert.DaysLeft)
		if cert.DaysLeft <= 7 {
			color = notifier.ColorDanger
		} else {
			color = notifier.ColorWarning
		}
	}

	daysLeftStr := fmt.Sprintf("%d days", cert.DaysLeft)
	if cert.DaysLeft < 0 {
		daysLeftStr = fmt.Sprintf("**EXPIRED** (%d days ago)", -cert.DaysLeft)
	} else if cert.DaysLeft == 0 {
		daysLeftStr = "**EXPIRES TODAY**"
	} else if cert.DaysLeft == 1 {
		daysLeftStr = "**1 day** ⚠️"
	}

	alert := notifier.Alert{
		Title:       title,
		Description: description,
		Color:       color,
		Fields: []notifier.Field{
			{Name: "🖥️ Host", Value: fmt.Sprintf("`%s`", cm.hostname), Inline: true},
			{Name: "📜 Subject", Value: fmt.Sprintf("`%s`", cert.Subject), Inline: true},
			{Name: "⏳ Days Left", Value: daysLeftStr, Inline: true},
			{Name: "📄 File", Value: fmt.Sprintf("`%s`", cert.Path), Inline: false},
			{Name: "🏛️ Issuer", Value: fmt.Sprintf("`%s`", cert.Issuer), Inline: true},
			{Name: "📅 Expires", Value: cert.NotAfter.Format("2006-01-02 15:04 MST"), Inline: true},
		},
	}

	if err := cm.discord.SendAlert(alert); err != nil {
		log.Printf("[cert-monitor] failed to send alert for %s: %v", cert.Subject, err)
	}
}

// ResetAlerts clears the alert dedup map (called on config reload so thresholds re-fire).
func (cm *CertMonitor) ResetAlerts() {
	if cm == nil {
		return
	}
	cm.mu.Lock()
	cm.alerted = make(map[string]bool)
	cm.mu.Unlock()
}
