package auth

import (
	"bytes"
	"errors"
	"os/exec"
)

// KeychainKey fetches the "Slack Safe Storage" password from the macOS
// Keychain. Account names differ between the direct-download build and the
// Mac App Store build; we try the most likely one first.
//
// macOS will prompt for keychain access on first run.
func KeychainKey(isAppStore bool) ([]byte, error) {
	var accounts []string
	if isAppStore {
		accounts = []string{"Slack App Store Key", "Slack Key", "Slack"}
	} else {
		accounts = []string{"Slack Key", "Slack", "Slack App Store Key"}
	}

	var lastErr error
	for _, account := range accounts {
		cmd := exec.Command("security", "find-generic-password",
			"-s", "Slack Safe Storage",
			"-a", account,
			"-w")
		out, err := cmd.Output()
		if err == nil {
			return bytes.TrimSpace(out), nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("could not find Slack Safe Storage key in Keychain")
}
