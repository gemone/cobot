package cron

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	brokersqlite "github.com/cobot-agent/cobot/internal/broker"
	"github.com/cobot-agent/cobot/pkg/broker"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// tempTestBroker creates a SQLiteBroker backed by a temporary database.
func tempTestBroker(t *testing.T) (broker.Broker, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "coord.db")
	b, err := brokersqlite.NewSQLiteBroker(dbPath)
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}
	return b, func() { _ = b.Close() }
}

// noopExecuteFn is a no-op job execution function for tests.
func noopExecuteFn(_ context.Context, _, _, _ string) (string, error) {
	return "test-result", nil
}

// newTestScheduler creates a Scheduler with a temporary store and the given broker.
func newTestScheduler(t *testing.T, br broker.Broker) *Scheduler {
	t.Helper()
	store := NewStore(t.TempDir())
	runStore := NewRunStore(t.TempDir())
	return NewScheduler(store, noopExecuteFn, runStore, br, nil)
}

// newTestSchedulerWithStore creates a Scheduler backed by a specific store directory.
func newTestSchedulerWithStore(t *testing.T, storeDir string, br broker.Broker) *Scheduler {
	t.Helper()
	store := NewStore(storeDir)
	runStore := NewRunStore(t.TempDir())
	return NewScheduler(store, noopExecuteFn, runStore, br, nil)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestScheduler_LeaderAcquireAndRelease verifies that a scheduler can acquire
// the leader lease on Start and release it on Stop, allowing another holder to
// take over.
func TestScheduler_LeaderAcquireAndRelease(t *testing.T) {
	t.Parallel()

	br, cleanup := tempTestBroker(t)
	defer cleanup()
	ctx := context.Background()

	s := newTestScheduler(t, br)

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Scheduler should be leader.
	if !s.isLeader.Load() {
		t.Fatal("expected scheduler to be leader after Start")
	}

	// Another holder should not be able to acquire the same lease.
	ok, err := br.TryAcquire(ctx, schedulerLeaseKey, "other-holder", leaseTTL)
	if err != nil {
		t.Fatalf("other TryAcquire: %v", err)
	}
	if ok {
		t.Fatal("other holder should not acquire lease while scheduler is leader")
	}

	// Renew should succeed for the current holder.
	if err := br.Renew(ctx, schedulerLeaseKey, s.holderID, leaseTTL); err != nil {
		t.Fatalf("Renew should succeed for leader: %v", err)
	}

	// Stop the scheduler — this should release the lease.
	s.Stop()

	// After Stop, another holder should be able to acquire.
	ok, err = br.TryAcquire(ctx, schedulerLeaseKey, "other-holder", leaseTTL)
	if err != nil {
		t.Fatalf("other TryAcquire after Stop: %v", err)
	}
	if !ok {
		t.Fatal("other holder should acquire lease after scheduler Stop")
	}
}

// TestScheduler_AddJobAsLeader verifies that a leader scheduler correctly
// schedules a cron job in-memory AND persists it to the store.
func TestScheduler_AddJobAsLeader(t *testing.T) {
	t.Parallel()

	br, cleanup := tempTestBroker(t)
	defer cleanup()
	ctx := context.Background()

	s := newTestScheduler(t, br)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	if !s.isLeader.Load() {
		t.Fatal("expected scheduler to be leader")
	}

	job := &Job{
		ID:       NewJobID(),
		Name:     "test-leader-job",
		Schedule: "*/5 * * * *",
		Prompt:   "say hello",
		Model:    "test-model",
		Status:   "active",
	}

	if err := s.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	// Verify the job is scheduled in-memory.
	s.mu.Lock()
	entryID, exists := s.jobs[job.ID]
	s.mu.Unlock()

	if !exists {
		t.Fatal("expected job to be scheduled in s.jobs map")
	}

	// Verify the cron instance has the entry.
	found := false
	for _, e := range s.cron.Entries() {
		if e.ID == entryID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("cron has no entry with ID %v", entryID)
	}

	// Verify the job is persisted.
	stored, err := s.store.Get(job.ID)
	if err != nil {
		t.Fatalf("store Get: %v", err)
	}
	if stored.ID != job.ID || stored.Name != job.Name {
		t.Fatalf("persisted job mismatch: got id=%q name=%q", stored.ID, stored.Name)
	}
}

// TestScheduler_AddJobFailsAsFollower verifies that a follower scheduler
// persists an added job but does NOT schedule it locally.
func TestScheduler_AddJobFailsAsFollower(t *testing.T) {
	t.Parallel()

	br, cleanup := tempTestBroker(t)
	defer cleanup()
	ctx := context.Background()

	// Pre-acquire the lease so the scheduler starts as a follower.
	ok, err := br.TryAcquire(ctx, schedulerLeaseKey, "preemptor", leaseTTL)
	if err != nil {
		t.Fatalf("preempt TryAcquire: %v", err)
	}
	if !ok {
		t.Fatal("should have acquired preempt lease")
	}

	s := newTestScheduler(t, br)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	if s.isLeader.Load() {
		t.Fatal("scheduler should NOT be leader when lease is held by another")
	}

	job := &Job{
		ID:       NewJobID(),
		Name:     "follower-job",
		Schedule: "*/5 * * * *",
		Prompt:   "say hello",
		Model:    "test-model",
		Status:   "active",
	}

	if err := s.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	// Job should be persisted even though we're follower.
	stored, err := s.store.Get(job.ID)
	if err != nil {
		t.Fatalf("store Get: %v", err)
	}
	if stored.ID != job.ID {
		t.Fatalf("persisted job ID mismatch: got %q, want %q", stored.ID, job.ID)
	}

	// But NOT scheduled locally.
	s.mu.Lock()
	_, exists := s.jobs[job.ID]
	s.mu.Unlock()

	if exists {
		t.Fatal("job should NOT be in s.jobs when scheduler is follower")
	}
}

// TestScheduler_LeaseRenewalFailure tests the leader step-down and re-acquire
// scenario. It simulates what renewLeaseLoop does when Renew fails: stop cron,
// mark as non-leader, attempt re-acquire, create a new cron and reschedule.
func TestScheduler_LeaseRenewalFailure(t *testing.T) {
	t.Parallel()

	br, cleanup := tempTestBroker(t)
	defer cleanup()
	ctx := context.Background()

	// Shared store so a second scheduler can see the first's persisted jobs.
	storeDir := t.TempDir()

	// --- Phase 1: Start scheduler, become leader, add a job. ---
	s1 := newTestSchedulerWithStore(t, storeDir, br)
	if err := s1.Start(ctx); err != nil {
		t.Fatalf("s1 Start: %v", err)
	}

	if !s1.isLeader.Load() {
		t.Fatal("s1 should be leader")
	}

	job := &Job{
		ID:       NewJobID(),
		Name:     "lease-test-job",
		Schedule: "*/5 * * * *",
		Prompt:   "say hello",
		Status:   "active",
	}
	if err := s1.AddJob(job); err != nil {
		t.Fatalf("s1 AddJob: %v", err)
	}

	// Confirm Renew works for s1.
	if err := br.Renew(ctx, schedulerLeaseKey, s1.holderID, leaseTTL); err != nil {
		t.Fatalf("Renew should succeed for s1: %v", err)
	}

	// --- Phase 2: Simulate lease loss (renew failure). ---
	// Release s1's lease and have another holder steal it.
	if err := br.Release(ctx, schedulerLeaseKey, s1.holderID); err != nil {
		t.Fatalf("release s1 lease: %v", err)
	}
	stolen, err := br.TryAcquire(ctx, schedulerLeaseKey, "steal-holder", leaseTTL)
	if err != nil {
		t.Fatalf("steal TryAcquire: %v", err)
	}
	if !stolen {
		t.Fatal("steal holder should have acquired the lease")
	}

	// Renew should now fail for s1's holderID.
	if err := br.Renew(ctx, schedulerLeaseKey, s1.holderID, leaseTTL); err == nil {
		t.Fatal("expected Renew to fail after lease was stolen")
	}

	// Manually simulate what renewLeaseLoop does: stop cron, step down.
	cronDone := s1.cron.Stop()
	<-cronDone.Done() // wait for in-flight jobs
	s1.isLeader.Store(false)

	// Re-acquire should fail while steal-holder has the lease.
	acquired, _ := br.TryAcquire(ctx, schedulerLeaseKey, s1.holderID, leaseTTL)
	if acquired {
		t.Fatal("s1 should NOT re-acquire while steal-holder holds the lease")
	}

	// --- Phase 3: Stop s1, verify failover to s2. ---
	s1.Stop() // cancel the context so goroutines exit

	// Release the stolen lease.
	if err := br.Release(ctx, schedulerLeaseKey, "steal-holder"); err != nil {
		t.Fatalf("release steal-holder: %v", err)
	}

	// Start a second scheduler with the same store — it should become leader
	// and pick up the persisted job.
	s2 := newTestSchedulerWithStore(t, storeDir, br)
	if err := s2.Start(ctx); err != nil {
		t.Fatalf("s2 Start: %v", err)
	}
	defer s2.Stop()

	if !s2.isLeader.Load() {
		t.Fatal("s2 should be leader after s1 stopped and lease released")
	}

	// s2 should have loaded and scheduled the persisted job.
	s2.mu.Lock()
	_, exists := s2.jobs[job.ID]
	s2.mu.Unlock()

	if !exists {
		t.Fatal("s2 should have scheduled the persisted job on Start")
	}
}

// TestScheduler_concurrentAccess exercises AddJob from multiple goroutines to
// verify no data races on isLeader (atomic.Bool), the jobs map, or cron state.
// Run with `go test -race` to detect violations.
func TestScheduler_concurrentAccess(t *testing.T) {
	t.Parallel()

	br, cleanup := tempTestBroker(t)
	defer cleanup()
	ctx := context.Background()

	s := newTestScheduler(t, br)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	const numGoroutines = 10
	var wg sync.WaitGroup
	var added atomic.Int32

	// Concurrently add jobs — exercises isLeader.Load(), scheduleJob (mu.Lock),
	// cron.Schedule, and store.Create.
	for i := range numGoroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			job := &Job{
				ID:       NewJobID(),
				Name:     "concurrent-job",
				Schedule: "*/5 * * * *",
				Prompt:   "say hello",
				Status:   "active",
			}
			if err := s.AddJob(job); err != nil {
				t.Logf("AddJob %d: %v", idx, err)
				return
			}
			added.Add(1)
		}(i)
	}

	wg.Wait()

	n := added.Load()
	if n == 0 {
		t.Fatal("expected at least one AddJob to succeed")
	}

	// Verify that exactly the number of successful adds appear in s.jobs.
	s.mu.Lock()
	jobCount := len(s.jobs)
	s.mu.Unlock()

	if int32(jobCount) != n {
		t.Fatalf("jobs map has %d entries, but %d adds succeeded", jobCount, n)
	}

	// All persisted jobs should be readable from the store.
	storeJobs, err := s.store.ListReadOnly()
	if err != nil {
		t.Fatalf("store ListReadOnly: %v", err)
	}
	if len(storeJobs) != int(n) {
		t.Fatalf("store has %d jobs, but %d adds succeeded", len(storeJobs), n)
	}
}

// TestScheduler_syncJobs verifies that jobs created directly in the store (e.g.
// by a follower instance) get picked up when syncJobs runs on the leader.
func TestScheduler_syncJobs(t *testing.T) {
	t.Parallel()

	br, cleanup := tempTestBroker(t)
	defer cleanup()
	ctx := context.Background()

	s := newTestScheduler(t, br)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	if !s.isLeader.Load() {
		t.Fatal("expected leader")
	}

	// Create two active jobs directly in the store (bypassing AddJob).
	jobs := []*Job{
		{
			ID:       NewJobID(),
			Name:     "sync-job-1",
			Schedule: "0 * * * *",
			Prompt:   "hourly task",
			Status:   "active",
		},
		{
			ID:       NewJobID(),
			Name:     "sync-job-2",
			Schedule: "*/15 * * * *",
			Prompt:   "quarterly task",
			Status:   "active",
		},
	}
	for _, j := range jobs {
		if err := s.store.Create(j); err != nil {
			t.Fatalf("store Create %s: %v", j.ID, err)
		}
	}

	// Before sync, no jobs should be in-memory.
	s.mu.Lock()
	initialCount := len(s.jobs)
	s.mu.Unlock()
	if initialCount != 0 {
		t.Fatalf("expected 0 scheduled jobs before sync, got %d", initialCount)
	}

	// Run sync.
	s.syncJobs()

	// Both jobs should now be scheduled.
	s.mu.Lock()
	count := len(s.jobs)
	s.mu.Unlock()

	if count != 2 {
		t.Fatalf("expected 2 scheduled jobs after sync, got %d", count)
	}

	// Verify each job has a cron entry.
	for _, j := range jobs {
		s.mu.Lock()
		entryID, exists := s.jobs[j.ID]
		s.mu.Unlock()
		if !exists {
			t.Errorf("job %s not in s.jobs after sync", j.ID)
			continue
		}
		found := false
		for _, e := range s.cron.Entries() {
			if e.ID == entryID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("cron has no entry for job %s (entryID %v)", j.ID, entryID)
		}
	}

	// Now pause one job directly in the store and verify sync removes it.
	jobs[0].Status = "paused"
	if err := s.store.Update(jobs[0]); err != nil {
		t.Fatalf("store Update pause: %v", err)
	}

	s.syncJobs()

	s.mu.Lock()
	_, stillScheduled := s.jobs[jobs[0].ID]
	s.mu.Unlock()
	if stillScheduled {
		t.Error("paused job should have been removed from s.jobs after sync")
	}
}

func TestScheduler_StartRefreshesPersistedNextRun(t *testing.T) {
	t.Parallel()

	br, cleanup := tempTestBroker(t)
	defer cleanup()
	ctx := context.Background()

	storeDir := t.TempDir()
	s := newTestSchedulerWithStore(t, storeDir, br)

	staleNextRun := time.Now().Add(-2 * time.Hour).Round(time.Second)
	job := &Job{
		ID:       NewJobID(),
		Name:     "restart-refresh",
		Schedule: "*/5 * * * *",
		Prompt:   "check next run",
		Status:   StatusActive,
		NextRun:  &staleNextRun,
	}
	if err := s.store.Create(job); err != nil {
		t.Fatalf("store Create: %v", err)
	}

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	jobs, err := s.ListJobs()
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].NextRun == nil {
		t.Fatal("expected NextRun to be set")
	}
	if !jobs[0].NextRun.After(time.Now()) {
		t.Fatalf("expected refreshed NextRun in future, got %v", jobs[0].NextRun)
	}
	if !jobs[0].NextRun.After(staleNextRun) {
		t.Fatalf("expected refreshed NextRun after stale value %v, got %v", staleNextRun, jobs[0].NextRun)
	}

	storeJobs, err := s.store.ListReadOnly()
	if err != nil {
		t.Fatalf("store ListReadOnly: %v", err)
	}
	if len(storeJobs) != 1 {
		t.Fatalf("expected 1 stored job, got %d", len(storeJobs))
	}
	if storeJobs[0].NextRun == nil {
		t.Fatal("expected stored NextRun to be set")
	}
	if !storeJobs[0].NextRun.After(staleNextRun) {
		t.Fatalf("expected stored NextRun after stale value %v, got %v", staleNextRun, storeJobs[0].NextRun)
	}
}

func TestScheduler_syncJobsRefreshesPersistedNextRun(t *testing.T) {
	t.Parallel()

	br, cleanup := tempTestBroker(t)
	defer cleanup()
	ctx := context.Background()

	s := newTestScheduler(t, br)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	staleNextRun := time.Now().Add(-90 * time.Minute).Round(time.Second)
	job := &Job{
		ID:       NewJobID(),
		Name:     "sync-refresh",
		Schedule: "*/10 * * * *",
		Prompt:   "sync next run",
		Status:   StatusActive,
		NextRun:  &staleNextRun,
	}
	if err := s.store.Create(job); err != nil {
		t.Fatalf("store Create: %v", err)
	}

	s.syncJobs()

	storeJobs, err := s.store.ListReadOnly()
	if err != nil {
		t.Fatalf("store ListReadOnly: %v", err)
	}
	if len(storeJobs) != 1 {
		t.Fatalf("expected 1 stored job, got %d", len(storeJobs))
	}
	if storeJobs[0].NextRun == nil {
		t.Fatal("expected stored NextRun to be set")
	}
	if !storeJobs[0].NextRun.After(staleNextRun) {
		t.Fatalf("expected stored NextRun after stale value %v, got %v", staleNextRun, storeJobs[0].NextRun)
	}
}

// TestScheduler_StopWaitsForGoroutines verifies that Stop() blocks until all
// background goroutines (renewLeaseLoop, consumeLoop, cleanupLoop) have exited.
func TestScheduler_StopWaitsForGoroutines(t *testing.T) {
	t.Parallel()

	br, cleanup := tempTestBroker(t)
	defer cleanup()
	ctx := context.Background()

	s := newTestScheduler(t, br)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Verify the scheduler is running as leader.
	if !s.isLeader.Load() {
		t.Fatal("expected leader")
	}

	// Stop should return within a reasonable time.  If it hangs (e.g. WaitGroup
	// imbalance), the test will time out.
	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success — Stop completed.
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return within 10s — likely a WaitGroup imbalance")
	}

	// After Stop, the lease should be released so another holder can acquire.
	ok, err := br.TryAcquire(ctx, schedulerLeaseKey, "post-stop-holder", leaseTTL)
	if err != nil {
		t.Fatalf("post-Stop TryAcquire: %v", err)
	}
	if !ok {
		t.Fatal("lease should be released after Stop, allowing a new holder")
	}
}

// TestScheduler_StopWaitsForGoroutines_Follower is the same test but for the
// follower path (only consumeLoop goroutine).
func TestScheduler_StopWaitsForGoroutines_Follower(t *testing.T) {
	t.Parallel()

	br, cleanup := tempTestBroker(t)
	defer cleanup()
	ctx := context.Background()

	// Pre-acquire the lease so the scheduler starts as a follower.
	ok, err := br.TryAcquire(ctx, schedulerLeaseKey, "other", leaseTTL)
	if err != nil {
		t.Fatalf("preempt TryAcquire: %v", err)
	}
	if !ok {
		t.Fatal("should have acquired preempt lease")
	}

	s := newTestScheduler(t, br)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if s.isLeader.Load() {
		t.Fatal("should be follower")
	}

	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(10 * time.Second):
		t.Fatal("Stop (follower) did not return within 10s")
	}
}
