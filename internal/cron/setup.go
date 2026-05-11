package cron

import (
	"context"
	"fmt"

	"github.com/cobot-agent/cobot/internal/agent"
	brokersqlite "github.com/cobot-agent/cobot/internal/broker"

	"github.com/cobot-agent/cobot/pkg/broker"
)

// SetupConfig holds inputs for cron subsystem setup.
type SetupConfig struct {
	BrokerDBPath string
	CronDir      string
	RunsDir      string
	NewAgent     func() *agent.Agent
	Deliverer    Deliverer
}

// Setup creates and starts the cron subsystem: SQLite broker, store, executor, scheduler.
// On error, all created resources are cleaned up.
// Returns the Scheduler (for lifecycle) and Broker (for agent.SetBroker).
func Setup(ctx context.Context, cfg SetupConfig) (*Scheduler, broker.Broker, error) {
	brokerDB, err := brokersqlite.NewSQLiteBroker(cfg.BrokerDBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("create broker: %w", err)
	}

	cronStore := NewStore(cfg.CronDir)
	runStore := NewRunStore(cfg.RunsDir)
	executeFn := NewAgentExecutor(cfg.NewAgent)
	scheduler := NewScheduler(cronStore, executeFn, runStore, brokerDB, cfg.Deliverer)

	if err := scheduler.Start(ctx); err != nil {
		_ = brokerDB.Close()
		runStore.Close()
		return nil, nil, fmt.Errorf("start scheduler: %w", err)
	}

	return scheduler, brokerDB, nil
}
