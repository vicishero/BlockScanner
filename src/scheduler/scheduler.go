package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
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
	runCtx          context.Context
	refreshInterval time.Duration
	alerts          *rpcAlertManager
	stopOnce        sync.Once
	stopped         chan struct{}
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

func WithRPCAlertConfig(threshold int, cooldown time.Duration) Option {
	return func(s *Scheduler) {
		s.alerts.threshold = threshold
		s.alerts.cooldown = cooldown
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
		stopped:         make(chan struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Start 初始化并启动调度器
func (s *Scheduler) Start(ctx context.Context) error {
	s.registerHandlers()
	s.mu.Lock()
	s.runCtx = ctx
	s.mu.Unlock()

	if err := s.refreshJobs(ctx); err != nil {
		return fmt.Errorf("refresh jobs: %w", err)
	}

	s.cron.Start()
	slog.Info("scheduler started", "component", "scheduler", "refresh_interval", s.refreshInterval.String())

	go s.runRefreshLoop(ctx)

	go func() {
		<-ctx.Done()
		s.stop()
	}()

	return nil
}

func (s *Scheduler) stop() {
	s.stopOnce.Do(func() {
		slog.Info("scheduler stopping", "component", "scheduler")
		stopCtx := s.cron.Stop()
		<-stopCtx.Done()
		slog.Info("scheduler stopped", "component", "scheduler")
		close(s.stopped)
	})
}

// Stop stops the scheduler and waits for running cron jobs to finish or ctx to expire.
func (s *Scheduler) Stop(ctx context.Context) error {
	go s.stop()
	select {
	case <-s.stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// registerHandlers 注册所有任务处理器
func (s *Scheduler) registerHandlers() {
	// Cron handlers are registered dynamically from infra_job records.
}

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

func (s *Scheduler) refreshJobs(ctx context.Context) error {
	chains, err := s.db.GetEnabledChains(ctx)
	if err != nil {
		return fmt.Errorf("get enabled chains: %w", err)
	}

	enabledChains := make(map[int64]entity.InfraEvmChain, len(chains))
	for _, chain := range chains {
		enabledChains[chain.ChainID] = chain
	}

	existingJobs, err := s.db.GetJobs(ctx)
	if err != nil {
		return fmt.Errorf("get jobs: %w", err)
	}

	for _, job := range missingBuiltInJobs(chains, existingJobs) {
		if err := s.db.UpsertJob(ctx, job); err != nil {
			slog.Error("create built-in job failed", "component", "scheduler", "handler", job.HandlerName, "handler_param", job.HandlerParam, "error", err)
		}
	}

	jobs, err := s.db.GetEnabledJobs(ctx)
	if err != nil {
		return fmt.Errorf("get enabled jobs: %w", err)
	}

	effective := effectiveJobs(jobs, enabledChains)
	validKeys := jobKeys(effective)

	s.mu.Lock()
	for _, job := range effective {
		s.upsertCronJob(&job)
	}
	s.removeStaleCronJobs(validKeys)
	s.mu.Unlock()

	slog.Info("scheduler jobs refreshed", "component", "scheduler", "chains", len(chains), "jobs", len(jobs), "effective", len(effective))
	return nil
}

func (s *Scheduler) refreshCronJobs(jobs []entity.InfraJob, enabledChains map[int64]entity.InfraEvmChain) int {
	effective := effectiveJobs(jobs, enabledChains)
	validKeys := jobKeys(effective)
	for _, job := range effective {
		s.upsertCronJob(&job)
	}
	s.removeStaleCronJobs(validKeys)
	return len(effective)
}

// upsertCronJob 动态添加或更新 cron 任务（需持有锁）
func (s *Scheduler) upsertCronJob(job *entity.InfraJob) {
	key := jobKey(job.HandlerName, job.HandlerParam)

	existing, hasExisting := s.jobs[key]
	if hasExisting && existing.cron == job.CronExpression {
		return
	}

	// 根据 handler_name 创建对应的 cron 任务
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
		slog.Error("add cron job failed",
			"component", "scheduler",
			"name", job.Name,
			"cron", job.CronExpression,
			"error", err,
		)
		return
	}

	if hasExisting {
		s.cron.Remove(existing.entryID)
	}
	s.jobs[key] = scheduledJob{entryID: entryID, cron: job.CronExpression}
	slog.Info("cron job registered",
		"component", "scheduler",
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

func (s *Scheduler) jobContext() context.Context {
	s.mu.Lock()
	ctx := s.runCtx
	s.mu.Unlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (s *Scheduler) runWithJobLog(ctx context.Context, job *entity.InfraJob, run func(context.Context) (string, error)) {
	start := time.Now()
	jobLog := entity.InfraJobLog{
		JobID:     job.ID,
		Status:    0,
		Message:   "running",
		StartTime: start,
	}
	if err := s.db.CreateJobLog(ctx, &jobLog); err != nil {
		slog.Error("create job log failed", "component", "scheduler", "job_id", job.ID, "handler", job.HandlerName, "error", err)
	}

	message, err := run(ctx)
	status := int8(1)
	if err != nil {
		status = 2
		message = err.Error()
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "completed"
	}

	if jobLog.ID > 0 {
		updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if updateErr := s.db.UpdateJobLog(updateCtx, jobLog.ID, status, message, time.Now()); updateErr != nil {
			slog.Error("update job log failed", "component", "scheduler", "job_id", job.ID, "log_id", jobLog.ID, "handler", job.HandlerName, "error", updateErr)
		}
	}

	duration := time.Since(start)
	if err != nil {
		slog.Error("job failed", "component", "scheduler", "job_id", job.ID, "handler", job.HandlerName, "duration", duration.String(), "error", err)
		return
	}
	slog.Info("job completed", "component", "scheduler", "job_id", job.ID, "handler", job.HandlerName, "duration", duration.String(), "message", message)
}

// buildScanEvmChainCmd 构建扫链任务的 cron 执行函数
func (s *Scheduler) buildScanEvmChainCmd(job *entity.InfraJob) func() {
	jobCopy := *job
	return func() {
		ctx := s.jobContext()
		s.runWithJobLog(ctx, &jobCopy, func(ctx context.Context) (string, error) {
			return s.executeScanEvmChain(ctx, &jobCopy)
		})
	}
}

func (s *Scheduler) executeScanEvmChain(ctx context.Context, job *entity.InfraJob) (string, error) {
	start := time.Now()

	chainID, err := strconv.ParseInt(job.HandlerParam, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid handler_param for scanEvmChain %q: %w", job.HandlerParam, err)
	}

	chain, err := s.db.GetChainByID(ctx, chainID)
	if err != nil {
		return "", fmt.Errorf("get chain %d: %w", chainID, err)
	}

	worker := scanner.NewChainWorker(s.db, chain.RPCURL, chain.ChainID)
	rounds := 0
	hasMore := false
	touchedRPC := false
	for round := 1; round <= 10; round++ {
		roundStart := time.Now()
		var roundTouchedRPC bool
		hasMore, roundTouchedRPC, err = worker.ScanRound(ctx)
		if roundTouchedRPC {
			touchedRPC = true
		}
		roundDuration := time.Since(roundStart)
		if err != nil {
			rpcErr := scanner.IsRPCError(err)
			if rpcErr {
				s.alerts.recordFailure(ctx, chain, err)
			}
			slog.Error("scan round error", "component", "scanner", "chain_id", chainID, "round", round, "duration", roundDuration.String(), "rpc_error", rpcErr, "error", err)
			return fmt.Sprintf("scan failed: chain_id=%d round=%d rpc_error=%t duration=%s", chainID, round, rpcErr, time.Since(start).String()), err
		}

		rounds++
		slog.Info("scan round completed", "component", "scanner", "chain_id", chainID, "round", round, "duration", roundDuration.String(), "has_more", hasMore, "touched_rpc", roundTouchedRPC)
		if !hasMore {
			break
		}
	}

	recordScanSuccessIfTouchedRPC(ctx, s.alerts, chain, touchedRPC)
	return fmt.Sprintf("scan completed: chain_id=%d rounds=%d has_more=%t touched_rpc=%t duration=%s", chainID, rounds, hasMore, touchedRPC, time.Since(start).String()), nil
}

func recordScanSuccessIfTouchedRPC(ctx context.Context, alerts *rpcAlertManager, chain *entity.InfraEvmChain, touchedRPC bool) {
	if !touchedRPC || alerts == nil {
		return
	}
	alerts.recordSuccess(ctx, chain)
}

// buildProcessScanEventCmd 构建事件消费任务的 cron 执行函数
func (s *Scheduler) buildProcessScanEventCmd(job *entity.InfraJob) func() {
	jobCopy := *job
	return func() {
		ctx := s.jobContext()
		s.runWithJobLog(ctx, &jobCopy, func(ctx context.Context) (string, error) {
			result, err := processScanEvents(ctx, s.db)
			return fmt.Sprintf("process scan events completed: batches=%d claimed=%d duration=%s", result.Batches, result.Claimed, result.Duration.String()), err
		})
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

func missingBuiltInJobs(chains []entity.InfraEvmChain, existingJobs []entity.InfraJob) []*entity.InfraJob {
	existingKeys := jobKeys(existingJobs)
	missing := make([]*entity.InfraJob, 0, len(chains)+1)

	for i := range chains {
		job := buildChainScanJob(&chains[i])
		if existingKeys[jobKey(job.HandlerName, job.HandlerParam)] {
			continue
		}
		missing = append(missing, job)
	}

	processJob := buildProcessScanEventJob()
	if !existingKeys[jobKey(processJob.HandlerName, processJob.HandlerParam)] {
		missing = append(missing, processJob)
	}

	return missing
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

type processScanEventsResult struct {
	Batches  int
	Claimed  int
	Duration time.Duration
}

// processScanEvents 消费未处理的事件日志
func processScanEvents(ctx context.Context, db *store.DB) (processScanEventsResult, error) {
	const batchSize = 100

	start := time.Now()
	result := processScanEventsResult{}
	for {
		events, err := db.ClaimUnprocessedEvents(ctx, batchSize)
		if err != nil {
			result.Duration = time.Since(start)
			return result, fmt.Errorf("claim events: %w", err)
		}

		if len(events) == 0 {
			result.Duration = time.Since(start)
			slog.Info("process scan events completed", "component", "processor", "batches", result.Batches, "claimed", result.Claimed, "duration", result.Duration.String())
			return result, nil
		}

		result.Batches++
		result.Claimed += len(events)
		slog.Info("claimed scan events", "component", "processor", "batch", result.Batches, "claimed", len(events))
		for _, event := range events {
			processor.RouteEvent(ctx, db, &event)
		}
	}
}
