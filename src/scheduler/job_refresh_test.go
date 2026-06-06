package scheduler

import (
	"context"
	"testing"
	"time"

	"blockscanner/entity"
	"github.com/robfig/cron/v3"
)

func TestEffectiveJobsSkipsScanForDisabledChains(t *testing.T) {
	jobs := []entity.InfraJob{
		{ID: 1, HandlerName: "scanEvmChain", HandlerParam: "137", CronExpression: "*/2 * * * * *", Status: 1},
		{ID: 2, HandlerName: "scanEvmChain", HandlerParam: "1", CronExpression: "*/12 * * * * *", Status: 1},
		{ID: 3, HandlerName: "processScanEvent", HandlerParam: "", CronExpression: "*/5 * * * * *", Status: 1},
	}
	enabledChains := map[int64]entity.InfraEvmChain{137: {ChainID: 137, Name: "Polygon", Status: 1}}
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
	jobs := []entity.InfraJob{{HandlerName: "scanEvmChain", HandlerParam: "137"}, {HandlerName: "processScanEvent", HandlerParam: ""}}
	keys := jobKeys(jobs)
	if !keys["scanEvmChain:137"] {
		t.Fatalf("missing scan key")
	}
	if !keys["processScanEvent:"] {
		t.Fatalf("missing process key")
	}
}

func TestRecordScanSuccessOnlyWhenJobTouchedRPC(t *testing.T) {
	fn := &fakeNotifier{}
	manager := newRPCAlertManager(fn, 1, time.Hour, func() time.Time { return time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC) })
	chain := &entity.InfraEvmChain{ChainID: 137, Name: "Polygon", RPCURL: "https://rpc.example.com/key1234567890"}

	manager.recordFailure(context.Background(), chain, assertErr("eth_blockNumber failed"))
	if len(fn.messages) != 1 {
		t.Fatalf("messages after failure alert = %d, want 1", len(fn.messages))
	}

	recordScanSuccessIfTouchedRPC(context.Background(), manager, chain, false)
	if len(fn.messages) != 1 {
		t.Fatalf("messages after untouched successful scan = %d, want 1", len(fn.messages))
	}

	recordScanSuccessIfTouchedRPC(context.Background(), manager, chain, true)
	if len(fn.messages) != 2 {
		t.Fatalf("messages after touched successful scan = %d, want 2", len(fn.messages))
	}
}

func TestJobContextUsesSchedulerRunContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Scheduler{runCtx: ctx}

	jobCtx := s.jobContext()
	cancel()

	select {
	case <-jobCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("job context was not canceled when scheduler run context was canceled")
	}
}

func TestStopWaitsForRunningCronJobs(t *testing.T) {
	s := &Scheduler{cron: cron.New(cron.WithSeconds()), stopped: make(chan struct{})}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	_, err := s.cron.AddFunc("* * * * * *", func() {
		close(started)
		<-release
		close(done)
	})
	if err != nil {
		t.Fatalf("AddFunc failed: %v", err)
	}
	s.cron.Start()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cron job to start")
	}

	stopDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		stopDone <- s.Stop(ctx)
	}()

	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop returned before job completed with error: %v", err)
		}
		t.Fatal("Stop returned before running job completed")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cron job to finish")
	}
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Stop after job completed")
	}
}

func TestStopReturnsContextErrorWhenShutdownTimeoutExpires(t *testing.T) {
	s := &Scheduler{cron: cron.New(cron.WithSeconds()), stopped: make(chan struct{})}
	started := make(chan struct{})
	release := make(chan struct{})
	_, err := s.cron.AddFunc("* * * * * *", func() {
		close(started)
		<-release
	})
	if err != nil {
		t.Fatalf("AddFunc failed: %v", err)
	}
	s.cron.Start()
	defer close(release)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cron job to start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.Stop(ctx); err != context.DeadlineExceeded {
		t.Fatalf("Stop error = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestMissingBuiltInJobsPreservesExistingEnabledAndPausedJobs(t *testing.T) {
	chains := []entity.InfraEvmChain{
		{ChainID: 137, Name: "Polygon", BlockIntervalSecs: 2, Status: 1},
		{ChainID: 8453, Name: "Base", BlockIntervalSecs: 3, Status: 1},
	}
	existing := []entity.InfraJob{
		{ID: 1, Name: "operator scan", HandlerName: "scanEvmChain", HandlerParam: "137", CronExpression: "17 * * * * *", Status: 1},
		{ID: 2, Name: "paused scan", HandlerName: "scanEvmChain", HandlerParam: "8453", CronExpression: "19 * * * * *", Status: 2},
		{ID: 3, Name: "operator process", HandlerName: "processScanEvent", HandlerParam: "", CronExpression: "23 * * * * *", Status: 1},
	}

	missing := missingBuiltInJobs(chains, existing)
	if len(missing) != 0 {
		t.Fatalf("missing built-in jobs = %d, want 0: %#v", len(missing), missing)
	}
}

func TestMissingBuiltInJobsCreatesOnlyAbsentBuiltIns(t *testing.T) {
	chains := []entity.InfraEvmChain{
		{ChainID: 137, Name: "Polygon", BlockIntervalSecs: 2, Status: 1},
		{ChainID: 8453, Name: "Base", BlockIntervalSecs: 3, Status: 1},
	}
	existing := []entity.InfraJob{
		{ID: 1, Name: "operator scan", HandlerName: "scanEvmChain", HandlerParam: "137", CronExpression: "17 * * * * *", Status: 1},
	}

	missing := missingBuiltInJobs(chains, existing)
	if len(missing) != 2 {
		t.Fatalf("missing built-in jobs = %d, want 2: %#v", len(missing), missing)
	}
	if missing[0].HandlerName != "scanEvmChain" || missing[0].HandlerParam != "8453" || missing[0].CronExpression != "*/3 * * * * *" || missing[0].Status != 1 {
		t.Fatalf("first missing job = %#v, want active Base scan job with default cron", missing[0])
	}
	if missing[1].HandlerName != "processScanEvent" || missing[1].HandlerParam != "" || missing[1].CronExpression != "*/5 * * * * *" || missing[1].Status != 1 {
		t.Fatalf("second missing job = %#v, want active processScanEvent default job", missing[1])
	}
}

func TestUpsertCronJobKeepsExistingJobWhenReplacementCronIsInvalid(t *testing.T) {
	s := &Scheduler{cron: cron.New(cron.WithSeconds()), jobs: make(map[string]scheduledJob)}
	original := &entity.InfraJob{Name: "scan", HandlerName: "scanEvmChain", HandlerParam: "137", CronExpression: "*/2 * * * * *"}
	s.upsertCronJob(original)

	key := jobKey(original.HandlerName, original.HandlerParam)
	before, ok := s.jobs[key]
	if !ok {
		t.Fatalf("original job was not registered")
	}
	if before.entryID == 0 {
		t.Fatalf("original entryID = 0, want nonzero")
	}

	invalid := &entity.InfraJob{Name: "scan", HandlerName: "scanEvmChain", HandlerParam: "137", CronExpression: "not a cron"}
	s.upsertCronJob(invalid)

	after, ok := s.jobs[key]
	if !ok {
		t.Fatalf("existing job was removed after invalid replacement cron")
	}
	if after.cron != original.CronExpression {
		t.Fatalf("stored cron = %q, want original %q", after.cron, original.CronExpression)
	}
	if after.entryID != before.entryID {
		t.Fatalf("entryID = %d, want original %d", after.entryID, before.entryID)
	}
	if entry := s.cron.Entry(before.entryID); entry.ID != before.entryID {
		t.Fatalf("cron entry %d was removed after invalid replacement cron", before.entryID)
	}
}

func TestSyncJobRefreshAppliesEffectiveJobsAndRemovesStaleCronJobs(t *testing.T) {
	jobs := []entity.InfraJob{
		{HandlerName: "scanEvmChain", HandlerParam: "137", CronExpression: "*/2 * * * * *", Status: 1},
		{HandlerName: "scanEvmChain", HandlerParam: "1", CronExpression: "*/12 * * * * *", Status: 1},
		{HandlerName: "processScanEvent", HandlerParam: "", CronExpression: "*/5 * * * * *", Status: 1},
	}
	enabledChains := map[int64]entity.InfraEvmChain{137: {ChainID: 137, Name: "Polygon", Status: 1}}

	s := &Scheduler{
		cron: cron.New(cron.WithSeconds()),
		jobs: map[string]scheduledJob{
			"scanEvmChain:1": {entryID: 99, cron: "*/12 * * * * *"},
		},
	}
	effectiveCount := s.refreshCronJobs(jobs, enabledChains)

	if effectiveCount != 2 {
		t.Fatalf("effectiveCount = %d, want 2", effectiveCount)
	}
	if _, ok := s.jobs["scanEvmChain:1"]; ok {
		t.Fatalf("disabled-chain scan job remained after refresh")
	}
	if _, ok := s.jobs["scanEvmChain:137"]; !ok {
		t.Fatalf("enabled-chain scan job was not registered")
	}
	if _, ok := s.jobs["processScanEvent:"]; !ok {
		t.Fatalf("non-chain job was not registered")
	}
}
