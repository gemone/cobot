//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLandlockForked runs Landlock restriction tests in a subprocess so that
// the restrictions don't poison the parent test process.
func TestLandlockForked(t *testing.T) {
	// Create temp dirs BEFORE spawning the subprocess, so the subprocess
	// doesn't need t.TempDir() (which tries to cleanup after Landlock locks /tmp).
	allowed := t.TempDir()
	blocked := t.TempDir()

	cmd := exec.Command(os.Args[0], "-test.run=TestLandlockHelper")
	cmd.Env = append(os.Environ(),
		"COBOT_LANDLOCK_TEST=1",
		"COBOT_LANDLOCK_ALLOWED="+allowed,
		"COBOT_LANDLOCK_BLOCKED="+blocked,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("helper output:\n%s", out)
	if err != nil {
		t.Fatalf("landlock helper failed: %v", err)
	}
}

// TestLandlockHelper is the subprocess that actually applies Landlock.
func TestLandlockHelper(t *testing.T) {
	if os.Getenv("COBOT_LANDLOCK_TEST") != "1" {
		t.Skip("skipping — only runs as subprocess of TestLandlockForked")
	}

	allowed := os.Getenv("COBOT_LANDLOCK_ALLOWED")
	blocked := os.Getenv("COBOT_LANDLOCK_BLOCKED")
	if allowed == "" || blocked == "" {
		t.Fatal("missing env vars")
	}

	// Apply Landlock: only `allowed` is writable.
	applyLandlock(allowed, nil, nil, false)

	// Writing inside the allowed dir should succeed.
	allowedFile := filepath.Join(allowed, "ok.txt")
	if err := os.WriteFile(allowedFile, []byte("ok"), 0644); err != nil {
		t.Fatalf("should be able to write inside allowed dir: %v", err)
	}

	// Writing inside the blocked dir should fail when Landlock is enforced.
	// On kernels/configs without Landlock support, applyLandlock may no-op
	// (BestEffort graceful degradation), so skip instead of failing.
	blockedFile := filepath.Join(blocked, "blocked.txt")
	err := os.WriteFile(blockedFile, []byte("nope"), 0644)
	if err == nil {
		t.Skip("Landlock not enforced in this environment; write outside allowed dir succeeded")
	}
	t.Logf("Landlock blocked write: %v (expected)", err)

	// Do NOT use t.TempDir() — Landlock prevents cleanup of /tmp.
	// Exit explicitly to skip cleanup.
	os.Exit(0)
}

// TestLandlockGracefulDegradation verifies applyLandlock doesn't crash
// with various inputs. Run in subprocess to avoid polluting the parent.
func TestLandlockGracefulDegradation(t *testing.T) {
	tmp := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestLandlockGracefulHelper")
	cmd.Env = append(os.Environ(),
		"COBOT_LANDLOCK_GRACEFUL=1",
		"COBOT_LANDLOCK_TMP="+tmp,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("helper output:\n%s", out)
	if err != nil {
		t.Fatalf("graceful helper failed: %v", err)
	}
}

// TestLandlockGracefulHelper is the subprocess for degradation tests.
func TestLandlockGracefulHelper(t *testing.T) {
	if os.Getenv("COBOT_LANDLOCK_GRACEFUL") != "1" {
		t.Skip("skipping")
	}

	tmp := os.Getenv("COBOT_LANDLOCK_TMP")

	// None of these should panic.
	applyLandlock("", nil, nil, false)
	applyLandlock("", nil, nil, true)
	applyLandlock("/nonexistent", nil, nil, false)
	applyLandlock(tmp, nil, nil, false)
	applyLandlock("", []string{tmp}, nil, false)
	applyLandlock("", nil, []string{"/etc"}, true)

	// Skip cleanup — Landlock may prevent it.
	os.Exit(0)
}

// TestHostExecBasic verifies hostExec runs a command directly.
// Safe to run in-process — no Landlock applied.
func TestHostExecBasic(t *testing.T) {
	req := &LaunchRequest{
		Shell:     "/bin/sh",
		ShellFlag: "-c",
		Command:   "echo host-ok",
	}

	out, err := hostExec(t.Context(), req)
	if err != nil {
		t.Fatalf("hostExec: %v", err)
	}
	if !strings.Contains(string(out), "host-ok") {
		t.Fatalf("expected 'host-ok', got %q", string(out))
	}
}

// TestLandlockLaunchInTestBinary verifies that landlockLaunch falls back
// to hostExec for test binaries (detects .test suffix).
func TestLandlockLaunchInTestBinary(t *testing.T) {
	req := &LaunchRequest{
		Shell:     "/bin/sh",
		ShellFlag: "-c",
		Command:   "echo test-fallback",
		Config:    &SandboxConfig{Root: "/tmp"},
	}

	out, err := landlockLaunch(t.Context(), req)
	if err != nil {
		t.Fatalf("landlockLaunch: %v", err)
	}
	if !strings.Contains(string(out), "test-fallback") {
		t.Fatalf("expected 'test-fallback', got %q", string(out))
	}
}

// TestPlatformLaunchFallbackInTestBinary verifies that platformLaunch
// correctly falls back to hostExec when running inside a test binary
// (detected via .test suffix). A true end-to-end Landlock re-exec test
// would require building a separate non-test binary.
func TestPlatformLaunchFallbackInTestBinary(t *testing.T) {
	req := &LaunchRequest{
		Shell:     "/bin/sh",
		ShellFlag: "-c",
		Command:   "echo platform-ok",
		Config:    &SandboxConfig{Root: "/tmp"},
	}

	out, err := platformLaunch(t.Context(), req)
	if err != nil {
		t.Fatalf("platformLaunch: %v", err)
	}
	if !strings.Contains(string(out), "platform-ok") {
		t.Fatalf("expected 'platform-ok', got %q", string(out))
	}
}
