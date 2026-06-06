package notifier

import (
	"context"
	"encoding/json"
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
