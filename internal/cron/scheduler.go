package cron

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	cobot "github.com/cobot-agent/cobot/pkg"
	"github.com/cobot-agent/cobot/pkg/broker"
)

const topicCronResult = "cron_result"

type Deliverer interface {
	Send(ctx context.Context, channelID string, msg *cobot.OutboundMessage) (*cobot.SendResult, error)
}

// Scheduler manages cron job lifecycle using robfig/cron.
type Scheduler struct {
	store        *Store
	cron         *cron.Cron
	executeFn    func(ctx context.Context, jobID, prompt, model string) (string, error)
	deliverer    Deliverer
	mu           sync.Mutex
	jobs         map[string]cron.EntryID // jobID -> cron entry ID
	jobSchedules map[string]string       // jobID -> schedule string (for change detection)
	runStore     *RunStore

	broker         broker.Broker
	holderID       string // leader lease identity
	sessionID      string // broker consume session identity
	isLeader       atomic.Bool
	cleanupRunning atomic.Bool
	wg             sync.WaitGroup
	cancel         context.CancelFunc
}

const maxCronJobs = 100

const jobTimeout = 10 * time.Minute

const leaseTTL = 30 * time.Second
const leaseRenewInterval = 10 * time.Second
const consumeInterval = 5 * time.Second
const cleanupInterval = 60 * time.Second

const brokerOpTimeout = 5 * time.Second
const schedulerLeaseKey = "cron:scheduler"

func NewScheduler(store *Store, executeFn func(ctx context.Context, jobID, prompt, model string) (string, error), runStore *RunStore, br broker.Broker, deliverer Deliverer) *Scheduler {
	return &Scheduler{
		store:        store,
		runStore:     runStore,
		deliverer:    deliverer,
		cron:         cron.New(),
		executeFn:    executeFn,
		jobs:         make(map[string]cron.EntryID),
		jobSchedules: make(map[string]string),
		broker:       br,
		holderID:     uuid.NewString(),
		sessionID:    uuid.NewString(),
	}
}

// Start loads all active jobs from the store, attempts to acquire the leader
// lease, and starts the appropriate loops. Returns an error only if loading
// jobs from the store fails.
func (s *Scheduler) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	jobs, err := s.store.ListReadOnly()
	if err != nil {
		return fmt.Errorf("load jobs: %w", err)
	}

	acquired, err := s.broker.TryAcquire(ctx, schedulerLeaseKey, s.holderID, leaseTTL)
	if err != nil {
		slog.Warn("failed to acquire scheduler lease", "error", err)
	}

	if acquired {
		s.isLeader.Store(true)
		slog.Info("acquired cron scheduler leader lease", "holder", s.holderID)
		for _, job := range jobs {
			if job.Status != StatusActive {
				continue
			}
			if err := s.scheduleJob(job); err != nil {
				slog.Warn("failed to schedule job on start",
					"job_id", job.ID, "error", err)
			}
		}
		s.cron.Start()
		s.wg.Add(1)
		go s.renewLeaseLoop(ctx)
		s.cleanupRunning.Store(true)
		s.wg.Add(1)
		go s.cleanupLoop(ctx)
	} else {
		s.isLeader.Store(false)
		slog.Info("running as cron scheduler follower", "holder", s.holderID)
	}

	s.wg.Add(1)
	go s.consumeLoop(ctx)
	// All wg.Add(1) calls above happen synchronously before their respective
	// goroutines start, ensuring Stop()'s wg.Wait() always observes all
	// counters.
	return nil
}

// stopCronAndWait stops the cron scheduler and waits for in-flight jobs.
func (s *Scheduler) stopCronAndWait() {
	s.mu.Lock()
	c := s.cron
	s.mu.Unlock()
	cronCtx := c.Stop()
	<-cronCtx.Done()
}

// Stop halts the cron scheduler, releases the leader lease, and closes the run store.
func (s *Scheduler) Stop() {
	// Stop cron first to prevent new job executions.
	if s.isLeader.Load() {
		s.stopCronAndWait()
	}
	s.cancel()
	s.wg.Wait()

	// Second check: catch cron started during wg.Wait()
	if s.isLeader.Load() {
		s.stopCronAndWait()
	}

	// Background context is correct — Stop has no request context.
	ctx, cancel := context.WithTimeout(context.Background(), brokerOpTimeout)
	defer cancel()

	if s.isLeader.Load() {
		if err := s.broker.Release(ctx, schedulerLeaseKey, s.holderID); err != nil {
			slog.Warn("failed to release scheduler lease", "error", err)
		}
	}

	s.runStore.Close()
}

// becomeLeader transitions this scheduler instance to leader state.
func (s *Scheduler) becomeLeader() {
	s.mu.Lock()
	old := s.cron
	s.cron = cron.New()
	s.mu.Unlock()

	// Stop the old cron outside the lock to avoid blocking other operations.
	// In the normal flow the old cron is already stopped (step-down calls
	// stopCronAndWait), so this is a safety measure for the shutdown race.
	cronCtx := old.Stop()
	<-cronCtx.Done()

	s.rescheduleAllJobs()
	s.isLeader.Store(true)
	s.cron.Start()
}

// validateSchedule parses the schedule without mutating in-memory cron state.
// Returns an error if the cron expression or one-shot timestamp is invalid.
func validateSchedule(job *Job) error {
	if job.OneShot {
		t, err := parseOneShotTime(job.Schedule)
		if err != nil {
			return fmt.Errorf("invalid timestamp %q: %w", job.Schedule, err)
		}
		now := time.Now()
		if !t.After(now) {
			return fmt.Errorf("one-shot time %q is in the past", job.Schedule)
		}
	} else {
		if _, err := cron.ParseStandard(job.Schedule); err != nil {
			return fmt.Errorf("invalid cron expression %q: %w", job.Schedule, err)
		}
	}
	return nil
}

// AddJob creates a new cron entry and persists the job.
// Validates the schedule regardless of leadership so that invalid expressions
// are rejected early. Only schedules in-memory if this instance is the leader;
// followers just persist — the leader will pick it up via syncJobs.
func (s *Scheduler) AddJob(job *Job) error {
	ids, err := s.store.ListJobIDs()
	if err != nil {
		return fmt.Errorf("check job count: %w", err)
	}
	if len(ids) >= maxCronJobs {
		return fmt.Errorf("maximum number of cron jobs (%d) reached", maxCronJobs)
	}
	// Validate schedule upfront regardless of leadership.
	if err := validateSchedule(job); err != nil {
		return err
	}
	// Persist first so we never have an in-memory-only job.
	if err := s.store.Create(job); err != nil {
		return err
	}
	if s.isLeader.Load() {
		if err := s.scheduleJob(job); err != nil {
			slog.Warn("failed to schedule persisted job", "job_id", job.ID, "error", err)
			// Job is persisted; leader will pick it up via syncJobs.
		}
		// Persist the NextRun that was set by scheduleJob
		if job.NextRun != nil {
			updatedJob := *job
			if err := s.store.Update(&updatedJob); err != nil {
				slog.Warn("failed to persist next run for job", "job_id", job.ID, "error", err)
			} else {
				*job = updatedJob
			}
		}
	}
	return nil
}

// RemoveJob removes a job from cron and deletes it from the store.
// readID is required — it's an opaque token from a prior list/get that proves
// the caller has seen the current state.
func (s *Scheduler) RemoveJob(readID string) error {
	jobID, token, err := parseSchedulerReadID(readID)
	if err != nil {
		return err
	}
	s.unscheduleJob(jobID)
	if err := s.store.Delete(jobID, token); err != nil {
		return err
	}
	s.CleanupJobDB(jobID)
	return nil
}

// PauseJob removes a job from cron but keeps it in the store as paused.
// readID is required to verify the caller has seen the current state.
func (s *Scheduler) PauseJob(readID string) error {
	jobID, token, err := parseSchedulerReadID(readID)
	if err != nil {
		return err
	}

	job, err := s.store.Read(jobID, token)
	if err != nil {
		return err
	}

	s.unscheduleJob(jobID)

	job.Status = StatusPaused
	return s.store.Update(job) // Update regenerates token
}

// ResumeJob re-adds a paused job to cron and sets its status to active.
// Only schedules locally if this instance is the leader.
// readID is required to verify the caller has seen the current state.
func (s *Scheduler) ResumeJob(readID string) error {
	jobID, token, err := parseSchedulerReadID(readID)
	if err != nil {
		return err
	}

	job, err := s.store.Read(jobID, token)
	if err != nil {
		return err
	}
	if job.Status != StatusPaused {
		return fmt.Errorf("job %s is not paused (status: %s)", jobID, job.Status)
	}
	job.Status = StatusActive
	if s.isLeader.Load() {
		if err := s.scheduleJob(job); err != nil {
			return err
		}
	}
	return s.store.Update(job) // Update regenerates token
}

// ListJobs returns all jobs from the store.
func (s *Scheduler) ListJobs() ([]*Job, error) {
	return s.store.List()
}

// scheduleJob — caller must NOT hold s.mu.
func (s *Scheduler) scheduleJob(job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scheduleJobLocked(job)
}

// unscheduleJob — caller must NOT hold s.mu.
func (s *Scheduler) unscheduleJob(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeJobEntryLocked(id)
}

// removeJobEntryLocked removes a job from cron and both maps.
// Caller must hold s.mu.
func (s *Scheduler) removeJobEntryLocked(id string) {
	if entryID, ok := s.jobs[id]; ok {
		s.cron.Remove(entryID)
		delete(s.jobs, id)
		delete(s.jobSchedules, id)
	}
}

// scheduleJobLocked registers a job assuming mu is already held.
func (s *Scheduler) scheduleJobLocked(job *Job) error {
	if _, ok := s.jobs[job.ID]; ok {
		s.removeJobEntryLocked(job.ID)
	}

	if job.OneShot {
		return s.scheduleOneShotLocked(job)
	}
	return s.scheduleCronExprLocked(job)
}

func (s *Scheduler) registerEntry(job *Job, sched cron.Schedule) {
	jobID := job.ID
	entryID := s.cron.Schedule(sched, cron.FuncJob(func() {
		s.runJob(jobID)
	}))
	s.jobs[jobID] = entryID
	s.jobSchedules[jobID] = job.Schedule
}

// scheduleCronExprLocked schedules a recurring job using a cron expression (caller holds mu).
func (s *Scheduler) scheduleCronExprLocked(job *Job) error {
	schedule, err := cron.ParseStandard(job.Schedule)
	if err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", job.Schedule, err)
	}

	s.registerEntry(job, schedule)

	next := s.cron.Entry(s.jobs[job.ID]).Next
	if !next.IsZero() {
		job.NextRun = &next
	}

	return nil
}

// scheduleOneShotLocked schedules a one-time job at a specific timestamp (caller holds mu).
func (s *Scheduler) scheduleOneShotLocked(job *Job) error {
	t, err := parseOneShotTime(job.Schedule)
	if err != nil {
		return fmt.Errorf("invalid timestamp %q: %w", job.Schedule, err)
	}

	now := time.Now()
	if t.Before(now) {
		return fmt.Errorf("one-shot time %q is in the past", job.Schedule)
	}

	job.NextRun = &t

	// Verify the schedule will actually fire; if the time has passed between
	// the check above and scheduling (race window), fail instead of silently
	// succeeding without creating a cron entry.
	sched := oneShotSchedule{at: t}
	if next := sched.Next(time.Now()); next.IsZero() {
		return fmt.Errorf("one-shot time %q passed before scheduling", job.Schedule)
	}

	s.registerEntry(job, sched)
	return nil
}

func (s *Scheduler) runJob(jobID string) {
	// Check if the job is still scheduled (may have been removed by syncJobs or RemoveJob).
	s.mu.Lock()
	_, scheduled := s.jobs[jobID]
	s.mu.Unlock()
	if !scheduled {
		return
	}

	// Load the latest job state from the store to avoid using a stale pointer.
	job, err := s.store.Read(jobID, "")
	if err != nil {
		slog.Debug("cron job no longer exists in store", "job_id", jobID, "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	defer cancel()

	start := time.Now()
	result, err := s.executeFn(ctx, job.ID, job.Prompt, job.Model)
	duration := time.Since(start)
	if err != nil {
		slog.Warn("cron job execution failed",
			"job_id", job.ID, "error", err)
	} else {
		slog.Debug("cron job executed",
			"job_id", job.ID, "result_len", len(result))
	}

	now := time.Now()
	stillScheduled := s.updateAndPersistJob(job, now)

	// Only store the run record if the job is still alive (not deleted mid-execution).
	if stillScheduled {
		s.storeRunRecord(job, start, duration, result, err)
	}

	// Always publish the result (even for deleted jobs, the user might want to see the last output).
	s.publishJobResult(job, result, err, duration)
}

// updateAndPersistJob updates job state (LastRun, RunCount, NextRun, Status)
// and persists the change, all under s.mu to avoid races with PauseJob/ResumeJob.
// Returns whether the job was still scheduled at the time of the update.
func (s *Scheduler) updateAndPersistJob(job *Job, now time.Time) bool {
	var toUpdate *Job
	s.mu.Lock()

	job.LastRun = &now
	job.RunCount++

	// stillScheduled guards against a stale *Job pointer captured by the cron
	// closure. If syncJobs (or RemoveJob) removed the entry from s.jobs after
	// the closure fired but before we acquired s.mu, the pointer is stale and
	// we must not persist its mutated fields. Without this check, we could
	// overwrite a job that was deleted or re-created with a different schedule.
	_, stillScheduled := s.jobs[job.ID]
	if !job.OneShot {
		if entryID, ok := s.jobs[job.ID]; ok {
			if next := s.cron.Entry(entryID).Next; !next.IsZero() {
				job.NextRun = &next
			}
		}
	} else {
		job.Status = StatusCompleted
		s.removeJobEntryLocked(job.ID)
	}

	// IMPORTANT: stillScheduled must be captured BEFORE one-shot cleanup
	// (one-shot cleanup deletes from s.jobs map, so check first).
	if stillScheduled {
		clone := *job // shallow copy of the job with updated fields
		toUpdate = &clone
	}
	s.mu.Unlock()

	if toUpdate != nil {
		if err := s.store.Update(toUpdate); err != nil {
			slog.Warn("failed to persist job update", "job_id", job.ID, "error", err)
		}
	}

	return stillScheduled
}

// storeRunRecord persists a single execution record for a job via the run store.
func (s *Scheduler) storeRunRecord(job *Job, start time.Time, duration time.Duration, result string, runErr error) {
	record := &RunRecord{
		ID:       NewJobID(),
		JobID:    job.ID,
		RunAt:    start,
		Duration: duration.Milliseconds(),
		Result:   result,
	}
	if runErr != nil {
		record.Error = runErr.Error()
	}
	if err := s.runStore.StoreRun(record); err != nil {
		slog.Warn("failed to store cron run record", "job_id", job.ID, "error", err)
	}
}

// CleanupJobDB removes the run database for a job.
func (s *Scheduler) CleanupJobDB(jobID string) {
	if err := s.runStore.DeleteJobDB(jobID); err != nil {
		slog.Warn("failed to delete run db", "job_id", jobID, "error", err)
	}
}

// ListJobRuns returns execution records for a job.
func (s *Scheduler) ListJobRuns(jobID string, limit int) ([]*RunRecord, error) {
	return s.runStore.ListRuns(jobID, limit)
}

// NewJobID generates a friendly cron job ID.
func NewJobID() string {
	return "cron_" + uuid.NewString()[:8]
}

// IsOneShot detects if a schedule string is an ISO timestamp (one-shot).
func IsOneShot(schedule string) bool {
	_, err := parseOneShotTime(schedule)
	return err == nil
}

// oneShotSchedule implements cron.Schedule for a single fire time.
type oneShotSchedule struct {
	at time.Time
}

func (o oneShotSchedule) Next(t time.Time) time.Time {
	if t.Before(o.at) {
		return o.at
	}
	return time.Time{} // zero = no more runs
}

// parseOneShotTime parses a schedule string as an RFC3339 timestamp.
// Returns the parsed time or an error if the string is not a valid RFC3339 timestamp.
func parseOneShotTime(schedule string) (time.Time, error) {
	return time.Parse(time.RFC3339, schedule)
}

func parseSchedulerReadID(readID string) (jobID, token string, err error) {
	return ParseReadID(readID)
}
