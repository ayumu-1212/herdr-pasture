# herdr-pasture v0.2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** List workspaces that have no agent pane, and let the dock be a fixed number of columns instead of a share of the tab width, so the standard herdr sidebar can be turned off entirely.

**Architecture:** `group.Row` gains a `Kind` so one row type covers both an agent pane and a bare workspace; `Build` reads every pane (not only agent panes) to find each workspace's directory. `ui` dispatches on `Kind` and calls a new `FocusWorkspace`. `dock` gains a column target: it converts columns to a split ratio at open time and re-applies the width with `pane resize` on later events.

**Tech Stack:** Go 1.27, bubbletea v1.3.10, lipgloss v1.1.0, BurntSushi/toml v1.6.0, herdr 0.8.0 CLI.

**Spec:** `docs/superpowers/specs/2026-09-08-herdr-pasture-v2-design.md`

**Branch:** `feat/pasture-v2`, based on the merged `origin/main`.

---

## Verified herdr CLI facts for this change

Measured against a live herdr 0.8.0 server; do not re-derive them.

- `herdr api snapshot` workspace entries have `workspace_id`, `label`, `number`, `focused`, `agent_status`, `pane_count`, `tab_count`, `active_tab_id`. **No cwd.** Pane entries do have `cwd`, including panes with no agent.
- The snapshot's top level carries `focused_workspace_id` alongside `focused_pane_id`.
- `herdr workspace focus <workspace_id>` focuses a workspace.
- `herdr pane resize --pane <id> --direction right --amount <float>` widens the pane on the LEFT of that split by `amount * tab_width` columns; `--direction left` narrows it. Measured on a 54-column tab: a 14-column pane given `--amount 0.1` became 19 columns.
- `herdr pane split <id> --ratio R` leaves the ORIGINAL pane at `R * tab_width`, and `pane swap` exchanges positions while each slot keeps its size, so the dock ends up at `R * tab_width`.

## Existing code this plan touches

- `internal/group/group.go` — `Row`, `Group`, `Options`, `Build`, `unknownOrder`, `uniqueLabels`, `paneNumber`.
- `internal/snapshot/snapshot.go` — `Snapshot`.
- `internal/ui/model.go` — `Fetcher` (currently `Snapshot`, `FocusAgent`), `activate`.
- `internal/ui/view.go` — row rendering.
- `internal/herdr/client.go` — `Client` methods.
- `internal/config/config.go` — `Config`, `Default`, `Load`.
- `internal/dock/dock.go` — `Client` interface, `openLocked`, `leftmost`, the `Split` call.

No new files. Each package keeps its single-responsibility file plus its test file.

---

### Task 1: snapshot exposes the focused workspace

Later tasks need this field, so it lands first.

**Files:**
- Modify: `internal/snapshot/snapshot.go`
- Test: `internal/snapshot/snapshot_test.go`

- [ ] **Step 1: Add the failing assertion**

In `internal/snapshot/snapshot_test.go`, add `"focused_workspace_id":"w6",` to the snapshot object inside the `snapshotJSON` constant, next to `"focused_pane_id"`. Then add to `TestDecodeSnapshot`:

```go
	if s.FocusedWorkspaceID != "w6" {
		t.Fatalf("FocusedWorkspaceID = %q", s.FocusedWorkspaceID)
	}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/snapshot/ -run DecodeSnapshot`
Expected: FAIL to build, `s.FocusedWorkspaceID undefined`

- [ ] **Step 3: Implement**

In `internal/snapshot/snapshot.go`, replace the `Snapshot` struct with:

```go
// Snapshot is the live session state returned by `herdr api snapshot`.
type Snapshot struct {
	Panes              []Pane      `json:"panes"`
	Workspaces         []Workspace `json:"workspaces"`
	FocusedPaneID      string      `json:"focused_pane_id"`
	FocusedWorkspaceID string      `json:"focused_workspace_id"`
}
```

- [ ] **Step 4: Run the package tests**

Run: `go vet ./internal/snapshot/ && go test -race ./internal/snapshot/ -v`
Expected: every test PASS.

- [ ] **Step 5: Commit**

```
git add internal/snapshot/
git commit -m "feat(snapshot): decode focused_workspace_id"
```

---

### Task 2: herdr.FocusWorkspace and herdr.Resize

Both new CLI wrappers land together so the client is finished before its consumers.

**Files:**
- Modify: `internal/herdr/client.go`
- Test: `internal/herdr/client_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/herdr/client_test.go`:

```go
func TestFocusWorkspaceArgv(t *testing.T) {
	r := &FakeRunner{}
	if err := New(r).FocusWorkspace("w3"); err != nil {
		t.Fatal(err)
	}
	want := []string{"workspace", "focus", "w3"}
	if !reflect.DeepEqual(r.Calls[0], want) {
		t.Fatalf("argv = %v, want %v", r.Calls[0], want)
	}
}

func TestResizeArgv(t *testing.T) {
	r := &FakeRunner{}
	c := New(r)
	if err := c.Resize("w1:p2", 0.15); err != nil {
		t.Fatal(err)
	}
	if err := c.Resize("w1:p2", -0.2); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"pane", "resize", "--pane", "w1:p2", "--direction", "right", "--amount", "0.1500"},
		{"pane", "resize", "--pane", "w1:p2", "--direction", "left", "--amount", "0.2000"},
	}
	if !reflect.DeepEqual(r.Calls, want) {
		t.Fatalf("argv = %v, want %v", r.Calls, want)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/herdr/ -run 'FocusWorkspace|ResizeArgv'`
Expected: FAIL to build, `FocusWorkspace undefined` and `Resize undefined`.

- [ ] **Step 3: Implement**

Add to `internal/herdr/client.go`, next to `FocusAgent`:

```go
// FocusWorkspace focuses workspaceID, switching the UI to its active tab. It
// is how a row for a workspace with no agent pane is activated.
func (c *Client) FocusWorkspace(workspaceID string) error {
	_, err := c.r.Run("workspace", "focus", workspaceID)
	return err
}

// Resize widens the pane on the left of paneID's split by amount * the tab's
// width, or narrows it when amount is negative. Verified against herdr 0.8.0:
// on a 54-column tab, --amount 0.1 moved a 14-column pane to 19 columns.
func (c *Client) Resize(paneID string, amount float64) error {
	direction := "right"
	if amount < 0 {
		direction = "left"
		amount = -amount
	}
	_, err := c.r.Run("pane", "resize", "--pane", paneID,
		"--direction", direction,
		"--amount", strconv.FormatFloat(amount, 'f', 4, 64))
	return err
}
```

`strconv` is already imported by this file for `Split`'s ratio formatting; confirm before adding an import.

- [ ] **Step 4: Run the package tests**

Run: `go vet ./internal/herdr/ && go test -race ./internal/herdr/ -v`
Expected: every test PASS.

- [ ] **Step 5: Commit**

```
git add internal/herdr/
git commit -m "feat(herdr): add FocusWorkspace and Resize"
```

---

### Task 3: group.Row gains a Kind

Only the field, keeping every existing behaviour identical, so the next task's diff stays readable.

**Files:**
- Modify: `internal/group/group.go`
- Test: `internal/group/group_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/group/group_test.go`:

```go
func TestBuildMarksAgentRows(t *testing.T) {
	s := snapshot.Snapshot{
		Workspaces: []snapshot.Workspace{{WorkspaceID: "w1", Number: 1, Label: "a"}},
		Panes:      []snapshot.Pane{pane("w1:p1", "w1", "/r/a", "claude", "idle", "one")},
	}
	gs := Build(s, mapResolver{"/r/a": {Root: "/r/a"}}, Options{})
	if len(gs) != 1 || len(gs[0].Rows) != 1 {
		t.Fatalf("got %+v", gs)
	}
	if gs[0].Rows[0].Kind != RowAgent {
		t.Fatalf("Kind = %v, want RowAgent", gs[0].Rows[0].Kind)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/group/ -run MarksAgentRows`
Expected: FAIL to build, `undefined: RowAgent`

- [ ] **Step 3: Implement**

In `internal/group/group.go`, above the `Row` type, add:

```go
// RowKind distinguishes the two things a row can stand for.
type RowKind int

const (
	// RowAgent is a pane running a recognized agent.
	RowAgent RowKind = iota
	// RowWorkspace is a workspace with no agent pane at all. It exists so a
	// workspace never disappears from the list just because nothing is
	// running in it, which is what lets the standard herdr sidebar be turned
	// off entirely.
	RowWorkspace
)
```

Add `Kind` as the first field of `Row`, keeping the existing doc comment above the type:

```go
type Row struct {
	Kind            RowKind
	PaneID          string
	WorkspaceID     string
	WorkspaceNumber int
	Agent           string
	Status          string
	Title           string
	Branch          string // non-empty only for linked worktrees
	Focused         bool
}
```

In `Build`, add `Kind: RowAgent,` as the first field of the existing `Row{...}` literal.

- [ ] **Step 4: Run the package tests**

Run: `go vet ./internal/group/ && go test -race ./internal/group/ -v`
Expected: every existing test still PASS plus the new one. `RowAgent` is the zero value, so no existing assertion changes meaning.

- [ ] **Step 5: Commit**

```
git add internal/group/
git commit -m "feat(group): tag rows with a kind"
```

---

### Task 4: Build emits a row for a workspace with no agent

**Files:**
- Modify: `internal/group/group.go`
- Test: `internal/group/group_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/group/group_test.go`. These use the file's existing `pane` and `labelsOf` helpers.

```go
func TestBuildAddsARowForAWorkspaceWithNoAgent(t *testing.T) {
	s := snapshot.Snapshot{
		Workspaces: []snapshot.Workspace{
			{WorkspaceID: "w1", Number: 1, Label: "a"},
			{WorkspaceID: "w2", Number: 2, Label: "bare"},
		},
		Panes: []snapshot.Pane{
			pane("w1:p1", "w1", "/r/a", "claude", "idle", "one"),
			pane("w2:p1", "w2", "/r/b", "", "", ""),
		},
	}
	gs := Build(s, mapResolver{"/r/a": {Root: "/r/a"}, "/r/b": {Root: "/r/b"}}, Options{})
	if len(gs) != 2 {
		t.Fatalf("want 2 groups, got %v", labelsOf(gs))
	}
	var bare *Row
	for i := range gs {
		for j := range gs[i].Rows {
			if gs[i].Rows[j].Kind == RowWorkspace {
				bare = &gs[i].Rows[j]
			}
		}
	}
	if bare == nil {
		t.Fatal("no RowWorkspace produced")
	}
	if bare.WorkspaceID != "w2" || bare.Title != "bare" || bare.WorkspaceNumber != 2 {
		t.Fatalf("row = %+v", *bare)
	}
	if bare.PaneID != "" || bare.Agent != "" {
		t.Fatalf("a workspace row carries no pane or agent: %+v", *bare)
	}
}

func TestBuildOmitsTheWorkspaceRowWhenAnAgentExists(t *testing.T) {
	s := snapshot.Snapshot{
		Workspaces: []snapshot.Workspace{{WorkspaceID: "w1", Number: 1, Label: "a"}},
		Panes: []snapshot.Pane{
			pane("w1:p1", "w1", "/r/a", "claude", "idle", "one"),
			pane("w1:p2", "w1", "/r/a", "", "", ""),
		},
	}
	gs := Build(s, mapResolver{"/r/a": {Root: "/r/a"}}, Options{})
	if len(gs) != 1 || len(gs[0].Rows) != 1 || gs[0].Rows[0].Kind != RowAgent {
		t.Fatalf("got %+v", gs)
	}
}

func TestBuildTakesTheWorkspaceCwdFromTheLowestPane(t *testing.T) {
	s := snapshot.Snapshot{
		Workspaces: []snapshot.Workspace{{WorkspaceID: "w1", Number: 1, Label: "w"}},
		Panes: []snapshot.Pane{
			pane("w1:p7", "w1", "/r/late", "", "", ""),
			pane("w1:p2", "w1", "/r/early", "", "", ""),
		},
	}
	gs := Build(s, mapResolver{"/r/late": {Root: "/r/late"}, "/r/early": {Root: "/r/early"}}, Options{})
	if len(gs) != 1 || gs[0].Key != "/r/early" {
		t.Fatalf("want the lowest-numbered pane's cwd, got %v", labelsOf(gs))
	}
}

func TestBuildIgnoresOwnPanesWhenPickingTheWorkspaceCwd(t *testing.T) {
	own := pane("w1:p1", "w1", "/r/pasture", "", "", "")
	own.Tokens = map[string]string{"pasture": "1"}
	s := snapshot.Snapshot{
		Workspaces: []snapshot.Workspace{{WorkspaceID: "w1", Number: 1, Label: "w"}},
		Panes:      []snapshot.Pane{own, pane("w1:p2", "w1", "/r/real", "", "", "")},
	}
	gs := Build(s, mapResolver{"/r/pasture": {Root: "/r/pasture"}, "/r/real": {Root: "/r/real"}},
		Options{SelfToken: "pasture"})
	if len(gs) != 1 || gs[0].Key != "/r/real" {
		t.Fatalf("got %v", labelsOf(gs))
	}
}

func TestBuildExcludeAppliesToWorkspaceRows(t *testing.T) {
	s := snapshot.Snapshot{
		Workspaces: []snapshot.Workspace{{WorkspaceID: "w1", Number: 1, Label: "w"}},
		Panes:      []snapshot.Pane{pane("w1:p1", "w1", "/tmp/x", "", "", "")},
	}
	gs := Build(s, mapResolver{"/tmp/x": {Root: "/tmp/x"}},
		Options{Exclude: func(cwd string) bool { return cwd == "/tmp/x" }})
	if len(gs) != 0 {
		t.Fatalf("want no groups, got %v", labelsOf(gs))
	}
}

func TestBuildWorkspaceRowFallsBackToTheWorkspaceID(t *testing.T) {
	s := snapshot.Snapshot{
		Workspaces: []snapshot.Workspace{{WorkspaceID: "w1", Number: 1, Label: ""}},
		Panes:      []snapshot.Pane{pane("w1:p1", "w1", "/r/a", "", "", "")},
	}
	gs := Build(s, mapResolver{"/r/a": {Root: "/r/a"}}, Options{})
	if len(gs) != 1 || gs[0].Rows[0].Title != "w1" {
		t.Fatalf("got %+v", gs)
	}
}

func TestBuildMarksTheFocusedWorkspaceRow(t *testing.T) {
	s := snapshot.Snapshot{
		FocusedWorkspaceID: "w1",
		Workspaces:         []snapshot.Workspace{{WorkspaceID: "w1", Number: 1, Label: "w"}},
		Panes:              []snapshot.Pane{pane("w1:p1", "w1", "/r/a", "", "", "")},
	}
	gs := Build(s, mapResolver{"/r/a": {Root: "/r/a"}}, Options{})
	if len(gs) != 1 || !gs[0].Rows[0].Focused {
		t.Fatalf("focused flag not set: %+v", gs)
	}
}

func TestBuildOrdersAgentAndWorkspaceRowsDeterministically(t *testing.T) {
	s := snapshot.Snapshot{
		Workspaces: []snapshot.Workspace{
			{WorkspaceID: "w1", Number: 1, Label: "one"},
			{WorkspaceID: "w2", Number: 2, Label: "two"},
		},
		Panes: []snapshot.Pane{
			pane("w2:p1", "w2", "/r", "", "", ""),
			pane("w1:p1", "w1", "/r", "claude", "idle", "agent"),
		},
	}
	gs := Build(s, mapResolver{"/r": {Root: "/r"}}, Options{})
	if len(gs) != 1 || len(gs[0].Rows) != 2 {
		t.Fatalf("got %+v", gs)
	}
	if gs[0].Rows[0].Kind != RowAgent || gs[0].Rows[1].Kind != RowWorkspace {
		t.Fatalf("workspace 1's agent must sort before workspace 2's row: %+v", gs[0].Rows)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/group/ -run 'Workspace|OrdersAgentAnd'`
Expected: FAIL. `TestBuildAddsARowForAWorkspaceWithNoAgent` reports "want 2 groups, got [a]" because a pane with no agent is currently skipped outright.

- [ ] **Step 3: Implement**

Replace the whole body of `Build` in `internal/group/group.go` with the following. The agent path is unchanged apart from where the skip conditions sit; everything from `hasAgent` onward is new.

```go
func Build(s snapshot.Snapshot, r Resolver, opt Options) []Group {
	wsNumber := map[string]int{}
	wsLabel := map[string]string{}
	for _, w := range s.Workspaces {
		wsNumber[w.WorkspaceID] = w.Number
		wsLabel[w.WorkspaceID] = w.Label
	}
	byKey := map[string]*Group{}
	groupFor := func(key string) *Group {
		g, ok := byKey[key]
		if !ok {
			g = &Group{Key: key}
			byKey[key] = g
		}
		return g
	}
	number := func(workspaceID string) int {
		n, known := wsNumber[workspaceID]
		if !known {
			return unknownOrder
		}
		return n
	}

	// hasAgent records which workspaces produced at least one agent row, so
	// the second pass can skip them: an agent row already shows that the
	// workspace exists.
	hasAgent := map[string]bool{}
	// A snapshot workspace carries no cwd, so a workspace's directory has to
	// come from one of its panes. The lowest pane number is used so the
	// choice stays the same across polls even when panes disagree.
	type cwdCandidate struct {
		paneNum int
		cwd     string
	}
	candidates := map[string]cwdCandidate{}
	note := func(p snapshot.Pane) {
		if c, ok := candidates[p.WorkspaceID]; !ok || paneNumber(p.PaneID) < c.paneNum {
			candidates[p.WorkspaceID] = cwdCandidate{paneNumber(p.PaneID), p.Cwd}
		}
	}

	for _, p := range s.Panes {
		if opt.SelfToken != "" {
			if _, ok := p.Tokens[opt.SelfToken]; ok {
				continue
			}
		}
		if opt.Exclude != nil && opt.Exclude(p.Cwd) {
			continue
		}
		note(p)
		if p.Agent == "" {
			continue
		}
		hasAgent[p.WorkspaceID] = true
		info := r.Resolve(p.Cwd)
		key := info.Root
		if key == "" {
			key = p.Cwd
		}
		title := p.Title
		if title == "" {
			title = p.Agent + " " + p.PaneID
		}
		branch := ""
		if info.IsWorktree {
			branch = info.Branch
		}
		g := groupFor(key)
		g.Rows = append(g.Rows, Row{
			Kind:            RowAgent,
			PaneID:          p.PaneID,
			WorkspaceID:     p.WorkspaceID,
			WorkspaceNumber: number(p.WorkspaceID),
			Agent:           p.Agent,
			Status:          p.AgentStatus,
			Title:           title,
			Branch:          branch,
			Focused:         p.PaneID == s.FocusedPaneID,
		})
	}

	// A workspace with no agent row still gets one row, so it stays visible
	// and clickable.
	for workspaceID, c := range candidates {
		if hasAgent[workspaceID] {
			continue
		}
		info := r.Resolve(c.cwd)
		key := info.Root
		if key == "" {
			key = c.cwd
		}
		title := wsLabel[workspaceID]
		if title == "" {
			title = workspaceID
		}
		g := groupFor(key)
		g.Rows = append(g.Rows, Row{
			Kind:            RowWorkspace,
			WorkspaceID:     workspaceID,
			WorkspaceNumber: number(workspaceID),
			Title:           title,
			Focused:         workspaceID == s.FocusedWorkspaceID,
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
		sort.Slice(g.Rows, func(i, j int) bool {
			a, b := g.Rows[i], g.Rows[j]
			if a.WorkspaceNumber != b.WorkspaceNumber {
				return a.WorkspaceNumber < b.WorkspaceNumber
			}
			// A workspace row has no pane id, so Kind breaks the tie before
			// the pane comparison would see two empty strings.
			if a.Kind != b.Kind {
				return a.Kind < b.Kind
			}
			an, bn := paneNumber(a.PaneID), paneNumber(b.PaneID)
			if an != bn {
				return an < bn
			}
			if a.PaneID != b.PaneID {
				return a.PaneID < b.PaneID
			}
			return a.WorkspaceID < b.WorkspaceID
		})
		groups = append(groups, *g)
	}
	// Label alone isn't unique when uniqueLabels still has a collision left
	// over (e.g. two keys that share both basename and parent basename), so
	// fall back to Key, which is unique by construction, to keep the order a
	// total order and therefore stable across calls.
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Label != groups[j].Label {
			return groups[i].Label < groups[j].Label
		}
		return groups[i].Key < groups[j].Key
	})
	return groups
}
```

- [ ] **Step 4: Run the package tests**

Run: `go vet ./internal/group/ && go test -race ./internal/group/ -v`
Expected: all PASS, including the pre-existing determinism tests.

- [ ] **Step 5: Check gofmt**

Run: `gofmt -l internal/group/`
Expected: prints nothing.

- [ ] **Step 6: Commit**

```
git add internal/group/
git commit -m "feat(group): list workspaces that have no agent pane"
```

---

### Task 5: ui focuses a workspace row

**Files:**
- Modify: `internal/ui/model.go`, `internal/ui/view.go`
- Test: `internal/ui/model_test.go`, `internal/ui/view_test.go`

- [ ] **Step 1: Extend the test fake**

In `internal/ui/model_test.go`, add a field to `fakeFetcher` and a method beside the existing `FocusAgent`:

```go
	focusedWS []string
```

```go
func (f *fakeFetcher) FocusWorkspace(id string) error {
	f.focusedWS = append(f.focusedWS, id)
	return nil
}
```

- [ ] **Step 2: Write the failing tests**

Add to `internal/ui/model_test.go`. Build the model the way the neighbouring tests do; if your copy has a `loaded` helper that seeds groups, use it and skip the manual `snapshotMsg`.

```go
func TestEnterOnAWorkspaceRowFocusesTheWorkspace(t *testing.T) {
	f := &fakeFetcher{}
	m := New(f, mapResolver{}, group.Options{}, time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	m, _ = m.Update(snapshotMsg{gen: m.gen, groups: []group.Group{{
		Key: "/r/a", Label: "a",
		Rows: []group.Row{{Kind: group.RowWorkspace, WorkspaceID: "w2", Title: "bare"}},
	}}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // cursor onto the row
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(cmd)
	if len(f.focusedWS) != 1 || f.focusedWS[0] != "w2" {
		t.Fatalf("focusedWS = %v", f.focusedWS)
	}
	if len(f.focused) != 0 {
		t.Fatalf("must not focus a pane: %v", f.focused)
	}
}

func TestEnterOnAnAgentRowStillFocusesThePane(t *testing.T) {
	f := &fakeFetcher{}
	m := New(f, mapResolver{}, group.Options{}, time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	m, _ = m.Update(snapshotMsg{gen: m.gen, groups: []group.Group{{
		Key: "/r/a", Label: "a",
		Rows: []group.Row{{Kind: group.RowAgent, PaneID: "w1:p1", Title: "one"}},
	}}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(cmd)
	if len(f.focused) != 1 || f.focused[0] != "w1:p1" {
		t.Fatalf("focused = %v", f.focused)
	}
	if len(f.focusedWS) != 0 {
		t.Fatalf("must not focus a workspace: %v", f.focusedWS)
	}
}
```

Add to `internal/ui/view_test.go`:

```go
func TestViewRendersAWorkspaceRow(t *testing.T) {
	f := &fakeFetcher{}
	m := New(f, mapResolver{}, group.Options{}, time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
	m, _ = m.Update(snapshotMsg{gen: m.gen, groups: []group.Group{{
		Key: "/r/a", Label: "a",
		Rows: []group.Row{{Kind: group.RowWorkspace, WorkspaceID: "w2", Title: "bare"}},
	}}})
	out := m.View()
	if !strings.Contains(out, "bare") {
		t.Fatalf("workspace label missing: %q", out)
	}
	if !strings.Contains(out, workspaceIcon) {
		t.Fatalf("workspace icon missing: %q", out)
	}
}
```

- [ ] **Step 3: Run them and watch them fail**

Run: `go test ./internal/ui/ -run 'WorkspaceRow|WorkspaceFocuses|AgentRowStill'`
Expected: FAIL to build — `undefined: workspaceIcon`, and `*fakeFetcher` does not satisfy `Fetcher` until the interface gains the method.

- [ ] **Step 4: Extend the Fetcher interface**

In `internal/ui/model.go`:

```go
// Fetcher is the slice of herdr.Client the UI depends on.
type Fetcher interface {
	Snapshot() (snapshot.Snapshot, error)
	FocusAgent(paneID string) error
	FocusWorkspace(workspaceID string) error
}
```

- [ ] **Step 5: Dispatch on the row kind**

In `internal/ui/model.go`, replace the tail of `activate` — everything after the `if l.header { ... }` block — with:

```go
	row := g.Rows[l.row]
	fetch := m.fetch
	return m, func() tea.Msg {
		// A row that has vanished simply disappears on the next poll, so
		// neither error is worth surfacing.
		if row.Kind == group.RowWorkspace {
			_ = fetch.FocusWorkspace(row.WorkspaceID)
		} else {
			_ = fetch.FocusAgent(row.PaneID)
		}
		return nil
	}
```

- [ ] **Step 6: Render the workspace icon**

In `internal/ui/view.go`, add to the const block at the top:

```go
	// workspaceIcon marks a workspace with no agent running in it.
	workspaceIcon = "◦"
```

In the row branch of `View`, replace the icon selection with:

```go
			var icon string
			if r.Kind == group.RowWorkspace {
				icon = dimStyle.Render(workspaceIcon)
			} else {
				icon = statusIcons[r.Status]
				if icon == "" {
					icon = statusIcons["unknown"]
				}
				if st, ok := statusStyles[r.Status]; ok {
					icon = st.Render(icon)
				}
			}
```

Keep the rest of the row rendering (the `[branch]` prefix, truncation and focused styling) exactly as it is; a workspace row simply has an empty `Branch`.

Add `"github.com/ayumu-1212/herdr-pasture/internal/group"` to `view.go`'s imports if it is not already there.

- [ ] **Step 7: Run the package tests**

Run: `go vet ./internal/ui/ && go test -race ./internal/ui/ -v`
Expected: all PASS.

- [ ] **Step 8: Check gofmt**

Run: `gofmt -l internal/ui/`
Expected: prints nothing.

- [ ] **Step 9: Commit**

```
git add internal/ui/
git commit -m "feat(ui): focus a workspace from its row"
```

---

### Task 6: config.WidthColumns

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/config/config_test.go`:

```go
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
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/config/ -run WidthColumns`
Expected: FAIL to build, `cfg.WidthColumns undefined`

- [ ] **Step 3: Implement**

In `internal/config/config.go`, add the field to `Config` after `WidthRatio`:

```go
	// WidthColumns fixes the dock at this many columns. 0 (the default) uses
	// WidthRatio instead, which is a share of the tab width.
	WidthColumns int `toml:"width_columns"`
```

and in `Load`, after the `PollIntervalMs` check:

```go
	if cfg.WidthColumns < 0 {
		warnings = append(warnings, fmt.Sprintf("config: width_columns %d is negative, using width_ratio", cfg.WidthColumns))
		cfg.WidthColumns = 0
	}
```

`Default()` needs no change: the zero value is the intended default.

- [ ] **Step 4: Run the package tests**

Run: `go vet ./internal/config/ && go test -race ./internal/config/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```
git add internal/config/
git commit -m "feat(config): add width_columns"
```

---

### Task 7: dock sizes and re-applies a fixed width

**Files:**
- Modify: `internal/dock/dock.go`
- Test: `internal/dock/dock_test.go`

- [ ] **Step 1: Add Resize to the fake**

In `internal/dock/dock_test.go`, add beside the other `fakeClient` methods:

```go
func (f *fakeClient) Resize(paneID string, amount float64) error {
	f.rec("resize", paneID, strconv.FormatFloat(amount, 'f', 4, 64))
	return f.errs["resize"]
}
```

`strconv` is already imported by this file; confirm before adding the import.

- [ ] **Step 2: Write the failing tests**

Read `oneTab` first and note the width of the layout it builds; the assertions below assume the tab's `Area.Width` is 200. If it differs, set `c.layout.Area.Width = 200` at the top of each test below and say so in your report.

```go
// widthDeps builds Deps with a column target so the ratio and resize
// arithmetic can be asserted exactly.
func widthDeps(t *testing.T, c *fakeClient, columns int) Deps {
	t.Helper()
	d := deps(t, c)
	d.Cfg.WidthColumns = columns
	return d
}

func argOf(calls []call, name string, i int) string {
	for _, c := range calls {
		if c.Name == name {
			return c.Args[i]
		}
	}
	return ""
}

func TestEnsureSplitsToTheColumnTarget(t *testing.T) {
	c := oneTab()
	c.layout.Area.Width = 200
	if err := Ensure(widthDeps(t, c, 50), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if got := argOf(c.calls, "split", 1); got != "0.25" {
		t.Fatalf("split ratio = %q, want 0.25 (50 of 200 columns)", got)
	}
}

func TestEnsureResizesAnExistingDockThatDrifted(t *testing.T) {
	c := oneTab()
	c.layout.Area.Width = 200
	c.panes = append(c.panes, snapshot.Pane{
		PaneID: "w1:p5", TabID: "w1:t1", Label: Label,
		Tokens: map[string]string{Token: "77"},
	})
	c.layout.Panes = append(c.layout.Panes, snapshot.LayoutPane{
		PaneID: "w1:p5", Rect: snapshot.Rect{X: 0, Y: 1, Width: 20, Height: 40},
	})
	if err := Ensure(widthDeps(t, c, 50), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if got := argOf(c.calls, "resize", 1); got != "0.1500" {
		t.Fatalf("resize amount = %q, want 0.1500 ((50-20)/200)", got)
	}
	if got := argOf(c.calls, "resize", 0); got != "w1:p5" {
		t.Fatalf("resized %q, want the dock pane", got)
	}
	if argOf(c.calls, "split", 0) != "" {
		t.Fatalf("a live dock must not be split again: %v", c.calls)
	}
}

func TestEnsureLeavesAWidthWithinOneColumnAlone(t *testing.T) {
	c := oneTab()
	c.layout.Area.Width = 200
	c.panes = append(c.panes, snapshot.Pane{
		PaneID: "w1:p5", TabID: "w1:t1", Label: Label,
		Tokens: map[string]string{Token: "77"},
	})
	c.layout.Panes = append(c.layout.Panes, snapshot.LayoutPane{
		PaneID: "w1:p5", Rect: snapshot.Rect{X: 0, Y: 1, Width: 49, Height: 40},
	})
	if err := Ensure(widthDeps(t, c, 50), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if argOf(c.calls, "resize", 0) != "" {
		t.Fatalf("a one-column drift must not resize: %v", c.calls)
	}
}

func TestEnsureWithoutAColumnTargetNeverResizes(t *testing.T) {
	c := oneTab()
	c.layout.Area.Width = 200
	c.panes = append(c.panes, snapshot.Pane{
		PaneID: "w1:p5", TabID: "w1:t1", Label: Label,
		Tokens: map[string]string{Token: "77"},
	})
	c.layout.Panes = append(c.layout.Panes, snapshot.LayoutPane{
		PaneID: "w1:p5", Rect: snapshot.Rect{X: 0, Y: 1, Width: 20, Height: 40},
	})
	if err := Ensure(deps(t, c), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if argOf(c.calls, "resize", 0) != "" {
		t.Fatalf("width_columns unset must not resize: %v", c.calls)
	}
}

func TestTargetColumnsClampsToTheFloorAndHalfTheTab(t *testing.T) {
	cases := []struct{ columns, tab, want int }{
		{50, 200, 50},
		{5, 200, 22},   // below the readable floor
		{150, 200, 100}, // more than half the tab
		{30, 30, 22},    // tab too narrow for both rules; the floor wins
	}
	for _, tc := range cases {
		if got := targetColumns(tc.columns, tc.tab); got != tc.want {
			t.Errorf("targetColumns(%d, %d) = %d, want %d", tc.columns, tc.tab, got, tc.want)
		}
	}
}
```

- [ ] **Step 3: Run them and watch them fail**

Run: `go test ./internal/dock/ -run 'ColumnTarget|Drifted|WithinOneColumn|WithoutAColumn|targetColumns'`
Expected: FAIL to build, `undefined: targetColumns` and `Resize` missing from the `Client` interface.

- [ ] **Step 4: Add Resize to the dock Client interface**

In `internal/dock/dock.go`:

```go
type Client interface {
	PaneList() ([]snapshot.Pane, error)
	Layout(paneID string) (snapshot.Layout, error)
	Split(paneID string, ratio float64, cwd string, env map[string]string) (string, error)
	Swap(source, target string) error
	RunInPane(paneID, command string) error
	Rename(paneID, label string) error
	Close(paneID string) error
	Resize(paneID string, amount float64) error
}
```

- [ ] **Step 5: Add targetColumns**

In `internal/dock/dock.go`:

```go
// minColumns is the narrowest the list stays readable at. The UI applies the
// same floor when it has no size yet.
const minColumns = 22

// targetColumns clamps a configured column count to what a tab of tabWidth can
// actually give the dock: never below minColumns, and never more than half the
// tab. On a tab too narrow for both rules the floor wins and the dock takes
// more than half rather than becoming unreadable.
func targetColumns(columns, tabWidth int) int {
	limit := tabWidth / 2
	if limit < minColumns {
		limit = minColumns
	}
	if columns > limit {
		return limit
	}
	if columns < minColumns {
		return minColumns
	}
	return columns
}
```

- [ ] **Step 6: Make leftmost return the layout**

`leftmost` already fetches the layout; returning it avoids a second call. Change its signature to:

```go
func leftmost(d Deps, panes []snapshot.Pane, tabID string) (snapshot.Pane, snapshot.Layout, bool) {
```

Return `snapshot.Pane{}, snapshot.Layout{}, false` on each existing failure path and `p, layout, true` on success. Update its only call site in `openLocked`:

```go
	anchor, layout, ok := leftmost(d, panes, tabID)
	if !ok {
		return nil // leftmost logged why
	}
```

- [ ] **Step 7: Use the column target when splitting**

In `openLocked`, replace the ratio passed to `Split`:

```go
	// WidthRatio goes to --ratio unchanged; see the note on Split below.
	// A column target overrides it by converting to the same ratio.
	ratio := d.Cfg.WidthRatio
	if d.Cfg.WidthColumns > 0 && layout.Area.Width > 0 {
		ratio = float64(targetColumns(d.Cfg.WidthColumns, layout.Area.Width)) / float64(layout.Area.Width)
	}
	newID, err := d.Client.Split(anchor.PaneID, ratio, anchor.Cwd, env)
```

Keep the existing comment above the `Split` call that records the verified `--ratio` and `swap` semantics.

- [ ] **Step 8: Re-apply the width when the dock already exists**

In `openLocked`, the early return for a live dock currently reads `if live != "" { return nil }`. Replace it with:

```go
	if live != "" {
		applyWidth(d, live, tabID)
		return nil
	}
```

and add:

```go
// applyWidth nudges an existing dock back to the configured column count, so a
// terminal resize (which herdr honours proportionally) does not change the
// dock's width. It is a no-op unless width_columns is set, and it ignores a
// drift of a single column so rounding cannot make the pane twitch on every
// focus event. A width the user set by hand is reset the same way; that is the
// stated trade-off of asking for a fixed width.
func applyWidth(d Deps, paneID, tabID string) {
	if d.Cfg.WidthColumns <= 0 {
		return
	}
	layout, err := d.Client.Layout(paneID)
	if err != nil {
		d.logf("width of tab %s: layout unavailable (%v); leaving it alone", tabID, err)
		return
	}
	if layout.Area.Width <= 0 {
		return
	}
	current := 0
	for _, lp := range layout.Panes {
		if lp.PaneID == paneID {
			current = lp.Rect.Width
		}
	}
	if current == 0 {
		return
	}
	delta := targetColumns(d.Cfg.WidthColumns, layout.Area.Width) - current
	if delta > -2 && delta < 2 {
		return
	}
	if err := d.Client.Resize(paneID, float64(delta)/float64(layout.Area.Width)); err != nil {
		d.logf("width of tab %s: resize failed (%v); leaving it alone", tabID, err)
	}
}
```

- [ ] **Step 9: Run the package tests**

Run: `go vet ./internal/dock/ && go test -race ./internal/dock/ -v`
Expected: all PASS, including every pre-existing lock, snooze and corpse test.

- [ ] **Step 10: Check gofmt**

Run: `gofmt -l internal/dock/`
Expected: prints nothing.

- [ ] **Step 11: Commit**

```
git add internal/dock/
git commit -m "feat(dock): fix the dock at a column count"
```

---

### Task 8: README and whole-repo verification

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document the workspace row**

In the bullet list near the top of `README.md`, after the bullet about panes without an agent, add:

```markdown
- A workspace with no agent running in it gets one `◦` row so it stays
  reachable; clicking it switches to that workspace.
```

The bullet currently reading "Panes without an agent are not listed, and neither are pasture's own panes." should become "Panes without an agent are not listed individually, and neither are pasture's own panes." so it no longer contradicts the new bullet.

- [ ] **Step 2: Document width_columns**

In the configuration TOML block, add:

```toml
width_columns = 0         # >0: fix the dock at this many columns, ignoring width_ratio
```

and below the block:

```markdown
`width_columns` wins over `width_ratio` when it is 1 or more. The dock is
re-measured on every focus event and nudged back to that column count, so it
keeps its width when the terminal is resized. It is clamped to at least 22
columns and at most half the tab.
```

- [ ] **Step 3: Document replacing the sidebar**

Add a section immediately before `## Known limitations`:

```markdown
## Replacing the herdr sidebar

Every workspace appears in the list, so pasture can stand in for the standard
sidebar. Turn that off in `~/.config/herdr/config.toml`:

    [ui]
    sidebar_start_collapsed = true
    sidebar_collapsed_mode = "hidden"

`hidden` gives the collapsed sidebar zero width, so the pasture pane becomes the
leftmost column. The change takes effect on the next herdr launch.
```

- [ ] **Step 4: Add the new limitation**

In `## Known limitations`:

```markdown
- With `width_columns` set, a width you change by hand is reset to the target on
  the next focus event.
```

- [ ] **Step 5: Verify the whole repo**

Run each command and confirm the stated result:

```
sh scripts/build.sh && ./bin/pasture version
go vet ./...
go test -race -count=1 ./...
gofmt -l .
```

Expected: `pasture 0.1.0`; no vet output; `ok` for all six internal packages; no gofmt output.

- [ ] **Step 6: Commit**

```
git add README.md
git commit -m "docs: workspace rows, width_columns and replacing the sidebar"
```

---

### Task 9: Real-session verification

Manual, in a throwaway herdr session. Never touch the user's live session, and never link the plugin into it.

- [ ] **Step 1: Start an isolated server**

```
herdr --session pasture-v2 server >/tmp/pasture-v2.log 2>&1 &
sleep 3
```

The socket is `~/.config/herdr/sessions/pasture-v2/herdr.sock`. Pass it as `HERDR_SOCKET_PATH` on every command below.

- [ ] **Step 2: Prepare a state dir and a config dir**

```
mkdir -p /tmp/pasture-v2-state /tmp/pasture-v2-cfg
printf 'width_columns = 30\n' > /tmp/pasture-v2-cfg/config.toml
```

- [ ] **Step 3: Create one workspace with an agent and one without**

```
herdr workspace create --cwd <this repo> --label withagent --no-focus
herdr workspace create --cwd /tmp --label bare --no-focus
herdr pane report-agent w1:p1 --source test --agent claude --state working
```

`report-agent` marks a pane as an agent without starting a real one, which keeps the check cheap.

- [ ] **Step 4: Open the dock**

Run `./bin/pasture toggle` with `HERDR_SOCKET_PATH`, `HERDR_PLUGIN_STATE_DIR=/tmp/pasture-v2-state`, `HERDR_PLUGIN_CONFIG_DIR=/tmp/pasture-v2-cfg`, `HERDR_BIN_PATH=$(command -v herdr)` and `HERDR_TAB_ID=w1:t1`, with `HERDR_PANE_ID`, `HERDR_WORKSPACE_ID` and `HERDR_ENV` unset so the live session's values cannot leak in.

- [ ] **Step 5: Confirm the width and the rows**

```
herdr pane layout --pane w1:p1
herdr pane read <dock-pane-id> --source visible --lines 20
```

Expected: the dock's `rect.width` is 30 when the tab is at least 60 columns wide, or half the tab when narrower. The rendered list shows a `◦ bare` row under a `tmp` group and a `◐` agent row under this repository's group.

- [ ] **Step 6: Confirm a workspace row switches workspace**

Move the cursor onto the `◦ bare` row with `herdr pane send-keys <dock> down` (repeat as needed, reading the pane between presses to see the `›` marker), then send `enter`, then:

```
herdr api snapshot
```

Expected: `focused_workspace_id` is the bare workspace's id.

- [ ] **Step 7: Confirm the width is restored after a manual resize**

```
herdr pane resize --pane <dock-pane-id> --direction right --amount 0.2
```

Re-run `./bin/pasture ensure` with the same environment as Step 4, then `herdr pane layout --pane w1:p1`.

Expected: the width is back to the target.

- [ ] **Step 8: Tear down**

```
herdr server stop
rm -rf /tmp/pasture-v2-state /tmp/pasture-v2-cfg /tmp/pasture-v2.log
```

Then run `herdr pane list` against the user's default socket and confirm the pane count is unchanged and no pane is labelled `pasture`.

- [ ] **Step 9: Record the result**

Append the verified behaviours to the `## Tested with` section of `README.md` and commit:

```
git add README.md
git commit -m "docs: record v0.2 real-session verification"
```

---

## Self-review

- **Spec coverage:** §3.1 row kinds → Task 3. §3.2 input change and §3.3 workspace cwd → Task 4. §3.4 ordering → Task 4's comparator plus the pre-existing determinism tests. §3.5 click dispatch → Tasks 2 and 5. §4.1 config → Task 6. §4.2 split sizing and §4.3 re-apply → Task 7. §5 error handling → Task 4 (no candidate pane, git failure via the existing fallback), Task 6 (negative value), Task 7 (`applyWidth` logs and continues). §6 tests → each task. §8 sidebar removal → Task 8 Step 3.
- **Ordering:** Task 1 lands `FocusedWorkspaceID` before Task 4 reads it, and Task 2 lands `Resize` before Task 7's interface requires it. Executing in order needs no forward references.
- **Placeholders:** none; every step carries either code or an exact command.
- **Type consistency:** `RowKind` / `RowAgent` / `RowWorkspace` from Task 3 are used unchanged in Tasks 4 and 5. `Resize(paneID string, amount float64) error` has the same signature in `herdr.Client`, the `dock.Client` interface and the test fake. `targetColumns(columns, tabWidth int) int` is used by both the split path and `applyWidth`. `leftmost`'s new three-value return is updated at its only call site. `workspaceIcon` is defined in Task 5 Step 6 and referenced by the test in Task 5 Step 2.
