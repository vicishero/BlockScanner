package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"blockscanner/config"
	"blockscanner/processor"
	"blockscanner/scanner"
	"blockscanner/scheduler"
	"blockscanner/store"
)

func main() {
	// 加载配置（config.yaml → 环境变量覆盖，日志初始化之前先读配置）
	cfg, err := config.Load("")
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// 初始化日志：同时输出到 stdout 和按天滚动的日志文件
	if err := initLogger(&cfg.Log); err != nil {
		slog.Error("failed to init logger", "error", err)
		os.Exit(1)
	}

	slog.Info("BlockScanner starting...")

	// 初始化数据库
	db, err := store.NewDB(cfg.DB)
	if err != nil {
		slog.Error("failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// 种子数据：首次运行时插入示例配置
	if err := seedData(db); err != nil {
		slog.Error("failed to seed data", "error", err)
		os.Exit(1)
	}

	// 创建上下文（监听系统信号）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 信号处理
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 初始化扫块器（独立运行模式用）
	evmScanner := scanner.NewEvmScanner(db)

	// 启动调度器（生产模式：由 cron 驱动扫描）
	sched := scheduler.New(db, evmScanner)
	if err := sched.Start(ctx); err != nil {
		slog.Error("failed to start scheduler", "error", err)
		os.Exit(1)
	}

	slog.Info("BlockScanner started successfully, waiting for signals...")

	// 等待关闭信号
	sig := <-sigCh
	slog.Info("received signal, shutting down...", "signal", sig.String())

	cancel()

	// 优雅关闭
	shutdownTimeout := time.Duration(cfg.App.ShutdownTimeoutSecs) * time.Second
	slog.Info("waiting for graceful shutdown...", "timeout", shutdownTimeout)

	// 给调度器和扫描器一些时间清理
	time.Sleep(2 * time.Second)

	slog.Info("BlockScanner stopped")
}

// seedData 首次运行时插入示例配置数据
// 如果已存在数据则跳过（幂等）
func seedData(db *store.DB) error {
	ctx := context.Background()

	// 检查是否已有链配置
	chains, err := db.GetEnabledChains(ctx)
	if err != nil {
		return err
	}
	if len(chains) > 0 {
		slog.Info("seed data skipped: chains already exist", "count", len(chains))
		return nil
	}

	slog.Info("seeding example data...")

	// === 示例链: Polygon (chain_id=137) ===
	polygonChain := map[string]interface{}{
		"chain_id":              137,
		"name":                  "Polygon",
		"rpc_url":               "https://polygon-rpc.com",
		"block_interval_secs":   2,
		"confirmations":         12,
		"batch_size":            2000,
		"catch_up_batch_size":   5000,
		"catch_up_interval_secs": 1,
		"start_block":           0,
		"last_synced_block":     0,
		"status":                1,
	}

	if err := db.WithContext(ctx).Table("infra_evm_chain").Create(polygonChain).Error; err != nil {
		slog.Warn("seed polygon chain failed", "error", err)
	}

	// === 示例链: Ethereum (chain_id=1) ===
	ethChain := map[string]interface{}{
		"chain_id":              1,
		"name":                  "Ethereum",
		"rpc_url":               "https://eth.llamarpc.com",
		"block_interval_secs":   12,
		"confirmations":         6,
		"batch_size":            2000,
		"catch_up_batch_size":   5000,
		"catch_up_interval_secs": 1,
		"start_block":           0,
		"last_synced_block":     0,
		"status":                1,
	}

	if err := db.WithContext(ctx).Table("infra_evm_chain").Create(ethChain).Error; err != nil {
		slog.Warn("seed ethereum chain failed", "error", err)
	}

	// === 示例合约事件: USDC Transfer on Polygon ===
	// 计算 topic0
	parsed, err := scanner.ParseEventSignature("Transfer(address indexed from, address indexed to, uint256 value)")
	if err != nil {
		slog.Warn("parse Transfer signature failed", "error", err)
	} else {
		usdcTransfer := map[string]interface{}{
			"chain_id":         137,
			"contract_address": "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174", // USDC on Polygon
			"event_signature":  "Transfer(address indexed from, address indexed to, uint256 value)",
			"event_name":       parsed.Name,
			"alias":            "nftTransfer", // 示例 alias
			"topic0":           parsed.Topic0,
			"start_block":      0,
			"last_synced_block": 0,
			"status":           1,
		}

		if err := db.WithContext(ctx).Table("infra_evm_contract_event").Create(usdcTransfer).Error; err != nil {
			slog.Warn("seed USDC Transfer event failed", "error", err)
		}
	}

	// === 打印已知 alias 列表 ===
	slog.Info("registered event aliases", "aliases", processor.KnownAliases())

	slog.Info("seed data completed")
	return nil
}

// initLogger 初始化日志：同时输出到 stdout 和按天滚动的日志文件
func initLogger(cfg *config.LogConfig) error {
	// 解析日志级别
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	// 按天滚动的日志文件
	dailyWriter, err := config.NewDailyWriter(cfg.Dir, "blockscanner", cfg.MaxAgeDays)
	if err != nil {
		return err
	}

	// 同时输出到 stdout 和日志文件
	multiWriter := io.MultiWriter(os.Stdout, dailyWriter)

	handler := slog.NewTextHandler(multiWriter, &slog.HandlerOptions{
		Level: level,
	})
	slog.SetDefault(slog.New(handler))

	return nil
}
