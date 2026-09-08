package group

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

// Row is one agent pane as displayed in the list. TabID is intentionally
// omitted: focusing (and the Focused field below) is keyed by pane id, not
// tab id, so callers never need it.
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

// unknownOrder is the sort position given to a workspace number or pane
// number pasture can't determine (a WorkspaceID absent from
// snapshot.Snapshot.Workspaces, or a PaneID that doesn't match "wM:pN").
// herdr numbers workspaces and panes starting at 1, so this sorts such rows
// after every real one rather than before (map/slice zero value would sort
// them first, ahead of workspace/pane 1).
const unknownOrder = 1 << 30

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
		if opt.SelfToken != "" {
			if _, ok := p.Tokens[opt.SelfToken]; ok {
				continue
			}
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
		wsNum, known := wsNumber[p.WorkspaceID]
		if !known {
			wsNum = unknownOrder
		}
		g.Rows = append(g.Rows, Row{
			PaneID:          p.PaneID,
			WorkspaceID:     p.WorkspaceID,
			WorkspaceNumber: wsNum,
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
		sort.Slice(g.Rows, func(i, j int) bool {
			a, b := g.Rows[i], g.Rows[j]
			if a.WorkspaceNumber != b.WorkspaceNumber {
				return a.WorkspaceNumber < b.WorkspaceNumber
			}
			an, bn := paneNumber(a.PaneID), paneNumber(b.PaneID)
			if an != bn {
				return an < bn
			}
			return a.PaneID < b.PaneID
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

// paneNumber extracts N from "wM:pN"; unknown shapes sort last. It uses the
// last ":p" rather than the first so a workspace id that itself contains
// ":p" can't be mistaken for the pane separator.
func paneNumber(paneID string) int {
	i := strings.LastIndex(paneID, ":p")
	if i < 0 {
		return unknownOrder
	}
	n, err := strconv.Atoi(paneID[i+2:])
	if err != nil {
		return unknownOrder
	}
	return n
}
