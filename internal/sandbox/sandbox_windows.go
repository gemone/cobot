//go:build windows && !race

package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This file implements the Windows Restricted Token sandbox.
// It uses Win32 ACL APIs (GetNamedSecurityInfoW, SetEntriesInAclW,
// SetNamedSecurityInfoW) directly — not icacls — for ACL management.
// Excluded from -race builds because CreateRestrictedToken + go test -race
// causes STATUS_HEAP_CORRUPTION (0xc0000374) on Windows.

var (
	winOnce                      sync.Once
	winProcCreateRestrictedToken *windows.LazyProc
	winProcGetNamedSecurityInfoW *windows.LazyProc
	winProcSetEntriesInAclW      *windows.LazyProc
	winProcSetNamedSecurityInfoW *windows.LazyProc
	winProcLocalFree             *windows.LazyProc
)

func initWinProcs() {
	winOnce.Do(func() {
		advapi32 := windows.NewLazyDLL("advapi32.dll")
		kernel32 := windows.NewLazyDLL("kernel32.dll")
		winProcCreateRestrictedToken = advapi32.NewProc("CreateRestrictedToken")
		winProcGetNamedSecurityInfoW = advapi32.NewProc("GetNamedSecurityInfoW")
		winProcSetEntriesInAclW = advapi32.NewProc("SetEntriesInAclW")
		winProcSetNamedSecurityInfoW = advapi32.NewProc("SetNamedSecurityInfoW")
		winProcLocalFree = kernel32.NewProc("LocalFree")
	})
}

const (
	disableMaxPrivilege = 0x0001
	luaToken            = 0x0004
	writeRestricted     = 0x0008

	seFileObject            = 1
	daclSecurityInformation = 0x00000004

	grantAccess  = 1
	denyAccess   = 3
	revokeAccess = 4

	subContainersAndObjectsInherit = 0x03
	noInheritance                  = 0x00

	trusteeIsSID     = 0
	trusteeIsUnknown = 0

	fileAllAccess   = 0x001F01FF
	fileDeleteChild = 0x00000040
)

type explicitAccessW struct {
	AccessPermissions uint32
	AccessMode        uint32
	Inheritance       uint32
	Trustee           trusteeW
}

type trusteeW struct {
	pMultipleTrustee         uintptr
	MultipleTrusteeOperation uint32
	TrusteeForm              uint32
	TrusteeType              uint32
	PtstrName                uintptr
}

type sidAndAttributes struct {
	Sid        *windows.SID
	Attributes uint32
}

func restrictedTokenLaunch(ctx context.Context, req *LaunchRequest) ([]byte, error) {
	if req.Config == nil {
		slog.Warn("sandbox: no config provided, running without Restricted Token enforcement", "command", req.Command)
		return hostExec(ctx, req)
	}

	initWinProcs()

	capSid, err := generateCapabilitySID()
	if err != nil {
		slog.Warn("sandbox: failed to generate capability SID, falling back to unsandboxed execution", "error", err)
		return hostExec(ctx, req)
	}
	defer windows.FreeSid(capSid)

	// Grant write access to allowed directories.
	writeDirs := buildWriteDirs(req.Config)
	grantedDirs := make([]string, 0, len(writeDirs))
	for _, dir := range writeDirs {
		if err := grantAccessACL(dir, capSid); err != nil {
			slog.Warn("sandbox: failed to grant write ACL", "dir", dir, "error", err)
		} else {
			grantedDirs = append(grantedDirs, dir)
		}
	}
	defer func() {
		for _, dir := range grantedDirs {
			if err := revokeAccessACL(dir, capSid); err != nil {
				slog.Debug("sandbox: failed to revoke ACL (non-fatal)", "dir", dir, "error", err)
			}
		}
	}()

	// If we failed to grant ACL on the root (required dir), fallback to hostExec
	// since the sandboxed process won't be able to write to its workspace.
	if req.Config.Root != "" && !containsDir(grantedDirs, req.Config.Root) {
		slog.Warn("sandbox: failed to grant ACL on root dir, falling back to unsandboxed execution", "root", req.Config.Root)
		return hostExec(ctx, req)
	}

	// Deny write access to readonly paths with inherited ACEs covering the full subtree.
	deniedDirs := make([]string, 0, len(req.Config.ReadonlyPaths))
	for _, rp := range req.Config.ReadonlyPaths {
		if err := denyWriteACL(rp, capSid); err != nil {
			slog.Warn("sandbox: failed to deny write ACL on readonly path", "path", rp, "error", err)
		} else {
			deniedDirs = append(deniedDirs, rp)
		}
	}
	defer func() {
		for _, dir := range deniedDirs {
			if err := revokeAccessACL(dir, capSid); err != nil {
				slog.Debug("sandbox: failed to revoke readonly deny ACL (non-fatal)", "dir", dir, "error", err)
			}
		}
	}()

	rtoken, err := createRestrictedToken(capSid)
	if err != nil {
		slog.Warn("sandbox: failed to create restricted token, falling back to unsandboxed execution", "error", err)
		return hostExec(ctx, req)
	}
	defer rtoken.Close()

	cmd := exec.CommandContext(ctx, req.Shell, req.ShellFlag, req.Command)
	if req.Dir != "" {
		cmd.Dir = req.Dir
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Token: syscall.Token(rtoken),
	}
	return cmd.CombinedOutput()
}

func containsDir(dirs []string, target string) bool {
	for _, d := range dirs {
		if d == target {
			return true
		}
	}
	return false
}

// generateCapabilitySID creates a random SID in the S-1-15-3-* namespace
// (Windows Capability SIDs). This namespace is designated for application-defined
// capabilities and cannot collide with real local/domain accounts (S-1-5-*).
func generateCapabilitySID() (*windows.SID, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, fmt.Errorf("generate random bytes: %w", err)
	}
	a := binary.LittleEndian.Uint32(buf[0:4])
	b := binary.LittleEndian.Uint32(buf[4:8])
	c := binary.LittleEndian.Uint32(buf[8:12])
	d := binary.LittleEndian.Uint32(buf[12:16])

	// S-1-15-3 = capability SID authority (application-defined, non-account)
	sidStr := fmt.Sprintf("S-1-15-3-%d-%d-%d-%d", a, b, c, d)
	sid, err := windows.StringToSid(sidStr)
	if err != nil {
		return nil, fmt.Errorf("StringToSid(%s): %w", sidStr, err)
	}
	return sid, nil
}

// buildWriteDirs collects directories that should be writable for the sandboxed process.
// It includes Root, AllowPaths, and temp directories (TEMP, TMP, os.TempDir()).
func buildWriteDirs(cfg *SandboxConfig) []string {
	seen := make(map[string]bool)
	dirs := make([]string, 0, 4+len(cfg.AllowPaths))

	add := func(dir string) {
		if dir == "" || seen[dir] {
			return
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}

	if cfg.Root != "" {
		add(cfg.Root)
	}
	// Include both TEMP and TMP env vars, plus os.TempDir() (which may differ).
	for _, env := range []string{"TEMP", "TMP"} {
		if v := os.Getenv(env); v != "" {
			add(v)
		}
	}
	add(os.TempDir())

	for _, p := range cfg.AllowPaths {
		add(p)
	}
	return dirs
}

// grantAccessACL grants FILE_ALL_ACCESS to sid on dir with container/object inheritance.
// It calls initWinProcs() internally so it's safe to use without prior initialization.
func grantAccessACL(dir string, sid *windows.SID) error {
	initWinProcs()
	dirPtr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}

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
		return fmt.Errorf("GetNamedSecurityInfo: %w", windows.Errno(ret))
	}
	defer winProcLocalFree.Call(sd)

	ea := explicitAccessW{
		AccessPermissions: fileAllAccess,
		AccessMode:        grantAccess,
		Inheritance:       subContainersAndObjectsInherit,
		Trustee: trusteeW{
			TrusteeForm: trusteeIsSID,
			TrusteeType: trusteeIsUnknown,
			PtstrName:   uintptr(unsafe.Pointer(sid)),
		},
	}

	var newAcl uintptr
	ret, _, _ = winProcSetEntriesInAclW.Call(
		1,
		uintptr(unsafe.Pointer(&ea)),
		dacl,
		uintptr(unsafe.Pointer(&newAcl)),
	)
	if ret != 0 {
		return fmt.Errorf("SetEntriesInAcl: %w", windows.Errno(ret))
	}
	defer winProcLocalFree.Call(newAcl)

	ret, _, _ = winProcSetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(dirPtr)),
		uintptr(seFileObject),
		uintptr(daclSecurityInformation),
		0, 0,
		newAcl,
		0,
	)
	if ret != 0 {
		return fmt.Errorf("SetNamedSecurityInfo: %w", windows.Errno(ret))
	}
	return nil
}

// denyWriteACL adds an inherited DENY ACE covering the full subtree for write-like
// permissions (generic write, delete, rename, ACL/owner changes). This matches
// the readonly semantics enforced by Landlock/Seatbelt on Linux/macOS.
func denyWriteACL(dir string, sid *windows.SID) error {
	initWinProcs()
	dirPtr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}

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
		return fmt.Errorf("GetNamedSecurityInfo: %w", windows.Errno(ret))
	}
	defer winProcLocalFree.Call(sd)

	// Deny all write-like operations including deletion and ACL changes,
	// propagated to children via subContainersAndObjectsInherit.
	denyMask := uint32(0x40011600) | // GENERIC_WRITE | FILE_WRITE_ATTRIBUTES | FILE_WRITE_EA | WRITE_DAC (fileGenericWrite)
		uint32(windows.DELETE) |
		uint32(windows.WRITE_DAC) |
		uint32(windows.WRITE_OWNER) |
		fileDeleteChild

	ea := explicitAccessW{
		AccessPermissions: denyMask,
		AccessMode:        denyAccess,
		Inheritance:       subContainersAndObjectsInherit,
		Trustee: trusteeW{
			TrusteeForm: trusteeIsSID,
			TrusteeType: trusteeIsUnknown,
			PtstrName:   uintptr(unsafe.Pointer(sid)),
		},
	}

	var newAcl uintptr
	ret, _, _ = winProcSetEntriesInAclW.Call(
		1,
		uintptr(unsafe.Pointer(&ea)),
		dacl,
		uintptr(unsafe.Pointer(&newAcl)),
	)
	if ret != 0 {
		return fmt.Errorf("SetEntriesInAcl: %w", windows.Errno(ret))
	}
	defer winProcLocalFree.Call(newAcl)

	ret, _, _ = winProcSetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(dirPtr)),
		uintptr(seFileObject),
		uintptr(daclSecurityInformation),
		0, 0,
		newAcl,
		0,
	)
	if ret != 0 {
		return fmt.Errorf("SetNamedSecurityInfo: %w", windows.Errno(ret))
	}
	return nil
}

func revokeAccessACL(dir string, sid *windows.SID) error {
	initWinProcs()
	dirPtr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}

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
		return fmt.Errorf("GetNamedSecurityInfo: %w", windows.Errno(ret))
	}
	defer winProcLocalFree.Call(sd)

	ea := explicitAccessW{
		AccessMode: revokeAccess,
		Trustee: trusteeW{
			TrusteeForm: trusteeIsSID,
			TrusteeType: trusteeIsUnknown,
			PtstrName:   uintptr(unsafe.Pointer(sid)),
		},
	}

	var newAcl uintptr
	ret, _, _ = winProcSetEntriesInAclW.Call(
		1,
		uintptr(unsafe.Pointer(&ea)),
		dacl,
		uintptr(unsafe.Pointer(&newAcl)),
	)
	if ret != 0 {
		return fmt.Errorf("SetEntriesInAcl: %w", windows.Errno(ret))
	}
	defer winProcLocalFree.Call(newAcl)

	ret, _, _ = winProcSetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(dirPtr)),
		uintptr(seFileObject),
		uintptr(daclSecurityInformation),
		0, 0,
		newAcl,
		0,
	)
	if ret != 0 {
		return fmt.Errorf("SetNamedSecurityInfo: %w", windows.Errno(ret))
	}
	return nil
}

func createRestrictedToken(capSid *windows.SID) (windows.Token, error) {
	var procToken windows.Token
	err := windows.OpenProcessToken(
		windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY|windows.TOKEN_ASSIGN_PRIMARY,
		&procToken,
	)
	if err != nil {
		return 0, fmt.Errorf("OpenProcessToken: %w", err)
	}
	defer procToken.Close()

	sidAttrs := [1]sidAndAttributes{
		{Sid: capSid, Attributes: 0},
	}

	var newToken windows.Token
	r1, _, e1 := winProcCreateRestrictedToken.Call(
		uintptr(procToken),
		uintptr(disableMaxPrivilege|luaToken|writeRestricted),
		0, 0,
		0, 0,
		1,
		uintptr(unsafe.Pointer(&sidAttrs[0])),
		uintptr(unsafe.Pointer(&newToken)),
	)
	if r1 == 0 {
		return 0, fmt.Errorf("CreateRestrictedToken: %w", e1)
	}
	return newToken, nil
}

// launchProcessWithRestrictedToken launches a process with Windows Restricted Token isolation.
// Unlike restrictedTokenLaunch which wraps commands via shell (for hostExec), this launches
// the command directly via CreateProcessAsUserW with the restricted token.
// The process can only write to paths where the capability SID has been granted access.
//
// Caller MUST call the returned cleanup func after cmd.Wait() to revoke ACLs and close
// the restricted token handle. The cleanup func is NOT called automatically.
func launchProcessWithRestrictedToken(ctx context.Context, command string, args []string, dir string, cfg *SandboxConfig) (*exec.Cmd, func(), error) {
	initWinProcs()

	capSid, err := generateCapabilitySID()
	if err != nil {
		return nil, nil, fmt.Errorf("generate capability SID: %w", err)
	}

	// Grant write access to allowed directories.
	writeDirs := buildWriteDirs(cfg)
	grantedDirs := make([]string, 0, len(writeDirs))
	for _, d := range writeDirs {
		if err := grantAccessACL(d, capSid); err != nil {
			slog.Warn("sandbox: failed to grant write ACL", "dir", d, "error", err)
		} else {
			grantedDirs = append(grantedDirs, d)
		}
	}

	// Deny write access to readonly paths.
	deniedDirs := make([]string, 0, len(cfg.ReadonlyPaths))
	for _, rp := range cfg.ReadonlyPaths {
		if err := denyWriteACL(rp, capSid); err != nil {
			slog.Warn("sandbox: failed to deny write ACL on readonly path", "path", rp, "error", err)
		} else {
			deniedDirs = append(deniedDirs, rp)
		}
	}

	rtoken, err := createRestrictedToken(capSid)
	if err != nil {
		// Best-effort cleanup granted ACLs before returning.
		for _, d := range grantedDirs {
			revokeAccessACL(d, capSid)
		}
		windows.FreeSid(capSid)
		return nil, nil, fmt.Errorf("create restricted token: %w", err)
	}

	cmd := exec.CommandContext(ctx, command, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Token: syscall.Token(rtoken),
	}

	cleanup := func() {
		for _, d := range grantedDirs {
			revokeAccessACL(d, capSid)
		}
		for _, d := range deniedDirs {
			revokeAccessACL(d, capSid)
		}
		rtoken.Close()
		windows.FreeSid(capSid)
	}
	return cmd, cleanup, nil
}
