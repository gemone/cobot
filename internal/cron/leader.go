package cron

import (
	"context"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"
)

// renewLeaseLoop periodically renews the leader lease. If renewal fails, it
// attempts to re-acquire the lease before giving up.
func (s *Scheduler) renewLeaseLoop(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(leaseRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewErr := s.broker.Renew(ctx, schedulerLeaseKey, s.holderID, leaseTTL)
			if isSQLITEBusy(renewErr) {
				continue
			}
			if renewErr != nil {
				slog.Warn("failed to renew scheduler lease, stepping down", "error", renewErr)
				s.stopCronAndWait()
				s.isLeader.Store(false)
				var acquired bool
				for attempt := 0; attempt < 3; attempt++ {
					acqErr := error(nil)
					acquired, acqErr = s.broker.TryAcquire(ctx, schedulerLeaseKey, s.holderID, leaseTTL)
					if !isSQLITEBusy(acqErr) {
						if acqErr != nil {
							slog.Warn("failed to re-acquire scheduler lease", "error", acqErr)
						}
						break
					}
					time.Sleep(time.Duration(50<<attempt) * time.Millisecond)
				}
				if acquired {
					slog.Info("re-acquired scheduler leader lease", "holder", s.holderID)
					s.becomeLeader()
				}
				continue
			}
			// Sync jobs to pick up changes made on follower instances.
			s.syncJobs()
		}
	}
}

// campaignLoop periodically attempts to acquire the leader lease when this
// instance is a follower. Once acquired, it promotes to leader and exits.
// This handles the case where the previous leader's lease expired (e.g. due
// to SQLITE_BUSY preventing renewal) but no follower was actively campaigning.
func (s *Scheduler) campaignLoop(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(leaseRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.isLeader.Load() {
				return
			}
			acquired := false
			for attempt := 0; attempt < 3; attempt++ {
				var err error
				acquired, err = s.broker.TryAcquire(ctx, schedulerLeaseKey, s.holderID, leaseTTL)
				if !isSQLITEBusy(err) {
					if err != nil {
						slog.Debug("campaign: failed to acquire lease", "error", err)
					}
					break
				}
				time.Sleep(time.Duration(50<<attempt) * time.Millisecond)
			}
			if acquired {
				slog.Info("campaign: acquired leader lease, promoting", "holder", s.holderID)
				s.becomeLeader()
				s.wg.Add(1)
				go s.renewLeaseLoop(ctx)
				s.wg.Add(1)
				go s.cleanupLoop(ctx)
				return
			}
		}
	}
}

// rescheduleAllJobs re-adds all active jobs from the store to the cron scheduler.
// This is used after leader failover to restore the schedule.
func (s *Scheduler) rescheduleAllJobs() {
	// Clear stale entries from deleted jobs before re-scheduling.
	s.mu.Lock()
	s.jobs = make(map[string]cron.EntryID)
	s.jobSchedules = make(map[string]string)
	s.mu.Unlock()

	// Don't hold mu here - scheduleJob handles its own locking.
	jobs, err := s.store.ListReadOnly()
	if err != nil {
		slog.Warn("failed to list jobs for re-scheduling", "error", err)
		return
	}
	for _, job := range jobs {
		if job.Status != StatusActive {
			continue
		}
		if err := s.scheduleJob(job); err != nil {
			slog.Warn("failed to re-schedule job", "job_id", job.ID, "error", err)
		}
	}
}

// syncJobs compares store jobs with in-memory scheduled jobs and reconciles
// differences. Called periodically by the leader to pick up jobs created or
// modified on follower instances.
func (s *Scheduler) syncJobs() {
	storeJobs, err := s.store.ListReadOnlyIfChanged()
	if err != nil {
		slog.Warn("failed to list jobs for sync", "error", err)
		return
	}
	if storeJobs == nil {
		return // unchanged
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Build set of store job IDs.
	storeIDs := make(map[string]*Job, len(storeJobs))
	for _, j := range storeJobs {
		storeIDs[j.ID] = j
	}

	// Remove jobs that no longer exist in store or are paused/deleted.
	for id := range s.jobs {
		sj, exists := storeIDs[id]
		if !exists || sj.Status != StatusActive {
			s.removeJobEntryLocked(id)
			if !exists {
				slog.Info("sync: removed deleted job", "job_id", id)
			} else {
				slog.Info("sync: unscheduled inactive job", "job_id", id, "status", sj.Status)
			}
		}
	}

	// Add new active jobs not yet in memory, and reschedule jobs with changed schedules.
	for id, sj := range storeIDs {
		if sj.Status != StatusActive {
			continue
		}
		if _, exists := s.jobs[id]; !exists {
			if err := s.scheduleJobLocked(sj); err != nil {
				slog.Warn("sync: failed to schedule job", "job_id", id, "error", err)
				continue
			}
			slog.Info("sync: scheduled new job", "job_id", id, "name", sj.Name)
		} else if curSchedule := s.jobSchedules[id]; curSchedule != sj.Schedule {
			slog.Info("sync: rescheduling job with changed schedule", "job_id", id)
			if err := s.scheduleJobLocked(sj); err != nil {
				slog.Warn("sync: failed to reschedule job", "job_id", id, "error", err)
			}
		}
	}
}

// cleanupLoop periodically runs broker cleanup (leader only).
// Guards each iteration with isLeader check so it stops working
// promptly after step-down, even though the loop itself exits on ctx.Done.
func (s *Scheduler) cleanupLoop(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.isLeader.Load() {
				continue
			}
			if err := s.broker.Cleanup(ctx); err != nil {
				slog.Warn("broker cleanup failed", "error", err)
			}
		}
	}
}
