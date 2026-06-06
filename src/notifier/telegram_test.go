package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"blockscanner/config"
)

func TestTelegramDisabledDoesNotSend(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	n := NewTelegramWithBaseURL(config.TelegramConfig{Enabled: false}, server.URL, server.Client())
	if err := n.SendMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if called {
		t.Fatalf("disabled notifier made an HTTP request")
	}
}

func TestTelegramMissingConfigDoesNotSend(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	n := NewTelegramWithBaseURL(config.TelegramConfig{Enabled: true, BotToken: "", ChatID: ""}, server.URL, server.Client())
	if err := n.SendMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if called {
		t.Fatalf("incomplete notifier made an HTTP request")
	}
}

func TestTelegramSendMessagePostsPayload(t *testing.T) {
	var path string
	var payload map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	n := NewTelegramWithBaseURL(config.TelegramConfig{
		Enabled:  true,
		BotToken: "123:abc",
		ChatID:   "-100123",
	}, server.URL, server.Client())

	if err := n.SendMessage(context.Background(), "rpc failed"); err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if path != "/bot123:abc/sendMessage" {
		t.Fatalf("path = %q, want /bot123:abc/sendMessage", path)
	}
	if payload["chat_id"] != "-100123" {
		t.Fatalf("chat_id = %q, want -100123", payload["chat_id"])
	}
	if payload["text"] != "rpc failed" {
		t.Fatalf("text = %q, want rpc failed", payload["text"])
	}
}

func TestTelegramSendMessageEscapesBotTokenPathSegment(t *testing.T) {
	var escapedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escapedPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	n := NewTelegramWithBaseURL(config.TelegramConfig{
		Enabled:  true,
		BotToken: "abc/def?x",
		ChatID:   "-100123",
	}, server.URL, server.Client())

	if err := n.SendMessage(context.Background(), "rpc failed"); err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if escapedPath != "/botabc%2Fdef%3Fx/sendMessage" {
		t.Fatalf("escaped path = %q, want /botabc%%2Fdef%%3Fx/sendMessage", escapedPath)
	}
}

func TestTelegramSendMessageRedactsBotTokenFromTransportError(t *testing.T) {
	const token = "123456:secret-token-value"
	n := NewTelegramWithBaseURL(config.TelegramConfig{
		Enabled:  true,
		BotToken: token,
		ChatID:   "-100123",
	}, "https://api.telegram.test", &http.Client{Transport: urlEchoErrorRoundTripper{}})

	err := n.SendMessage(context.Background(), "rpc failed")
	if err == nil {
		t.Fatal("SendMessage returned nil error")
	}

	errorText := err.Error()
	if strings.Contains(errorText, token) {
		t.Fatalf("SendMessage error leaked bot token: %s", errorText)
	}
	if strings.Contains(errorText, "/bot"+token+"/sendMessage") {
		t.Fatalf("SendMessage error leaked token endpoint: %s", errorText)
	}
	if !strings.Contains(errorText, "send telegram message") {
		t.Fatalf("SendMessage error missing operation context: %s", errorText)
	}
	if !strings.Contains(errorText, "transport failed") {
		t.Fatalf("SendMessage error missing safe transport context: %s", errorText)
	}
	if !strings.Contains(errorText, "<telegram-bot-token>") {
		t.Fatalf("SendMessage error missing redacted token placeholder: %s", errorText)
	}
}

type urlEchoErrorRoundTripper struct{}

func (urlEchoErrorRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("transport failed for %s", req.URL.String())
}

func TestRedactRPCURL(t *testing.T) {
	got := RedactRPCURL("https://rpc.example.com/v1/secretToken123456?apikey=abcdef")
	if strings.Contains(got, "apikey") || strings.Contains(got, "abcdef") || strings.Contains(got, "secretToken123456") {
		t.Fatalf("redacted URL leaked secret: %s", got)
	}
	if !strings.HasPrefix(got, "https://rpc.example.com/") {
		t.Fatalf("redacted URL = %q, want host preserved", got)
	}
}

func TestRedactRPCURLRemovesUserinfo(t *testing.T) {
	got := RedactRPCURL("https://alice:secret@rpc.example.com/v1/foo?apikey=abcdef")
	if strings.Contains(got, "alice") || strings.Contains(got, "secret") || strings.Contains(got, "@") {
		t.Fatalf("redacted URL leaked userinfo: %s", got)
	}
	if !strings.Contains(got, "rpc.example.com") {
		t.Fatalf("redacted URL = %q, want host preserved", got)
	}
}
