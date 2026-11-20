// Package mobile holds the pieces of the phone-width sidebar that both the UI
// and the dock actions need: deciding whether a tab is being looked at on a
// phone, remembering which tabs pasture zoomed, and choosing the pane a closed
// sidebar gives the screen back to.
//
// On a phone herdr keeps a tab's splits, so the dock would eat a third of a
// 45-column screen. Instead pasture zooms: the dock zoomed is the sidebar open,
// any other pane zoomed is the sidebar closed. Zoom is server state and
// survives the trip back to the desktop, which is why the tabs pasture zoomed
// are marked, so it can unzoom those and only those.
package mobile

import (
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

// Marks records the tabs pasture zoomed, one file per tab under Dir/zoomed.
// A file's content is the pane closing the sidebar returns to; it may be empty.
type Marks struct{ Dir string }

func (m Marks) path(tabID string) string {
	return filepath.Join(m.Dir, "zoomed", SanitizeID(tabID))
}

// Set marks tabID, remembering returnPane.
func (m Marks) Set(tabID, returnPane string) error {
	p := m.path(tabID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(returnPane), 0o644)
}

// Get reports whether tabID is marked and the pane it remembers.
func (m Marks) Get(tabID string) (returnPane string, ok bool) {
	b, err := os.ReadFile(m.path(tabID))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// Clear removes tabID's mark; a tab that was never marked is not an error.
func (m Marks) Clear(tabID string) error {
	err := os.Remove(m.path(tabID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// ReturnPane picks the pane that closing the sidebar zooms: preferred when it
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
