package auth

import (
	"os"
	"path/filepath"
)

// Source describes where Slack credentials live on disk and where we
// cache discovered workspaces.
type Source struct {
	SlackDir string
	// IsAppStore is set only on macOS (Mac App Store sandbox build); it
	// selects Keychain account-name order. Always false on Linux.
	IsAppStore    bool
	CookiesDB     string
	LevelDBDir    string
	CacheDir      string
	ConfigPath    string
	WorkspacesDir string
}

// DefaultSource locates the Slack desktop data directory for the current OS
// (see discoverSlackDir, implemented per-platform) and derives the cookie
// store, LevelDB, and spy cache paths from it. The Cookies and
// Local Storage/leveldb subpaths are identical across macOS and Linux; only
// the base directory probe is platform-specific.
func DefaultSource() (*Source, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	slackDir, isAppStore, err := discoverSlackDir(home)
	if err != nil {
		return nil, err
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
