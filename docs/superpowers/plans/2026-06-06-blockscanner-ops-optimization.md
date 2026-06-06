# BlockScanner Ops Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove startup seed data, make backend DB configuration changes effective within 60 seconds, notify the Telegram ops group after 5 consecutive RPC failures, and persist scan progress/errors to logs and job logs.

**Architecture:** Keep the current cron-driven scanner. Add focused units: `notifier` for Telegram delivery, RPC error typing in `scanner`, refresh and alert state in `scheduler`, and job-log update support in `store`. Scheduler remains the orchestrator: it refreshes DB jobs every 60 seconds, wraps cron executions with `infra_job_log`, and updates per-chain RPC alert state.

**Tech Stack:** Go 1.26.3, slog, GORM/MySQL, robfig/cron v3, net/http Telegram Bot API, Go standard `testing` package.

---

## File Structure

- Modify `src/config/config.go`
  - Add `TelegramConfig` to `Config`.
  - Add defaults and environment overrides for Telegram settings.
- Modify `config.yaml`
  - Document Telegram settings with disabled defaults.
- Create `src/config/config_test.go`
  - Verify Telegram defaults and environment overrides.
- Create `src/notifier/telegram.go`
  - Implement Telegram Bot API sender and RPC URL redaction.
- Create `src/notifier/telegram_test.go`
  - Verify disabled behavior, message POST payload, and URL redaction.
- Modify `src/scanner/rpc.go`
  - Add typed `RPCError` and `IsRPCError`.
  - Wrap JSON-RPC/HTTP/response failures while preserving existing public methods.
- Create `src/scanner/rpc_test.go`
  - Verify `IsRPCError` detects wrapped RPC errors.
- Modify `src/main.go`
  - Remove `seedData` call and function.
  - Wire Telegram notifier into scheduler.
- Modify `src/store/job.go`
  - Keep existing `CreateJobLog`.
  - Add `UpdateJobLog`.
  - Adjust `UpsertJob` so existing jobs keep their manual status.
  - Filter deleted rows in `GetEnabledJobs`.
- Modify `src/scheduler/scheduler.go`
  - Add notifier dependency, refresh interval, job entry tracking, and RPC alert state.
  - Replace one-time sync with immediate refresh plus 60-second refresh loop.
  - Add stale cron removal.
  - Wrap cron executions with job logs.
  - Record RPC failure/recovery notifications.
- Create `src/scheduler/alerts.go`
  - Keep RPC alert state and notification helpers focused and testable.
- Create `src/scheduler/alerts_test.go`
  - Verify failure threshold, cooldown, and recovery notification behavior.
- Create `src/scheduler/job_refresh_test.go`
  - Verify pure helper logic for effective jobs and stale removals.
- Modify `src/scanner/worker.go`
  - Add progress/error log fields and RPC duration logs.
- Optional if needed during implementation: create small helper functions inside existing packages rather than adding broad abstractions.

Run all Go commands from `/home/v3/workspace/BlockScanner/src` unless a step explicitly says otherwise.

---

### Task 1: Telegram Configuration

**Files:**
- Modify: `src/config/config.go`
- Modify: `config.yaml`
- Create: `src/config/config_test.go`

- [ ] **Step 1: Write failing tests for Telegram defaults and env overrides**

Create `src/config/config_test.go` with:

```go
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
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
go test ./config
```

Expected: FAIL because `Config.Telegram` does not exist.

- [ ] **Step 3: Add Telegram config fields and env parsing**

Modify `src/config/config.go` as follows.

Add `Telegram` to `Config`:

```go
type Config struct {
	DB       DBConfig       `yaml:"db"`
	Log      LogConfig      `yaml:"log"`
	App      AppConfig      `yaml:"app"`
	Telegram TelegramConfig `yaml:"telegram"`
}
```

Add this struct after `AppConfig`:

```go
// TelegramConfig 运营通知配置
type TelegramConfig struct {
	Enabled             bool   `yaml:"enabled"`
	BotToken            string `yaml:"bot_token"`
	ChatID              string `yaml:"chat_id"`
	RPCFailureThreshold int    `yaml:"rpc_failure_threshold"`
	CooldownSecs        int    `yaml:"cooldown_secs"`
}
```

Add defaults inside `defaultConfig()`:

```go
Telegram: TelegramConfig{
	Enabled:             false,
	BotToken:            "",
	ChatID:              "",
	RPCFailureThreshold: 5,
	CooldownSecs:        1800,
},
```

Add this helper near `applyEnvOverrides`:

```go
func parseBoolEnv(v string) (bool, bool) {
	switch v {
	case "1", "true", "TRUE", "True", "yes", "YES", "on", "ON":
		return true, true
	case "0", "false", "FALSE", "False", "no", "NO", "off", "OFF":
		return false, true
	default:
		return false, false
	}
}
```

Add these environment overrides to `applyEnvOverrides`:

```go
if v := os.Getenv("TELEGRAM_ENABLED"); v != "" {
	if enabled, ok := parseBoolEnv(v); ok {
		cfg.Telegram.Enabled = enabled
	}
}
if v := os.Getenv("TELEGRAM_BOT_TOKEN"); v != "" {
	cfg.Telegram.BotToken = v
}
if v := os.Getenv("TELEGRAM_CHAT_ID"); v != "" {
	cfg.Telegram.ChatID = v
}
if v := os.Getenv("TELEGRAM_RPC_FAILURE_THRESHOLD"); v != "" {
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		cfg.Telegram.RPCFailureThreshold = n
	}
}
if v := os.Getenv("TELEGRAM_COOLDOWN_SECS"); v != "" {
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		cfg.Telegram.CooldownSecs = n
	}
}
```

- [ ] **Step 4: Document Telegram defaults in root config**

Append to `config.yaml` after the `app` block:

```yaml

telegram:
  enabled: false                 # 是否启用 Telegram 通知
  bot_token: ""                  # Telegram Bot Token，生产建议用环境变量 TELEGRAM_BOT_TOKEN
  chat_id: ""                    # 运营群 Chat ID，生产建议用环境变量 TELEGRAM_CHAT_ID
  rpc_failure_threshold: 5       # RPC 连续失败次数达到该值后告警
  cooldown_secs: 1800            # 持续故障时重复告警冷却时间（秒）
```

- [ ] **Step 5: Run tests and verify they pass**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
go test ./config
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git -C /home/v3/workspace/BlockScanner add config.yaml src/config/config.go src/config/config_test.go
git -C /home/v3/workspace/BlockScanner commit -m "feat: add telegram notification config"
```

---

### Task 2: Telegram Notifier Package

**Files:**
- Create: `src/notifier/telegram.go`
- Create: `src/notifier/telegram_test.go`

- [ ] **Step 1: Write failing notifier tests**

Create `src/notifier/telegram_test.go` with:

```go
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

func TestRedactRPCURL(t *testing.T) {
	got := RedactRPCURL("https://rpc.example.com/v1/secretToken123456?apikey=abcdef")
	if strings.Contains(got, "apikey") || strings.Contains(got, "abcdef") || strings.Contains(got, "secretToken123456") {
		t.Fatalf("redacted URL leaked secret: %s", got)
	}
	if !strings.HasPrefix(got, "https://rpc.example.com/") {
		t.Fatalf("redacted URL = %q, want host preserved", got)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
go test ./notifier
```

Expected: FAIL because the `notifier` package does not exist.

- [ ] **Step 3: Implement notifier**

Create `src/notifier/telegram.go` with:

```go
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

// Sender sends operational notifications.
type Sender interface {
	SendMessage(ctx context.Context, text string) error
}

// Noop never sends external requests.
type Noop struct{}

func (Noop) SendMessage(ctx context.Context, text string) error { return nil }

// Telegram sends notifications through Telegram Bot API.
type Telegram struct {
	cfg     config.TelegramConfig
	baseURL string
	client  *http.Client
}

func NewTelegram(cfg config.TelegramConfig) *Telegram {
	return NewTelegramWithBaseURL(cfg, "https://api.telegram.org", &http.Client{Timeout: 10 * time.Second})
}

func NewTelegramWithBaseURL(cfg config.TelegramConfig, baseURL string, client *http.Client) *Telegram {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Telegram{cfg: cfg, baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (t *Telegram) enabled() bool {
	return t != nil && t.cfg.Enabled && t.cfg.BotToken != "" && t.cfg.ChatID != ""
}

func (t *Telegram) SendMessage(ctx context.Context, text string) error {
	if !t.enabled() {
		slog.Debug("telegram notifier disabled or incomplete", "component", "notifier")
		return nil
	}

	payload := map[string]string{
		"chat_id": t.cfg.ChatID,
		"text":    text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telegram payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", t.baseURL, t.cfg.BotToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("send telegram request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram http status %d", resp.StatusCode)
	}
	return nil
}

func RedactRPCURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid-rpc-url>"
	}
	u.RawQuery = ""
	u.Fragment = ""

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, part := range parts {
		lower := strings.ToLower(part)
		if len(part) >= 12 || strings.Contains(lower, "key") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") {
			parts[i] = "***"
		}
	}
	if len(parts) == 1 && parts[0] == "" {
		u.Path = ""
	} else {
		u.Path = "/" + strings.Join(parts, "/")
	}
	return u.String()
}
```

- [ ] **Step 4: Run notifier tests**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
go test ./notifier
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git -C /home/v3/workspace/BlockScanner add src/notifier/telegram.go src/notifier/telegram_test.go
git -C /home/v3/workspace/BlockScanner commit -m "feat: add telegram notifier"
```

---

### Task 3: Typed RPC Errors

**Files:**
- Modify: `src/scanner/rpc.go`
- Create: `src/scanner/rpc_test.go`

- [ ] **Step 1: Write failing RPC error tests**

Create `src/scanner/rpc_test.go` with:

```go
package scanner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsRPCErrorDetectsWrappedHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewRPCClient(server.URL)
	_, err := client.BlockNumber(context.Background())
	if err == nil {
		t.Fatalf("BlockNumber returned nil error")
	}
	if !IsRPCError(err) {
		t.Fatalf("IsRPCError(%v) = false, want true", err)
	}

	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error does not unwrap to RPCError: %v", err)
	}
	if rpcErr.Method != "eth_blockNumber" {
		t.Fatalf("RPCError.Method = %q, want eth_blockNumber", rpcErr.Method)
	}
}

func TestIsRPCErrorReturnsFalseForNonRPCError(t *testing.T) {
	if IsRPCError(errors.New("database failed")) {
		t.Fatalf("IsRPCError returned true for non-RPC error")
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
go test ./scanner
```

Expected: FAIL because `RPCError` and `IsRPCError` do not exist.

- [ ] **Step 3: Add RPCError type and helpers**

Modify `src/scanner/rpc.go` imports to include `errors`.

Add below `jsonRPCError`:

```go
// RPCError marks failures that came from the JSON-RPC transport or response.
type RPCError struct {
	Method string
	Err    error
}

func (e *RPCError) Error() string {
	if e == nil || e.Err == nil {
		return "rpc error"
	}
	return fmt.Sprintf("%s: %v", e.Method, e.Err)
}

func (e *RPCError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func IsRPCError(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr)
}

func wrapRPCError(method string, err error) error {
	if err == nil {
		return nil
	}
	if IsRPCError(err) {
		return err
	}
	return &RPCError{Method: method, Err: err}
}
```

- [ ] **Step 4: Wrap RPC failures in `call`, `BlockNumber`, and `GetLogs`**

In `call`, wrap every returned error after request marshaling begins. Example replacements:

```go
if err != nil {
	return nil, wrapRPCError(method, fmt.Errorf("marshal request: %w", err))
}
```

Apply the same pattern to:

```go
return nil, wrapRPCError(method, fmt.Errorf("create request: %w", err))
return nil, wrapRPCError(method, fmt.Errorf("rpc call: %w", err))
return nil, wrapRPCError(method, fmt.Errorf("read response: %w", err))
return nil, wrapRPCError(method, fmt.Errorf("http status %d: %s", resp.StatusCode, preview))
return nil, wrapRPCError(method, fmt.Errorf("unmarshal response: %w (body: %s)", err, preview))
return nil, wrapRPCError(method, fmt.Errorf("rpc error %d: %s", rpcErr.Code, rpcErr.Message))
return nil, wrapRPCError(method, fmt.Errorf("rpc error: %s", errStr))
return nil, wrapRPCError(method, fmt.Errorf("rpc error: %s", string(rpcResp.Error)))
```

In `BlockNumber`, wrap result parsing:

```go
if err := json.Unmarshal(result, &hex); err != nil {
	return 0, wrapRPCError("eth_blockNumber", fmt.Errorf("unmarshal blockNumber: %w", err))
}
```

In `GetLogs`, wrap result parsing:

```go
if err := json.Unmarshal(result, &logs); err != nil {
	return nil, wrapRPCError("eth_getLogs", fmt.Errorf("unmarshal logs: %w", err))
}
```

- [ ] **Step 5: Run scanner tests**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
go test ./scanner
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git -C /home/v3/workspace/BlockScanner add src/scanner/rpc.go src/scanner/rpc_test.go
git -C /home/v3/workspace/BlockScanner commit -m "feat: mark rpc failures with typed errors"
```

---

### Task 4: Remove Startup Seed Data and Wire Notifier

**Files:**
- Modify: `src/main.go`

- [ ] **Step 1: Verify current seed references**

Run:

```bash
grep -R "seedData\|seeding example data\|seed data" -n /home/v3/workspace/BlockScanner/src
```

Expected: matches only in `src/main.go` before this task.

- [ ] **Step 2: Remove seed call and seed function**

In `src/main.go`, delete this block:

```go
// 种子数据：首次运行时插入示例配置
if err := seedData(db); err != nil {
	slog.Error("failed to seed data", "error", err)
	os.Exit(1)
}
```

Delete the entire `seedData` function from `func seedData(db *store.DB) error {` through its closing brace.

- [ ] **Step 3: Remove unused imports**

In `src/main.go`, remove the unused import:

```go
"blockscanner/processor"
```

Keep `blockscanner/scanner` because main still creates `scanner.NewEvmScanner(db)`.

- [ ] **Step 4: Wire Telegram notifier into main**

Add import:

```go
"blockscanner/notifier"
```

After creating `evmScanner`, add:

```go
opsNotifier := notifier.NewTelegram(cfg.Telegram)
```

Change scheduler construction from:

```go
sched := scheduler.New(db, evmScanner)
```

to:

```go
sched := scheduler.New(db, evmScanner, opsNotifier)
```

- [ ] **Step 5: Run gofmt and build to reveal scheduler signature failure**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
gofmt -w main.go
go test ./...
```

Expected at this point: FAIL because `scheduler.New` still accepts two arguments. That failure is resolved in a later scheduler task. If it fails only for unused imports, fix imports and re-run until the only remaining failure is the scheduler signature.

- [ ] **Step 6: Commit after scheduler task, not now**

Do not commit this task yet if `go test ./...` cannot compile due to the scheduler constructor. Include the `main.go` changes in the scheduler integration commit in Task 8.

---

### Task 5: Job Store Support

**Files:**
- Modify: `src/store/job.go`

- [ ] **Step 1: Add store tests if using a local MySQL test database is available**

If this project has a developer MySQL database available, create integration tests later around `store.NewDB`. If no test database is available, keep this task verified through build and scheduler-level behavior. Do not introduce sqlite because the project uses MySQL-specific GORM behavior and no sqlite dependency exists.

- [ ] **Step 2: Filter deleted rows in `GetEnabledJobs`**

Change `GetEnabledJobs` to:

```go
func (d *DB) GetEnabledJobs(ctx context.Context) ([]entity.InfraJob, error) {
	var jobs []entity.InfraJob
	err := d.WithContext(ctx).
		Where("status = 1").
		Where("deleted IS NULL OR deleted = ?", false).
		Find(&jobs).Error
	return jobs, err
}
```

- [ ] **Step 3: Preserve manual job status in `UpsertJob` for existing rows**

In `UpsertJob`, replace the existing update map with:

```go
Updates(map[string]interface{}{
	"name":            job.Name,
	"cron_expression": job.CronExpression,
}).Error
```

Leave the create path unchanged so new jobs are created with `Status: 1`.

- [ ] **Step 4: Add `UpdateJobLog`**

Add below `CreateJobLog`:

```go
// UpdateJobLog 更新任务执行日志
func (d *DB) UpdateJobLog(ctx context.Context, id int64, status int8, message string, endTime time.Time) error {
	return d.WithContext(ctx).
		Model(&entity.InfraJobLog{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":   status,
			"message":  message,
			"end_time": endTime,
		}).Error
}
```

Update imports in `src/store/job.go` to include `time`:

```go
import (
	"blockscanner/entity"
	"context"
	"time"
)
```

- [ ] **Step 5: Run package tests/build**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
gofmt -w store/job.go
go test ./store
```

Expected: PASS or `? blockscanner/store [no test files]`.

- [ ] **Step 6: Commit**

Run:

```bash
git -C /home/v3/workspace/BlockScanner add src/store/job.go
git -C /home/v3/workspace/BlockScanner commit -m "feat: support job log updates"
```

---

### Task 6: Scheduler Alert State

**Files:**
- Create: `src/scheduler/alerts.go`
- Create: `src/scheduler/alerts_test.go`

- [ ] **Step 1: Write failing alert tests**

Create `src/scheduler/alerts_test.go` with:

```go
package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"blockscanner/entity"
)

type fakeNotifier struct {
	messages []string
}

func (f *fakeNotifier) SendMessage(ctx context.Context, text string) error {
	f.messages = append(f.messages, text)
	return nil
}

func TestRPCAlertThresholdCooldownAndRecovery(t *testing.T) {
	fn := &fakeNotifier{}
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	state := newRPCAlertManager(fn, 5, 30*time.Minute, func() time.Time { return now })
	chain := &entity.InfraEvmChain{ChainID: 137, Name: "Polygon", RPCURL: "https://rpc.example.com/key1234567890?token=secret"}

	for i := 0; i < 4; i++ {
		state.recordFailure(context.Background(), chain, assertErr("eth_blockNumber failed"))
	}
	if len(fn.messages) != 0 {
		t.Fatalf("messages before threshold = %d, want 0", len(fn.messages))
	}

	state.recordFailure(context.Background(), chain, assertErr("eth_blockNumber failed"))
	if len(fn.messages) != 1 {
		t.Fatalf("messages at threshold = %d, want 1", len(fn.messages))
	}
	if !strings.Contains(fn.messages[0], "RPC 连续失败告警") {
		t.Fatalf("alert message missing title: %s", fn.messages[0])
	}
	if strings.Contains(fn.messages[0], "token=secret") || strings.Contains(fn.messages[0], "key1234567890") {
		t.Fatalf("alert message leaked RPC secret: %s", fn.messages[0])
	}

	state.recordFailure(context.Background(), chain, assertErr("still failed"))
	if len(fn.messages) != 1 {
		t.Fatalf("messages inside cooldown = %d, want 1", len(fn.messages))
	}

	now = now.Add(31 * time.Minute)
	state.recordFailure(context.Background(), chain, assertErr("still failed"))
	if len(fn.messages) != 2 {
		t.Fatalf("messages after cooldown = %d, want 2", len(fn.messages))
	}

	state.recordSuccess(context.Background(), chain)
	if len(fn.messages) != 3 {
		t.Fatalf("messages after recovery = %d, want 3", len(fn.messages))
	}
	if !strings.Contains(fn.messages[2], "RPC 已恢复") {
		t.Fatalf("recovery message missing title: %s", fn.messages[2])
	}

	state.recordSuccess(context.Background(), chain)
	if len(fn.messages) != 3 {
		t.Fatalf("second success sent duplicate recovery, messages = %d", len(fn.messages))
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
go test ./scheduler
```

Expected: FAIL because `newRPCAlertManager` does not exist.

- [ ] **Step 3: Implement alert manager**

Create `src/scheduler/alerts.go` with:

```go
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"blockscanner/entity"
	"blockscanner/notifier"
)

type rpcFailureState struct {
	consecutiveFailures int
	lastError           string
	lastNotifiedAt      time.Time
	alerting            bool
}

type rpcAlertManager struct {
	mu        sync.Mutex
	sender    notifier.Sender
	threshold int
	cooldown  time.Duration
	now       func() time.Time
	states    map[int64]*rpcFailureState
}

func newRPCAlertManager(sender notifier.Sender, threshold int, cooldown time.Duration, now func() time.Time) *rpcAlertManager {
	if sender == nil {
		sender = notifier.Noop{}
	}
	if threshold <= 0 {
		threshold = 5
	}
	if now == nil {
		now = time.Now
	}
	return &rpcAlertManager{
		sender:    sender,
		threshold: threshold,
		cooldown:  cooldown,
		now:       now,
		states:    make(map[int64]*rpcFailureState),
	}
}

func (m *rpcAlertManager) recordFailure(ctx context.Context, chain *entity.InfraEvmChain, err error) {
	if chain == nil || err == nil {
		return
	}

	m.mu.Lock()
	state := m.states[chain.ChainID]
	if state == nil {
		state = &rpcFailureState{}
		m.states[chain.ChainID] = state
	}
	state.consecutiveFailures++
	state.lastError = err.Error()

	shouldNotify := state.consecutiveFailures >= m.threshold && (state.lastNotifiedAt.IsZero() || m.cooldown <= 0 || m.now().Sub(state.lastNotifiedAt) >= m.cooldown)
	if shouldNotify {
		state.alerting = true
		state.lastNotifiedAt = m.now()
	}
	failures := state.consecutiveFailures
	m.mu.Unlock()

	if !shouldNotify {
		return
	}

	text := fmt.Sprintf("[BlockScanner] RPC 连续失败告警\n链: %s (%d)\n失败次数: %d\nRPC: %s\n错误: %s\n时间: %s",
		chain.Name,
		chain.ChainID,
		failures,
		notifier.RedactRPCURL(chain.RPCURL),
		err.Error(),
		m.now().Format("2006-01-02 15:04:05"),
	)
	if sendErr := m.sender.SendMessage(ctx, text); sendErr != nil {
		slog.Error("send rpc failure alert failed", "component", "notifier", "chain_id", chain.ChainID, "error", sendErr)
	}
}

func (m *rpcAlertManager) recordSuccess(ctx context.Context, chain *entity.InfraEvmChain) {
	if chain == nil {
		return
	}

	m.mu.Lock()
	state := m.states[chain.ChainID]
	if state == nil {
		m.mu.Unlock()
		return
	}
	wasAlerting := state.alerting
	failures := state.consecutiveFailures
	state.consecutiveFailures = 0
	state.lastError = ""
	state.alerting = false
	m.mu.Unlock()

	if !wasAlerting {
		return
	}

	text := fmt.Sprintf("[BlockScanner] RPC 已恢复\n链: %s (%d)\nRPC: %s\n此前连续失败: %d\n时间: %s",
		chain.Name,
		chain.ChainID,
		notifier.RedactRPCURL(chain.RPCURL),
		failures,
		m.now().Format("2006-01-02 15:04:05"),
	)
	if sendErr := m.sender.SendMessage(ctx, text); sendErr != nil {
		slog.Error("send rpc recovery alert failed", "component", "notifier", "chain_id", chain.ChainID, "error", sendErr)
	}
}
```

- [ ] **Step 4: Run scheduler alert tests**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
gofmt -w scheduler/alerts.go scheduler/alerts_test.go
go test ./scheduler -run TestRPCAlertThresholdCooldownAndRecovery
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git -C /home/v3/workspace/BlockScanner add src/scheduler/alerts.go src/scheduler/alerts_test.go
git -C /home/v3/workspace/BlockScanner commit -m "feat: track rpc alert state"
```

---

### Task 7: Scheduler Refresh Helpers

**Files:**
- Modify: `src/scheduler/scheduler.go`
- Create: `src/scheduler/job_refresh_test.go`

- [ ] **Step 1: Write tests for effective job filtering**

Create `src/scheduler/job_refresh_test.go` with:

```go
package scheduler

import (
	"testing"

	"blockscanner/entity"
)

func TestEffectiveJobsSkipsScanForDisabledChains(t *testing.T) {
	jobs := []entity.InfraJob{
		{ID: 1, HandlerName: "scanEvmChain", HandlerParam: "137", CronExpression: "*/2 * * * * *", Status: 1},
		{ID: 2, HandlerName: "scanEvmChain", HandlerParam: "1", CronExpression: "*/12 * * * * *", Status: 1},
		{ID: 3, HandlerName: "processScanEvent", HandlerParam: "", CronExpression: "*/5 * * * * *", Status: 1},
	}
	enabledChains := map[int64]entity.InfraEvmChain{
		137: {ChainID: 137, Name: "Polygon", Status: 1},
	}

	effective := effectiveJobs(jobs, enabledChains)
	if len(effective) != 2 {
		t.Fatalf("len(effective) = %d, want 2", len(effective))
	}
	if effective[0].HandlerParam != "137" {
		t.Fatalf("first effective handler_param = %q, want 137", effective[0].HandlerParam)
	}
	if effective[1].HandlerName != "processScanEvent" {
		t.Fatalf("second effective handler = %q, want processScanEvent", effective[1].HandlerName)
	}
}

func TestJobKeys(t *testing.T) {
	jobs := []entity.InfraJob{
		{HandlerName: "scanEvmChain", HandlerParam: "137"},
		{HandlerName: "processScanEvent", HandlerParam: ""},
	}
	keys := jobKeys(jobs)
	if !keys["scanEvmChain:137"] {
		t.Fatalf("missing scan key")
	}
	if !keys["processScanEvent:"] {
		t.Fatalf("missing process key")
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
go test ./scheduler -run 'TestEffectiveJobs|TestJobKeys'
```

Expected: FAIL because `effectiveJobs` and `jobKeys` do not exist.

- [ ] **Step 3: Add scheduler fields and helper types**

In `src/scheduler/scheduler.go`, add import:

```go
"strconv"
```

Add import:

```go
"blockscanner/notifier"
```

Replace `jobIDs map[string]cron.EntryID` with:

```go
jobs map[string]scheduledJob
```

Add these types near `Scheduler`:

```go
type scheduledJob struct {
	entryID cron.EntryID
	cron    string
}

type Option func(*Scheduler)

func WithRefreshInterval(interval time.Duration) Option {
	return func(s *Scheduler) {
		if interval > 0 {
			s.refreshInterval = interval
		}
	}
}
```

Update `Scheduler` fields:

```go
type Scheduler struct {
	db              *store.DB
	scanner         *scanner.EvmScanner
	cron            *cron.Cron
	mu              sync.Mutex
	jobs            map[string]scheduledJob
	refreshInterval time.Duration
	alerts          *rpcAlertManager
}
```

Update constructor signature:

```go
func New(db *store.DB, evmScanner *scanner.EvmScanner, sender notifier.Sender, opts ...Option) *Scheduler {
	s := &Scheduler{
		db:              db,
		scanner:         evmScanner,
		cron: cron.New(
			cron.WithSeconds(),
			cron.WithLocation(time.Local),
		),
		jobs:            make(map[string]scheduledJob),
		refreshInterval: 60 * time.Second,
		alerts:          newRPCAlertManager(sender, 5, 30*time.Minute, time.Now),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}
```

The Telegram threshold/cooldown from config is wired in Task 8.

- [ ] **Step 4: Add pure refresh helpers**

Add near `jobKey`:

```go
func effectiveJobs(jobs []entity.InfraJob, enabledChains map[int64]entity.InfraEvmChain) []entity.InfraJob {
	effective := make([]entity.InfraJob, 0, len(jobs))
	for _, job := range jobs {
		if job.HandlerName == "scanEvmChain" {
			chainID, err := strconv.ParseInt(job.HandlerParam, 10, 64)
			if err != nil {
				slog.Warn("invalid scan job handler_param", "component", "scheduler", "param", job.HandlerParam, "error", err)
				continue
			}
			if _, ok := enabledChains[chainID]; !ok {
				continue
			}
		}
		effective = append(effective, job)
	}
	return effective
}

func jobKeys(jobs []entity.InfraJob) map[string]bool {
	keys := make(map[string]bool, len(jobs))
	for _, job := range jobs {
		keys[jobKey(job.HandlerName, job.HandlerParam)] = true
	}
	return keys
}
```

- [ ] **Step 5: Update `upsertCronJob` to use `jobs` map**

Replace `upsertCronJob` body with:

```go
func (s *Scheduler) upsertCronJob(job *entity.InfraJob) {
	key := jobKey(job.HandlerName, job.HandlerParam)

	if existing, ok := s.jobs[key]; ok && existing.cron == job.CronExpression {
		return
	}
	if existing, ok := s.jobs[key]; ok {
		s.cron.Remove(existing.entryID)
	}

	var cmd func()
	switch job.HandlerName {
	case "scanEvmChain":
		cmd = s.buildScanEvmChainCmd(job)
	case "processScanEvent":
		cmd = s.buildProcessScanEventCmd(job)
	default:
		slog.Warn("unknown handler", "component", "scheduler", "handler_name", job.HandlerName)
		return
	}

	entryID, err := s.cron.AddFunc(job.CronExpression, cmd)
	if err != nil {
		slog.Error("add cron job failed", "component", "scheduler", "name", job.Name, "cron", job.CronExpression, "error", err)
		return
	}

	s.jobs[key] = scheduledJob{entryID: entryID, cron: job.CronExpression}
	slog.Info("cron job registered", "component", "scheduler", "name", job.Name, "handler", job.HandlerName, "cron", job.CronExpression)
}
```

This changes `buildProcessScanEventCmd` to accept `job *entity.InfraJob`; Task 8 updates that function.

- [ ] **Step 6: Add stale removal helper**

Add:

```go
func (s *Scheduler) removeStaleCronJobs(validKeys map[string]bool) {
	for key, existing := range s.jobs {
		if validKeys[key] {
			continue
		}
		s.cron.Remove(existing.entryID)
		delete(s.jobs, key)
		slog.Info("cron job removed", "component", "scheduler", "job_key", key)
	}
}
```

- [ ] **Step 7: Run targeted scheduler helper tests**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
gofmt -w scheduler/scheduler.go scheduler/job_refresh_test.go
go test ./scheduler -run 'TestEffectiveJobs|TestJobKeys'
```

Expected: PASS for helper tests. Package may still fail to compile if `buildProcessScanEventCmd` signature is not updated; if so, update the signature in Task 8 before re-running.

- [ ] **Step 8: Commit after Task 8 if package does not compile yet**

If `go test ./scheduler` cannot compile until Task 8, defer this commit and include these changes in Task 8's scheduler commit.

---

### Task 8: Scheduler Refresh Loop, Job Logs, and RPC Alerts Integration

**Files:**
- Modify: `src/scheduler/scheduler.go`
- Modify: `src/main.go`

- [ ] **Step 1: Add notifier alert options to scheduler**

In `src/scheduler/scheduler.go`, extend options with:

```go
func WithRPCAlertConfig(threshold int, cooldown time.Duration) Option {
	return func(s *Scheduler) {
		s.alerts.threshold = threshold
		s.alerts.cooldown = cooldown
	}
}
```

- [ ] **Step 2: Wire config threshold/cooldown in main**

In `src/main.go`, change scheduler construction to:

```go
sched := scheduler.New(
	db,
	evmScanner,
	opsNotifier,
	scheduler.WithRPCAlertConfig(
		cfg.Telegram.RPCFailureThreshold,
		time.Duration(cfg.Telegram.CooldownSecs)*time.Second,
	),
)
```

- [ ] **Step 3: Replace `Start` with refresh loop behavior**

Replace `Start` in `src/scheduler/scheduler.go` with:

```go
func (s *Scheduler) Start(ctx context.Context) error {
	s.registerHandlers()

	if err := s.refreshJobs(ctx); err != nil {
		return fmt.Errorf("refresh jobs: %w", err)
	}

	s.cron.Start()
	slog.Info("scheduler started", "component", "scheduler", "refresh_interval", s.refreshInterval)

	go s.runRefreshLoop(ctx)

	go func() {
		<-ctx.Done()
		slog.Info("scheduler stopping...", "component", "scheduler")
		stopCtx := s.cron.Stop()
		<-stopCtx.Done()
		slog.Info("scheduler stopped", "component", "scheduler")
	}()

	return nil
}
```

Add:

```go
func (s *Scheduler) runRefreshLoop(ctx context.Context) {
	ticker := time.NewTicker(s.refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.refreshJobs(ctx); err != nil {
				slog.Error("refresh jobs failed", "component", "scheduler", "error", err)
			}
		}
	}
}
```

- [ ] **Step 4: Implement `refreshJobs`**

Add:

```go
func (s *Scheduler) refreshJobs(ctx context.Context) error {
	chains, err := s.db.GetEnabledChains(ctx)
	if err != nil {
		return fmt.Errorf("get enabled chains: %w", err)
	}

	enabledChains := make(map[int64]entity.InfraEvmChain, len(chains))
	for _, ch := range chains {
		enabledChains[ch.ChainID] = ch
		job := buildChainScanJob(&ch)
		if err := s.db.UpsertJob(ctx, job); err != nil {
			slog.Error("upsert chain scan job failed", "component", "scheduler", "chain_id", ch.ChainID, "error", err)
		}
	}

	processJob := buildProcessScanEventJob()
	if err := s.db.UpsertJob(ctx, processJob); err != nil {
		slog.Error("upsert process scan event job failed", "component", "scheduler", "error", err)
	}

	jobs, err := s.db.GetEnabledJobs(ctx)
	if err != nil {
		return fmt.Errorf("get enabled jobs: %w", err)
	}
	effective := effectiveJobs(jobs, enabledChains)
	validKeys := jobKeys(effective)

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range effective {
		s.upsertCronJob(&effective[i])
	}
	s.removeStaleCronJobs(validKeys)

	slog.Info("refreshed jobs from database", "component", "scheduler", "enabled_chains", len(chains), "enabled_jobs", len(jobs), "effective_jobs", len(effective))
	return nil
}
```

Remove old `syncJobs` and `ensureJobsForAllChains` if they are no longer called.

- [ ] **Step 5: Add job log wrapper**

Add imports if missing:

```go
"strings"
```

Add helper:

```go
func (s *Scheduler) runWithJobLog(ctx context.Context, job *entity.InfraJob, run func(context.Context) (string, error)) {
	start := time.Now()
	jobLog := &entity.InfraJobLog{
		JobID:     job.ID,
		Status:    0,
		Message:   "running",
		StartTime: start,
	}

	logID := int64(0)
	if err := s.db.CreateJobLog(ctx, jobLog); err != nil {
		slog.Error("create job log failed", "component", "scheduler", "job_id", job.ID, "handler", job.HandlerName, "error", err)
	} else {
		logID = jobLog.ID
	}

	message, err := run(ctx)
	status := int8(1)
	if err != nil {
		status = 2
		message = err.Error()
	}
	if strings.TrimSpace(message) == "" {
		message = "completed"
	}

	if logID > 0 {
		if updateErr := s.db.UpdateJobLog(ctx, logID, status, message, time.Now()); updateErr != nil {
			slog.Error("update job log failed", "component", "scheduler", "job_id", job.ID, "job_log_id", logID, "error", updateErr)
		}
	}

	duration := time.Since(start)
	if err != nil {
		slog.Error("job failed", "component", "scheduler", "job_id", job.ID, "handler", job.HandlerName, "duration", duration, "error", err)
		return
	}
	slog.Info("job completed", "component", "scheduler", "job_id", job.ID, "handler", job.HandlerName, "duration", duration, "message", message)
}
```

- [ ] **Step 6: Replace scan cron command with job-log and alert-aware execution**

Replace `buildScanEvmChainCmd` with:

```go
func (s *Scheduler) buildScanEvmChainCmd(job *entity.InfraJob) func() {
	return func() {
		ctx := context.Background()
		s.runWithJobLog(ctx, job, func(ctx context.Context) (string, error) {
			return s.executeScanEvmChain(ctx, job)
		})
	}
}
```

Add:

```go
func (s *Scheduler) executeScanEvmChain(ctx context.Context, job *entity.InfraJob) (string, error) {
	start := time.Now()

	var chainID int64
	if _, err := fmt.Sscanf(job.HandlerParam, "%d", &chainID); err != nil {
		return "", fmt.Errorf("invalid handler_param for scanEvmChain param=%s: %w", job.HandlerParam, err)
	}

	chain, err := s.db.GetChainByID(ctx, chainID)
	if err != nil {
		return "", fmt.Errorf("chain not found chain_id=%d: %w", chainID, err)
	}

	worker := scanner.NewChainWorker(s.db, chain.RPCURL, chain.ChainID)
	rounds := 0
	hasMore := false

	for round := 0; round < 10; round++ {
		roundStart := time.Now()
		var err error
		hasMore, err = worker.ScanRound(ctx)
		if err != nil {
			if scanner.IsRPCError(err) {
				s.alerts.recordFailure(ctx, chain, err)
			}
			slog.Error("scan round error", "component", "scanner", "chain_id", chainID, "round", round, "duration", time.Since(roundStart), "rpc_error", scanner.IsRPCError(err), "error", err)
			return fmt.Sprintf("scan failed: chain_id=%d rpc_error=%t error=%v", chainID, scanner.IsRPCError(err), err), err
		}

		rounds++
		s.alerts.recordSuccess(ctx, chain)
		slog.Info("scan round completed", "component", "scanner", "chain_id", chainID, "round", round, "has_more", hasMore, "duration", time.Since(roundStart))
		if !hasMore {
			break
		}
	}

	message := fmt.Sprintf("scan completed: chain_id=%d rounds=%d has_more=%t duration=%s", chainID, rounds, hasMore, time.Since(start))
	return message, nil
}
```

- [ ] **Step 7: Replace process cron command with job-log execution**

Change `buildProcessScanEventCmd` signature and body:

```go
func (s *Scheduler) buildProcessScanEventCmd(job *entity.InfraJob) func() {
	return func() {
		ctx := context.Background()
		s.runWithJobLog(ctx, job, func(ctx context.Context) (string, error) {
			result, err := processScanEvents(ctx, s.db)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("process events completed: batches=%d claimed=%d duration=%s", result.Batches, result.Claimed, result.Duration), nil
		})
	}
}
```

Add result type:

```go
type processScanEventsResult struct {
	Batches  int
	Claimed  int
	Duration time.Duration
}
```

Replace `processScanEvents` with:

```go
func processScanEvents(ctx context.Context, db *store.DB) (processScanEventsResult, error) {
	const batchSize = 100
	result := processScanEventsResult{}
	start := time.Now()
	defer func() { result.Duration = time.Since(start) }()

	for {
		events, err := db.ClaimUnprocessedEvents(ctx, batchSize)
		if err != nil {
			return result, fmt.Errorf("claim events: %w", err)
		}

		if len(events) == 0 {
			result.Duration = time.Since(start)
			slog.Info("process scan events completed", "component", "processor", "batches", result.Batches, "claimed", result.Claimed, "duration", result.Duration)
			return result, nil
		}

		result.Batches++
		result.Claimed += len(events)
		slog.Info("claimed scan events", "component", "processor", "batch", result.Batches, "count", len(events))

		for _, event := range events {
			processor.RouteEvent(ctx, db, &event)
		}
	}
}
```

- [ ] **Step 8: Run all tests and build**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
gofmt -w main.go scheduler/scheduler.go
go test ./...
go build -o /tmp/blockscanner-test .
```

Expected: all tests pass and build succeeds.

- [ ] **Step 9: Commit scheduler integration and seed removal**

Run:

```bash
git -C /home/v3/workspace/BlockScanner add src/main.go src/scheduler/scheduler.go src/scheduler/job_refresh_test.go
git -C /home/v3/workspace/BlockScanner commit -m "feat: refresh scheduler jobs and wire rpc alerts"
```

---

### Task 9: Scanner Progress Logging

**Files:**
- Modify: `src/scanner/worker.go`

- [ ] **Step 1: Add scan timing and progress logs**

In `ScanRound`, add near the top after function entry:

```go
roundStart := time.Now()
```

After computing `confirmed` and before computing `fromBlock`, add:

```go
slog.Info("scan round loaded chain state",
	"component", "scanner",
	"chain_id", w.chainID,
	"chain_name", chain.Name,
	"latest_block", latest,
	"confirmed_block", confirmed,
	"last_synced_block", chain.LastSyncedBlock,
	"start_block", chain.StartBlock,
)
```

Before `w.client.GetLogs`, add:

```go
rpcStart := time.Now()
```

After `GetLogs` succeeds, add:

```go
slog.Info("rpc get logs completed",
	"component", "rpc",
	"chain_id", w.chainID,
	"from_block", fromBlock,
	"to_block", toBlock,
	"log_count", len(logs),
	"duration", time.Since(rpcStart),
)
```

When `GetLogs` returns error, replace the existing return with:

```go
slog.Error("rpc get logs failed",
	"component", "rpc",
	"chain_id", w.chainID,
	"from_block", fromBlock,
	"to_block", toBlock,
	"duration", time.Since(rpcStart),
	"error", err,
)
return false, fmt.Errorf("eth_getLogs: %w", err)
```

After empty-log `UpdateLastSyncedBlock` succeeds, add:

```go
slog.Info("scan round advanced without logs",
	"component", "scanner",
	"chain_id", w.chainID,
	"from_block", fromBlock,
	"to_block", toBlock,
	"last_synced_block", toBlock,
	"has_more", toBlock < confirmed,
	"duration", time.Since(roundStart),
)
```

After transaction succeeds and before return, add:

```go
slog.Info("scan round completed",
	"component", "scanner",
	"chain_id", w.chainID,
	"from_block", fromBlock,
	"to_block", toBlock,
	"decoded_logs", len(eventLogs),
	"has_more", hasMore,
	"duration", time.Since(roundStart),
)
```

- [ ] **Step 2: Add RPC block number timing**

Before `latest, err := w.client.BlockNumber(ctx)`, add:

```go
blockNumberStart := time.Now()
```

If `BlockNumber` fails, log before returning:

```go
slog.Error("rpc block number failed",
	"component", "rpc",
	"chain_id", w.chainID,
	"duration", time.Since(blockNumberStart),
	"error", err,
)
return false, fmt.Errorf("eth_blockNumber: %w", err)
```

After success, add:

```go
slog.Debug("rpc block number completed",
	"component", "rpc",
	"chain_id", w.chainID,
	"latest_block", latest,
	"duration", time.Since(blockNumberStart),
)
```

- [ ] **Step 3: Run tests/build**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
gofmt -w scanner/worker.go
go test ./scanner
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```bash
git -C /home/v3/workspace/BlockScanner add src/scanner/worker.go
git -C /home/v3/workspace/BlockScanner commit -m "feat: log scanner progress"
```

---

### Task 10: Final Verification and Packaging Check

**Files:**
- No code changes expected unless tests reveal a defect.

- [ ] **Step 1: Confirm seed code is gone**

Run:

```bash
grep -R "seedData\|seeding example data\|seed data" -n /home/v3/workspace/BlockScanner/src || true
```

Expected: no output.

- [ ] **Step 2: Run full test suite**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Build binary**

Run:

```bash
cd /home/v3/workspace/BlockScanner/src
go build -o /tmp/blockscanner-test .
```

Expected: command exits 0 and `/tmp/blockscanner-test` exists.

- [ ] **Step 4: Run packaging script**

Run from repo root:

```bash
cd /home/v3/workspace/BlockScanner
./build.sh
```

Expected: script exits 0 and creates `blockscanner-YYYYMMDD-HHMMSS.zip` in the repository root.

- [ ] **Step 5: Inspect git status**

Run:

```bash
git -C /home/v3/workspace/BlockScanner status --short
```

Expected: either clean working tree or only the generated binary/package artifacts. If generated artifacts are present and should not be committed, leave them untracked and report them.

- [ ] **Step 6: Commit verification fixes if any were needed**

If final verification required a code fix, commit it with:

```bash
git -C /home/v3/workspace/BlockScanner add <fixed-files>
git -C /home/v3/workspace/BlockScanner commit -m "fix: stabilize ops optimization"
```

If no fixes were needed, do not create an empty commit.

---

## Self-Review

**Spec coverage:**
- Remove startup test data: Task 4 and Task 10.
- Config changes effective within 60 seconds: Tasks 7 and 8.
- Telegram config from YAML/env: Tasks 1 and 2.
- RPC failure threshold 5, cooldown, recovery notification: Tasks 3, 6, and 8.
- Progress/error file logs: Task 9 plus scheduler logs in Task 8.
- `infra_job_log` execution records: Tasks 5 and 8.

**Placeholder scan:** This plan does not contain unresolved placeholder markers or deferred implementation language.

**Type consistency:**
- `notifier.Sender` exposes `SendMessage(context.Context, string) error` and is used by `rpcAlertManager`.
- `scheduler.New` accepts `(db *store.DB, evmScanner *scanner.EvmScanner, sender notifier.Sender, opts ...Option)` and `main.go` uses that signature.
- `processScanEvents` returns `(processScanEventsResult, error)`, and `buildProcessScanEventCmd` uses that return value.
- `scanner.IsRPCError` accepts any `error` and is used only for scan errors.
