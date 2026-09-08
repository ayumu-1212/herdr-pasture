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
	if err := os.WriteFile(path, []byte("poll_interval_ms = 2500\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(path, []byte("auto_open = false\nwidth_ratio = 0.9\npoll_interval_ms = 10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(path, []byte("width_ratio = [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, warnings := Load(path)
	if !reflect.DeepEqual(cfg, Default()) || len(warnings) != 1 {
		t.Fatalf("got %+v warnings=%v", cfg, warnings)
	}
}

func TestLoadReadErrorNotEnoent(t *testing.T) {
	// Passing a directory makes os.ReadFile fail with something other than
	// os.ErrNotExist (e.g. EISDIR); Load must still fall back to defaults
	// with exactly one warning, not treat it like a missing file.
	dir := t.TempDir()
	cfg, warnings := Load(dir)
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("got %+v, want %+v", cfg, Default())
	}
	if len(warnings) != 1 {
		t.Fatalf("want 1 warning, got %v", warnings)
	}
}

func TestLoadDropsInvalidExcludePattern(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`exclude = ["/opt/["]`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, warnings := Load(path)
	if len(cfg.Exclude) != 0 {
		t.Fatalf("invalid pattern not dropped: %+v", cfg.Exclude)
	}
	if len(warnings) != 1 {
		t.Fatalf("want 1 warning, got %v", warnings)
	}
}

func TestExcluded(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("HERDR_X", "/x")
	cfg := Config{Exclude: []string{"~/tmp/**", "/opt/scratch", "/srv/*-cache", "$HERDR_X/**"}}
	cases := map[string]bool{
		"/home/u/tmp":      true,
		"/home/u/tmp/a/b":  true,
		"/home/u/tmpx":     false,
		"/opt/scratch":     true,
		"/opt/scratch/sub": false,
		"/srv/build-cache": true,
		"/home/u/work":     false,
		"/x/y":             true,
	}
	for cwd, want := range cases {
		if got := cfg.Excluded(cwd); got != want {
			t.Errorf("Excluded(%q) = %v, want %v", cwd, got, want)
		}
	}
}

func TestExcludedHomeUnset(t *testing.T) {
	t.Setenv("HOME", "")
	cfg := Config{Exclude: []string{"~/tmp/**"}}
	if cfg.Excluded("/tmp/x") {
		t.Fatal("expected false when HOME is unset")
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

func TestLoadWidthColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("width_columns = 30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, warnings := Load(path)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if cfg.WidthColumns != 30 {
		t.Fatalf("WidthColumns = %d", cfg.WidthColumns)
	}
}

func TestLoadNegativeWidthColumnsResetsWithAWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("width_columns = -5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, warnings := Load(path)
	if cfg.WidthColumns != 0 {
		t.Fatalf("WidthColumns = %d, want 0", cfg.WidthColumns)
	}
	if len(warnings) != 1 {
		t.Fatalf("want 1 warning, got %v", warnings)
	}
}

func TestDefaultWidthColumnsIsZero(t *testing.T) {
	if Default().WidthColumns != 0 {
		t.Fatalf("WidthColumns = %d, want 0 so width_ratio stays the default path", Default().WidthColumns)
	}
}
