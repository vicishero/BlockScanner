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
	"blockscanner/notifier"
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

	// 创建上下文（监听系统信号）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 信号处理
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 初始化扫块器（独立运行模式用）
	evmScanner := scanner.NewEvmScanner(db)
	opsNotifier := notifier.NewTelegram(cfg.Telegram)

	// 启动调度器（生产模式：由 cron 驱动扫描）
	sched := scheduler.New(
		db,
		evmScanner,
		opsNotifier,
		scheduler.WithRPCAlertConfig(cfg.Telegram.RPCFailureThreshold, time.Duration(cfg.Telegram.CooldownSecs)*time.Second),
	)
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

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	if err := sched.Stop(shutdownCtx); err != nil {
		slog.Error("scheduler shutdown timed out", "timeout", shutdownTimeout, "error", err)
	}

	slog.Info("BlockScanner stopped")
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
