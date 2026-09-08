package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, warnings := Load(filepath.Join(t.TempDir(), "config.toml"))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("got %+v, want %+v", cfg, Default())
	}
}

func TestLoadPartialFileKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("poll_interval_ms = 2500\n"), 0o644)
	cfg, warnings := Load(path)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if !cfg.AutoOpen || cfg.WidthRatio != 0.25 || cfg.PollIntervalMs != 2500 {
		t.Fatalf("got %+v", cfg)
	}
}

func TestLoadResetsOutOfRangeValuesWithWarnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("auto_open = false\nwidth_ratio = 0.9\npoll_interval_ms = 10\n"), 0o644)
	cfg, warnings := Load(path)
	if cfg.AutoOpen {
		t.Fatal("auto_open should be false")
	}
	if cfg.WidthRatio != 0.25 || cfg.PollIntervalMs != 1000 {
		t.Fatalf("out-of-range values not reset: %+v", cfg)
	}
	if len(warnings) != 2 {
		t.Fatalf("want 2 warnings, got %v", warnings)
	}
}

func TestLoadInvalidTomlFallsBackToDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("width_ratio = [\n"), 0o644)
	cfg, warnings := Load(path)
	if !reflect.DeepEqual(cfg, Default()) || len(warnings) != 1 {
		t.Fatalf("got %+v warnings=%v", cfg, warnings)
	}
}

func TestExcluded(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	cfg := Config{Exclude: []string{"~/tmp/**", "/opt/scratch", "/srv/*-cache"}}
	cases := map[string]bool{
		"/home/u/tmp":      true,
		"/home/u/tmp/a/b":  true,
		"/home/u/tmpx":     false,
		"/opt/scratch":     true,
		"/opt/scratch/sub": false,
		"/srv/build-cache": true,
		"/home/u/work":     false,
	}
	for cwd, want := range cases {
		if got := cfg.Excluded(cwd); got != want {
			t.Errorf("Excluded(%q) = %v, want %v", cwd, got, want)
		}
	}
}

func TestDirPrefersPluginEnv(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "/x/cfg")
	if Dir() != "/x/cfg" {
		t.Fatalf("got %q", Dir())
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")
	t.Setenv("HOME", "/home/u")
	if Dir() != "/home/u/.config/herdr-pasture" {
		t.Fatalf("got %q", Dir())
	}
}
