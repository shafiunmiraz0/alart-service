package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Embed color constants for Discord.
const (
	ColorSuccess  = 0x2ECC71 // Green — startup, healthy
	ColorDanger   = 0xE74C3C // Red — critical alerts (CPU, RAM, Disk)
	ColorWarning  = 0xF39C12 // Orange — warning alerts (I/O, Network)
	ColorInfo     = 0x3498DB // Blue — info (config reload)
	ColorCritical = 0xC0392B // Dark red — shutdown, crash, unexpected reboot
	ColorSecurity = 0x9B59B6 // Purple — /etc file changes
)

// Default Discord webhook identity.
const (
	DefaultUsername = "🚨 Alart Service"
	DefaultAvatar  = "https://raw.githubusercontent.com/shafiunmiraz0/alart-service/main/assets/logo.png"
	footerText     = "alart-service v1.0.0"
)

// Field represents a single field in a Discord embed.
type Field struct {
	Name   string
	Value  string
	Inline bool
}

// Alert represents a rich Discord embed message.
type Alert struct {
	Title       string
	Description string
	Color       int
	Fields      []Field
}

// Discord sends alert messages to a Discord webhook.
type Discord struct {
	mu         sync.RWMutex
	webhookURL string
	avatarURL  string
	client     *http.Client
}

// NewDiscord creates a new Discord notifier.
func NewDiscord(webhookURL, avatarURL string) *Discord {
	if avatarURL == "" {
		avatarURL = DefaultAvatar
	}
	return &Discord{
		webhookURL: webhookURL,
		avatarURL:  avatarURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// UpdateWebhookURL safely updates the webhook URL (used during config reload).
func (d *Discord) UpdateWebhookURL(url string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.webhookURL = url
}

// UpdateAvatarURL safely updates the avatar URL.
func (d *Discord) UpdateAvatarURL(url string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if url != "" {
		d.avatarURL = url
	}
}

func (d *Discord) getWebhookURL() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.webhookURL
}

func (d *Discord) getAvatarURL() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.avatarURL
}

// --- Internal Discord payload types ---

type embedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type embedFooter struct {
	Text    string `json:"text"`
	IconURL string `json:"icon_url,omitempty"`
}

type discordEmbed struct {
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color"`
	Fields      []embedField `json:"fields,omitempty"`
	Timestamp   string       `json:"timestamp"`
	Footer      embedFooter  `json:"footer"`
}

type webhookPayload struct {
	Username  string         `json:"username"`
	AvatarURL string         `json:"avatar_url,omitempty"`
	Embeds    []discordEmbed `json:"embeds"`
}

// SendAlert sends a rich embed alert to Discord with avatar and branding.
func (d *Discord) SendAlert(alert Alert) error {
	var fields []embedField
	for _, f := range alert.Fields {
		fields = append(fields, embedField{
			Name:   f.Name,
			Value:  f.Value,
			Inline: f.Inline,
		})
	}

	avatarURL := d.getAvatarURL()

	payload := webhookPayload{
		Username:  DefaultUsername,
		AvatarURL: avatarURL,
		Embeds: []discordEmbed{{
			Title:       alert.Title,
			Description: alert.Description,
			Color:       alert.Color,
			Fields:      fields,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			Footer: embedFooter{
				Text:    footerText,
				IconURL: avatarURL,
			},
		}},
	}

	return d.post(payload)
}

// Send sends a simple text message wrapped in an embed (backwards compatible).
func (d *Discord) Send(message string) error {
	return d.SendAlert(Alert{
		Description: message,
		Color:       ColorInfo,
	})
}

// post marshals and sends the payload to Discord.
func (d *Discord) post(payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	url := d.getWebhookURL()
	resp, err := d.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("post to discord: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("discord rate limited (429)")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
