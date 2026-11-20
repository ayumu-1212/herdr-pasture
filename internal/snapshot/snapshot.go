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
	ActiveTabID string `json:"active_tab_id"`
}

// Snapshot is the live session state returned by `herdr api snapshot`.
type Snapshot struct {
	Panes              []Pane      `json:"panes"`
	Workspaces         []Workspace `json:"workspaces"`
	FocusedPaneID      string      `json:"focused_pane_id"`
	FocusedWorkspaceID string      `json:"focused_workspace_id"`
	FocusedTabID       string      `json:"focused_tab_id"`
	// Layouts holds every tab's geometry. Each area follows the size of the
	// client that last attached or typed, so it is how a dock learns that the
	// session is being looked at on a phone.
	Layouts []Layout `json:"layouts"`
}

// Layout returns tabID's layout, or false when the snapshot has none.
func (s Snapshot) Layout(tabID string) (Layout, bool) {
	for _, l := range s.Layouts {
		if l.TabID == tabID {
			return l, true
		}
	}
	return Layout{}, false
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

// Layout is the geometry of one tab, as returned by `herdr pane layout` and
// inside `api snapshot`.
type Layout struct {
	TabID       string       `json:"tab_id"`
	WorkspaceID string       `json:"workspace_id"`
	Area        Rect         `json:"area"`
	Panes       []LayoutPane `json:"panes"`
	// Zoomed is per tab and lives in the server, so a zoom made while looking
	// from a phone is still there when the desktop client looks again.
	Zoomed        bool   `json:"zoomed"`
	FocusedPaneID string `json:"focused_pane_id"`
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
