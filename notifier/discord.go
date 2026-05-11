package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Discord sends alert messages to a Discord webhook.
type Discord struct {
	webhookURL string
	client     *http.Client
}

// NewDiscord creates a new Discord notifier.
func NewDiscord(webhookURL string) *Discord {
	return &Discord{
		webhookURL: webhookURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// discordPayload is the JSON body Discord expects.
type discordPayload struct {
	Content string `json:"content"`
}

// Send sends a message to the configured Discord webhook.
func (d *Discord) Send(message string) error {
	payload := discordPayload{Content: message}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	resp, err := d.client.Post(d.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("post to discord: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		// Discord rate limiting — log and retry later.
		return fmt.Errorf("discord rate limited (429)")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// SendEmbed sends a richer embedded message to Discord.
func (d *Discord) SendEmbed(title, description string, color int, fields map[string]string) error {
	type embedField struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Inline bool   `json:"inline"`
	}

	type embed struct {
		Title       string       `json:"title"`
		Description string       `json:"description"`
		Color       int          `json:"color"`
		Fields      []embedField `json:"fields,omitempty"`
		Timestamp   string       `json:"timestamp"`
	}

	type embedPayload struct {
		Embeds []embed `json:"embeds"`
	}

	var ef []embedField
	for k, v := range fields {
		ef = append(ef, embedField{Name: k, Value: v, Inline: true})
	}

	p := embedPayload{
		Embeds: []embed{{
			Title:       title,
			Description: description,
			Color:       color,
			Fields:      ef,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
		}},
	}

	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal embed: %w", err)
	}

	resp, err := d.client.Post(d.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("post embed to discord: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord embed returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
