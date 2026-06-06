package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"blockscanner/entity"
)

type fakeNotifier struct{ messages []string }

func (f *fakeNotifier) SendMessage(ctx context.Context, text string) error {
	f.messages = append(f.messages, text)
	return nil
}

type failingNotifier struct {
	messages []string
	err      error
}

func (f *failingNotifier) SendMessage(ctx context.Context, text string) error {
	f.messages = append(f.messages, text)
	return f.err
}

func TestRPCAlertSendFailureDoesNotEnterAlertCooldown(t *testing.T) {
	fn := &failingNotifier{err: errors.New("notification unavailable")}
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	manager := newRPCAlertManager(fn, 2, time.Hour, func() time.Time { return now })
	chain := &entity.InfraEvmChain{ChainID: 1, Name: "Ethereum", RPCURL: "https://rpc.example.com/key1234567890"}

	manager.recordFailure(context.Background(), chain, assertErr("first failure"))
	if len(fn.messages) != 0 {
		t.Fatalf("messages before threshold = %d, want 0", len(fn.messages))
	}

	manager.recordFailure(context.Background(), chain, assertErr("threshold failure"))
	if len(fn.messages) != 1 {
		t.Fatalf("messages after failed send = %d, want 1", len(fn.messages))
	}

	manager.mu.Lock()
	state := manager.states[chain.ChainID]
	if state == nil {
		manager.mu.Unlock()
		t.Fatal("failure state missing after failed send")
	}
	if state.consecutiveFailures != 2 {
		manager.mu.Unlock()
		t.Fatalf("consecutive failures after failed send = %d, want 2", state.consecutiveFailures)
	}
	if state.lastError != "threshold failure" {
		manager.mu.Unlock()
		t.Fatalf("last error after failed send = %q, want %q", state.lastError, "threshold failure")
	}
	if state.alerting {
		manager.mu.Unlock()
		t.Fatal("state is alerting after failed send, want not alerting")
	}
	if !state.lastNotifiedAt.IsZero() {
		manager.mu.Unlock()
		t.Fatalf("last notified after failed send = %s, want zero", state.lastNotifiedAt)
	}
	manager.mu.Unlock()

	now = now.Add(time.Minute)
	manager.recordFailure(context.Background(), chain, assertErr("retry failure"))
	if len(fn.messages) != 2 {
		t.Fatalf("messages after immediate retry failure = %d, want 2", len(fn.messages))
	}

	manager.recordSuccess(context.Background(), chain)
	if len(fn.messages) != 2 {
		t.Fatalf("success after undelivered alert sent recovery, messages = %d, want 2", len(fn.messages))
	}
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
