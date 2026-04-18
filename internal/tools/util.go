package tools

import (
	"fmt"
	"strings"

	cobot "github.com/cobot-agent/cobot/pkg"
)

var decodeArgs = cobot.DecodeToolArgs

// validateName ensures a name does not contain path separators or parent references.
func validateName(name string) error {
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return fmt.Errorf("invalid name: must not contain path separators or parent directory references")
	}
	return nil
}
