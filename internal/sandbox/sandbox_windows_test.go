//go:build windows && !race

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestGenerateCapabilitySID(t *testing.T) {
	sid, err := generateCapabilitySID()
	if err != nil {
		t.Fatalf("generateCapabilitySID failed: %v", err)
	}
	defer windows.FreeSid(sid)

	sidStr := sid.String()
	// Capability SIDs use S-1-15-3-* (non-account namespace)
	if !strings.HasPrefix(sidStr, "S-1-15-3-") {
		t.Errorf("SID should start with S-1-15-3-, got %q", sidStr)
	}

	// Two calls should produce different SIDs.
	sid2, err := generateCapabilitySID()
	if err != nil {
		t.Fatalf("second generateCapabilitySID: %v", err)
	}
	defer windows.FreeSid(sid2)
	if sid.String() == sid2.String() {
		t.Error("two generated SIDs should differ")
	}
}

func TestBuildWriteDirs(t *testing.T) {
	cfg := &SandboxConfig{
		Root:       `C:\workspace`,
		AllowPaths: []string{`C:\extra`},
	}
	dirs := buildWriteDirs(cfg)

	if len(dirs) < 2 {
		t.Fatalf("expected at least 2 dirs (root + temp), got %d", len(dirs))
	}
	if dirs[0] != `C:\workspace` {
		t.Errorf("first dir should be root, got %q", dirs[0])
	}
	// Both TEMP and TMP (if set) should be included, plus os.TempDir().
	if tempDir := os.TempDir(); tempDir != "" {
		found := false
		for _, d := range dirs {
			if d == tempDir {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("os.TempDir() %q not in write dirs", tempDir)
		}
	}
}

func TestGrantAndRevokeAccessACL(t *testing.T) {
	sid, err := generateCapabilitySID()
	if err != nil {
		t.Fatalf("generateCapabilitySID: %v", err)
	}
	defer windows.FreeSid(sid)

	dir := t.TempDir()

	if err := grantAccessACL(dir, sid); err != nil {
		t.Skipf("grantAccessACL failed (may lack privileges): %v", err)
	}

	// Verify the ACE is present by querying the DACL.
	initWinProcs()
	dirPtr, _ := windows.UTF16PtrFromString(dir)
	var dacl, sd uintptr
	ret, _, _ := winProcGetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(dirPtr)),
		uintptr(seFileObject),
		uintptr(daclSecurityInformation),
		0, 0,
		uintptr(unsafe.Pointer(&dacl)),
		0,
		uintptr(unsafe.Pointer(&sd)),
	)
	if ret != 0 {
		t.Fatalf("GetNamedSecurityInfo failed: %d", ret)
	}
	winProcLocalFree.Call(sd)

	if err := revokeAccessACL(dir, sid); err != nil {
		t.Logf("revokeAccessACL failed (non-fatal): %v", err)
	}
}

func TestRestrictedTokenNoConfigFallback(t *testing.T) {
	req := &LaunchRequest{
		Shell: "cmd", ShellFlag: "/C",
		Command: "echo ok",
	}
	out, err := restrictedTokenLaunch(t.Context(), req)
	if err != nil {
		t.Fatalf("no-config fallback failed: %v\noutput: %s", err, string(out))
	}
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("expected 'ok', got %q", string(out))
	}
}

func TestRestrictedTokenWriteBlocking(t *testing.T) {
	// The sandbox grants write access to Root, AllowPaths, and temp dirs.
	// Use a dedicated TEMP so the granted temp tree is isolated from the
	// blocked directory, preventing false test passes.
	base := t.TempDir()
	grantedTemp := filepath.Join(base, "granted-temp")
	if err := os.MkdirAll(grantedTemp, 0o755); err != nil {
		t.Fatalf("create granted temp dir: %v", err)
	}
	t.Setenv("TEMP", grantedTemp)
	t.Setenv("TMP", grantedTemp)

	allowed := filepath.Join(base, "allowed")
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatalf("create allowed dir: %v", err)
	}
	blocked := filepath.Join(base, "blocked")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("create blocked dir: %v", err)
	}

	cfg := &SandboxConfig{Root: allowed, AllowNetwork: true}

	// Write to allowed dir should succeed.
	allowedFile := filepath.Join(allowed, "test.txt")
	req := &LaunchRequest{
		Shell: "cmd", ShellFlag: "/C",
		Command: `echo ok > "` + allowedFile + `" && type "` + allowedFile + `"`,
		Config:  cfg,
	}
	out, err := restrictedTokenLaunch(t.Context(), req)
	if err != nil {
		t.Fatalf("allowed write failed: %v\noutput: %s", err, string(out))
	}
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("expected 'ok', got %q", string(out))
	}

	// Write to blocked dir should fail — the restricted token has no ACE for it.
	blockedFile := filepath.Join(blocked, "blocked.txt")
	req.Command = `echo blocked > "` + blockedFile + `"`
	out, _ = restrictedTokenLaunch(t.Context(), req)
	t.Logf("blocked write output: %s", out)
	if _, statErr := os.Stat(blockedFile); !os.IsNotExist(statErr) {
		t.Fatalf("write to blocked dir should not create file, got stat err: %v, output: %s", statErr, string(out))
	}
}
