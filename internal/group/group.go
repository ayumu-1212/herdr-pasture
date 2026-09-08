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
