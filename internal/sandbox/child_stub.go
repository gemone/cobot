//go:build !linux

package sandbox

// HandleSandboxChildMode is a no-op on non-Linux platforms.
func HandleSandboxChildMode() bool {
	return false
}
