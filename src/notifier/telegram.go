package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"blockscanner/config"
)

// Sender sends notification messages.
type Sender interface {
	SendMessage(context.Context, string) error
}

// Noop is a notification sender that does nothing.
type Noop struct{}

// SendMessage discards the message and returns nil.
func (Noop) SendMessage(context.Context, string) error {
	return nil
}

// Telegram sends notifications via the Telegram Bot API.
type Telegram struct {
	cfg     config.TelegramConfig
	baseURL string
	client  *http.Client
}

// NewTelegram creates a Telegram notifier using the public Telegram API.
func NewTelegram(cfg config.TelegramConfig) *Telegram {
	return NewTelegramWithBaseURL(cfg, "https://api.telegram.org", &http.Client{Timeout: 10 * time.Second})
}

// NewTelegramWithBaseURL creates a Telegram notifier with an injectable API base URL and HTTP client.
func NewTelegramWithBaseURL(cfg config.TelegramConfig, baseURL string, client *http.Client) *Telegram {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	return &Telegram{
		cfg:     cfg,
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  client,
	}
}

func (t *Telegram) enabled() bool {
	return t.cfg.Enabled && t.cfg.BotToken != "" && t.cfg.ChatID != ""
}

// SendMessage posts text to the configured Telegram chat when the notifier is enabled.
func (t *Telegram) SendMessage(ctx context.Context, text string) error {
	if !t.enabled() {
		slog.Debug("telegram notifier disabled or incomplete")
		return nil
	}

	payload := map[string]string{
		"chat_id": t.cfg.ChatID,
		"text":    text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telegram message: %w", err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", t.baseURL, t.cfg.BotToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("send telegram message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("telegram send message returned status %d", resp.StatusCode)
	}

	return nil
}

// RedactRPCURL removes secrets from an RPC URL while preserving the scheme and host.
func RedactRPCURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "<invalid-rpc-url>"
	}

	u.RawQuery = ""
	u.Fragment = ""

	segments := strings.Split(u.EscapedPath(), "/")
	for i, segment := range segments {
		if segment == "" {
			continue
		}

		unescaped, err := url.PathUnescape(segment)
		if err != nil {
			unescaped = segment
		}
		lower := strings.ToLower(unescaped)
		if len(unescaped) >= 12 || strings.Contains(lower, "key") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") {
			segments[i] = "***"
		}
	}
	u.Path = strings.Join(segments, "/")
	u.RawPath = ""

	return u.String()
}
