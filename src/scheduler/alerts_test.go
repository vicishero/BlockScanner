package scheduler

import (
	"context"
	"errors"
	"strings"
	"sync"
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

func TestRPCAlertSuccessBeforeThresholdResetsFailures(t *testing.T) {
	fn := &fakeNotifier{}
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	manager := newRPCAlertManager(fn, 5, 30*time.Minute, func() time.Time { return now })
	chain := &entity.InfraEvmChain{ChainID: 137, Name: "Polygon", RPCURL: "https://rpc.example.com/key1234567890?token=secret"}

	for i := 0; i < 4; i++ {
		manager.recordFailure(context.Background(), chain, assertErr("eth_blockNumber failed"))
	}
	if len(fn.messages) != 0 {
		t.Fatalf("messages before threshold = %d, want 0", len(fn.messages))
	}

	manager.recordSuccess(context.Background(), chain)
	if len(fn.messages) != 0 {
		t.Fatalf("success before threshold sent notification, messages = %d, want 0", len(fn.messages))
	}

	manager.mu.Lock()
	state := manager.states[chain.ChainID]
	if state == nil {
		manager.mu.Unlock()
		t.Fatal("failure state missing after pre-threshold success")
	}
	if state.consecutiveFailures != 0 {
		manager.mu.Unlock()
		t.Fatalf("consecutive failures after pre-threshold success = %d, want 0", state.consecutiveFailures)
	}
	if state.lastError != "" {
		manager.mu.Unlock()
		t.Fatalf("last error after pre-threshold success = %q, want empty", state.lastError)
	}
	manager.mu.Unlock()

	for i := 0; i < 4; i++ {
		manager.recordFailure(context.Background(), chain, assertErr("eth_blockNumber failed again"))
	}
	if len(fn.messages) != 0 {
		t.Fatalf("messages after four post-success failures = %d, want 0", len(fn.messages))
	}

	manager.recordFailure(context.Background(), chain, assertErr("threshold failure after reset"))
	if len(fn.messages) != 1 {
		t.Fatalf("messages at threshold after reset = %d, want 1", len(fn.messages))
	}
}

func TestRPCAlertSanitizesURLSecretsFromErrorText(t *testing.T) {
	fn := &fakeNotifier{}
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	manager := newRPCAlertManager(fn, 1, time.Hour, func() time.Time { return now })
	chain := &entity.InfraEvmChain{ChainID: 137, Name: "Polygon", RPCURL: "https://rpc.example.com/key1234567890?token=secret"}

	manager.recordFailure(context.Background(), chain, assertErr(`Post "https://rpc.example.com/key1234567890?token=secret": timeout`))
	if len(fn.messages) != 1 {
		t.Fatalf("messages after threshold = %d, want 1", len(fn.messages))
	}

	message := fn.messages[0]
	for _, leaked := range []string{"key1234567890", "token=secret", "secret"} {
		if strings.Contains(message, leaked) {
			t.Fatalf("alert message leaked %q: %s", leaked, message)
		}
	}
	if !strings.Contains(message, "timeout") {
		t.Fatalf("alert message missing error context %q: %s", "timeout", message)
	}
}

func TestRPCAlertRecoveryDoesNotClearNewFailureRecordedInFlight(t *testing.T) {
	fn := newBlockingNotifier()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	manager := newRPCAlertManager(fn, 1, time.Hour, func() time.Time { return now })
	chain := &entity.InfraEvmChain{ChainID: 8453, Name: "Base", RPCURL: "https://rpc.example.com/key1234567890"}

	manager.mu.Lock()
	manager.states[chain.ChainID] = &rpcFailureState{
		consecutiveFailures: 1,
		lastError:           "delivered threshold failure",
		alerting:            true,
		lastNotifiedAt:      now,
	}
	manager.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.recordSuccess(context.Background(), chain)
	}()

	fn.waitStarted(t)
	manager.recordFailure(context.Background(), chain, assertErr("new failure while recovery is in flight"))

	fn.unblock(nil)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recovery send to finish")
	}

	manager.mu.Lock()
	state := manager.states[chain.ChainID]
	if state == nil {
		manager.mu.Unlock()
		t.Fatal("failure state missing after recovery race")
	}
	if state.consecutiveFailures == 0 {
		manager.mu.Unlock()
		t.Fatal("recovery cleared new failure recorded while send was in flight")
	}
	if !state.alerting {
		manager.mu.Unlock()
		t.Fatal("recovery cleared alerting state despite newer failure")
	}
	if state.recoveryNotifyInFlight {
		manager.mu.Unlock()
		t.Fatal("recovery in-flight flag still set after send completed")
	}
	manager.mu.Unlock()
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

func TestRPCAlertNewIncidentAfterRecoveryBypassesPreviousCooldown(t *testing.T) {
	fn := &fakeNotifier{}
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	manager := newRPCAlertManager(fn, 1, 30*time.Minute, func() time.Time { return now })
	chain := &entity.InfraEvmChain{ChainID: 8453, Name: "Base", RPCURL: "https://rpc.example.com/key1234567890"}

	manager.recordFailure(context.Background(), chain, assertErr("initial outage"))
	if len(fn.messages) != 1 {
		t.Fatalf("messages after initial alert = %d, want 1", len(fn.messages))
	}
	if !strings.Contains(fn.messages[0], "RPC 连续失败告警") {
		t.Fatalf("initial message missing alert title: %s", fn.messages[0])
	}

	now = now.Add(time.Minute)
	manager.recordSuccess(context.Background(), chain)
	if len(fn.messages) != 2 {
		t.Fatalf("messages after recovery = %d, want 2", len(fn.messages))
	}
	if !strings.Contains(fn.messages[1], "RPC 已恢复") {
		t.Fatalf("second message missing recovery title: %s", fn.messages[1])
	}

	now = now.Add(time.Minute)
	manager.recordFailure(context.Background(), chain, assertErr("new outage"))
	if len(fn.messages) != 3 {
		t.Fatalf("messages after new post-recovery alert = %d, want 3", len(fn.messages))
	}
	if !strings.Contains(fn.messages[2], "RPC 连续失败告警") {
		t.Fatalf("third message missing alert title: %s", fn.messages[2])
	}
}

func TestRPCAlertFailureNotifyInFlightSuppressesDuplicateInitialAlert(t *testing.T) {
	fn := newBlockingNotifier()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	manager := newRPCAlertManager(fn, 1, time.Hour, func() time.Time { return now })
	chain := &entity.InfraEvmChain{ChainID: 10, Name: "Optimism", RPCURL: "https://rpc.example.com/key1234567890"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.recordFailure(context.Background(), chain, assertErr("threshold failure"))
	}()

	fn.waitStarted(t)
	manager.recordFailure(context.Background(), chain, assertErr("concurrent threshold failure"))
	if attempts := fn.attempts(); attempts != 1 {
		t.Fatalf("send attempts while first alert in flight = %d, want 1", attempts)
	}

	fn.unblock(nil)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial alert send to finish")
	}
	if attempts := fn.attempts(); attempts != 1 {
		t.Fatalf("send attempts after unblocking initial alert = %d, want 1", attempts)
	}

	manager.recordFailure(context.Background(), chain, assertErr("inside cooldown failure"))
	if attempts := fn.attempts(); attempts != 1 {
		t.Fatalf("send attempts inside cooldown = %d, want 1", attempts)
	}
}

func TestRPCAlertRecoverySendFailureRetriesUntilDelivered(t *testing.T) {
	fn := &sequencedNotifier{errs: []error{nil, errors.New("recovery unavailable"), nil}}
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	manager := newRPCAlertManager(fn, 1, time.Hour, func() time.Time { return now })
	chain := &entity.InfraEvmChain{ChainID: 8453, Name: "Base", RPCURL: "https://rpc.example.com/key1234567890"}

	manager.recordFailure(context.Background(), chain, assertErr("threshold failure"))
	if attempts := fn.attempts(); attempts != 1 {
		t.Fatalf("send attempts after delivered failure alert = %d, want 1", attempts)
	}

	manager.recordSuccess(context.Background(), chain)
	if attempts := fn.attempts(); attempts != 2 {
		t.Fatalf("send attempts after failed recovery = %d, want 2", attempts)
	}
	manager.mu.Lock()
	state := manager.states[chain.ChainID]
	if state == nil {
		manager.mu.Unlock()
		t.Fatal("failure state missing after failed recovery")
	}
	if !state.alerting {
		manager.mu.Unlock()
		t.Fatal("state not alerting after failed recovery, want alerting so success can retry")
	}
	manager.mu.Unlock()

	manager.recordSuccess(context.Background(), chain)
	if attempts := fn.attempts(); attempts != 3 {
		t.Fatalf("send attempts after recovery retry = %d, want 3", attempts)
	}
	if msg := fn.message(2); !strings.Contains(msg, "RPC 已恢复") {
		t.Fatalf("recovery retry message missing title: %s", msg)
	}

	manager.recordSuccess(context.Background(), chain)
	if attempts := fn.attempts(); attempts != 3 {
		t.Fatalf("send attempts after subsequent success = %d, want 3", attempts)
	}
}

type blockingNotifier struct {
	mu       sync.Mutex
	messages []string
	started  chan struct{}
	release  chan error
}

func newBlockingNotifier() *blockingNotifier {
	return &blockingNotifier{
		started: make(chan struct{}, 1),
		release: make(chan error),
	}
}

func (f *blockingNotifier) SendMessage(ctx context.Context, text string) error {
	f.mu.Lock()
	f.messages = append(f.messages, text)
	attempts := len(f.messages)
	f.mu.Unlock()

	if attempts > 1 {
		return errors.New("unexpected duplicate send")
	}

	select {
	case f.started <- struct{}{}:
	default:
	}

	select {
	case err := <-f.release:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *blockingNotifier) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for send attempt to start")
	}
}

func (f *blockingNotifier) unblock(err error) {
	f.release <- err
}

func (f *blockingNotifier) attempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.messages)
}

type sequencedNotifier struct {
	mu       sync.Mutex
	messages []string
	errs     []error
}

func (f *sequencedNotifier) SendMessage(ctx context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.messages = append(f.messages, text)
	attempt := len(f.messages) - 1
	if attempt < len(f.errs) {
		return f.errs[attempt]
	}
	return nil
}

func (f *sequencedNotifier) attempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.messages)
}

func (f *sequencedNotifier) message(index int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= len(f.messages) {
		return ""
	}
	return f.messages[index]
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
