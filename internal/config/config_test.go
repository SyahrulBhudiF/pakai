package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigPath_XDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/xdg/config")
	got := resolveConfigPath()
	want := filepath.Join("/custom/xdg/config", "pakai", "config.toml")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestConfigPath_Fallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	got := resolveConfigPath()
	if got == "" {
		t.Error("configPath returned empty string")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("configPath should be absolute, got %q", got)
	}
}

func TestConfig_AbsentFileReturnsDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/nonexistent/xdg")
	cfg := Config()
	if cfg.Daemon.Port != 7731 {
		t.Errorf("got Port=%d, want 7731", cfg.Daemon.Port)
	}
	if cfg.Daemon.PollInterval != 30 {
		t.Errorf("got PollInterval=%d, want 30", cfg.Daemon.PollInterval)
	}
}

func TestConfig_ReadsTOML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	configDir := filepath.Join(dir, "pakai")
	os.MkdirAll(configDir, 0755)

	toml := `
[daemon]
port = 9090
poll_interval = 60

[display]
separator = " // "

[thresholds]
warning = 60
critical = 90
`
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(toml), 0644)

	// Force re-read
	invalidateConfig()

	cfg := Config()
	if cfg.Daemon.Port != 9090 {
		t.Errorf("got Port=%d, want 9090", cfg.Daemon.Port)
	}
	if cfg.Daemon.PollInterval != 60 {
		t.Errorf("got PollInterval=%d, want 60", cfg.Daemon.PollInterval)
	}
	if cfg.Display.Separator != " // " {
		t.Errorf("got Separator=%q, want %q", cfg.Display.Separator, " // ")
	}
}

func TestConfig_ParseErrorReturnsLastGood(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	configDir := filepath.Join(dir, "pakai")
	os.MkdirAll(configDir, 0755)

	// First write valid config
	toml1 := `
[daemon]
port = 8080
`
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(toml1), 0644)
	invalidateConfig()
	cfg1 := Config()
	if cfg1.Daemon.Port != 8080 {
		t.Fatalf("setup: first read got Port=%d, want 8080", cfg1.Daemon.Port)
	}

	// Write invalid TOML
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("{{{ broken"), 0644)
	invalidateConfig()
	cfg2 := Config()
	if cfg2.Daemon.Port != 8080 {
		t.Errorf("got Port=%d after parse error, want last good 8080", cfg2.Daemon.Port)
	}
}

func TestConfig_ProviderConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	configDir := filepath.Join(dir, "pakai")
	os.MkdirAll(configDir, 0755)

	toml := `
[provider.opencode]
label = "OC"
limit = 10.0
`
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(toml), 0644)
	invalidateConfig()

	cfg := Config()
	if cfg.Provider["opencode"].Label != "OC" {
		t.Errorf("got Label=%q, want OC", cfg.Provider["opencode"].Label)
	}
	if cfg.Provider["opencode"].Limit == nil || *cfg.Provider["opencode"].Limit != 10.0 {
		t.Errorf("got Limit=%v, want 10.0", cfg.Provider["opencode"].Limit)
	}
}

func TestConfig_DefaultValues(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/nonexistent")
	cfg := Config()
	if cfg.Daemon.Port != 7731 {
		t.Errorf("Port default = %d, want 7731", cfg.Daemon.Port)
	}
	if cfg.Daemon.PollInterval != 30 {
		t.Errorf("PollInterval default = %d, want 30", cfg.Daemon.PollInterval)
	}
	if cfg.Thresholds.Warning != 50 {
		t.Errorf("Warning default = %d, want 50", cfg.Thresholds.Warning)
	}
	if cfg.Thresholds.Critical != 80 {
		t.Errorf("Critical default = %d, want 80", cfg.Thresholds.Critical)
	}
}

func TestGetDaemonPort(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	configDir := filepath.Join(dir, "pakai")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("[daemon]\nport = 9999\n"), 0644)
	invalidateConfig()

	got := GetDaemonPort()
	if got != 9999 {
		t.Errorf("got %d, want 9999", got)
	}
}

func TestGetSeparator(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	configDir := filepath.Join(dir, "pakai")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("[display]\nseparator = \" · \"\n"), 0644)
	invalidateConfig()

	got := GetSeparator()
	if got != " · " {
		t.Errorf("got %q, want %q", got, " · ")
	}
}

func TestSetKey_DynamicProviderLimit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	invalidateConfig()

	if err := SetKey("provider.claude.limit", "4500"); err != nil {
		t.Fatalf("SetKey returned error: %v", err)
	}

	invalidateConfig()
	if got := GetProviderLimit("claude"); got != 4500 {
		t.Fatalf("got %.2f, want 4500", got)
	}

	val, err := GetKey("provider.claude.limit")
	if err != nil {
		t.Fatalf("GetKey returned error: %v", err)
	}
	if val != "4500.00" {
		t.Fatalf("got %q, want %q", val, "4500.00")
	}
}

func TestIsProviderEnabled_SectionWithoutEnabled(t *testing.T) {
	tests := []struct {
		name string
		id   string
		toml string
		want bool
	}{
		{"no section", "inferhub", "", true},
		{"key only", "inferhub", "[provider.inferhub]\nkey = \"abc\"\n", true},
		{"label only", "openai", "[provider.openai]\nlabel = \"x\"\n", true},
		{"explicitly disabled", "inferhub", "[provider.inferhub]\nkey = \"abc\"\nenabled = false\n", false},
		{"explicitly enabled", "inferhub", "[provider.inferhub]\nenabled = true\n", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)

			configDir := filepath.Join(dir, "pakai")
			if err := os.MkdirAll(configDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(tt.toml), 0600); err != nil {
				t.Fatal(err)
			}

			if got := IsProviderEnabled(tt.id); got != tt.want {
				t.Fatalf("IsProviderEnabled(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}
