package scheduler

import (
	"testing"

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
