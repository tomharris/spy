//go:build darwin

package auth

import (
	"bytes"
	"errors"
	"os/exec"
)

// macOS Chromium derives the cookie AES key from the Keychain "Safe Storage"
// password with 1003 PBKDF2 iterations, regardless of the v10/v11 prefix.
const darwinIterations = 1003

// cookieKey resolves a cookie version prefix to the (password, iterations)
// pair aesDecrypt needs. On macOS the password is always the Keychain
// "Slack Safe Storage" key; the prefix is not used.
func cookieKey(src *Source, _prefix string) ([]byte, int, error) {
	key, err := KeychainKey(src.IsAppStore)
	if err != nil {
		return nil, 0, err
	}
	return key, darwinIterations, nil
}

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
