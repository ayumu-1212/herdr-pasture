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
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return filepath.Join(home, ".config", "herdr-pasture")
}

// Load reads the TOML file at path. A missing file yields Default().
// Unparseable files yield Default() plus one warning. Out-of-range values are
// reset to their defaults and reported as warnings. Exclude patterns that
// are not valid globs are dropped and reported as warnings.
func Load(path string) (Config, []string) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, []string{fmt.Sprintf("config: %s: %v (using defaults)", path, err)}
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return Default(), []string{fmt.Sprintf("config: %s: %v (using defaults)", path, err)}
	}
	var warnings []string
	if !(cfg.WidthRatio >= 0.1 && cfg.WidthRatio <= 0.5) {
		warnings = append(warnings, fmt.Sprintf("config: width_ratio %v outside [0.1, 0.5], using 0.25", cfg.WidthRatio))
		cfg.WidthRatio = 0.25
	}
	if cfg.PollIntervalMs < 100 {
		warnings = append(warnings, fmt.Sprintf("config: poll_interval_ms %d below 100, using 1000", cfg.PollIntervalMs))
		cfg.PollIntervalMs = 1000
	}
	if len(cfg.Exclude) > 0 {
		valid := cfg.Exclude[:0]
		for _, p := range cfg.Exclude {
			if _, err := filepath.Match(expand(p), ""); err != nil {
				warnings = append(warnings, fmt.Sprintf("config: exclude pattern %q is invalid, ignoring", p))
				continue
			}
			valid = append(valid, p)
		}
		cfg.Exclude = valid
	}
	return cfg, warnings
}

// Excluded reports whether cwd matches any exclude pattern. Patterns expand
// ~ and $VARS. A trailing "/**" matches the directory and everything under it;
// anything else is a filepath.Match glob against the whole path.
func (c Config) Excluded(cwd string) bool {
	cwd = filepath.Clean(cwd)
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

// expand expands a leading "~" to the user's home directory and any $VAR
// references. If the home directory can't be determined, the pattern is
// returned unexpanded rather than silently producing a wrong path (e.g.
// "~/tmp/**" must never collapse to "/tmp/**" just because HOME is unset).
func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return os.ExpandEnv(p)
}
