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
	// WidthRatio goes to --ratio unchanged. Verified against herdr 0.8.0:
	// --ratio is the share kept by the ORIGINAL pane (split w1:p1 --ratio 0.25
	// leaves w1:p1 at 26% and puts the new w1:p2 at 74% on the right), and
	// `pane swap` exchanges the two panes' POSITIONS while each slot keeps its
	// size (after the swap w1:p2 sits in the 26% left slot). So splitting with
	// WidthRatio and swapping the new pane leftwards lands the dock in a slot
	// exactly WidthRatio wide. Do not "fix" this to 1-WidthRatio.
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
		d.logf("pane list while waiting for token: %v", err)
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
	if err := os.MkdirAll(d.StateDir, 0o755); err != nil {
		d.logf("create state dir %s: %v", d.StateDir, err)
		return nil, false
	}
	err := os.Mkdir(path, 0o755)
	if errors.Is(err, fs.ErrExist) {
		fi, statErr := os.Stat(path)
		if statErr != nil || d.Now().Sub(fi.ModTime()) <= lockStale {
			return nil, false
		}
		d.logf("stealing stale lock")
		if rmErr := os.RemoveAll(path); rmErr != nil {
			d.logf("remove stale lock: %v", rmErr)
			return nil, false
		}
		err = os.Mkdir(path, 0o755)
	}
	if err != nil {
		d.logf("acquire lock: %v", err)
		return nil, false
	}
	return func() {
		if err := os.Remove(path); err != nil {
			d.logf("release lock: %v", err)
		}
	}, true
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
