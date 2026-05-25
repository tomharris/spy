package auth

import (
	"encoding/json"
	"errors"
	"os"
)

// Config is the persistent user-level config at ~/.local/spy/config.json.
type Config struct {
	// DefaultWorkspace is the team_id of the workspace to use when no
	// --workspace flag (or SPY_WORKSPACE env var) is supplied. Stored as
	// the team_id (not domain) so config survives a workspace rename.
	DefaultWorkspace string `json:"default_workspace,omitempty"`
}

func LoadConfig(src *Source) (*Config, error) {
	data, err := os.ReadFile(src.ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func SaveConfig(src *Source, cfg *Config) error {
	if err := os.MkdirAll(src.CacheDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(src.ConfigPath, data, 0o600)
}
