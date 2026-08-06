package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Addr  string `json:"addr,omitempty"`
	Token string `json:"token,omitempty"`
	Cloud bool   `json:"cloud,omitempty"`
}

func configPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		var err error
		if dir, err = os.UserConfigDir(); err != nil {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".config")
		}
	}
	return filepath.Join(dir, "busybar", "config.json")
}

func loadConfig() Config {
	var cfg Config
	data, err := os.ReadFile(configPath())
	if err != nil {
		if !os.IsNotExist(err) {
			note("warning: ignoring unreadable config %s: %v", configPath(), err)
		}
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		note("warning: ignoring malformed config %s: %v", configPath(), err)
		return Config{}
	}
	return cfg
}

func saveConfig(cfg Config) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// envBool reports the value of a truthy env var, and whether it was set at all.
func envBool(name string) (bool, bool) {
	v, ok := os.LookupEnv(name)
	if !ok {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, true
	default:
		return false, true
	}
}
