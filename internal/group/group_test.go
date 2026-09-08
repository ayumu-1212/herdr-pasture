package group

import (
	"math/rand"
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

// TestBuildGroupAndRowOrderIsDeterministic guards against Build's output
// order depending on Go's randomized map iteration. Two groups
// ("/a/x/hp" and "/b/x/hp") collide on basename, and two rows within the
// first group tie on both workspace number and pane number (neither wA nor
// wB appears in s.Workspaces, and both panes are "p1"), so both the group
// sort and the row sort need a total order (a tiebreak beyond Label and
// beyond WorkspaceNumber/pane-number) to be stable across input orderings.
func TestBuildGroupAndRowOrderIsDeterministic(t *testing.T) {
	basePanes := []snapshot.Pane{
		pane("wA:p1", "wA", "/a/x/hp", "claude", "idle", "a1"),
		pane("wB:p1", "wB", "/a/x/hp", "claude", "idle", "a2"),
		pane("wC:p1", "wC", "/b/x/hp", "claude", "idle", "b1"),
	}
	resolver := mapResolver{"/a/x/hp": {Root: "/a/x/hp"}, "/b/x/hp": {Root: "/b/x/hp"}}
	rng := rand.New(rand.NewSource(1))
	var first []Group
	for i := 0; i < 50; i++ {
		panes := append([]snapshot.Pane(nil), basePanes...)
		rng.Shuffle(len(panes), func(i, j int) { panes[i], panes[j] = panes[j], panes[i] })
		gs := Build(snapshot.Snapshot{Panes: panes}, resolver, Options{})
		if first == nil {
			first = gs
			continue
		}
		if !reflect.DeepEqual(gs, first) {
			t.Fatalf("run %d: order differs from first run\ngot  %+v\nwant %+v", i, gs, first)
		}
	}
}

func TestBuildOrdersMalformedPaneIDLast(t *testing.T) {
	s := snapshot.Snapshot{
		Workspaces: []snapshot.Workspace{{WorkspaceID: "w1", Number: 1}},
		Panes: []snapshot.Pane{
			pane("bogus", "w1", "/r", "claude", "idle", "bogus"),
			pane("w1:p1", "w1", "/r", "claude", "idle", "ok"),
		},
	}
	gs := Build(s, mapResolver{"/r": {Root: "/r"}}, Options{})
	got := []string{gs[0].Rows[0].PaneID, gs[0].Rows[1].PaneID}
	want := []string{"w1:p1", "bogus"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestBuildOrdersMissingWorkspaceLast(t *testing.T) {
	s := snapshot.Snapshot{
		Workspaces: []snapshot.Workspace{{WorkspaceID: "w1", Number: 1}},
		Panes: []snapshot.Pane{
			pane("w9:p1", "w9", "/r", "claude", "idle", "unknown-ws"),
			pane("w1:p1", "w1", "/r", "claude", "idle", "known-ws"),
		},
	}
	gs := Build(s, mapResolver{"/r": {Root: "/r"}}, Options{})
	got := []string{gs[0].Rows[0].PaneID, gs[0].Rows[1].PaneID}
	want := []string{"w1:p1", "w9:p1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestBuildWorktreeEmptyBranchYieldsEmptyRowBranch(t *testing.T) {
	s := snapshot.Snapshot{Panes: []snapshot.Pane{
		pane("w1:p1", "w1", "/r/.worktrees/feat", "claude", "idle", "t"),
	}}
	gs := Build(s, mapResolver{"/r/.worktrees/feat": {Root: "/r", IsWorktree: true, Branch: ""}}, Options{})
	if gs[0].Rows[0].Branch != "" {
		t.Fatalf("branch = %q, want empty", gs[0].Rows[0].Branch)
	}
}

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
