//go:build darwin

package auth

import (
	"fmt"
	"path/filepath"
)

// discoverSlackDir probes the two standard macOS Slack data directories
// (direct download and Mac App Store sandbox) and returns the first that
// exists, along with whether it is the sandboxed App Store build.
func discoverSlackDir(home string) (dir string, isAppStore bool, err error) {
	direct := filepath.Join(home, "Library", "Application Support", "Slack")
	appstore := filepath.Join(home,
		"Library", "Containers", "com.tinyspeck.slackmacgap",
		"Data", "Library", "Application Support", "Slack")

	switch {
	case dirExists(direct):
		return direct, false, nil
	case dirExists(appstore):
		return appstore, true, nil
	default:
		return "", false, fmt.Errorf(
			"could not find Slack data directory.\nChecked:\n  %s\n  %s\nIs Slack installed?",
			direct, appstore)
	}
}
