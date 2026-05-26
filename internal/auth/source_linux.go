//go:build linux

package auth

import (
	"fmt"
	"path/filepath"
	"strings"
)

// discoverSlackDir probes the standard Linux Slack data directories for the
// three common packaging formats — native (.deb/.rpm), Snap, and Flatpak —
// and returns the first that exists. isAppStore is always false on Linux.
func discoverSlackDir(home string) (dir string, isAppStore bool, err error) {
	candidates := []string{
		filepath.Join(home, ".config", "Slack"),                                  // native package
		filepath.Join(home, "snap", "slack", "current", ".config", "Slack"),      // Snap
		filepath.Join(home, ".var", "app", "com.slack.Slack", "config", "Slack"), // Flatpak
	}
	for _, c := range candidates {
		if dirExists(c) {
			return c, false, nil
		}
	}
	return "", false, fmt.Errorf(
		"could not find Slack data directory.\nChecked:\n  %s\nIs Slack installed?",
		strings.Join(candidates, "\n  "))
}
