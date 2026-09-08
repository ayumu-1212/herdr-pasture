# herdr-pasture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A herdr plugin that docks a pane on the left of every tab listing running agent panes grouped by git repository, with click-to-focus.

**Architecture:** One Go binary `bin/pasture` with four subcommands. `ui` is a bubbletea TUI that polls `herdr api snapshot`, groups agent panes via `internal/group`, and focuses a pane with `herdr agent focus`. `ensure` / `toggle` / `redeploy` (package `internal/dock`) are run by herdr event hooks and actions; they split the leftmost pane of a tab, swap the new pane to the left edge, and run `pasture ui` in it. All herdr CLI calls go through `internal/herdr.Client` so tests use a fake runner.

**Tech Stack:** Go 1.27 (brew), `github.com/charmbracelet/bubbletea v1.3.10`, `github.com/charmbracelet/lipgloss v1.1.0`, `github.com/BurntSushi/toml v1.6.0`, herdr 0.8.0 CLI.

**Spec:** `docs/superpowers/specs/2026-09-08-herdr-pasture-design.md`

---

## Verified herdr CLI facts (do not re-discover)

- `herdr api snapshot` → `{"result":{"snapshot":{"panes":[...],"workspaces":[...],"focused_pane_id":"w6:p1",...}}}`.
  Pane fields used: `pane_id`, `workspace_id`, `tab_id`, `cwd`, `agent` (string or null), `agent_status`,
  `terminal_title_stripped`, `focused`, `label` (only present after `pane rename`), `tokens` (map, only present after `report-metadata --token`).
- `herdr pane list` → `{"result":{"panes":[...]}}` with the same pane shape.
- `herdr pane layout --pane <id>` → `{"result":{"layout":{"area":{x,y,width,height},"panes":[{"pane_id","rect":{x,y,width,height},"focused"}],"tab_id","workspace_id"}}}`.
- `herdr pane split <id> --direction right --ratio 0.25 --no-focus [--cwd P] [--env K=V]` → `{"result":{"pane":{"pane_id":"w6:p2",...}}}`.
  `--ratio` is the share kept by the ORIGINAL pane; the new pane appears on the right.
- `herdr pane swap --source-pane <new> --target-pane <orig>` swaps positions (IDs unchanged).
- `herdr pane run <id> <command>` types the command + Enter into the pane's shell.
- `herdr pane rename <id> <label>` sets `label` (visible in `pane list`).
- `herdr pane report-metadata <id> --source pasture --token pasture=<value>` sets `tokens.pasture` (visible in `pane list`). Tokens do not survive a server restart; labels do.
- `herdr pane focus` only accepts `--direction`; to focus an arbitrary agent pane use `herdr agent focus <pane_id>`.
- `herdr pane close <id>`.
- Env injected into plugin commands: `HERDR_BIN_PATH`, `HERDR_PLUGIN_CONFIG_DIR`, `HERDR_PLUGIN_STATE_DIR`, `HERDR_TAB_ID`, `HERDR_PANE_ID`, `HERDR_WORKSPACE_ID`.
  Env injected into every managed pane: `HERDR_PANE_ID`, `HERDR_TAB_ID`, `HERDR_WORKSPACE_ID`, `HERDR_ENV=1`.

## File structure

```
go.mod                                  module github.com/ayumu-1212/herdr-pasture
herdr-plugin.toml                       plugin manifest
scripts/build.sh                        go build -o bin/pasture ./cmd/pasture
cmd/pasture/main.go                     subcommand dispatch: ui | ensure | toggle | redeploy | version
internal/config/config.go               Config, Default, Dir, Load, Excluded
internal/config/config_test.go
internal/snapshot/snapshot.go           JSON types + Decode / DecodePaneList / DecodeLayout / DecodeSplit
internal/snapshot/snapshot_test.go
internal/herdr/runner.go                Runner interface, ExecRunner
internal/herdr/client.go                Client: Snapshot, PaneList, Layout, FocusAgent, Split, Swap, RunInPane, Rename, Close, ReportToken
internal/herdr/client_test.go           FakeRunner-based tests
internal/group/resolver.go              RepoInfo, Resolver, GitResolver (cached)
internal/group/resolver_test.go
internal/group/group.go                 Row, Group, Options, Build (pure)
internal/group/group_test.go
internal/ui/model.go                    bubbletea Model (state, Update, lines, click mapping)
internal/ui/view.go                     rendering, styles, truncate
internal/ui/model_test.go
internal/dock/dock.go                   Ensure / Toggle / Redeploy, lock, snooze
internal/dock/dock_test.go              FakeClient-based tests
README.md
```

---

### Task 0: Toolchain, worktree, module skeleton

**Files:**
- Create: `go.mod`, `.gitignore`, `scripts/build.sh`

- [ ] **Step 1: Install Go**

Run: `brew install go`
Expected: `go version` prints `go version go1.27.x darwin/arm64` (or amd64).

- [ ] **Step 2: Create the working worktree**

Use the `superpowers:using-git-worktrees` skill. Branch name: `feat/pasture-v0`. All later tasks run inside that worktree.

- [ ] **Step 3: Init module and add pinned deps**

```bash
go mod init github.com/ayumu-1212/herdr-pasture
go get github.com/charmbracelet/bubbletea@v1.3.10
go get github.com/charmbracelet/lipgloss@v1.1.0
go get github.com/BurntSushi/toml@v1.6.0
```

- [ ] **Step 4: Write `.gitignore` and `scripts/build.sh`**

`.gitignore`:
```
bin/
```

`scripts/build.sh`:
```sh
#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
mkdir -p bin
go build -o bin/pasture ./cmd/pasture
```

Run: `chmod +x scripts/build.sh`

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum .gitignore scripts/build.sh
git commit -m "chore: init go module and build script"
```

---

### Task 1: config package

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, warnings := Load(filepath.Join(t.TempDir(), "config.toml"))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if cfg != Default() {
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
	if cfg != Default() || len(warnings) != 1 {
		t.Fatalf("got %+v warnings=%v", cfg, warnings)
	}
}

func TestExcluded(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	cfg := Config{Exclude: []string{"~/tmp/**", "/opt/scratch", "/srv/*-cache"}}
	cases := map[string]bool{
		"/home/u/tmp":            true,
		"/home/u/tmp/a/b":        true,
		"/home/u/tmpx":           false,
		"/opt/scratch":           true,
		"/opt/scratch/sub":       false,
		"/srv/build-cache":       true,
		"/home/u/work":           false,
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/`
Expected: FAIL (undefined: Load, Default, Config, Dir)

- [ ] **Step 3: Implement**

```go
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
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config/ -v`
Expected: all 6 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat(config): load config.toml with defaults, validation and exclude globs"
```

---

### Task 2: snapshot package

**Files:**
- Create: `internal/snapshot/snapshot.go`
- Test: `internal/snapshot/snapshot_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package snapshot

import "testing"

const snapshotJSON = `{"id":"cli:api:snapshot","result":{"snapshot":{
 "focused_pane_id":"w6:p1",
 "panes":[
  {"agent":"claude","agent_status":"working","cwd":"/r/a","focused":true,"pane_id":"w6:p1","tab_id":"w6:t1","terminal_title_stripped":"task one","workspace_id":"w6"},
  {"agent":null,"agent_status":"idle","cwd":"/r/a","focused":false,"pane_id":"w6:p2","tab_id":"w6:t1","terminal_title_stripped":"zsh","workspace_id":"w6","label":"pasture","tokens":{"pasture":"123"}}
 ],
 "workspaces":[{"workspace_id":"w6","number":4,"label":"a"}]
}}}`

func TestDecodeSnapshot(t *testing.T) {
	s, err := Decode([]byte(snapshotJSON))
	if err != nil {
		t.Fatal(err)
	}
	if s.FocusedPaneID != "w6:p1" || len(s.Panes) != 2 || len(s.Workspaces) != 1 {
		t.Fatalf("got %+v", s)
	}
	if s.Panes[0].Agent != "claude" || s.Panes[0].Title != "task one" {
		t.Fatalf("pane0 = %+v", s.Panes[0])
	}
	if s.Panes[1].Agent != "" {
		t.Fatalf("null agent should decode to empty string, got %q", s.Panes[1].Agent)
	}
	if s.Panes[1].Label != "pasture" || s.Panes[1].Tokens["pasture"] != "123" {
		t.Fatalf("pane1 = %+v", s.Panes[1])
	}
	if s.Workspaces[0].Number != 4 {
		t.Fatalf("workspace = %+v", s.Workspaces[0])
	}
}

func TestDecodePaneList(t *testing.T) {
	panes, err := DecodePaneList([]byte(`{"id":"cli:pane:list","result":{"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","cwd":"/x"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 1 || panes[0].PaneID != "w1:p1" {
		t.Fatalf("got %+v", panes)
	}
}

func TestDecodeLayout(t *testing.T) {
	l, err := DecodeLayout([]byte(`{"id":"cli:pane:layout","result":{"layout":{"area":{"height":96,"width":348,"x":26,"y":1},"focused_pane_id":"w5:p3","panes":[{"focused":false,"pane_id":"w5:p1","rect":{"height":96,"width":174,"x":26,"y":1}},{"focused":true,"pane_id":"w5:p3","rect":{"height":96,"width":174,"x":200,"y":1}}],"tab_id":"w5:t1","workspace_id":"w5"},"type":"pane_layout"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if l.TabID != "w5:t1" || len(l.Panes) != 2 || l.Panes[1].Rect.X != 200 || l.Area.Width != 348 {
		t.Fatalf("got %+v", l)
	}
}

func TestDecodeSplit(t *testing.T) {
	id, err := DecodeSplit([]byte(`{"id":"cli:pane:split","result":{"pane":{"pane_id":"w6:p9","tab_id":"w6:t1"}}}`))
	if err != nil || id != "w6:p9" {
		t.Fatalf("got %q, %v", id, err)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := Decode([]byte("not json")); err == nil {
		t.Fatal("expected error")
	}
	if _, err := DecodeSplit([]byte(`{"result":{}}`)); err == nil {
		t.Fatal("expected error for missing pane id")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/snapshot/`
Expected: FAIL (undefined: Decode ...)

- [ ] **Step 3: Implement**

```go
// Package snapshot defines the subset of herdr's JSON responses that pasture
// reads, plus decoders for each response envelope.
package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Pane is one entry of `api snapshot`'s panes or `pane list`.
type Pane struct {
	PaneID      string            `json:"pane_id"`
	WorkspaceID string            `json:"workspace_id"`
	TabID       string            `json:"tab_id"`
	Cwd         string            `json:"cwd"`
	Agent       string            `json:"agent"` // "" when herdr reports null
	AgentStatus string            `json:"agent_status"`
	Title       string            `json:"terminal_title_stripped"`
	Label       string            `json:"label"` // set by `pane rename`
	Focused     bool              `json:"focused"`
	Tokens      map[string]string `json:"tokens"` // set by `pane report-metadata --token`
}

// Workspace is one entry of `api snapshot`'s workspaces.
type Workspace struct {
	WorkspaceID string `json:"workspace_id"`
	Number      int    `json:"number"`
	Label       string `json:"label"`
}

// Snapshot is the live session state returned by `herdr api snapshot`.
type Snapshot struct {
	Panes         []Pane      `json:"panes"`
	Workspaces    []Workspace `json:"workspaces"`
	FocusedPaneID string      `json:"focused_pane_id"`
}

// Rect is a pane rectangle in terminal cells.
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// LayoutPane is one pane of `pane layout`.
type LayoutPane struct {
	PaneID  string `json:"pane_id"`
	Focused bool   `json:"focused"`
	Rect    Rect   `json:"rect"`
}

// Layout is the geometry of one tab returned by `herdr pane layout`.
type Layout struct {
	TabID       string       `json:"tab_id"`
	WorkspaceID string       `json:"workspace_id"`
	Area        Rect         `json:"area"`
	Panes       []LayoutPane `json:"panes"`
}

// Decode parses the `herdr api snapshot` envelope.
func Decode(data []byte) (Snapshot, error) {
	var env struct {
		Result struct {
			Snapshot Snapshot `json:"snapshot"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return Snapshot{}, fmt.Errorf("decode snapshot: %w", err)
	}
	return env.Result.Snapshot, nil
}

// DecodePaneList parses the `herdr pane list` envelope.
func DecodePaneList(data []byte) ([]Pane, error) {
	var env struct {
		Result struct {
			Panes []Pane `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("decode pane list: %w", err)
	}
	return env.Result.Panes, nil
}

// DecodeLayout parses the `herdr pane layout` envelope.
func DecodeLayout(data []byte) (Layout, error) {
	var env struct {
		Result struct {
			Layout Layout `json:"layout"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return Layout{}, fmt.Errorf("decode layout: %w", err)
	}
	return env.Result.Layout, nil
}

// DecodeSplit parses the `herdr pane split` envelope and returns the new pane id.
func DecodeSplit(data []byte) (string, error) {
	var env struct {
		Result struct {
			Pane struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return "", fmt.Errorf("decode split: %w", err)
	}
	if env.Result.Pane.PaneID == "" {
		return "", errors.New("decode split: response has no pane_id")
	}
	return env.Result.Pane.PaneID, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/snapshot/ -v`
Expected: 5 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/snapshot
git commit -m "feat(snapshot): decode herdr snapshot, pane list, layout and split responses"
```

---

### Task 3: herdr CLI client

**Files:**
- Create: `internal/herdr/runner.go`, `internal/herdr/client.go`
- Test: `internal/herdr/client_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package herdr

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// FakeRunner records argv and replies from a queue of canned outputs.
type FakeRunner struct {
	Calls   [][]string
	Outputs []string
	Err     error
}

func (f *FakeRunner) Run(args ...string) ([]byte, error) {
	f.Calls = append(f.Calls, args)
	if f.Err != nil {
		return nil, f.Err
	}
	if len(f.Outputs) == 0 {
		return []byte("{}"), nil
	}
	out := f.Outputs[0]
	f.Outputs = f.Outputs[1:]
	return []byte(out), nil
}

func TestSnapshotDecodes(t *testing.T) {
	r := &FakeRunner{Outputs: []string{`{"result":{"snapshot":{"focused_pane_id":"w1:p1","panes":[]}}}`}}
	s, err := New(r).Snapshot()
	if err != nil || s.FocusedPaneID != "w1:p1" {
		t.Fatalf("got %+v, %v", s, err)
	}
	if !reflect.DeepEqual(r.Calls[0], []string{"api", "snapshot"}) {
		t.Fatalf("argv = %v", r.Calls[0])
	}
}

func TestSnapshotPropagatesRunnerError(t *testing.T) {
	r := &FakeRunner{Err: errors.New("socket down")}
	if _, err := New(r).Snapshot(); err == nil {
		t.Fatal("expected error")
	}
}

func TestSplitBuildsArgvAndReturnsNewPane(t *testing.T) {
	r := &FakeRunner{Outputs: []string{`{"result":{"pane":{"pane_id":"w1:p7"}}}`}}
	id, err := New(r).Split("w1:p1", 0.25, "/work", map[string]string{"A": "1"})
	if err != nil || id != "w1:p7" {
		t.Fatalf("got %q, %v", id, err)
	}
	want := []string{"pane", "split", "w1:p1", "--direction", "right", "--ratio", "0.25", "--no-focus", "--cwd", "/work", "--env", "A=1"}
	if !reflect.DeepEqual(r.Calls[0], want) {
		t.Fatalf("argv = %v", r.Calls[0])
	}
}

func TestSplitOmitsEmptyCwd(t *testing.T) {
	r := &FakeRunner{Outputs: []string{`{"result":{"pane":{"pane_id":"w1:p7"}}}`}}
	New(r).Split("w1:p1", 0.3, "", nil)
	if strings.Contains(strings.Join(r.Calls[0], " "), "--cwd") {
		t.Fatalf("argv should not contain --cwd: %v", r.Calls[0])
	}
}

func TestSimpleCommandsArgv(t *testing.T) {
	r := &FakeRunner{}
	c := New(r)
	c.FocusAgent("w2:p3")
	c.Swap("w1:p7", "w1:p1")
	c.RunInPane("w1:p7", "/bin/pasture ui")
	c.Rename("w1:p7", "pasture")
	c.Close("w1:p7")
	c.ReportToken("w1:p7", "pasture", "4242")
	c.Layout("w1:p1")
	c.PaneList()
	want := [][]string{
		{"agent", "focus", "w2:p3"},
		{"pane", "swap", "--source-pane", "w1:p7", "--target-pane", "w1:p1"},
		{"pane", "run", "w1:p7", "/bin/pasture ui"},
		{"pane", "rename", "w1:p7", "pasture"},
		{"pane", "close", "w1:p7"},
		{"pane", "report-metadata", "w1:p7", "--source", "pasture", "--token", "pasture=4242"},
		{"pane", "layout", "--pane", "w1:p1"},
		{"pane", "list"},
	}
	if !reflect.DeepEqual(r.Calls, want) {
		t.Fatalf("argv:\n got %v\nwant %v", r.Calls, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/herdr/`
Expected: FAIL (undefined: New)

- [ ] **Step 3: Implement runner.go**

```go
// Package herdr wraps the herdr CLI so callers never build argv themselves.
package herdr

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes one herdr CLI invocation and returns its stdout.
type Runner interface {
	Run(args ...string) ([]byte, error)
}

// ExecRunner runs the real herdr binary.
type ExecRunner struct {
	Bin string
}

// NewExecRunner uses HERDR_BIN_PATH when set, else "herdr" from PATH.
func NewExecRunner() ExecRunner {
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	return ExecRunner{Bin: bin}
}

// Run executes herdr with args. On a non-zero exit the error includes stderr,
// which is where herdr prints its JSON error.
func (r ExecRunner) Run(args ...string) ([]byte, error) {
	cmd := exec.Command(r.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
```

- [ ] **Step 4: Implement client.go**

```go
package herdr

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

// Client exposes the handful of herdr commands pasture needs.
type Client struct {
	r Runner
}

// New wraps a Runner.
func New(r Runner) *Client {
	return &Client{r: r}
}

// Snapshot runs `herdr api snapshot`.
func (c *Client) Snapshot() (snapshot.Snapshot, error) {
	out, err := c.r.Run("api", "snapshot")
	if err != nil {
		return snapshot.Snapshot{}, err
	}
	return snapshot.Decode(out)
}

// PaneList runs `herdr pane list` across all workspaces.
func (c *Client) PaneList() ([]snapshot.Pane, error) {
	out, err := c.r.Run("pane", "list")
	if err != nil {
		return nil, err
	}
	return snapshot.DecodePaneList(out)
}

// Layout runs `herdr pane layout --pane <id>` for the tab containing paneID.
func (c *Client) Layout(paneID string) (snapshot.Layout, error) {
	out, err := c.r.Run("pane", "layout", "--pane", paneID)
	if err != nil {
		return snapshot.Layout{}, err
	}
	return snapshot.DecodeLayout(out)
}

// FocusAgent focuses the agent pane paneID, switching workspace and tab as needed.
func (c *Client) FocusAgent(paneID string) error {
	_, err := c.r.Run("agent", "focus", paneID)
	return err
}

// Split splits paneID to the right without focusing the new pane. ratio is the
// share kept by paneID. env is passed as --env KEY=VALUE in sorted key order.
// It returns the new pane id.
func (c *Client) Split(paneID string, ratio float64, cwd string, env map[string]string) (string, error) {
	args := []string{"pane", "split", paneID, "--direction", "right",
		"--ratio", strconv.FormatFloat(ratio, 'f', 2, 64), "--no-focus"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--env", k+"="+env[k])
	}
	out, err := c.r.Run(args...)
	if err != nil {
		return "", err
	}
	return snapshot.DecodeSplit(out)
}

// Swap exchanges the positions of two panes.
func (c *Client) Swap(source, target string) error {
	_, err := c.r.Run("pane", "swap", "--source-pane", source, "--target-pane", target)
	return err
}

// RunInPane types command + Enter into paneID's shell.
func (c *Client) RunInPane(paneID, command string) error {
	_, err := c.r.Run("pane", "run", paneID, command)
	return err
}

// Rename sets the pane label shown in herdr's UI and `pane list`.
func (c *Client) Rename(paneID, label string) error {
	_, err := c.r.Run("pane", "rename", paneID, label)
	return err
}

// Close closes a pane.
func (c *Client) Close(paneID string) error {
	_, err := c.r.Run("pane", "close", paneID)
	return err
}

// ReportToken stamps a display-metadata token on paneID under source "pasture".
func (c *Client) ReportToken(paneID, name, value string) error {
	_, err := c.r.Run("pane", "report-metadata", paneID, "--source", "pasture",
		"--token", fmt.Sprintf("%s=%s", name, value))
	return err
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/herdr/ -v`
Expected: 5 tests PASS

- [ ] **Step 6: Commit**

```bash
git add internal/herdr
git commit -m "feat(herdr): CLI client with exec runner and argv builders"
```

---

### Task 4: group package — resolver

**Files:**
- Create: `internal/group/resolver.go`
- Test: `internal/group/resolver_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package group

import (
	"errors"
	"testing"
)

// fakeGit answers `git -C dir <args>` from a table keyed by dir + " " + joined args.
type fakeGit struct {
	answers map[string]string
	calls   int
}

func (f *fakeGit) run(dir string, args ...string) (string, error) {
	f.calls++
	key := dir + " " + joinArgs(args)
	if out, ok := f.answers[key]; ok {
		return out, nil
	}
	return "", errors.New("fatal: not a git repository")
}

func joinArgs(args []string) string {
	s := ""
	for i, a := range args {
		if i > 0 {
			s += " "
		}
		s += a
	}
	return s
}

func TestResolveMainWorktree(t *testing.T) {
	g := &fakeGit{answers: map[string]string{
		"/repo/sub rev-parse --show-toplevel":                        "/repo",
		"/repo/sub rev-parse --path-format=absolute --git-common-dir": "/repo/.git",
	}}
	r := newGitResolver(g.run)
	got := r.Resolve("/repo/sub")
	want := RepoInfo{Root: "/repo"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestResolveLinkedWorktree(t *testing.T) {
	g := &fakeGit{answers: map[string]string{
		"/repo/.worktrees/feat rev-parse --show-toplevel":                        "/repo/.worktrees/feat",
		"/repo/.worktrees/feat rev-parse --path-format=absolute --git-common-dir": "/repo/.git",
		"/repo/.worktrees/feat rev-parse --abbrev-ref HEAD":                      "feat/x",
	}}
	got := newGitResolver(g.run).Resolve("/repo/.worktrees/feat")
	want := RepoInfo{Root: "/repo", Branch: "feat/x", IsWorktree: true}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestResolveNonGitReturnsEmpty(t *testing.T) {
	got := newGitResolver((&fakeGit{answers: map[string]string{}}).run).Resolve("/tmp/x")
	if got != (RepoInfo{}) {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveCachesPerCwd(t *testing.T) {
	g := &fakeGit{answers: map[string]string{
		"/repo rev-parse --show-toplevel":                        "/repo",
		"/repo rev-parse --path-format=absolute --git-common-dir": "/repo/.git",
	}}
	r := newGitResolver(g.run)
	r.Resolve("/repo")
	r.Resolve("/repo")
	if g.calls != 2 {
		t.Fatalf("expected 2 git calls total (cached second Resolve), got %d", g.calls)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/group/ -run Resolve`
Expected: FAIL (undefined: newGitResolver, RepoInfo)

- [ ] **Step 3: Implement**

```go
// Package group turns a herdr snapshot into repository groups of agent rows.
package group

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
)

// RepoInfo describes where a cwd belongs. Root is the grouping key: the main
// repository path for worktrees, the toplevel for ordinary checkouts, and ""
// when cwd is not inside a git repository.
type RepoInfo struct {
	Root       string
	Branch     string
	IsWorktree bool
}

// Resolver maps a working directory to its RepoInfo.
type Resolver interface {
	Resolve(cwd string) RepoInfo
}

type gitRunner func(dir string, args ...string) (string, error)

// GitResolver shells out to git and caches results per cwd for the process
// lifetime. It is not safe for concurrent use.
type GitResolver struct {
	run   gitRunner
	cache map[string]RepoInfo
}

// NewGitResolver returns a resolver backed by the git binary on PATH.
func NewGitResolver() *GitResolver {
	return newGitResolver(execGit)
}

func newGitResolver(run gitRunner) *GitResolver {
	return &GitResolver{run: run, cache: map[string]RepoInfo{}}
}

// Resolve implements Resolver.
func (g *GitResolver) Resolve(cwd string) RepoInfo {
	if info, ok := g.cache[cwd]; ok {
		return info
	}
	info := g.resolve(cwd)
	g.cache[cwd] = info
	return info
}

func (g *GitResolver) resolve(cwd string) RepoInfo {
	top, err := g.run(cwd, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		return RepoInfo{}
	}
	common, err := g.run(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || common == "" {
		return RepoInfo{Root: top}
	}
	mainRoot := filepath.Dir(filepath.Clean(common))
	if mainRoot == filepath.Clean(top) {
		return RepoInfo{Root: top}
	}
	branch, _ := g.run(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	return RepoInfo{Root: mainRoot, Branch: branch, IsWorktree: true}
}

func execGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/group/ -run Resolve -v`
Expected: 4 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/group/resolver.go internal/group/resolver_test.go
git commit -m "feat(group): git resolver that folds worktrees into their main repo"
```

---

### Task 5: group package — Build

**Files:**
- Create: `internal/group/group.go`
- Test: `internal/group/group_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package group

import (
	"reflect"
	"testing"

	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

type mapResolver map[string]RepoInfo

func (m mapResolver) Resolve(cwd string) RepoInfo { return m[cwd] }

func pane(id, ws, cwd, agent, status, title string) snapshot.Pane {
	return snapshot.Pane{PaneID: id, WorkspaceID: ws, TabID: ws + ":t1", Cwd: cwd, Agent: agent, AgentStatus: status, Title: title}
}

func labelsOf(gs []Group) []string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = g.Label
	}
	return out
}

func TestBuildSkipsNonAgentSelfAndExcluded(t *testing.T) {
	s := snapshot.Snapshot{Panes: []snapshot.Pane{
		pane("w1:p1", "w1", "/r/a", "claude", "idle", "a"),
		pane("w1:p2", "w1", "/r/a", "", "idle", "zsh"),
		{PaneID: "w1:p3", WorkspaceID: "w1", Cwd: "/r/a", Agent: "claude", Tokens: map[string]string{"pasture": "1"}},
		pane("w1:p4", "w1", "/tmp/x", "claude", "idle", "scratch"),
	}}
	gs := Build(s, mapResolver{"/r/a": {Root: "/r/a"}}, Options{
		SelfToken: "pasture",
		Exclude:   func(cwd string) bool { return cwd == "/tmp/x" },
	})
	if len(gs) != 1 || len(gs[0].Rows) != 1 || gs[0].Rows[0].PaneID != "w1:p1" {
		t.Fatalf("got %+v", gs)
	}
}

func TestBuildGroupsWorktreesUnderMainRepoWithBranch(t *testing.T) {
	s := snapshot.Snapshot{Panes: []snapshot.Pane{
		pane("w1:p1", "w1", "/r/a", "claude", "idle", "main work"),
		pane("w2:p1", "w2", "/r/a/.worktrees/feat", "codex", "working", "feature"),
	}}
	gs := Build(s, mapResolver{
		"/r/a":                 {Root: "/r/a"},
		"/r/a/.worktrees/feat": {Root: "/r/a", Branch: "feat/x", IsWorktree: true},
	}, Options{})
	if len(gs) != 1 || gs[0].Key != "/r/a" || gs[0].Label != "a" {
		t.Fatalf("got %+v", gs)
	}
	if gs[0].Rows[1].Branch != "feat/x" || gs[0].Rows[0].Branch != "" {
		t.Fatalf("branches: %+v", gs[0].Rows)
	}
}

func TestBuildFallsBackToCwdWhenNotGit(t *testing.T) {
	s := snapshot.Snapshot{Panes: []snapshot.Pane{pane("w1:p1", "w1", "/home/u/notes", "claude", "idle", "n")}}
	gs := Build(s, mapResolver{}, Options{})
	if len(gs) != 1 || gs[0].Key != "/home/u/notes" || gs[0].Label != "notes" {
		t.Fatalf("got %+v", gs)
	}
}

func TestBuildDisambiguatesDuplicateLabels(t *testing.T) {
	s := snapshot.Snapshot{Panes: []snapshot.Pane{
		pane("w1:p1", "w1", "/x/hp", "claude", "idle", "1"),
		pane("w2:p1", "w2", "/y/hp", "claude", "idle", "2"),
		pane("w3:p1", "w3", "/z/other", "claude", "idle", "3"),
	}}
	gs := Build(s, mapResolver{"/x/hp": {Root: "/x/hp"}, "/y/hp": {Root: "/y/hp"}, "/z/other": {Root: "/z/other"}}, Options{})
	want := []string{"other", "x/hp", "y/hp"}
	if got := labelsOf(gs); !reflect.DeepEqual(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
}

func TestBuildOrdersRowsByWorkspaceNumberThenPane(t *testing.T) {
	s := snapshot.Snapshot{
		Workspaces: []snapshot.Workspace{{WorkspaceID: "w9", Number: 1}, {WorkspaceID: "w2", Number: 2}},
		Panes: []snapshot.Pane{
			pane("w2:p10", "w2", "/r", "claude", "idle", "c"),
			pane("w2:p3", "w2", "/r", "claude", "idle", "b"),
			pane("w9:p1", "w9", "/r", "claude", "working", "a"),
		},
	}
	gs := Build(s, mapResolver{"/r": {Root: "/r"}}, Options{})
	got := []string{gs[0].Rows[0].PaneID, gs[0].Rows[1].PaneID, gs[0].Rows[2].PaneID}
	want := []string{"w9:p1", "w2:p3", "w2:p10"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestBuildTitleFallbackAndFocus(t *testing.T) {
	s := snapshot.Snapshot{
		FocusedPaneID: "w1:p2",
		Panes: []snapshot.Pane{
			pane("w1:p1", "w1", "/r", "claude", "idle", ""),
			pane("w1:p2", "w1", "/r", "codex", "blocked", "asking"),
		},
	}
	gs := Build(s, mapResolver{"/r": {Root: "/r"}}, Options{})
	rows := gs[0].Rows
	if rows[0].Title != "claude w1:p1" {
		t.Fatalf("fallback title = %q", rows[0].Title)
	}
	if rows[0].Focused || !rows[1].Focused {
		t.Fatalf("focus flags: %+v", rows)
	}
	if rows[1].Status != "blocked" || rows[1].Agent != "codex" {
		t.Fatalf("row = %+v", rows[1])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/group/ -run Build`
Expected: FAIL (undefined: Build, Options, Group)

- [ ] **Step 3: Implement**

```go
package group

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

// Row is one agent pane as displayed in the list.
type Row struct {
	PaneID          string
	WorkspaceID     string
	WorkspaceNumber int
	Agent           string
	Status          string
	Title           string
	Branch          string // non-empty only for linked worktrees
	Focused         bool
}

// Group is one repository (or bare directory) and its rows.
type Group struct {
	Key   string // absolute path used for grouping
	Label string // display name, unique across groups
	Rows  []Row
}

// Options tunes Build.
type Options struct {
	// SelfToken is the pane token that marks pasture's own panes ("pasture").
	SelfToken string
	// Exclude reports whether a cwd should be hidden. nil excludes nothing.
	Exclude func(cwd string) bool
}

// Build groups the snapshot's agent panes by repository. The output is fully
// sorted so repeated calls with the same input give the same order.
func Build(s snapshot.Snapshot, r Resolver, opt Options) []Group {
	wsNumber := map[string]int{}
	for _, w := range s.Workspaces {
		wsNumber[w.WorkspaceID] = w.Number
	}
	byKey := map[string]*Group{}
	for _, p := range s.Panes {
		if p.Agent == "" {
			continue
		}
		if opt.SelfToken != "" && p.Tokens[opt.SelfToken] != "" {
			continue
		}
		if opt.Exclude != nil && opt.Exclude(p.Cwd) {
			continue
		}
		info := r.Resolve(p.Cwd)
		key := info.Root
		if key == "" {
			key = p.Cwd
		}
		g, ok := byKey[key]
		if !ok {
			g = &Group{Key: key}
			byKey[key] = g
		}
		title := p.Title
		if title == "" {
			title = p.Agent + " " + p.PaneID
		}
		branch := ""
		if info.IsWorktree {
			branch = info.Branch
		}
		g.Rows = append(g.Rows, Row{
			PaneID:          p.PaneID,
			WorkspaceID:     p.WorkspaceID,
			WorkspaceNumber: wsNumber[p.WorkspaceID],
			Agent:           p.Agent,
			Status:          p.AgentStatus,
			Title:           title,
			Branch:          branch,
			Focused:         p.PaneID == s.FocusedPaneID,
		})
	}

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	labels := uniqueLabels(keys)
	groups := make([]Group, 0, len(keys))
	for _, k := range keys {
		g := byKey[k]
		g.Label = labels[k]
		sort.SliceStable(g.Rows, func(i, j int) bool {
			a, b := g.Rows[i], g.Rows[j]
			if a.WorkspaceNumber != b.WorkspaceNumber {
				return a.WorkspaceNumber < b.WorkspaceNumber
			}
			return paneNumber(a.PaneID) < paneNumber(b.PaneID)
		})
		groups = append(groups, *g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Label < groups[j].Label })
	return groups
}

// uniqueLabels maps each key to its basename, or parent/basename when two
// keys share a basename.
func uniqueLabels(keys []string) map[string]string {
	byBase := map[string][]string{}
	for _, k := range keys {
		b := filepath.Base(k)
		byBase[b] = append(byBase[b], k)
	}
	out := map[string]string{}
	for b, ks := range byBase {
		if len(ks) == 1 {
			out[ks[0]] = b
			continue
		}
		for _, k := range ks {
			out[k] = filepath.Base(filepath.Dir(k)) + "/" + b
		}
	}
	return out
}

// paneNumber extracts N from "wM:pN"; unknown shapes sort last.
func paneNumber(paneID string) int {
	_, after, ok := strings.Cut(paneID, ":p")
	if !ok {
		return 1 << 30
	}
	n, err := strconv.Atoi(after)
	if err != nil {
		return 1 << 30
	}
	return n
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/group/ -v`
Expected: all 10 tests PASS (4 resolver + 6 build)

- [ ] **Step 5: Commit**

```bash
git add internal/group
git commit -m "feat(group): build sorted repository groups from a snapshot"
```

---

### Task 6: ui package — model and click mapping

**Files:**
- Create: `internal/ui/model.go`, `internal/ui/view.go`
- Test: `internal/ui/model_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package ui

import (
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ayumu-1212/herdr-pasture/internal/group"
	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

type fakeFetcher struct {
	snap    snapshot.Snapshot
	err     error
	focused []string
}

func (f *fakeFetcher) Snapshot() (snapshot.Snapshot, error) { return f.snap, f.err }
func (f *fakeFetcher) FocusAgent(id string) error {
	f.focused = append(f.focused, id)
	return nil
}

type mapResolver map[string]group.RepoInfo

func (m mapResolver) Resolve(cwd string) group.RepoInfo { return m[cwd] }

func twoGroups() []group.Group {
	return []group.Group{
		{Key: "/r/a", Label: "a", Rows: []group.Row{{PaneID: "w1:p1", Title: "one", Status: "idle"}, {PaneID: "w1:p2", Title: "two", Status: "working"}}},
		{Key: "/r/b", Label: "b", Rows: []group.Row{{PaneID: "w2:p1", Title: "three", Status: "done"}}},
	}
}

func loaded(f *fakeFetcher) Model {
	m := New(f, mapResolver{}, group.Options{}, time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	m, _ = m.Update(snapshotMsg{groups: twoGroups()})
	return m
}

// drain runs a returned tea.Cmd once so side effects (FocusAgent) happen.
func drain(cmd tea.Cmd) {
	if cmd != nil {
		cmd()
	}
}

func TestLinesFlattenGroupsAndRespectCollapse(t *testing.T) {
	m := loaded(&fakeFetcher{})
	if got := len(m.lines()); got != 5 {
		t.Fatalf("lines = %d, want 5 (2 headers + 3 rows)", got)
	}
	m.collapsed["/r/a"] = true
	ls := m.lines()
	if len(ls) != 3 || !ls[0].header || !ls[1].header || ls[2].header {
		t.Fatalf("collapsed lines = %+v", ls)
	}
}

func TestClickOnRowFocusesPane(t *testing.T) {
	f := &fakeFetcher{}
	m := loaded(f)
	// screen row 0 = title bar, 1 = header "a", 2 = row one, 3 = row two
	_, cmd := m.Update(tea.MouseMsg{Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	drain(cmd)
	if len(f.focused) != 1 || f.focused[0] != "w1:p2" {
		t.Fatalf("focused = %v", f.focused)
	}
}

func TestClickOnHeaderTogglesCollapse(t *testing.T) {
	f := &fakeFetcher{}
	m := loaded(f)
	m, cmd := m.Update(tea.MouseMsg{Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	drain(cmd)
	if !m.collapsed["/r/a"] || len(f.focused) != 0 {
		t.Fatalf("collapsed=%v focused=%v", m.collapsed, f.focused)
	}
	if len(m.lines()) != 3 {
		t.Fatalf("lines after collapse = %d", len(m.lines()))
	}
}

func TestClickOutsideListIsIgnored(t *testing.T) {
	f := &fakeFetcher{}
	m := loaded(f)
	_, cmd := m.Update(tea.MouseMsg{Y: 15, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	drain(cmd)
	_, cmd = m.Update(tea.MouseMsg{Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	drain(cmd)
	if len(f.focused) != 0 {
		t.Fatalf("focused = %v", f.focused)
	}
}

func TestKeyboardNavigationAndEnter(t *testing.T) {
	f := &fakeFetcher{}
	m := loaded(f)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // cursor on header "b"
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // row three
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // clamped
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(cmd)
	if len(f.focused) != 1 || f.focused[0] != "w2:p1" {
		t.Fatalf("focused = %v", f.focused)
	}
}

func TestSnapshotErrorMarksDisconnectedAndKeepsGroups(t *testing.T) {
	m := loaded(&fakeFetcher{})
	m, _ = m.Update(snapshotMsg{err: errors.New("socket down")})
	if !m.disconnected || len(m.groups) != 2 {
		t.Fatalf("disconnected=%v groups=%d", m.disconnected, len(m.groups))
	}
	m, _ = m.Update(snapshotMsg{groups: twoGroups()})
	if m.disconnected {
		t.Fatal("should reconnect")
	}
}

func TestCursorClampsWhenRowsDisappear(t *testing.T) {
	m := loaded(&fakeFetcher{})
	m.cursor = 4
	m, _ = m.Update(snapshotMsg{groups: twoGroups()[:1]})
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want 2 (last line)", m.cursor)
	}
}

func TestTruncateHonorsDisplayWidth(t *testing.T) {
	if got := truncate("abcdefgh", 5); got != "abcd…" {
		t.Fatalf("got %q", got)
	}
	if got := truncate("日本語のタイトル", 7); got != "日本語…" {
		t.Fatalf("got %q", got)
	}
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/`
Expected: FAIL (undefined: New, snapshotMsg, truncate)

- [ ] **Step 3: Implement model.go**

```go
// Package ui is the bubbletea program shown inside the docked pane.
package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ayumu-1212/herdr-pasture/internal/group"
	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

// Fetcher is the slice of herdr.Client the UI depends on.
type Fetcher interface {
	Snapshot() (snapshot.Snapshot, error)
	FocusAgent(paneID string) error
}

// Model is the bubbletea model. Copy semantics: Update returns a new value.
type Model struct {
	fetch    Fetcher
	resolver group.Resolver
	opt      group.Options
	interval time.Duration

	groups       []group.Group
	collapsed    map[string]bool
	cursor       int
	width        int
	height       int
	disconnected bool
}

// line is one screen row below the title bar.
type line struct {
	header bool
	group  int // index into groups
	row    int // index into groups[group].Rows; -1 for headers
}

type snapshotMsg struct {
	groups []group.Group
	err    error
}

type tickMsg struct{}

// New builds a model that polls fetch every interval.
func New(f Fetcher, r group.Resolver, opt group.Options, interval time.Duration) Model {
	return Model{fetch: f, resolver: r, opt: opt, interval: interval, collapsed: map[string]bool{}}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchCmd(), tea.SetWindowTitle("pasture"))
}

func (m Model) fetchCmd() tea.Cmd {
	fetch, resolver, opt := m.fetch, m.resolver, m.opt
	return func() tea.Msg {
		s, err := fetch.Snapshot()
		if err != nil {
			return snapshotMsg{err: err}
		}
		return snapshotMsg{groups: group.Build(s, resolver, opt)}
	}
}

func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(m.interval, func(time.Time) tea.Msg { return tickMsg{} })
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case snapshotMsg:
		if msg.err != nil {
			m.disconnected = true
		} else {
			m.disconnected = false
			m.groups = msg.groups
		}
		m.clampCursor()
		return m, m.tickCmd()
	case tickMsg:
		return m, m.fetchCmd()
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		idx := msg.Y - 1 // row 0 is the title bar
		if idx < 0 || idx >= len(m.lines()) {
			return m, nil
		}
		m.cursor = idx
		return m.activate(idx)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.cursor--
		m.clampCursor()
	case "down", "j":
		m.cursor++
		m.clampCursor()
	case "enter":
		return m.activate(m.cursor)
	case " ":
		if ls := m.lines(); m.cursor < len(ls) && ls[m.cursor].header {
			return m.activate(m.cursor)
		}
	case "r":
		return m, m.fetchCmd()
	}
	return m, nil
}

// activate toggles a header or focuses a row's pane.
func (m Model) activate(idx int) (Model, tea.Cmd) {
	ls := m.lines()
	if idx < 0 || idx >= len(ls) {
		return m, nil
	}
	l := ls[idx]
	g := m.groups[l.group]
	if l.header {
		m.collapsed[g.Key] = !m.collapsed[g.Key]
		m.clampCursor()
		return m, nil
	}
	paneID := g.Rows[l.row].PaneID
	fetch := m.fetch
	return m, func() tea.Msg {
		_ = fetch.FocusAgent(paneID) // a vanished pane simply disappears on the next poll
		return nil
	}
}

// lines flattens groups into screen rows, skipping collapsed rows.
func (m Model) lines() []line {
	var out []line
	for gi, g := range m.groups {
		out = append(out, line{header: true, group: gi, row: -1})
		if m.collapsed[g.Key] {
			continue
		}
		for ri := range g.Rows {
			out = append(out, line{group: gi, row: ri})
		}
	}
	return out
}

func (m *Model) clampCursor() {
	n := len(m.lines())
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}
```

Note: `Update` returns `(Model, tea.Cmd)` rather than `(tea.Model, tea.Cmd)` so tests keep the concrete type. Task 8 wraps it for `tea.NewProgram`.

- [ ] **Step 4: Implement view.go**

```go
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	dimStyle      = lipgloss.NewStyle().Faint(true)
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	focusedStyle  = lipgloss.NewStyle().Reverse(true)
	cursorMarker  = "›"
	statusStyles  = map[string]lipgloss.Style{
		"working": lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		"done":    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		"blocked": lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		"unknown": lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	}
	statusIcons = map[string]string{
		"working": "◐",
		"idle":    "○",
		"done":    "✓",
		"blocked": "●",
		"unknown": "?",
	}
)

// View implements tea.Model.
func (m Model) View() string {
	width := m.width
	if width < 22 {
		width = 22
	}
	var b strings.Builder
	title := "herdr-pasture"
	if m.disconnected {
		title += " " + dimStyle.Render("disconnected")
	}
	b.WriteString(titleStyle.Render(truncate(title, width)))
	b.WriteString("\n")

	ls := m.lines()
	for i, l := range ls {
		if m.height > 0 && i+1 >= m.height {
			break
		}
		g := m.groups[l.group]
		marker := " "
		if i == m.cursor {
			marker = cursorMarker
		}
		var text string
		if l.header {
			chevron := "▾"
			if m.collapsed[g.Key] {
				chevron = "▸"
			}
			text = headerStyle.Render(truncate(chevron+" "+g.Label, width-2))
		} else {
			r := g.Rows[l.row]
			icon := statusIcons[r.Status]
			if icon == "" {
				icon = statusIcons["unknown"]
			}
			if st, ok := statusStyles[r.Status]; ok {
				icon = st.Render(icon)
			}
			label := r.Title
			if r.Branch != "" {
				label = "[" + r.Branch + "] " + label
			}
			label = truncate(label, width-6)
			if r.Focused {
				label = focusedStyle.Render(label)
			}
			text = "  " + icon + " " + label
		}
		b.WriteString(marker + text + "\n")
	}
	return b.String()
}

// truncate cuts s to at most width display cells, appending "…" when cut.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/ui/ -v`
Expected: 8 tests PASS

- [ ] **Step 6: Commit**

```bash
git add internal/ui
git commit -m "feat(ui): bubbletea list with grouping, click-to-focus and keyboard navigation"
```

---

### Task 7: dock package — ensure / toggle / redeploy

**Files:**
- Create: `internal/dock/dock.go`
- Test: `internal/dock/dock_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package dock

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ayumu-1212/herdr-pasture/internal/config"
	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

type call struct {
	Name string
	Args []string
}

// fakeClient serves a mutable pane list and records mutations.
type fakeClient struct {
	panes  []snapshot.Pane
	layout snapshot.Layout
	calls  []call
	nextID string
	// stampTokenOnList makes the next PaneList show the token on nextID,
	// simulating the UI process stamping itself.
	stampTokenOnList bool
}

func (f *fakeClient) rec(name string, args ...string) { f.calls = append(f.calls, call{name, args}) }

func (f *fakeClient) PaneList() ([]snapshot.Pane, error) {
	if f.stampTokenOnList {
		for i := range f.panes {
			if f.panes[i].PaneID == f.nextID {
				f.panes[i].Tokens = map[string]string{Token: "1"}
			}
		}
	}
	return append([]snapshot.Pane(nil), f.panes...), nil
}
func (f *fakeClient) Layout(paneID string) (snapshot.Layout, error) { return f.layout, nil }
func (f *fakeClient) Split(paneID string, ratio float64, cwd string, env map[string]string) (string, error) {
	f.rec("split", paneID)
	tab := ""
	for _, p := range f.panes {
		if p.PaneID == paneID {
			tab = p.TabID
		}
	}
	f.panes = append(f.panes, snapshot.Pane{PaneID: f.nextID, TabID: tab, Cwd: cwd})
	return f.nextID, nil
}
func (f *fakeClient) Swap(source, target string) error     { f.rec("swap", source, target); return nil }
func (f *fakeClient) RunInPane(paneID, command string) error { f.rec("run", paneID, command); return nil }
func (f *fakeClient) Rename(paneID, label string) error    { f.rec("rename", paneID, label); return nil }
func (f *fakeClient) Close(paneID string) error {
	f.rec("close", paneID)
	kept := f.panes[:0]
	for _, p := range f.panes {
		if p.PaneID != paneID {
			kept = append(kept, p)
		}
	}
	f.panes = kept
	return nil
}

func names(calls []call) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Name
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func oneTab() *fakeClient {
	return &fakeClient{
		nextID: "w1:p9",
		panes: []snapshot.Pane{
			{PaneID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1", Cwd: "/r", Agent: "claude", Focused: true},
			{PaneID: "w1:p2", TabID: "w1:t1", WorkspaceID: "w1", Cwd: "/r"},
		},
		layout: snapshot.Layout{TabID: "w1:t1", Panes: []snapshot.LayoutPane{
			{PaneID: "w1:p2", Rect: snapshot.Rect{X: 100, Y: 1, Width: 100, Height: 40}},
			{PaneID: "w1:p1", Rect: snapshot.Rect{X: 0, Y: 1, Width: 100, Height: 40}},
		}},
		stampTokenOnList: true,
	}
}

func deps(t *testing.T, c *fakeClient) Deps {
	t.Helper()
	return Deps{
		Client:   c,
		StateDir: t.TempDir(),
		Bin:      "/opt/pasture",
		Cfg:      config.Default(),
		Now:      time.Now,
		Sleep:    func(time.Duration) {},
		Log:      io.Discard,
	}
}

func TestEnsureOpensDockOnLeftOfLeftmostPane(t *testing.T) {
	c := oneTab()
	if err := Ensure(deps(t, c), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"split", "swap", "run", "rename"}
	if !equal(names(c.calls), want) {
		t.Fatalf("calls = %v, want %v", names(c.calls), want)
	}
	if c.calls[0].Args[0] != "w1:p1" {
		t.Fatalf("should split the leftmost pane, split %v", c.calls[0].Args)
	}
	if c.calls[1].Args[0] != "w1:p9" || c.calls[1].Args[1] != "w1:p1" {
		t.Fatalf("swap args = %v", c.calls[1].Args)
	}
	if c.calls[2].Args[1] != "'/opt/pasture' ui" {
		t.Fatalf("run command = %q", c.calls[2].Args[1])
	}
	if c.calls[3].Args[1] != Label {
		t.Fatalf("rename label = %q", c.calls[3].Args[1])
	}
}

func TestEnsureNoopWhenLiveDockExists(t *testing.T) {
	c := oneTab()
	c.panes = append(c.panes, snapshot.Pane{PaneID: "w1:p5", TabID: "w1:t1", Label: Label, Tokens: map[string]string{Token: "77"}})
	Ensure(deps(t, c), "w1:t1")
	if len(c.calls) != 0 {
		t.Fatalf("expected no calls, got %v", names(c.calls))
	}
}

func TestEnsureReplacesCorpse(t *testing.T) {
	c := oneTab()
	c.panes = append(c.panes, snapshot.Pane{PaneID: "w1:p5", TabID: "w1:t1", Label: Label})
	Ensure(deps(t, c), "w1:t1")
	want := []string{"close", "split", "swap", "run", "rename"}
	if !equal(names(c.calls), want) || c.calls[0].Args[0] != "w1:p5" {
		t.Fatalf("calls = %v", c.calls)
	}
}

func TestEnsureRespectsAutoOpenOff(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	d.Cfg.AutoOpen = false
	Ensure(d, "w1:t1")
	if len(c.calls) != 0 {
		t.Fatalf("expected no calls, got %v", names(c.calls))
	}
}

func TestEnsureRespectsSnooze(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	os.MkdirAll(filepath.Join(d.StateDir, "snooze"), 0o755)
	os.WriteFile(filepath.Join(d.StateDir, "snooze", "w1_t1"), nil, 0o644)
	Ensure(d, "w1:t1")
	if len(c.calls) != 0 {
		t.Fatalf("expected no calls, got %v", names(c.calls))
	}
}

func TestEnsureFallsBackToFocusedTab(t *testing.T) {
	c := oneTab()
	Ensure(deps(t, c), "")
	if len(c.calls) == 0 || c.calls[0].Args[0] != "w1:p1" {
		t.Fatalf("calls = %v", c.calls)
	}
}

func TestEnsureSkipsWhenLocked(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	os.Mkdir(filepath.Join(d.StateDir, "ensure.lock"), 0o755)
	Ensure(d, "w1:t1")
	if len(c.calls) != 0 {
		t.Fatalf("expected no calls under lock, got %v", names(c.calls))
	}
}

func TestEnsureStealsStaleLock(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	os.Mkdir(filepath.Join(d.StateDir, "ensure.lock"), 0o755)
	d.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	Ensure(d, "w1:t1")
	if len(c.calls) == 0 {
		t.Fatal("stale lock should have been stolen")
	}
	if _, err := os.Stat(filepath.Join(d.StateDir, "ensure.lock")); err == nil {
		t.Fatal("lock should be released after Ensure")
	}
}

func TestToggleClosesLiveDockAndSnoozes(t *testing.T) {
	c := oneTab()
	c.panes = append(c.panes, snapshot.Pane{PaneID: "w1:p5", TabID: "w1:t1", Label: Label, Tokens: map[string]string{Token: "77"}})
	d := deps(t, c)
	Toggle(d, "w1:t1")
	if !equal(names(c.calls), []string{"close"}) || c.calls[0].Args[0] != "w1:p5" {
		t.Fatalf("calls = %v", c.calls)
	}
	if _, err := os.Stat(filepath.Join(d.StateDir, "snooze", "w1_t1")); err != nil {
		t.Fatal("snooze file should exist")
	}
}

func TestToggleOpensAndClearsSnooze(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	os.MkdirAll(filepath.Join(d.StateDir, "snooze"), 0o755)
	os.WriteFile(filepath.Join(d.StateDir, "snooze", "w1_t1"), nil, 0o644)
	Toggle(d, "w1:t1")
	if !equal(names(c.calls), []string{"split", "swap", "run", "rename"}) {
		t.Fatalf("calls = %v", names(c.calls))
	}
	if _, err := os.Stat(filepath.Join(d.StateDir, "snooze", "w1_t1")); err == nil {
		t.Fatal("snooze file should be removed")
	}
}

func TestRedeployClosesAllDocksAndClearsSnooze(t *testing.T) {
	c := oneTab()
	c.panes = append(c.panes,
		snapshot.Pane{PaneID: "w1:p5", TabID: "w1:t1", Label: Label, Tokens: map[string]string{Token: "77"}},
		snapshot.Pane{PaneID: "w2:p3", TabID: "w2:t1", Label: Label},
	)
	d := deps(t, c)
	os.MkdirAll(filepath.Join(d.StateDir, "snooze"), 0o755)
	os.WriteFile(filepath.Join(d.StateDir, "snooze", "w1_t1"), nil, 0o644)
	Redeploy(d)
	if !equal(names(c.calls), []string{"close", "close"}) {
		t.Fatalf("calls = %v", names(c.calls))
	}
	if _, err := os.Stat(filepath.Join(d.StateDir, "snooze")); err == nil {
		t.Fatal("snooze dir should be removed")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/dock/`
Expected: FAIL (undefined: Ensure, Deps, Label, Token)

- [ ] **Step 3: Implement**

```go
// Package dock places, toggles and redeploys the pasture pane inside herdr tabs.
package dock

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ayumu-1212/herdr-pasture/internal/config"
	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

const (
	// Label is the pane label set with `pane rename`; it survives server restarts.
	Label = "pasture"
	// Token is the metadata token name stamped by the running UI; it does not
	// survive restarts, so Label-without-Token identifies a dead pane.
	Token = "pasture"

	lockStale    = 30 * time.Second
	tokenRetries = 30
	tokenWait    = 200 * time.Millisecond
)

// Client is the slice of herdr.Client that dock uses.
type Client interface {
	PaneList() ([]snapshot.Pane, error)
	Layout(paneID string) (snapshot.Layout, error)
	Split(paneID string, ratio float64, cwd string, env map[string]string) (string, error)
	Swap(source, target string) error
	RunInPane(paneID, command string) error
	Rename(paneID, label string) error
	Close(paneID string) error
}

// Deps bundles everything the dock operations need; tests inject fakes.
type Deps struct {
	Client   Client
	StateDir string // HERDR_PLUGIN_STATE_DIR
	Bin      string // absolute path of the pasture binary
	Cfg      config.Config
	Now      func() time.Time
	Sleep    func(time.Duration)
	Log      io.Writer
}

func (d Deps) logf(format string, args ...any) {
	if d.Log != nil {
		fmt.Fprintf(d.Log, "pasture: "+format+"\n", args...)
	}
}

// Ensure makes sure tabID (or the focused tab when empty) has a live pasture
// pane on its left edge. It never steals focus and never returns an error for
// expected conditions (locked, snoozed, auto_open off).
func Ensure(d Deps, tabID string) error {
	if !d.Cfg.AutoOpen {
		d.logf("auto_open is false; skipping")
		return nil
	}
	panes, err := d.Client.PaneList()
	if err != nil {
		return err
	}
	if tabID == "" {
		tabID = focusedTab(panes)
	}
	if tabID == "" {
		d.logf("no target tab; skipping")
		return nil
	}
	unlock, ok := acquireLock(d)
	if !ok {
		d.logf("another ensure is running; skipping")
		return nil
	}
	defer unlock()
	return open(d, panes, tabID)
}

// open assumes the lock is held.
func open(d Deps, panes []snapshot.Pane, tabID string) error {
	live, corpses := classify(panes, tabID)
	if live != "" {
		return nil
	}
	for _, id := range corpses {
		d.logf("closing dead pasture pane %s", id)
		if err := d.Client.Close(id); err != nil {
			d.logf("close %s: %v", id, err)
		}
	}
	if len(corpses) > 0 {
		var err error
		if panes, err = d.Client.PaneList(); err != nil {
			return err
		}
	}
	if snoozed(d, tabID) {
		d.logf("tab %s is snoozed; skipping", tabID)
		return nil
	}
	anchor, ok := leftmost(d, panes, tabID)
	if !ok {
		d.logf("tab %s has no panes; skipping", tabID)
		return nil
	}
	env := map[string]string{
		"HERDR_PLUGIN_STATE_DIR":  d.StateDir,
		"HERDR_PLUGIN_CONFIG_DIR": config.Dir(),
	}
	newID, err := d.Client.Split(anchor.PaneID, d.Cfg.WidthRatio, anchor.Cwd, env)
	if err != nil {
		return err
	}
	if err := d.Client.Swap(newID, anchor.PaneID); err != nil {
		return err
	}
	if err := d.Client.RunInPane(newID, shellQuote(d.Bin)+" ui"); err != nil {
		return err
	}
	if err := d.Client.Rename(newID, Label); err != nil {
		d.logf("rename %s: %v", newID, err)
	}
	for i := 0; i < tokenRetries; i++ {
		if hasToken(d, newID) {
			return nil
		}
		d.Sleep(tokenWait)
	}
	d.logf("pane %s never stamped its token", newID)
	return nil
}

// Toggle closes the tab's live pasture pane (and snoozes the tab) or opens one
// (clearing the snooze).
func Toggle(d Deps, tabID string) error {
	panes, err := d.Client.PaneList()
	if err != nil {
		return err
	}
	if tabID == "" {
		tabID = focusedTab(panes)
	}
	if tabID == "" {
		return errors.New("toggle: no focused tab")
	}
	live, _ := classify(panes, tabID)
	if live != "" {
		if err := d.Client.Close(live); err != nil {
			return err
		}
		return setSnooze(d, tabID, true)
	}
	if err := setSnooze(d, tabID, false); err != nil {
		return err
	}
	unlock, ok := acquireLock(d)
	if !ok {
		d.logf("another ensure is running; skipping")
		return nil
	}
	defer unlock()
	return open(d, panes, tabID)
}

// Redeploy closes every pasture pane (live or dead) and clears all snoozes so
// the next focus event respawns them on the current build.
func Redeploy(d Deps) error {
	panes, err := d.Client.PaneList()
	if err != nil {
		return err
	}
	for _, p := range panes {
		if isPasture(p) {
			if err := d.Client.Close(p.PaneID); err != nil {
				d.logf("close %s: %v", p.PaneID, err)
			}
		}
	}
	return os.RemoveAll(filepath.Join(d.StateDir, "snooze"))
}

func isPasture(p snapshot.Pane) bool {
	return p.Label == Label || p.Tokens[Token] != ""
}

// classify returns the live pasture pane id in tabID ("" if none) and the ids
// of dead ones (label present, token missing).
func classify(panes []snapshot.Pane, tabID string) (live string, corpses []string) {
	for _, p := range panes {
		if p.TabID != tabID || !isPasture(p) {
			continue
		}
		if p.Tokens[Token] != "" {
			live = p.PaneID
		} else {
			corpses = append(corpses, p.PaneID)
		}
	}
	return live, corpses
}

func focusedTab(panes []snapshot.Pane) string {
	for _, p := range panes {
		if p.Focused {
			return p.TabID
		}
	}
	return ""
}

// leftmost picks the pane to split: smallest X, then tallest, in tabID.
func leftmost(d Deps, panes []snapshot.Pane, tabID string) (snapshot.Pane, bool) {
	byID := map[string]snapshot.Pane{}
	var sample snapshot.Pane
	for _, p := range panes {
		if p.TabID == tabID {
			byID[p.PaneID] = p
			sample = p
		}
	}
	if len(byID) == 0 {
		return snapshot.Pane{}, false
	}
	layout, err := d.Client.Layout(sample.PaneID)
	if err != nil || len(layout.Panes) == 0 {
		d.logf("layout unavailable (%v); using %s", err, sample.PaneID)
		return sample, true
	}
	best := layout.Panes[0]
	for _, lp := range layout.Panes[1:] {
		if lp.Rect.X < best.Rect.X || (lp.Rect.X == best.Rect.X && lp.Rect.Height > best.Rect.Height) {
			best = lp
		}
	}
	if p, ok := byID[best.PaneID]; ok {
		return p, true
	}
	return sample, true
}

func hasToken(d Deps, paneID string) bool {
	panes, err := d.Client.PaneList()
	if err != nil {
		return false
	}
	for _, p := range panes {
		if p.PaneID == paneID && p.Tokens[Token] != "" {
			return true
		}
	}
	return false
}

// acquireLock creates StateDir/ensure.lock. A lock older than lockStale is
// treated as abandoned and taken over.
func acquireLock(d Deps) (release func(), ok bool) {
	path := filepath.Join(d.StateDir, "ensure.lock")
	_ = os.MkdirAll(d.StateDir, 0o755)
	err := os.Mkdir(path, 0o755)
	if errors.Is(err, fs.ErrExist) {
		fi, statErr := os.Stat(path)
		if statErr != nil || d.Now().Sub(fi.ModTime()) <= lockStale {
			return nil, false
		}
		d.logf("stealing stale lock")
		_ = os.RemoveAll(path)
		err = os.Mkdir(path, 0o755)
	}
	if err != nil {
		return nil, false
	}
	return func() { _ = os.Remove(path) }, true
}

func snoozePath(d Deps, tabID string) string {
	return filepath.Join(d.StateDir, "snooze", strings.ReplaceAll(tabID, ":", "_"))
}

func snoozed(d Deps, tabID string) bool {
	_, err := os.Stat(snoozePath(d, tabID))
	return err == nil
}

func setSnooze(d Deps, tabID string, on bool) error {
	path := snoozePath(d, tabID)
	if !on {
		err := os.Remove(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, nil, 0o644)
}

// shellQuote wraps s in single quotes for the pane's shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/dock/ -v`
Expected: 11 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/dock
git commit -m "feat(dock): ensure/toggle/redeploy with lock, snooze and corpse cleanup"
```

---

### Task 8: cmd/pasture main, manifest, README

**Files:**
- Create: `cmd/pasture/main.go`, `herdr-plugin.toml`, `README.md`

- [ ] **Step 1: Write main.go**

```go
// Command pasture is the herdr-pasture plugin binary.
//
//	pasture ui        run the docked TUI (inside a herdr pane)
//	pasture ensure    make sure the current tab has a pasture pane (event hook)
//	pasture toggle    open/close the pasture pane in the current tab (action)
//	pasture redeploy  close every pasture pane so they respawn on the next focus
//	pasture version
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ayumu-1212/herdr-pasture/internal/config"
	"github.com/ayumu-1212/herdr-pasture/internal/dock"
	"github.com/ayumu-1212/herdr-pasture/internal/group"
	"github.com/ayumu-1212/herdr-pasture/internal/herdr"
	"github.com/ayumu-1212/herdr-pasture/internal/ui"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: pasture <ui|ensure|toggle|redeploy|version>")
		os.Exit(2)
	}
	cfg, warnings := config.Load(filepath.Join(config.Dir(), "config.toml"))
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "pasture:", w)
	}
	client := herdr.New(herdr.NewExecRunner())

	var err error
	switch os.Args[1] {
	case "ui":
		err = runUI(cfg, client)
	case "ensure":
		err = dock.Ensure(deps(cfg, client), os.Getenv("HERDR_TAB_ID"))
	case "toggle":
		err = dock.Toggle(deps(cfg, client), os.Getenv("HERDR_TAB_ID"))
	case "redeploy":
		err = dock.Redeploy(deps(cfg, client))
	case "version":
		fmt.Println("pasture", version)
	default:
		fmt.Fprintf(os.Stderr, "pasture: unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pasture:", err)
		os.Exit(1)
	}
}

func stateDir() string {
	if d := os.Getenv("HERDR_PLUGIN_STATE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "herdr-pasture")
}

func deps(cfg config.Config, client *herdr.Client) dock.Deps {
	bin, err := os.Executable()
	if err != nil {
		bin = os.Args[0]
	}
	return dock.Deps{
		Client:   client,
		StateDir: stateDir(),
		Bin:      bin,
		Cfg:      cfg,
		Now:      time.Now,
		Sleep:    time.Sleep,
		Log:      os.Stderr,
	}
}

// program adapts ui.Model's concrete Update signature to tea.Model.
type program struct{ ui.Model }

func (p program) Init() tea.Cmd { return p.Model.Init() }
func (p program) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := p.Model.Update(msg)
	return program{m}, cmd
}
func (p program) View() string { return p.Model.View() }

func runUI(cfg config.Config, client *herdr.Client) error {
	if paneID := os.Getenv("HERDR_PANE_ID"); paneID != "" {
		if err := client.ReportToken(paneID, dock.Token, strconv.Itoa(os.Getpid())); err != nil {
			fmt.Fprintln(os.Stderr, "pasture: token:", err)
		}
	}
	opt := group.Options{SelfToken: dock.Token, Exclude: cfg.Excluded}
	model := ui.New(client, group.NewGitResolver(), opt, time.Duration(cfg.PollIntervalMs)*time.Millisecond)
	_, err := tea.NewProgram(program{model}, tea.WithMouseCellMotion()).Run()
	return err
}
```

- [ ] **Step 2: Build and smoke-test outside herdr**

Run: `sh scripts/build.sh && ./bin/pasture version`
Expected: `pasture 0.1.0`

Run: `HERDR_BIN_PATH=/nonexistent ./bin/pasture redeploy; echo "exit=$?"`
Expected: an error line mentioning `herdr pane list` and `exit=1`.

- [ ] **Step 3: Write herdr-plugin.toml**

```toml
id = "herdr-pasture"
name = "Pasture"
version = "0.1.0"
min_herdr_version = "0.8.0"
description = "Agents grouped by repository, docked on the left of every tab"
platforms = ["macos", "linux"]

[[build]]
command = ["sh", "scripts/build.sh"]

# Manual entry point (`herdr plugin pane open --plugin herdr-pasture --entrypoint ui`).
# Normal docking goes through `ensure`, which needs split + swap for a left edge.
[[panes]]
id = "ui"
title = "Pasture"
placement = "split"
command = ["./bin/pasture", "ui"]

[[actions]]
id = "toggle"
title = "Pasture: toggle"
contexts = ["global"]
command = ["./bin/pasture", "toggle"]

[[actions]]
id = "redeploy"
title = "Pasture: redeploy panes"
contexts = ["global"]
command = ["./bin/pasture", "redeploy"]

[[events]]
on = "pane.focused"
command = ["./bin/pasture", "ensure"]

[[events]]
on = "tab.focused"
command = ["./bin/pasture", "ensure"]

[[events]]
on = "tab.created"
command = ["./bin/pasture", "ensure"]

[[events]]
on = "workspace.created"
command = ["./bin/pasture", "ensure"]
```

- [ ] **Step 4: Write README.md**

````markdown
# herdr-pasture

A [herdr](https://herdr.dev) plugin that docks a pane on the left of every tab
listing your running agent panes, grouped by git repository. Click a row to
jump to that agent; click a group header to collapse it.

```
 herdr-pasture
 ▾ global-sourcing-tool
   ◐ watchdog techcrunch ingestion…
   ○ Notion-research plist設定
 ▾ polala
   ✓ email feature #206
```

Worktrees are folded into their main repository and show their branch as
`[branch]`. Panes without an agent are not listed.

## Install

```bash
herdr plugin install ayumu-1212/herdr-pasture
```

Requires herdr ≥ 0.8.0 and Go ≥ 1.22 (the build step compiles the binary).

For development:

```bash
sh scripts/build.sh
herdr plugin link /path/to/herdr-pasture
```

## Usage

The pane appears automatically when a tab is focused or created. Actions:

- `herdr-pasture.toggle` – close the pane in the current tab (it stays closed
  until you toggle again) or open it.
- `herdr-pasture.redeploy` – close every pasture pane so they respawn on the
  latest build.

Bind the toggle in `~/.config/herdr/config.toml`:

```toml
[[keys.command]]
key = "prefix+g"
type = "shell"
command = "herdr plugin action invoke herdr-pasture.toggle"
description = "toggle pasture"
```

Keys inside the pane: `↑`/`↓` or `j`/`k` move, `Enter` focuses, `Space`
collapses a group, `r` refreshes, `q` quits.

## Configuration

`$(herdr plugin config-dir herdr-pasture)/config.toml` — all keys optional:

```toml
auto_open = true          # false: never auto-dock; use the toggle action
width_ratio = 0.25        # share of the tab width (0.1–0.5)
poll_interval_ms = 1000   # how often to read `herdr api snapshot`
exclude = ["~/tmp/**"]    # hide panes whose cwd matches
```

## Known limitations

- Snooze state is keyed by tab id and shared across named herdr sessions.
- If the leftmost column of a tab is stacked, the pane takes the tallest
  pane's height rather than the full tab height.
````

- [ ] **Step 5: Run the full test suite and vet**

Run: `go vet ./... && go test ./...`
Expected: `ok` for config, snapshot, herdr, group, ui, dock; no vet findings.

- [ ] **Step 6: Commit**

```bash
git add cmd herdr-plugin.toml README.md
git commit -m "feat: pasture binary, plugin manifest and README"
```

---

### Task 9: Real-session verification

Manual, in a throwaway herdr session. Nothing is committed here except fixes found along the way.

- [ ] **Step 1: Link the plugin with auto-open off**

```bash
sh scripts/build.sh
herdr plugin link "$(pwd)"
mkdir -p "$(herdr plugin config-dir herdr-pasture)"
printf 'auto_open = false\n' > "$(herdr plugin config-dir herdr-pasture)/config.toml"
herdr plugin list
```
Expected: `herdr-pasture` listed as enabled.

- [ ] **Step 2: Start a named test session and open the pane manually**

In a separate terminal: `herdr --session pasture-dev`. Inside it, create one workspace with an agent pane (e.g. `claude`), then from another pane run:

```bash
herdr plugin action invoke herdr-pasture.toggle
```
Expected: a `pasture` pane appears at the LEFT edge, 25% wide, focus stays where it was, the title bar reads `herdr-pasture`, and the agent pane is listed under its repository name.

- [ ] **Step 3: Check click, keyboard and toggle**

- Click a row → focus moves to that agent pane (verify with `herdr pane current --current` from the agent pane, or visually).
- Click the group header → rows collapse; click again → expand.
- Run the toggle action again → pane closes. Switch tabs and back → it does NOT reappear (snoozed). Toggle once more → it reappears.

- [ ] **Step 4: Enable auto-open and check events**

```bash
printf 'auto_open = true\n' > "$(herdr plugin config-dir herdr-pasture)/config.toml"
```
- Create a new tab (`prefix+t`) → pasture pane appears in it without stealing focus.
- Create a new workspace → same.
- Inspect `herdr plugin log list --plugin herdr-pasture` for errors.

- [ ] **Step 5: Check restart recovery**

`herdr server stop` in the pasture-dev session only (never in your main session), relaunch `herdr --session pasture-dev`, focus a tab → the dead `pasture` pane is closed and a live one opens.

- [ ] **Step 6: Record findings**

If any step fails, fix the code with a test where possible, commit, run `herdr plugin action invoke herdr-pasture.redeploy`, and repeat the failing step. When all steps pass, note the herdr version tested in README under a "Tested with" line and commit:

```bash
git commit -am "docs: note tested herdr version"
```

---

## Self-review

- **Spec coverage:** §4 data/grouping → Tasks 2, 4, 5. §5 screen → Task 6 (icons, colors, branch prefix, focused row, collapse, keys, width/truncate, disconnected header). §6 lifecycle → Task 7 (events in manifest, lock, corpse cleanup, snooze, toggle, redeploy, token wait) and Task 8 (token stamping in `runUI`). §7 config → Task 1. §8 errors → Task 6 (disconnected keeps groups), Task 4 (git fallback), Task 7 (logs, exit 0 paths). §10 tests → each task. §11 worktree → Task 0.
- **Placeholders:** none; every step has code or an exact command.
- **Type consistency:** `herdr.Client` methods match the `dock.Client` and `ui.Fetcher` interfaces; `snapshot.Pane.Tokens`/`Label` used identically in group and dock; `dock.Token` == `group.Options.SelfToken` value set in main.
