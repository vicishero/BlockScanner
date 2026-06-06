package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"blockscanner/entity"
	"blockscanner/notifier"
	"blockscanner/processor"
	"blockscanner/scanner"
	"blockscanner/store"

	"github.com/robfig/cron/v3"
)

// Scheduler 定时任务调度器
// 基于 infra_job 表的配置，管理扫链和事件消费的 cron 任务
type Scheduler struct {
	db              *store.DB
	scanner         *scanner.EvmScanner
	cron            *cron.Cron
	mu              sync.Mutex
	jobs            map[string]scheduledJob // key: "handler_name:handler_param"
	refreshInterval time.Duration
	alerts          *rpcAlertManager
}

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

// New 创建调度器
func New(db *store.DB, evmScanner *scanner.EvmScanner, sender notifier.Sender, opts ...Option) *Scheduler {
	s := &Scheduler{
		db:      db,
		scanner: evmScanner,
		cron: cron.New(
			cron.WithSeconds(), // 支持秒级 cron
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

// Start 初始化并启动调度器
func (s *Scheduler) Start(ctx context.Context) error {
	// 1. 注册处理器
	s.registerHandlers()

	// 2. 从数据库同步任务
	if err := s.syncJobs(ctx); err != nil {
		return fmt.Errorf("sync jobs: %w", err)
	}

	// 3. 确保所有链都有对应的定时任务
	if err := s.ensureJobsForAllChains(ctx); err != nil {
		slog.Warn("ensure jobs for chains failed", "error", err)
	}

	// 4. 启动 cron 调度器
	s.cron.Start()
	slog.Info("scheduler started")

	// 5. 监听 ctx 取消
	go func() {
		<-ctx.Done()
		slog.Info("scheduler stopping...")
		stopCtx := s.cron.Stop()
		<-stopCtx.Done()
		slog.Info("scheduler stopped")
	}()

	return nil
}

// registerHandlers 注册所有任务处理器
func (s *Scheduler) registerHandlers() {
	// scanEvmChain 处理器
	s.cron.AddFunc("placeholder", func() {
		// 占位，实际由 syncJobs 动态添加
	})
}

// syncJobs 从数据库加载启用的任务并注册到 cron
func (s *Scheduler) syncJobs(ctx context.Context) error {
	jobs, err := s.db.GetEnabledJobs(ctx)
	if err != nil {
		return fmt.Errorf("get enabled jobs: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, job := range jobs {
		s.upsertCronJob(&job)
	}

	slog.Info("synced jobs from database", "count", len(jobs))
	return nil
}

// ensureJobsForAllChains 确保所有启用的链都有对应的 scanEvmChain 定时任务
func (s *Scheduler) ensureJobsForAllChains(ctx context.Context) error {
	chains, err := s.db.GetEnabledChains(ctx)
	if err != nil {
		return err
	}

	for _, ch := range chains {
		job := buildChainScanJob(&ch)

		// 写入数据库
		if err := s.db.UpsertJob(ctx, job); err != nil {
			slog.Error("upsert chain scan job failed",
				"chain_id", ch.ChainID,
				"error", err,
			)
			continue
		}

		// 注册到 cron
		s.mu.Lock()
		s.upsertCronJob(job)
		s.mu.Unlock()
	}

	// 确保 processScanEvent 任务存在
	processJob := buildProcessScanEventJob()
	if err := s.db.UpsertJob(ctx, processJob); err != nil {
		slog.Error("upsert process scan event job failed", "error", err)
	} else {
		s.mu.Lock()
		s.upsertCronJob(processJob)
		s.mu.Unlock()
	}

	return nil
}

// upsertCronJob 动态添加或更新 cron 任务（需持有锁）
func (s *Scheduler) upsertCronJob(job *entity.InfraJob) {
	key := jobKey(job.HandlerName, job.HandlerParam)

	if existing, ok := s.jobs[key]; ok {
		if existing.cron == job.CronExpression {
			return
		}
		s.cron.Remove(existing.entryID)
	}

	// 根据 handler_name 创建对应的 cron 任务
	var cmd func()
	switch job.HandlerName {
	case "scanEvmChain":
		cmd = s.buildScanEvmChainCmd(job)
	case "processScanEvent":
		cmd = s.buildProcessScanEventCmd(job)
	default:
		slog.Warn("unknown handler", "handler_name", job.HandlerName)
		return
	}

	entryID, err := s.cron.AddFunc(job.CronExpression, cmd)
	if err != nil {
		slog.Error("add cron job failed",
			"name", job.Name,
			"cron", job.CronExpression,
			"error", err,
		)
		return
	}

	s.jobs[key] = scheduledJob{entryID: entryID, cron: job.CronExpression}
	slog.Info("cron job registered",
		"name", job.Name,
		"handler", job.HandlerName,
		"cron", job.CronExpression,
	)
}

func (s *Scheduler) removeStaleCronJobs(validKeys map[string]bool) {
	for key, job := range s.jobs {
		if validKeys[key] {
			continue
		}
		s.cron.Remove(job.entryID)
		delete(s.jobs, key)
	}
}

// buildScanEvmChainCmd 构建扫链任务的 cron 执行函数
func (s *Scheduler) buildScanEvmChainCmd(job *entity.InfraJob) func() {
	return func() {
		ctx := context.Background()

		// 解析 chain_id
		var chainID int64
		if _, err := fmt.Sscanf(job.HandlerParam, "%d", &chainID); err != nil {
			slog.Error("invalid handler_param for scanEvmChain",
				"param", job.HandlerParam,
				"error", err,
			)
			return
		}

		// 获取链配置
		chain, err := s.db.GetChainByID(ctx, chainID)
		if err != nil {
			slog.Error("chain not found",
				"chain_id", chainID,
				"error", err,
			)
			return
		}

		// 创建 worker 并循环最多 10 轮
		worker := scanner.NewChainWorker(s.db, chain.RPCURL, chain.ChainID)

		for round := 0; round < 10; round++ {
			hasMore, err := worker.ScanRound(ctx)
			if err != nil {
				slog.Error("scan round error",
					"chain_id", chainID,
					"round", round,
					"error", err,
				)
				break
			}
			if !hasMore {
				break // 已追到最新块
			}
		}
	}
}

// buildProcessScanEventCmd 构建事件消费任务的 cron 执行函数
func (s *Scheduler) buildProcessScanEventCmd(job *entity.InfraJob) func() {
	return func() {
		ctx := context.Background()
		if err := processScanEvents(ctx, s.db); err != nil {
			slog.Error("process scan events error", "error", err)
		}
	}
}

// jobKey 生成任务的唯一键
func jobKey(handlerName, handlerParam string) string {
	return fmt.Sprintf("%s:%s", handlerName, handlerParam)
}

func effectiveJobs(jobs []entity.InfraJob, enabledChains map[int64]entity.InfraEvmChain) []entity.InfraJob {
	effective := make([]entity.InfraJob, 0, len(jobs))
	for _, job := range jobs {
		if job.HandlerName != "scanEvmChain" {
			effective = append(effective, job)
			continue
		}

		chainID, err := strconv.ParseInt(job.HandlerParam, 10, 64)
		if err != nil {
			continue
		}
		if _, ok := enabledChains[chainID]; !ok {
			continue
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

// buildChainScanJob 根据链配置构建定时任务实体
func buildChainScanJob(chain *entity.InfraEvmChain) *entity.InfraJob {
	cronExpr := buildCron(chain.BlockIntervalSecs)
	return &entity.InfraJob{
		Name:           fmt.Sprintf("扫链[%s]", chain.Name),
		HandlerName:    "scanEvmChain",
		HandlerParam:   fmt.Sprintf("%d", chain.ChainID),
		CronExpression: cronExpr,
		Status:         1,
	}
}

// buildProcessScanEventJob 构建事件消费定时任务实体
func buildProcessScanEventJob() *entity.InfraJob {
	return &entity.InfraJob{
		Name:           "事件消费处理",
		HandlerName:    "processScanEvent",
		HandlerParam:   "",
		CronExpression: "*/5 * * * * *", // 每 5 秒
		Status:         1,
	}
}

// buildCron 根据出块间隔生成 cron 表达式（秒级）
func buildCron(blockIntervalSecs int) string {
	secs := blockIntervalSecs
	if secs < 1 {
		secs = 1
	}
	if secs > 59 {
		secs = 59
	}
	return fmt.Sprintf("*/%d * * * * *", secs)
}

// processScanEvents 消费未处理的事件日志
func processScanEvents(ctx context.Context, db *store.DB) error {
	const batchSize = 100

	for {
		events, err := db.ClaimUnprocessedEvents(ctx, batchSize)
		if err != nil {
			return fmt.Errorf("claim events: %w", err)
		}

		if len(events) == 0 {
			return nil
		}

		for _, event := range events {
			processor.RouteEvent(ctx, db, &event)
		}
	}
}
