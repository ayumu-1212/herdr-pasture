// Package mobile holds the phone-width sidebar that both the UI and the dock
// actions drive: deciding whether a tab is being looked at on a phone,
// remembering which tabs pasture took over, and the herdr calls that open,
// close and restore the sidebar.
//
// On a phone herdr keeps a tab's splits, so the dock would eat a third of a
// 45-column screen. Instead the dock zoomed is the sidebar open, and the dock
// squeezed to herdr's narrowest split, a strip a few columns wide, is the
// sidebar closed: the strip stays on screen to be tapped. Zoom and split ratios
// are server state shared with the desktop, which is why the tabs pasture
// touches are marked, so it can put those and only those back.
package mobile

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

// Phone reports whether the client looking at tab l is phone-sized. herdr
// switches to its mobile layout by the terminal width, and a tab's area starts
// to the right of herdr's own sidebar when that is showing, so the terminal
// width is the area's right edge rather than its width: on a desktop with the
// sidebar out, the tab alone can be narrower than the threshold. A threshold of
// 0 or less turns the feature off, and an empty area means the size is unknown.
func Phone(l snapshot.Layout, threshold int) bool {
	if threshold <= 0 || l.Area.Width <= 0 {
		return false
	}
	return l.Area.X+l.Area.Width <= threshold
}

// Marks records the tabs pasture took over on a phone, one JSON file per tab
// under Dir/zoomed.
type Marks struct{ Dir string }

// Mark is what pasture remembers about a tab it took over.
type Mark struct {
	// Back is the pane closing the sidebar brings forward; it may be empty.
	Back string `json:"back"`
	// Width is the dock's share of the tab's width when the tab was first
	// marked. herdr keeps one split ratio for every client, so this is the
	// width the desktop had, and the one to give back when width_columns does
	// not say. 0 means unknown.
	Width float64 `json:"width"`
}

func (m Marks) path(tabID string) string {
	return filepath.Join(m.Dir, "zoomed", SanitizeID(tabID))
}

// Set writes tabID's mark.
func (m Marks) Set(tabID string, mk Mark) error {
	p := m.path(tabID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(mk)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

// Get reports whether tabID is marked, and its mark. A mark written by v0.4.0
// holds nothing but the pane id, and is read as Back with an unknown width.
func (m Marks) Get(tabID string) (Mark, bool) {
	b, err := os.ReadFile(m.path(tabID))
	if err != nil {
		return Mark{}, false
	}
	var mk Mark
	if json.Unmarshal(b, &mk) != nil {
		return Mark{Back: strings.TrimSpace(string(b))}, true
	}
	return mk, true
}

// Remember marks tabID, or updates its mark, with back as the pane to return
// to; an empty back keeps the one already remembered. The dock's width is
// recorded only for a tab not marked yet: once marked, the dock may already be
// a strip, and remembering that would lose the desktop's width.
func (m Marks) Remember(tabID, back string, l snapshot.Layout, dockID string) error {
	mk, ok := m.Get(tabID)
	if !ok {
		mk.Width = share(l, dockID)
	}
	if back != "" {
		mk.Back = back
	}
	return m.Set(tabID, mk)
}

// Clear removes tabID's mark; a tab that was never marked is not an error.
func (m Marks) Clear(tabID string) error {
	err := os.Remove(m.path(tabID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Herdr is the slice of herdr.Client that opening and closing need.
type Herdr interface {
	Zoom(paneID string, on bool) error
	Resize(paneID string, amount float64) error
}

// squeeze is the resize that turns the dock into the strip. herdr stops a split
// at a ratio of 0.1, so asking for far more than the dock has lands exactly on
// the narrowest split it allows: 5 columns of a 45-column phone.
const squeeze = -0.9

// Open opens tabID's sidebar: the dock zoomed, which also focuses it. The pane
// that had the focus is remembered so closing can bring it back.
func Open(h Herdr, m Marks, l snapshot.Layout, tabID, dockID string) error {
	back := ""
	if l.FocusedPaneID != dockID {
		back = l.FocusedPaneID
	}
	if err := m.Remember(tabID, back, l, dockID); err != nil {
		return err
	}
	return h.Zoom(dockID, true)
}

// Close closes tabID's sidebar onto target: the dock squeezed to the strip, no
// pane zoomed, and the focus on target. The mark is written first, so the dock
// of a tab the user is about to be taken to finds it and stays shut. A dockID
// of "" (a tab whose dock is not up yet) only marks the tab; the dock squeezes
// itself once it starts. herdr has no call to focus an arbitrary pane (agent
// focus refuses a shell), so target is zoomed and unzoomed, which leaves the
// focus on it.
func Close(h Herdr, m Marks, l snapshot.Layout, tabID, dockID, target string) error {
	if err := m.Remember(tabID, target, l, dockID); err != nil {
		return err
	}
	if dockID != "" && Wide(l, dockID) {
		if err := h.Resize(dockID, squeeze); err != nil {
			return err
		}
	}
	if target != "" {
		if err := h.Zoom(target, true); err != nil {
			return err
		}
		return h.Zoom(target, false)
	}
	if l.Zoomed && l.FocusedPaneID != "" {
		return h.Zoom(l.FocusedPaneID, false)
	}
	return nil
}

// Squeeze turns the dock back into the strip when it has grown, as it does when
// ensure builds a dock in a marked tab at the usual width.
func Squeeze(h Herdr, l snapshot.Layout, dockID string) error {
	if !Wide(l, dockID) {
		return nil
	}
	return h.Resize(dockID, squeeze)
}

// Restore gives tabID back to the desktop: unzoomed, the dock desktopColumns
// wide (width_columns, which ensure would enforce anyway) or, without one, at
// the share of the tab it had before the phone, and the mark cleared. With
// neither the split is left alone. The mark goes last, so a failure leaves it
// for the next dock to try again.
func Restore(h Herdr, m Marks, l snapshot.Layout, tabID, dockID string, desktopColumns int) error {
	mk, _ := m.Get(tabID)
	if l.Zoomed {
		focused := l.FocusedPaneID
		if focused == "" {
			focused = dockID
		}
		if err := h.Zoom(focused, false); err != nil {
			return err
		}
	}
	if area := l.Area.Width; area > 0 && dockID != "" {
		want := desktopColumns
		if want <= 0 {
			want = int(mk.Width*float64(area) + 0.5)
		}
		if current := columns(l, dockID); want > 0 && current > 0 {
			if delta := want - current; delta <= -2 || delta >= 2 {
				if err := h.Resize(dockID, float64(delta)/float64(area)); err != nil {
					return err
				}
			}
		}
	}
	return m.Clear(tabID)
}

// Wide reports whether the dock is wider than a strip. The line sits at a fifth
// of the tab, well clear of the tenth a strip takes, so rounding can never make
// a strip look wide and get squeezed on every poll.
func Wide(l snapshot.Layout, dockID string) bool {
	return l.Area.Width > 0 && columns(l, dockID)*5 > l.Area.Width
}

// columns is dockID's width in l, 0 when l does not have it.
func columns(l snapshot.Layout, paneID string) int {
	for _, p := range l.Panes {
		if p.PaneID == paneID {
			return p.Rect.Width
		}
	}
	return 0
}

// share is dockID's width as a share of the tab, 0 when unknown.
func share(l snapshot.Layout, dockID string) float64 {
	c := columns(l, dockID)
	if c == 0 || l.Area.Width <= 0 {
		return 0
	}
	return float64(c) / float64(l.Area.Width)
}

// ReturnPane picks the pane that closing the sidebar brings forward: preferred when it
// is still in the tab and is not the dock, otherwise the largest pane other
// than the dock. It reports false when the dock is all the tab holds.
func ReturnPane(l snapshot.Layout, dockID, preferred string) (string, bool) {
	best, bestArea := "", -1
	for _, p := range l.Panes {
		if p.PaneID == dockID {
			continue
		}
		if p.PaneID == preferred {
			return p.PaneID, true
		}
		if a := p.Rect.Width * p.Rect.Height; a > bestArea {
			best, bestArea = p.PaneID, a
		}
	}
	return best, best != ""
}

// SanitizeID turns a herdr id into one safe filename component: everything
// outside [A-Za-z0-9_-] becomes "_", so the ":" and "/" herdr puts in its ids
// cannot survive, and filepath.Base is applied as a second guard against a
// path ever escaping the state dir.
func SanitizeID(id string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, id)
	if safe == "" {
		// filepath.Base("") is ".", which would name the directory itself.
		safe = "_"
	}
	return filepath.Base(safe)
}
