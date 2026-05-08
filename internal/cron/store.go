package cron

import (
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

const (
	StatusActive    = "active"
	StatusPaused    = "paused"
	StatusCompleted = "completed"
)

const jobExt = ".yaml"

var validJobID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validateJobID returns an error if the ID contains characters that could
// cause path traversal or is empty.
// ID must not contain ":" (used as separator in ReadID format).
func validateJobID(id string) error {
	if id == "" {
		return fmt.Errorf("job id is empty")
	}
	if !validJobID.MatchString(id) {
		return fmt.Errorf("job id %q contains invalid characters (only alphanumeric, underscore, hyphen allowed)", id)
	}
	return nil
}

// Job represents a scheduled or one-shot cron job.
type Job struct {
	ID        string     `yaml:"id"`
	Name      string     `yaml:"name"`
	Schedule  string     `yaml:"schedule"`
	Prompt    string     `yaml:"prompt"`
	Model     string     `yaml:"model,omitempty"`
	Status    string     `yaml:"status"`
	OneShot   bool       `yaml:"one_shot"`
	CreatedAt time.Time  `yaml:"created_at"`
	LastRun   *time.Time `yaml:"last_run,omitempty"`
	NextRun   *time.Time `yaml:"next_run,omitempty"`
	RunCount  int        `yaml:"run_count"`

	// ReadToken is a random opaque token persisted in YAML, regenerated on
	// every read (list/get) and every mutation (create/update). It guarantees
	// the caller has seen the latest state before performing destructive ops.
	ReadToken string `yaml:"read_token"`

	// Delivery is the target for cron result notifications.
	// Embedded so YAML keys stay flat (channel_id, chat_id, etc.).
	Delivery DeliveryTarget `yaml:",inline"`
}

// ReadID returns a temporary opaque token combining the job ID with the
// current ReadToken. This value must be passed back for delete/pause/resume
// operations. Note: calling ReadID() does NOT regenerate the token — that
// only happens when reading from or writing to the Store.
func (j *Job) ReadID() string {
	return j.ID + ":" + j.ReadToken
}

// ParseReadID splits a read_id token ("<jobID>:<token>") into its components.
// Returns the job ID, expected token, and any parse error.
func ParseReadID(readID string) (jobID string, token string, err error) {
	parts := strings.SplitN(readID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid read_id format: %q (expected \"<job_id>:<token>\")", readID)
	}
	if err := validateJobID(parts[0]); err != nil {
		return "", "", err
	}
	return parts[0], parts[1], nil
}

func verifyReadToken(job *Job, expected string) error {
	if expected != "" && job.ReadToken != expected {
		return fmt.Errorf("job %s has been modified since last read. Re-read the job and retry", job.ID)
	}
	return nil
}

// Store manages Job persistence as individual YAML files.
type Store struct {
	dir     string
	lastMod atomic.Int64 // UnixNano of directory's last known mtime
}

// NewStore creates a new Store backed by the given directory.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) jobPath(id string) string {
	return filepath.Join(s.dir, id+jobExt)
}

func (s *Store) writeJob(job *Job) error {
	data, err := yaml.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}
	if err := os.WriteFile(s.jobPath(job.ID), data, 0644); err != nil {
		return fmt.Errorf("write job file: %w", err)
	}
	return nil
}

// HasChanged returns true if any job file has been modified since last check.
// It uses an FNV-1a hash of filenames and modification times to detect
// additions, deletions, and content changes without reading file contents.
// First call always reports "changed" (zero-value fingerprint).
func (s *Store) HasChanged() bool {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		return true
	}
	h := fnv.New64a()
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != jobExt {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s:%d\n", e.Name(), info.ModTime().UnixNano())
	}
	fingerprint := int64(h.Sum64())
	for {
		prev := s.lastMod.Load()
		if fingerprint == prev {
			return false
		}
		if s.lastMod.CompareAndSwap(prev, fingerprint) {
			return true
		}
	}
}

func (s *Store) loadJob(id string) (*Job, error) {
	data, err := os.ReadFile(s.jobPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("job not found: %s", id)
		}
		return nil, fmt.Errorf("read job file: %w", err)
	}
	var job Job
	if err := yaml.Unmarshal(data, &job); err != nil {
		return nil, fmt.Errorf("unmarshal job: %w", err)
	}
	return &job, nil
}

func (s *Store) ListJobIDs() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read jobs directory: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != jobExt {
			continue
		}
		ids = append(ids, strings.TrimSuffix(entry.Name(), jobExt))
	}
	return ids, nil
}

// Create persists a new job with an initial ReadToken for optimistic concurrency.
func (s *Store) Create(job *Job) error {
	if err := validateJobID(job.ID); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("create cron dir: %w", err)
	}
	job.ReadToken = uuid.NewString()
	return s.writeJob(job)
}

// Get reads a single job by ID, generates a fresh ReadToken, and writes the
// updated token back to disk. This write-on-read behaviour is intentional:
// it implements optimistic concurrency by ensuring every read invalidates any
// previously issued read_id. Callers that need job data without refreshing the
// token (e.g. internal reconciliation) should use Read() instead to avoid the
// unnecessary I/O.
func (s *Store) Get(id string) (*Job, error) {
	if err := validateJobID(id); err != nil {
		return nil, err
	}
	job, err := s.loadJob(id)
	if err != nil {
		return nil, err
	}
	// Regenerate token on every read.
	job.ReadToken = uuid.NewString()
	if writeErr := s.writeJob(job); writeErr != nil {
		return nil, fmt.Errorf("refresh read token: %w", writeErr)
	}
	return job, nil
}

// Read reads a single job by ID without refreshing the ReadToken.
// Used internally by mutations that need the current state but should not
// change the token (the subsequent Update/Delete will handle that).
// If expectedToken is non-empty, validates the token matches before returning.
func (s *Store) Read(id string, expectedToken string) (*Job, error) {
	if err := validateJobID(id); err != nil {
		return nil, err
	}
	job, err := s.loadJob(id)
	if err != nil {
		return nil, err
	}
	if err := verifyReadToken(job, expectedToken); err != nil {
		return nil, err
	}
	return job, nil
}

// List returns all jobs with refreshed ReadTokens for optimistic concurrency.
func (s *Store) List() ([]*Job, error) {
	ids, err := s.ListJobIDs()
	if err != nil {
		return nil, err
	}
	jobs := make([]*Job, 0, len(ids))
	for _, id := range ids {
		job, err := s.loadJob(id)
		if err != nil {
			slog.Warn("cron store: skipping job due to load error", "job_id", id, "error", err)
			continue
		}
		job.ReadToken = uuid.NewString()
		if err := s.writeJob(job); err != nil {
			slog.Warn("cron store: skipping job due to write error", "job_id", id, "error", err)
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// ListReadOnly reads all jobs without refreshing ReadTokens.
// Use this for internal sync/reconcile operations that don't need
// optimistic-concurrency tokens, avoiding O(n) file writes.
func (s *Store) ListReadOnly() ([]*Job, error) {
	ids, err := s.ListJobIDs()
	if err != nil {
		return nil, err
	}
	jobs := make([]*Job, 0, len(ids))
	for _, id := range ids {
		job, err := s.loadJob(id)
		if err != nil {
			slog.Warn("skip unloadable job", "id", id, "error", err)
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// ListReadOnlyIfChanged returns jobs only if the store has changed since
// the last call. Returns (nil, nil) if unchanged.
// First call always reports "changed" (zero-value fingerprint).
func (s *Store) ListReadOnlyIfChanged() ([]*Job, error) {
	if !s.HasChanged() {
		return nil, nil
	}
	return s.ListReadOnly()
}

// Update rewrites a job file. Regenerates ReadToken on each update.
// If the file does not exist it will be created (equivalent to Create).
// Precondition: the store directory exists (established by Create's MkdirAll).
func (s *Store) Update(job *Job) error {
	if err := validateJobID(job.ID); err != nil {
		return err
	}
	job.ReadToken = uuid.NewString()
	return s.writeJob(job)
}

// Delete removes a job file from disk after verifying the expected ReadToken.
func (s *Store) Delete(id string, expectedToken string) error {
	// NOTE: There is a TOCTOU (time-of-check-time-of-use) race between reading
	// the file to verify the token and the actual os.Remove. An external process
	// could modify the file in that window, causing us to delete a newer version.
	// This is acceptable for the current single-process deployment model. If
	// multi-process access is needed, consider file-locking or a database backend.
	if err := validateJobID(id); err != nil {
		return err
	}
	if expectedToken != "" {
		job, err := s.loadJob(id)
		if err != nil {
			return err
		}
		if err := verifyReadToken(job, expectedToken); err != nil {
			return err
		}
	}
	if err := os.Remove(s.jobPath(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete job file: %w", err)
	}
	return nil
}
