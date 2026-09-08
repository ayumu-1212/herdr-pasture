// Package config loads the optional plugin configuration file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds user-tunable settings. Zero values are never used directly;
// call Default or Load.
type Config struct {
	AutoOpen       bool     `toml:"auto_open"`
	WidthRatio     float64  `toml:"width_ratio"`
	PollIntervalMs int      `toml:"poll_interval_ms"`
	Exclude        []string `toml:"exclude"`
}

// Default returns the built-in defaults.
func Default() Config {
	return Config{AutoOpen: true, WidthRatio: 0.25, PollIntervalMs: 1000}
}

// Dir returns the directory that holds config.toml: the herdr-managed plugin
// config dir when running under herdr, otherwise ~/.config/herdr-pasture.
func Dir() string {
	if d := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdr-pasture")
}

// Load reads the TOML file at path. A missing file yields Default().
// Unparseable files yield Default() plus one warning. Out-of-range values are
// reset to their defaults and reported as warnings.
func Load(path string) (Config, []string) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, []string{fmt.Sprintf("config: %v (using defaults)", err)}
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return Default(), []string{fmt.Sprintf("config: %v (using defaults)", err)}
	}
	var warnings []string
	if cfg.WidthRatio < 0.1 || cfg.WidthRatio > 0.5 {
		warnings = append(warnings, fmt.Sprintf("config: width_ratio %v outside [0.1, 0.5], using 0.25", cfg.WidthRatio))
		cfg.WidthRatio = 0.25
	}
	if cfg.PollIntervalMs < 100 {
		warnings = append(warnings, fmt.Sprintf("config: poll_interval_ms %d below 100, using 1000", cfg.PollIntervalMs))
		cfg.PollIntervalMs = 1000
	}
	return cfg, warnings
}

// Excluded reports whether cwd matches any exclude pattern. Patterns expand
// ~ and $VARS. A trailing "/**" matches the directory and everything under it;
// anything else is a filepath.Match glob against the whole path.
func (c Config) Excluded(cwd string) bool {
	for _, p := range c.Exclude {
		p = expand(p)
		if base, ok := strings.CutSuffix(p, "/**"); ok {
			if cwd == base || strings.HasPrefix(cwd, base+"/") {
				return true
			}
			continue
		}
		if ok, _ := filepath.Match(p, cwd); ok {
			return true
		}
	}
	return false
}

func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return os.ExpandEnv(p)
}
