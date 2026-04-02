package neurorouter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ListenPort != 4000 {
		t.Errorf("expected port 4000, got %d", cfg.ListenPort)
	}
	if cfg.Verbosity != "default" {
		t.Errorf("expected default verbosity, got %s", cfg.Verbosity)
	}
	if cfg.ProtectPolicy != "warn" {
		t.Errorf("expected warn policy, got %s", cfg.ProtectPolicy)
	}
	if cfg.InputPricePerMillionUSD != DefaultInputPricePerMillionUSD {
		t.Errorf("expected default input price %.1f, got %.1f", DefaultInputPricePerMillionUSD, cfg.InputPricePerMillionUSD)
	}
	if cfg.StateRetentionDays != 90 {
		t.Errorf("expected 90 days retention, got %d", cfg.StateRetentionDays)
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	cfg, err := LoadConfig("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if cfg.ListenPort != 4000 {
		t.Error("should return defaults for missing file")
	}
}

func TestLoadConfig_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`
listen_port = 5000
verbosity = "verbose"
protect_policy = "block"
input_price_per_million_usd = 4.5
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ListenPort != 5000 {
		t.Errorf("expected 5000, got %d", cfg.ListenPort)
	}
	if cfg.Verbosity != "verbose" {
		t.Errorf("expected verbose, got %s", cfg.Verbosity)
	}
	if cfg.ProtectPolicy != "block" {
		t.Errorf("expected block, got %s", cfg.ProtectPolicy)
	}
	if cfg.InputPricePerMillionUSD != 4.5 {
		t.Errorf("expected 4.5, got %.1f", cfg.InputPricePerMillionUSD)
	}
}

func TestLoadConfig_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`listen_port = 5000`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("NEUROROUTER_LISTEN_PORT", "6000")
	t.Setenv("NEUROROUTER_INPUT_PRICE_PER_MILLION_USD", "5.5")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ListenPort != 6000 {
		t.Errorf("env should override file: expected 6000, got %d", cfg.ListenPort)
	}
	if cfg.InputPricePerMillionUSD != 5.5 {
		t.Errorf("env should override price: expected 5.5, got %.1f", cfg.InputPricePerMillionUSD)
	}
}

func TestValidateKey(t *testing.T) {
	tests := []struct {
		key   string
		value string
		ok    bool
	}{
		{"listen_port", "4000", true},
		{"listen_port", "abc", false},
		{"verbosity", "silent", true},
		{"verbosity", "invalid", false},
		{"protect_policy", "block", true},
		{"protect_policy", "yolo", false},
		{"input_price_per_million_usd", "3.5", true},
		{"input_price_per_million_usd", "abc", false},
		{"dnd_persistent", "true", true},
		{"dnd_persistent", "maybe", false},
		{"unknown_key", "value", false},
	}

	for _, tt := range tests {
		err := ValidateKey(tt.key, tt.value)
		if tt.ok && err != nil {
			t.Errorf("ValidateKey(%s, %s) should pass, got %v", tt.key, tt.value, err)
		}
		if !tt.ok && err == nil {
			t.Errorf("ValidateKey(%s, %s) should fail", tt.key, tt.value)
		}
	}
}

func TestSetConfigValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := SetConfigValue(path, "listen_port", "9000"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := SetConfigValue(path, "input_price_per_million_usd", "4.25"); err != nil {
		t.Fatalf("set price: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ListenPort != 9000 {
		t.Errorf("expected 9000, got %d", cfg.ListenPort)
	}
	if cfg.InputPricePerMillionUSD != 4.25 {
		t.Errorf("expected 4.25, got %.2f", cfg.InputPricePerMillionUSD)
	}
}

func TestSetConfigValue_Rejects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := SetConfigValue(path, "verbosity", "extreme"); err == nil {
		t.Error("should reject invalid verbosity")
	}
}

func TestGetConfigValue(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ListenPort = 8080
	cfg.InputPricePerMillionUSD = 6.25

	val, err := GetConfigValue(cfg, "listen_port")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if val != "8080" {
		t.Errorf("expected 8080, got %s", val)
	}
	price, err := GetConfigValue(cfg, "input_price_per_million_usd")
	if err != nil {
		t.Fatalf("get price: %v", err)
	}
	if price != "6.25" {
		t.Errorf("expected 6.25, got %s", price)
	}
}

func TestGetConfigValue_Unknown(t *testing.T) {
	cfg := DefaultConfig()
	_, err := GetConfigValue(cfg, "nonexistent")
	if err == nil {
		t.Error("should error on unknown key")
	}
}

func TestInitDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := InitDefaultConfig(path); err != nil {
		t.Fatalf("init: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)

	if len(content) == 0 {
		t.Error("config file should not be empty")
	}
	if !contains(content, "listen_port") {
		t.Error("should contain listen_port")
	}
	if !contains(content, "Precedence") {
		t.Error("should contain precedence note")
	}
}

func TestInitDefaultConfig_NoOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(path, []byte("custom content"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := InitDefaultConfig(path); err != nil {
		t.Fatalf("init default config: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "custom content" {
		t.Error("should not overwrite existing config")
	}
}
