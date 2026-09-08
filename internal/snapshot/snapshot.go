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
