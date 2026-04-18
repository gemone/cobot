//go:build windows

package sandbox

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxConfig_AutoResolvePath_WindowsAbsoluteDrivePath(t *testing.T) {
	vr := VirtualHome("ws")
	root := filepath.Join(`C:\tmp`, "real")
	s := &SandboxConfig{VirtualRoot: vr, Root: root}

	path, err := s.AutoResolvePath(`C:\Windows\System32\drivers\etc\hosts`)
	if err != nil {
		t.Fatal(err)
	}

	expected := filepath.Join(root, "Windows", "System32", "drivers", "etc", "hosts")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestSandboxConfig_AutoResolvePath_WindowsAbsoluteRootPath(t *testing.T) {
	vr := VirtualHome("ws")
	root := filepath.Join(`C:\tmp`, "real")
	s := &SandboxConfig{VirtualRoot: vr, Root: root}

	path, err := s.AutoResolvePath(`C:\`)
	if err != nil {
		t.Fatal(err)
	}

	if path != root {
		t.Errorf("expected %q, got %q", root, path)
	}
}

func TestSandboxConfig_AutoResolvePath_WindowsRealRootCaseInsensitive(t *testing.T) {
	vr := VirtualHome("ws")
	root := filepath.Join(`C:\tmp`, "real")
	s := &SandboxConfig{VirtualRoot: vr, Root: root}

	caseVariant := `c:\TMP\REAL\src\main.go`
	path, err := s.AutoResolvePath(caseVariant)
	if err != nil {
		t.Fatal(err)
	}

	expected := filepath.Join(root, "src", "main.go")
	if !strings.EqualFold(path, expected) {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestSandboxConfig_ResolvePath_WindowsVirtualRootCaseInsensitive(t *testing.T) {
	vr := VirtualHome("ws")
	root := filepath.Join(`C:\tmp`, "real")
	s := &SandboxConfig{VirtualRoot: vr, Root: root}

	path, err := s.ResolvePath(strings.ToLower(PathJoinVirtual(vr, "src/main.go")))
	if err != nil {
		t.Fatal(err)
	}

	expected := filepath.Join(root, "src", "main.go")
	if !strings.EqualFold(path, expected) {
		t.Errorf("expected %q, got %q", expected, path)
	}
}
