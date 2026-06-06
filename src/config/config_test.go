package config

import "testing"

func TestLoadTelegramDefaults(t *testing.T) {
	t.Setenv("TELEGRAM_ENABLED", "")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("TELEGRAM_RPC_FAILURE_THRESHOLD", "")
	t.Setenv("TELEGRAM_COOLDOWN_SECS", "")

	cfg, err := Load(t.TempDir() + "/missing.yaml")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Telegram.Enabled {
		t.Fatalf("Telegram.Enabled = true, want false")
	}
	if cfg.Telegram.BotToken != "" {
		t.Fatalf("Telegram.BotToken = %q, want empty", cfg.Telegram.BotToken)
	}
	if cfg.Telegram.ChatID != "" {
		t.Fatalf("Telegram.ChatID = %q, want empty", cfg.Telegram.ChatID)
	}
	if cfg.Telegram.RPCFailureThreshold != 5 {
		t.Fatalf("Telegram.RPCFailureThreshold = %d, want 5", cfg.Telegram.RPCFailureThreshold)
	}
	if cfg.Telegram.CooldownSecs != 1800 {
		t.Fatalf("Telegram.CooldownSecs = %d, want 1800", cfg.Telegram.CooldownSecs)
	}
}

func TestLoadTelegramEnvOverrides(t *testing.T) {
	t.Setenv("TELEGRAM_ENABLED", "true")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_CHAT_ID", "-100123")
	t.Setenv("TELEGRAM_RPC_FAILURE_THRESHOLD", "7")
	t.Setenv("TELEGRAM_COOLDOWN_SECS", "60")

	cfg, err := Load(t.TempDir() + "/missing.yaml")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if !cfg.Telegram.Enabled {
		t.Fatalf("Telegram.Enabled = false, want true")
	}
	if cfg.Telegram.BotToken != "123:abc" {
		t.Fatalf("Telegram.BotToken = %q, want token", cfg.Telegram.BotToken)
	}
	if cfg.Telegram.ChatID != "-100123" {
		t.Fatalf("Telegram.ChatID = %q, want chat id", cfg.Telegram.ChatID)
	}
	if cfg.Telegram.RPCFailureThreshold != 7 {
		t.Fatalf("Telegram.RPCFailureThreshold = %d, want 7", cfg.Telegram.RPCFailureThreshold)
	}
	if cfg.Telegram.CooldownSecs != 60 {
		t.Fatalf("Telegram.CooldownSecs = %d, want 60", cfg.Telegram.CooldownSecs)
	}
}
