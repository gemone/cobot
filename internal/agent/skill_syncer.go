package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cobot-agent/cobot/internal/memory"
	"github.com/cobot-agent/cobot/internal/skills"
)

// BackgroundSkillSyncer periodically runs the WorkflowAnalyzer and writes
// generated skill files to disk.
type BackgroundSkillSyncer struct {
	analyzer *memory.WorkflowAnalyzer
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewBackgroundSkillSyncer creates a new syncer with the given analyzer and interval.
// If interval is zero, it defaults to 1 hour.
func NewBackgroundSkillSyncer(analyzer *memory.WorkflowAnalyzer, interval time.Duration) *BackgroundSkillSyncer {
	if interval <= 0 {
		interval = 1 * time.Hour
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &BackgroundSkillSyncer{
		analyzer: analyzer,
		interval: interval,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Start begins the background sync loop.
func (s *BackgroundSkillSyncer) Start() {
	s.wg.Add(1)
	go s.loop()
}

// Stop cancels the sync loop and waits for it to exit.
func (s *BackgroundSkillSyncer) Stop() {
	s.cancel()
	s.wg.Wait()
}

func (s *BackgroundSkillSyncer) loop() {
	defer s.wg.Done()

	// Run immediately on start, then on every interval.
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.runOnce()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.runOnce()
		}
	}
}

func (s *BackgroundSkillSyncer) runOnce() {
	content, changed, err := s.analyzer.Analyze(s.ctx)
	if err != nil {
		slog.Error("skill syncer analysis failed", "error", err)
		return
	}
	if !changed {
		slog.Debug("skill syncer: no changes detected")
		return
	}

	skillDir := filepath.Join(s.analyzer.SkillsDir(), "auto-user-workflow-patterns")
	if err := skills.EnsureContainedDir(skillDir, s.analyzer.SkillsDir()); err != nil {
		slog.Error("skill syncer: failed to ensure skill dir", "error", err)
		return
	}

	skillPath := filepath.Join(skillDir, skills.SkillFile)
	if err := os.WriteFile(skillPath, []byte(content), 0644); err != nil {
		slog.Error("skill syncer: failed to write skill file", "path", skillPath, "error", err)
		return
	}

	slog.Info("skill syncer: updated auto-user-workflow-patterns skill", "path", skillPath)
}
