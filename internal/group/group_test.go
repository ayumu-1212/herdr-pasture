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
