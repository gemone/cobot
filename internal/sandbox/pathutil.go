package sandbox

import (
	"path/filepath"
	"runtime"
	"strings"
)

func EvalSymlinks(path string) string {
	realPath, err := filepath.EvalSymlinks(path)
	if err == nil {
		return realPath
	}

	dir := filepath.Dir(path)
	tail := filepath.Base(path)

	for len(dir) > 0 && dir != "/" && dir != "." {
		if runtime.GOOS == "windows" {
			vol := filepath.VolumeName(dir)
			if vol != "" && dir == vol+"\\" {
				if realDir, err := filepath.EvalSymlinks(dir); err == nil {
					return filepath.Join(realDir, tail)
				}
				break
			}
		}

		realDir, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return filepath.Join(realDir, tail)
		}
		tail = filepath.Base(dir) + string(filepath.Separator) + tail
		dir = filepath.Dir(dir)
	}

	return path
}

func IsSubpath(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
