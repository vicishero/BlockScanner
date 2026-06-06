package scanner

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"blockscanner/entity"
	"blockscanner/store"
)

// EvmScanner 扫块系统入口 — 轻量级 orchestrator
// 支持独立运行模式（调试/测试场景），生产环境由 scheduler 驱动
type EvmScanner struct {
	db     *store.DB
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewEvmScanner 创建扫块器
func NewEvmScanner(db *store.DB) *EvmScanner {
	return &EvmScanner{db: db}
}

// StartStandalone 独立运行模式：为每条启用的链创建后台扫描循环
// 生产环境由 scheduler 驱动，此方法用于调试/测试
func (s *EvmScanner) StartStandalone(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)

	chains, err := s.db.GetEnabledChains(ctx)
	if err != nil {
		return err
	}

	if len(chains) == 0 {
		slog.Warn("no enabled chains found, scanner will be idle")
		return nil
	}

	slog.Info("starting standalone evm scanner", "chain_count", len(chains))

	for i := range chains {
		ch := chains[i]
		s.wg.Add(1)
		go s.runChainStandalone(ctx, &ch)
	}

	return nil
}

// runChainStandalone 单链独立扫描循环
func (s *EvmScanner) runChainStandalone(ctx context.Context, chain *entity.InfraEvmChain) {
	defer s.wg.Done()

	worker := NewChainWorker(s.db, chain.RPCURL, chain.ChainID)
	interval := time.Duration(chain.BlockIntervalSecs) * time.Second
	if interval < 1*time.Second {
		interval = 1 * time.Second
	}

	slog.Info("chain scanner started",
		"chain_id", chain.ChainID,
		"name", chain.Name,
		"interval", interval,
	)

	for {
		select {
		case <-ctx.Done():
			slog.Info("chain scanner stopped", "chain_id", chain.ChainID)
			return
		default:
		}

		// 执行扫描（最多 10 轮连续追块）
		for round := 0; round < 10; round++ {
			hasMore, _, err := worker.ScanRound(ctx)
			if err != nil {
				slog.Error("scan round error",
					"chain_id", chain.ChainID,
					"round", round,
					"error", err,
				)
				break
			}
			if !hasMore {
				break // 已追到最新块
			}
		}

		// 等待下一次扫描
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// Stop 优雅关闭
func (s *EvmScanner) Stop(timeout time.Duration) {
	if s.cancel != nil {
		s.cancel()
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("evm scanner stopped gracefully")
	case <-time.After(timeout):
		slog.Warn("evm scanner stop timed out, forcing shutdown")
	}
}
