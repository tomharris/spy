package auth

import (
	"fmt"
	"os"
	"path/filepath"
)

// Source describes where Slack credentials live on disk and where we
// cache discovered workspaces.
type Source struct {
	SlackDir      string
	IsAppStore    bool
	CookiesDB     string
	LevelDBDir    string
	CacheDir      string
	ConfigPath    string
	WorkspacesDir string
}

// DefaultSource probes the two standard macOS Slack data directories
// (direct download and Mac App Store sandbox) and returns the first one
// that exists.
func DefaultSource() (*Source, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	direct := filepath.Join(home, "Library", "Application Support", "Slack")
	appstore := filepath.Join(home,
		"Library", "Containers", "com.tinyspeck.slackmacgap",
		"Data", "Library", "Application Support", "Slack")

	var (
		slackDir   string
		isAppStore bool
	)
	switch {
	case dirExists(direct):
		slackDir = direct
	case dirExists(appstore):
		slackDir = appstore
		isAppStore = true
	default:
		return nil, fmt.Errorf(
			"could not find Slack data directory.\nChecked:\n  %s\n  %s\nIs Slack installed?",
			direct, appstore)
	}

	cacheDir := filepath.Join(home, ".local", "spy")
	return &Source{
		SlackDir:      slackDir,
		IsAppStore:    isAppStore,
		CookiesDB:     filepath.Join(slackDir, "Cookies"),
		LevelDBDir:    filepath.Join(slackDir, "Local Storage", "leveldb"),
		CacheDir:      cacheDir,
		ConfigPath:    filepath.Join(cacheDir, "config.json"),
		WorkspacesDir: filepath.Join(cacheDir, "workspaces"),
	}, nil
}

// WorkspaceDir returns the directory holding a workspace's metadata file
// and per-workspace caches (users.json, channels.json, ...).
func (s *Source) WorkspaceDir(teamID string) string {
	return filepath.Join(s.WorkspacesDir, teamID)
}

// WorkspaceCachePath returns the path to a workspace's identity/token file.
func (s *Source) WorkspaceCachePath(teamID string) string {
	return filepath.Join(s.WorkspaceDir(teamID), "workspace.json")
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
