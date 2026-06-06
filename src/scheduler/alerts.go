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
	if m == nil || chain == nil || err == nil {
		return
	}

	currentTime := m.now()

	m.mu.Lock()
	state := m.states[chain.ChainID]
	if state == nil {
		state = &rpcFailureState{}
		m.states[chain.ChainID] = state
	}

	state.consecutiveFailures++
	state.lastError = err.Error()

	shouldNotify := state.consecutiveFailures >= m.threshold && (state.lastNotifiedAt.IsZero() || m.cooldown <= 0 || currentTime.Sub(state.lastNotifiedAt) >= m.cooldown)
	failures := state.consecutiveFailures
	lastError := state.lastError
	if shouldNotify {
		state.alerting = true
		state.lastNotifiedAt = currentTime
	}
	m.mu.Unlock()

	if !shouldNotify {
		return
	}

	message := fmt.Sprintf("[BlockScanner] RPC 连续失败告警\n链: %s (%d)\n失败次数: %d\nRPC: %s\n错误: %s\n时间: %s",
		chain.Name,
		chain.ChainID,
		failures,
		notifier.RedactRPCURL(chain.RPCURL),
		lastError,
		currentTime.Format(time.RFC3339),
	)
	if sendErr := m.sender.SendMessage(ctx, message); sendErr != nil {
		slog.Error("send rpc alert notification failed", "component", "notifier", "chain_id", chain.ChainID, "error", sendErr)
	}
}

func (m *rpcAlertManager) recordSuccess(ctx context.Context, chain *entity.InfraEvmChain) {
	if m == nil || chain == nil {
		return
	}

	currentTime := m.now()

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

	message := fmt.Sprintf("[BlockScanner] RPC 已恢复\n链: %s (%d)\nRPC: %s\n此前连续失败: %d\n时间: %s",
		chain.Name,
		chain.ChainID,
		notifier.RedactRPCURL(chain.RPCURL),
		failures,
		currentTime.Format(time.RFC3339),
	)
	if sendErr := m.sender.SendMessage(ctx, message); sendErr != nil {
		slog.Error("send rpc recovery notification failed", "component", "notifier", "chain_id", chain.ChainID, "error", sendErr)
	}
}
