package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

func WriteConfigAtomic(cfg *AppConfig) error {
	path := resolveConfigPath()
	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	b, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".config.toml.tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write temp config: %w", err)
	}
	tmp.Close()

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename config: %w", err)
	}

	// The config can hold an API key, so keep it owner-only regardless of the
	// temp file's mode or the process umask.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("failed to set config permissions: %w", err)
	}

	return nil
}
