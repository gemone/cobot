//go:build !windows

package sandbox

import "path/filepath"

func VirtualHome(name string) string {
	return filepath.Join("/home", name)
}

func VirtualSeparator() string {
	return `/`
}

func PathJoinVirtual(elem ...string) string {
	return filepath.Join(elem...)
}

func PathCleanVirtual(path string) string {
	return filepath.Clean(path)
}

func VirtualToNative(path string) string {
	return path
}
