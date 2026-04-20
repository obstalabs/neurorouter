package neurorouter

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds all neurorouter configuration.
// Precedence: CLI flag > NEUROROUTER_* env var > config file > default.
type Config struct {
	ListenPort              int     `toml:"listen_port"`
	Upstream                string  `toml:"upstream"`
	Verbosity               string  `toml:"verbosity"`
	ContextLimit            int     `toml:"context_limit"`
	ProtectPolicy           string  `toml:"protect_policy"`
	InputPricePerMillionUSD float64 `toml:"input_price_per_million_usd"`
	DNDPersistent           bool    `toml:"dnd_persistent"`
	StateDBPath             string  `toml:"state_db_path"`
	StateRetentionDays      int     `toml:"state_retention_days"`
}

// ConfigKey describes a config key for validation and documentation.
type ConfigKey struct {
	Name     string
	Type     string // "int", "string", "bool"
	Default  string
	Desc     string
	ValidSet []string // valid values for enum types
}

// ConfigKeys is the registry of all known config keys.
var ConfigKeys = []ConfigKey{
	{"listen_port", "int", "4000", "proxy listen port", nil},
	{"upstream", "string", "", "upstream LLM API URL", nil},
	{"verbosity", "string", "default", "alert verbosity level", []string{"silent", "minimal", "default", "verbose"}},
	{"context_limit", "int", "200000", "context window size in tokens", nil},
	{"protect_policy", "string", "warn", "secret detection policy", []string{"block", "redact", "warn"}},
	{"input_price_per_million_usd", "float", "3.0", "estimated input token price used for context-cost telemetry", nil},
	{"dnd_persistent", "bool", "false", "persist DND state across restarts", nil},
	{"state_db_path", "string", "~/.neurorouter/state.db", "SQLite state database path", nil},
	{"state_retention_days", "int", "90", "days to retain state data", nil},
}

// DefaultConfig returns a config with all defaults.
func DefaultConfig() *Config {
	return &Config{
		ListenPort:              4000,
		Upstream:                "",
		Verbosity:               "default",
		ContextLimit:            200000,
		ProtectPolicy:           "warn",
		InputPricePerMillionUSD: DefaultInputPricePerMillionUSD,
		DNDPersistent:           false,
		StateDBPath:             DefaultDBPath(),
		StateRetentionDays:      90,
	}
}

// DefaultConfigPath returns ~/.neurorouter/config.toml.
func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".neurorouter", "config.toml")
}

// LoadConfig loads config from file, then overlays env vars.
// Missing file is not an error — returns defaults.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()

	if path == "" {
		path = DefaultConfigPath()
	}

	// Load from file if exists.
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
	}

	// Overlay env vars (NEUROROUTER_ prefix).
	overlayEnv(cfg)

	return cfg, nil
}

func overlayEnv(cfg *Config) {
	if v := os.Getenv("NEUROROUTER_LISTEN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.ListenPort = n
		}
	}
	if v := os.Getenv("NEUROROUTER_UPSTREAM"); v != "" {
		cfg.Upstream = v
	}
	if v := os.Getenv("NEUROROUTER_VERBOSITY"); v != "" {
		cfg.Verbosity = v
	}
	if v := os.Getenv("NEUROROUTER_CONTEXT_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.ContextLimit = n
		}
	}
	if v := os.Getenv("NEUROROUTER_PROTECT_POLICY"); v != "" {
		cfg.ProtectPolicy = v
	}
	if v := os.Getenv("NEUROROUTER_INPUT_PRICE_PER_MILLION_USD"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.InputPricePerMillionUSD = n
		}
	}
	if v := os.Getenv("NEUROROUTER_STATE_DB_PATH"); v != "" {
		cfg.StateDBPath = v
	}
	if v := os.Getenv("NEUROROUTER_STATE_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.StateRetentionDays = n
		}
	}
}

// ValidateKey checks if a key name and value are valid.
func ValidateKey(key, value string) error {
	for _, k := range ConfigKeys {
		if k.Name == key {
			return validateValue(k, value)
		}
	}
	return fmt.Errorf("unknown config key %q", key)
}

func validateValue(k ConfigKey, value string) error {
	switch k.Type {
	case "int":
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Errorf("%s must be an integer", k.Name)
		}
	case "float":
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Errorf("%s must be a float", k.Name)
		}
	case "bool":
		if value != "true" && value != "false" {
			return fmt.Errorf("%s must be true or false", k.Name)
		}
	}
	if len(k.ValidSet) > 0 {
		valid := false
		for _, v := range k.ValidSet {
			if v == value {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("%s must be one of: %s", k.Name, strings.Join(k.ValidSet, ", "))
		}
	}
	return nil
}

// SetConfigValue writes a single key=value to the config file.
func SetConfigValue(path, key, value string) error {
	if err := ValidateKey(key, value); err != nil {
		return err
	}

	if path == "" {
		path = DefaultConfigPath()
	}

	// Load existing config.
	cfg, err := LoadConfig(path)
	if err != nil {
		cfg = DefaultConfig()
	}

	// Set value.
	switch key {
	case "listen_port":
		n, _ := strconv.Atoi(value)
		cfg.ListenPort = n
	case "upstream":
		cfg.Upstream = value
	case "verbosity":
		cfg.Verbosity = value
	case "context_limit":
		n, _ := strconv.Atoi(value)
		cfg.ContextLimit = n
	case "protect_policy":
		cfg.ProtectPolicy = value
	case "input_price_per_million_usd":
		n, _ := strconv.ParseFloat(value, 64)
		cfg.InputPricePerMillionUSD = n
	case "dnd_persistent":
		cfg.DNDPersistent = value == "true"
	case "state_db_path":
		cfg.StateDBPath = value
	case "state_retention_days":
		n, _ := strconv.Atoi(value)
		cfg.StateRetentionDays = n
	}

	return SaveConfig(path, cfg)
}

// SaveConfig writes config to file.
func SaveConfig(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}

	enc := toml.NewEncoder(f)
	if err := enc.Encode(cfg); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// InitDefaultConfig creates a config file with commented defaults.
func InitDefaultConfig(path string) error {
	if path == "" {
		path = DefaultConfigPath()
	}
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("# NeuroRouter configuration\n")
	b.WriteString("# Precedence: CLI flag > NEUROROUTER_* env var > this file > defaults\n\n")

	for _, k := range ConfigKeys {
		fmt.Fprintf(&b, "# %s (%s) — %s\n", k.Name, k.Type, k.Desc)
		if len(k.ValidSet) > 0 {
			fmt.Fprintf(&b, "# Valid: %s\n", strings.Join(k.ValidSet, ", "))
		}
		fmt.Fprintf(&b, "# %s = %s\n\n", k.Name, k.Default)
	}

	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// GetConfigValue returns the current effective value for a key.
func GetConfigValue(cfg *Config, key string) (string, error) {
	switch key {
	case "listen_port":
		return strconv.Itoa(cfg.ListenPort), nil
	case "upstream":
		return cfg.Upstream, nil
	case "verbosity":
		return cfg.Verbosity, nil
	case "context_limit":
		return strconv.Itoa(cfg.ContextLimit), nil
	case "protect_policy":
		return cfg.ProtectPolicy, nil
	case "input_price_per_million_usd":
		return strconv.FormatFloat(cfg.InputPricePerMillionUSD, 'f', -1, 64), nil
	case "dnd_persistent":
		if cfg.DNDPersistent {
			return "true", nil
		}
		return "false", nil
	case "state_db_path":
		return cfg.StateDBPath, nil
	case "state_retention_days":
		return strconv.Itoa(cfg.StateRetentionDays), nil
	default:
		return "", fmt.Errorf("unknown key %q", key)
	}
}
