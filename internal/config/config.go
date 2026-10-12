package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

type AppConfig struct {
	Daemon     DaemonConfig              `toml:"daemon"`
	Provider   map[string]ProviderConfig `toml:"provider"`
	Display    DisplayConfig             `toml:"display"`
	Thresholds ThresholdConfig           `toml:"thresholds"`
	Widget     WidgetConfig              `toml:"widget"`
}

type DaemonConfig struct {
	Port         int `toml:"port"`
	PollInterval int `toml:"poll_interval"`
}

type ProviderConfig struct {
	Enabled bool     `toml:"enabled"`
	Label   string   `toml:"label"`
	Limit   *float64 `toml:"limit"`
	Source  string   `toml:"source"`
}

type DisplayConfig struct {
	Separator string   `toml:"separator"`
	Order     []string `toml:"order"`
}

type ThresholdConfig struct {
	Warning  int `toml:"warning"`
	Critical int `toml:"critical"`
}

type WidgetConfig struct {
	Pinned string   `toml:"pinned" json:"pinned"`
	Hidden []string `toml:"hidden" json:"hidden"`
}

var (
	lastGoodConfig *AppConfig
	mu             sync.RWMutex
	invalidated    bool
	defaultConfig  = AppConfig{
		Daemon: DaemonConfig{
			Port:         7731,
			PollInterval: 30,
		},
		Display: DisplayConfig{
			Separator: " | ",
		},
		Thresholds: ThresholdConfig{
			Warning:  50,
			Critical: 80,
		},
	}
)

func init() {
	lastGoodConfig = &defaultConfig
}

func resolveConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			base = "~/.config"
		} else {
			base = filepath.Join(home, ".config")
		}
	}
	return filepath.Join(base, "pakai", "config.toml")
}

func invalidateConfig() {
	mu.Lock()
	invalidated = true
	mu.Unlock()
}

func Config() *AppConfig {
	mu.RLock()
	// Always return a fresh copy
	cfg := lastGoodConfig
	mu.RUnlock()

	// Re-read from disk if invalidated
	mu.Lock()
	defer mu.Unlock()

	if invalidated || true {
		invalidated = false
		newCfg, err := readConfig()
		if err != nil {
			slog.Error("config parse error", "path", resolveConfigPath(), "error", err)
		} else {
			lastGoodConfig = newCfg
			cfg = newCfg
		}
	}

	return cfg
}

func readConfig() (*AppConfig, error) {
	cfg := defaultConfig

	data, err := os.ReadFile(resolveConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &cfg, nil
		}
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return &cfg, nil
	}

	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config TOML: %w", err)
	}

	// Apply defaults for maps that are nil after unmarshal
	if cfg.Provider == nil {
		cfg.Provider = make(map[string]ProviderConfig)
	}

	// A provider section that omits `enabled` is enabled, matching a provider
	// that has no section at all. Without this, a section written to hold only a
	// key or label would read as Enabled: false and silently disable it.
	for id, p := range cfg.Provider {
		if !md.IsDefined("provider", id, "enabled") {
			p.Enabled = true
			cfg.Provider[id] = p
		}
	}

	return &cfg, nil
}

func GetDaemonPort() int {
	cfg := Config()
	return cfg.Daemon.Port
}

func GetProviderLabel(id string) string {
	cfg := Config()
	if p, ok := cfg.Provider[id]; ok && p.Label != "" {
		return p.Label
	}
	return id
}

func IsProviderEnabled(id string) bool {
	p, configured := Config().Provider[id]
	return !configured || p.Enabled
}

func GetProviderLimit(id string) float64 {
	cfg := Config()
	if p, ok := cfg.Provider[id]; ok && p.Limit != nil {
		return *p.Limit
	}
	return 0
}

func GetProviderSource(id string) string {
	if p, ok := Config().Provider[id]; ok {
		return p.Source
	}
	return ""
}

func GetSeparator() string {
	cfg := Config()
	return cfg.Display.Separator
}

func GetRefreshInterval() int {
	cfg := Config()
	return cfg.Daemon.PollInterval
}

func GetAlertThresholds() []float64 {
	cfg := Config()
	return []float64{
		float64(cfg.Thresholds.Warning),
		float64(cfg.Thresholds.Critical),
	}
}

func Init() error {
	cfg, err := readConfig()
	if err != nil {
		cfg = &defaultConfig
	}
	mu.Lock()
	lastGoodConfig = cfg
	mu.Unlock()
	return nil
}

func EnsureConfigDir() (string, error) {
	dir := filepath.Dir(resolveConfigPath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func EnsureCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cacheDir := filepath.Join(home, ".cache", "pakai")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", err
	}
	return cacheDir, nil
}

func splitCSV(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	parts = append(parts, strings.TrimSpace(s[start:]))
	return parts
}

var configKeySchema = map[string]struct {
	kind string
	min  float64
	max  float64
	set  func(cfg *AppConfig, val string) error
	get  func(cfg *AppConfig) string
}{
	"daemon.port": {
		kind: "int", min: 1, max: 65535,
		set: func(cfg *AppConfig, val string) error {
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("expected integer, got %q", val)
			}
			if n < 1 || n > 65535 {
				return fmt.Errorf("daemon.port must be between 1 and 65535")
			}
			cfg.Daemon.Port = n
			return nil
		},
		get: func(cfg *AppConfig) string { return strconv.Itoa(cfg.Daemon.Port) },
	},
	"daemon.poll_interval": {
		kind: "int", min: 1,
		set: func(cfg *AppConfig, val string) error {
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("expected integer, got %q", val)
			}
			if n < 1 {
				return fmt.Errorf("daemon.poll_interval must be >= 1")
			}
			cfg.Daemon.PollInterval = n
			return nil
		},
		get: func(cfg *AppConfig) string { return strconv.Itoa(cfg.Daemon.PollInterval) },
	},
	"display.separator": {
		kind: "string",
		set: func(cfg *AppConfig, val string) error {
			cfg.Display.Separator = val
			return nil
		},
		get: func(cfg *AppConfig) string { return cfg.Display.Separator },
	},
	"display.order": {
		kind: "string",
		set: func(cfg *AppConfig, val string) error {
			parts := strings.Split(val, ",")
			order := make([]string, 0, len(parts))
			for _, p := range parts {
				order = append(order, strings.TrimSpace(p))
			}
			cfg.Display.Order = order
			return nil
		},
		get: func(cfg *AppConfig) string { return strings.Join(cfg.Display.Order, ", ") },
	},
	"thresholds.warning": {
		kind: "int", min: 0, max: 100,
		set: func(cfg *AppConfig, val string) error {
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("expected integer, got %q", val)
			}
			if n < 0 || n > 100 {
				return fmt.Errorf("thresholds.warning must be between 0 and 100")
			}
			cfg.Thresholds.Warning = n
			return nil
		},
		get: func(cfg *AppConfig) string { return strconv.Itoa(cfg.Thresholds.Warning) },
	},
	"thresholds.critical": {
		kind: "int", min: 0, max: 100,
		set: func(cfg *AppConfig, val string) error {
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("expected integer, got %q", val)
			}
			if n < 0 || n > 100 {
				return fmt.Errorf("thresholds.critical must be between 0 and 100")
			}
			cfg.Thresholds.Critical = n
			return nil
		},
	},

	"widget.pinned": {
		kind: "string",
		set: func(cfg *AppConfig, val string) error {
			cfg.Widget.Pinned = val
			return nil
		},
		get: func(cfg *AppConfig) string { return cfg.Widget.Pinned },
	},
	"widget.hidden": {
		kind: "string",
		set: func(cfg *AppConfig, val string) error {
			if val == "" {
				cfg.Widget.Hidden = nil
				return nil
			}
			parts := strings.Split(val, ",")
			hidden := make([]string, 0, len(parts))
			for _, p := range parts {
				hidden = append(hidden, strings.TrimSpace(p))
			}
			cfg.Widget.Hidden = hidden
			return nil
		},
		get: func(cfg *AppConfig) string { return strings.Join(cfg.Widget.Hidden, ", ") },
	},
	"provider.claude.enabled": {
		kind: "bool",
		set: func(cfg *AppConfig, val string) error {
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("expected boolean, got %q", val)
			}
			if cfg.Provider == nil {
				cfg.Provider = make(map[string]ProviderConfig)
			}
			p := cfg.Provider["claude"]
			p.Enabled = b
			cfg.Provider["claude"] = p
			return nil
		},
		get: func(cfg *AppConfig) string {
			if p, ok := cfg.Provider["claude"]; ok {
				return strconv.FormatBool(p.Enabled)
			}
			return "true"
		},
	},
	"provider.claude.label": {
		kind: "string",
		set: func(cfg *AppConfig, val string) error {
			if cfg.Provider == nil {
				cfg.Provider = make(map[string]ProviderConfig)
			}
			p := cfg.Provider["claude"]
			p.Label = val
			cfg.Provider["claude"] = p
			return nil
		},
		get: func(cfg *AppConfig) string {
			if p, ok := cfg.Provider["claude"]; ok {
				return p.Label
			}
			return ""
		},
	},
	"provider.opencode.enabled": {
		kind: "bool",
		set: func(cfg *AppConfig, val string) error {
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("expected boolean, got %q", val)
			}
			if cfg.Provider == nil {
				cfg.Provider = make(map[string]ProviderConfig)
			}
			p := cfg.Provider["opencode"]
			p.Enabled = b
			cfg.Provider["opencode"] = p
			return nil
		},
		get: func(cfg *AppConfig) string {
			if p, ok := cfg.Provider["opencode"]; ok {
				return strconv.FormatBool(p.Enabled)
			}
			return "true"
		},
	},
	"provider.opencode.label": {
		kind: "string",
		set: func(cfg *AppConfig, val string) error {
			if cfg.Provider == nil {
				cfg.Provider = make(map[string]ProviderConfig)
			}
			p := cfg.Provider["opencode"]
			p.Label = val
			cfg.Provider["opencode"] = p
			return nil
		},
		get: func(cfg *AppConfig) string {
			if p, ok := cfg.Provider["opencode"]; ok {
				return p.Label
			}
			return ""
		},
	},
	"provider.opencode.limit": {
		kind: "float", min: 0,
		set: func(cfg *AppConfig, val string) error {
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return fmt.Errorf("expected float, got %q", val)
			}
			if f < 0 {
				return fmt.Errorf("provider.opencode.limit must be >= 0")
			}
			if cfg.Provider == nil {
				cfg.Provider = make(map[string]ProviderConfig)
			}
			p := cfg.Provider["opencode"]
			p.Limit = &f
			cfg.Provider["opencode"] = p
			return nil
		},
		get: func(cfg *AppConfig) string {
			if p, ok := cfg.Provider["opencode"]; ok && p.Limit != nil {
				return fmt.Sprintf("%.2f", *p.Limit)
			}
			return "0.00"
		},
	},
}

func SetKey(key, value string) error {
	schema, ok := configKeySchema[key]
	if !ok {
		return setDynamicProviderKey(key, value)
	}

	cfg, err := readConfig()
	if err != nil {
		cfg = &defaultConfig
	}

	if err := schema.set(cfg, value); err != nil {
		return err
	}

	if err := WriteConfigAtomic(cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

func GetKey(key string) (string, error) {
	schema, ok := configKeySchema[key]
	if !ok {
		return getDynamicProviderKey(key)
	}

	cfg := Config()
	return schema.get(cfg), nil
}

func setDynamicProviderKey(key, value string) error {
	id, field, ok := parseProviderKey(key)
	if !ok {
		return fmt.Errorf("unknown config key: %s", key)
	}

	cfg, err := readConfig()
	if err != nil {
		cfg = &defaultConfig
	}
	if cfg.Provider == nil {
		cfg.Provider = make(map[string]ProviderConfig)
	}

	p := cfg.Provider[id]
	p.Enabled = true
	switch field {
	case "enabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("expected boolean, got %q", value)
		}
		p.Enabled = b
	case "label":
		p.Label = value
	case "source":
		p.Source = value
	case "limit":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("expected float, got %q", value)
		}
		if f < 0 {
			return fmt.Errorf("provider.%s.limit must be >= 0", id)
		}
		p.Limit = &f
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}

	cfg.Provider[id] = p
	if err := WriteConfigAtomic(cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

func getDynamicProviderKey(key string) (string, error) {
	id, field, ok := parseProviderKey(key)
	if !ok {
		return "", fmt.Errorf("unknown config key: %s", key)
	}

	cfg := Config()
	p, exists := cfg.Provider[id]
	switch field {
	case "enabled":
		if exists {
			return strconv.FormatBool(p.Enabled), nil
		}
		return "true", nil
	case "label":
		if exists {
			return p.Label, nil
		}
		return "", nil
	case "source":
		if exists {
			return p.Source, nil
		}
		return "", nil
	case "limit":
		if exists && p.Limit != nil {
			return fmt.Sprintf("%.2f", *p.Limit), nil
		}
		return "0.00", nil
	default:
		return "", fmt.Errorf("unknown config key: %s", key)
	}
}

func parseProviderKey(key string) (id, field string, ok bool) {
	parts := strings.Split(key, ".")
	if len(parts) != 3 || parts[0] != "provider" {
		return "", "", false
	}
	if parts[1] == "" {
		return "", "", false
	}
	switch parts[2] {
	case "enabled", "label", "limit", "source":
		return parts[1], parts[2], true
	default:
		return "", "", false
	}
}

func SetWidgetPinned(providerID string) error {
	cfg, err := readConfig()
	if err != nil {
		cfgCopy := defaultConfig
		cfg = &cfgCopy
	}
	// Don't pin a hidden provider
	if providerID != "" {
		for _, id := range cfg.Widget.Hidden {
			if id == providerID {
				return fmt.Errorf("cannot pin hidden provider: %s", providerID)
			}
		}
	}
	cfg.Widget.Pinned = providerID
	if err := WriteConfigAtomic(cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	invalidateConfig()
	return nil
}

// SetWidgetHiddenList replaces the entire hidden providers list.
// If a currently pinned provider is in the new hidden list, it is unpinned.
func SetWidgetHiddenList(hidden []string) error {
	cfg, err := readConfig()
	if err != nil {
		cfgCopy := defaultConfig
		cfg = &cfgCopy
	}
	// If a previously pinned provider is now hidden, unpin it
	hiddenSet := make(map[string]bool, len(hidden))
	for _, id := range hidden {
		hiddenSet[id] = true
	}
	if cfg.Widget.Pinned != "" && hiddenSet[cfg.Widget.Pinned] {
		cfg.Widget.Pinned = ""
	}
	cfg.Widget.Hidden = hidden
	if err := WriteConfigAtomic(cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	invalidateConfig()
	return nil
}

func IsProviderHidden(providerID string) bool {
	cfg := Config()
	for _, id := range cfg.Widget.Hidden {
		if id == providerID {
			return true
		}
	}
	return false
}

func GetWidgetPinned() string {
	cfg := Config()
	return cfg.Widget.Pinned
}
