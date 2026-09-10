// Package dock places, toggles and redeploys the pasture pane inside herdr tabs.
package dock

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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

	// tokenRetries and tokenWait bound the wait for a freshly spawned UI to
	// stamp its token: 30 x 200ms of sleeping, plus one `herdr pane list` exec
	// per iteration, so the lock is really held for something nearer 8s than
	// the nominal 6s.
	tokenRetries = 30
	tokenWait    = 200 * time.Millisecond
	// lockStale must comfortably exceed that worst-case hold time, or a healthy
	// ensure still waiting for its token would have its lock stolen mid-build.
	lockStale = 30 * time.Second

	// ownerFile holds the nonce of the process that created a lock directory,
	// so a holder that was declared stale never deletes its successor's lock.
	ownerFile = "owner"
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
	Resize(paneID string, amount float64) error
	FocusTab(tabID string) error
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

// normalize fills in the optional fields so a caller that left them nil cannot
// panic a herdr hook. Every entry point calls it first.
func (d Deps) normalize() Deps {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Sleep == nil {
		d.Sleep = time.Sleep
	}
	if d.Log == nil {
		d.Log = io.Discard
	}
	return d
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
	d = d.normalize()
	if !d.Cfg.AutoOpen {
		d.logf("auto_open is false; skipping")
		return nil
	}
	if tabID == "" {
		panes, err := d.Client.PaneList()
		if err != nil {
			return err
		}
		tabID = focusedTab(panes)
	}
	if tabID == "" {
		d.logf("no target tab; skipping")
		return nil
	}
	// Checked before locking as well as inside open: a snoozed tab is the
	// common case on a busy session, and four hooks contending for a lock only
	// to do nothing is pure latency.
	if snoozed(d, tabID) {
		d.logf("tab %s is snoozed; skipping", tabID)
		return nil
	}
	return open(d, tabID)
}

// Startup restores the docks a herdr server restart left dead. Restarting
// rebuilds the pane but not the process inside it, so a dock comes back as a
// bare shell carrying Label without Token, and herdr fires no lifecycle event
// for the restore: verified against 0.9.0, a restarted server logs no plugin
// command at all, and the empty column sits there until the user happens to
// change focus. This runs from the manifest's [[startup]] hook instead.
//
// It heals only tabs that already had a dock. A tab that never had one is left
// alone: bringing docks back is restoring what the user had, whereas docking a
// new tab is what auto_open decides on focus. For the same reason auto_open is
// not consulted here, so a dock opened by hand with auto_open = false also
// survives a restart.
func Startup(d Deps) error {
	d = d.normalize()
	panes, err := d.Client.PaneList()
	if err != nil {
		return err
	}
	var tabs []string
	seen := map[string]bool{}
	for _, p := range panes {
		if !isPasture(p) || p.Tokens[Token] != "" || seen[p.TabID] {
			continue
		}
		// A live dock elsewhere in the same tab means this is not a restart
		// corpse, so classify has the final say.
		if live, _ := classify(panes, p.TabID); live != "" {
			continue
		}
		seen[p.TabID] = true
		tabs = append(tabs, p.TabID)
	}
	sort.Strings(tabs) // deterministic order, and deterministic logs
	for _, tabID := range tabs {
		if err := restore(d, tabID); err != nil {
			d.logf("restoring tab %s: %v", tabID, err)
		}
	}
	return nil
}

// restore brings tab tabID's dock back. It first tries to revive the pane the
// restart left behind by running the UI in it again: that pane is already on
// the left edge at the width the user had, and its shell survived the restart,
// so reviving costs one command and disturbs no layout. Only if the pane never
// stamps its token does it fall back to openLocked, which closes it and builds
// a fresh one. Rebuilding first is worse right after a restart, when every
// shell in the session is still coming up and a freshly split pane is the least
// likely to be ready for `pane run`.
func restore(d Deps, tabID string) error {
	unlock, ok := acquireLock(d, tabID)
	if !ok {
		d.logf("another pasture command holds tab %s; skipping", tabID)
		return nil
	}
	defer unlock()

	panes, err := d.Client.PaneList()
	if err != nil {
		return err
	}
	live, corpses := classify(panes, tabID)
	if live != "" {
		return nil // something got there first
	}
	if len(corpses) == 1 {
		paneID := corpses[0]
		d.logf("reviving the dock in pane %s after a server restart", paneID)
		if err := d.Client.RunInPane(paneID, shellQuote(d.Bin)+" ui"); err != nil {
			d.logf("reviving %s: %v; rebuilding instead", paneID, err)
		} else if waitForToken(d, paneID) {
			return nil
		} else {
			d.logf("pane %s did not come back; rebuilding it", paneID)
		}
	}
	// More than one corpse, or the revived pane stayed dead: openLocked closes
	// every corpse in the tab and docks a fresh one.
	return openLocked(d, tabID)
}

// open takes tabID's lock and builds the dock under it. A caller that already
// holds the lock (Toggle) must call openLocked instead, or it deadlocks against
// itself.
func open(d Deps, tabID string) error {
	unlock, ok := acquireLock(d, tabID)
	if !ok {
		d.logf("another pasture command holds tab %s; skipping", tabID)
		return nil
	}
	defer unlock()
	return openLocked(d, tabID)
}

// openLocked assumes the caller holds tabID's lock. It reads the pane list
// itself rather than taking one from the caller: every herdr event runs
// `pasture ensure` as its own process, so a list read before the lock can be
// stale by the time the lock is held (the process that went first may already
// have docked this tab) and deciding from it would open a second pane in the
// tab.
//
// The lock excludes other pasture processes, not the user: between leftmost and
// Split they can close the anchor (Split fails and nothing is created) or
// rearrange the tab (the dock lands somewhere unintended and self-corrects on
// the next toggle or redeploy). Neither leaves an orphan.
func openLocked(d Deps, tabID string) error {
	panes, err := d.Client.PaneList()
	if err != nil {
		return err
	}
	live, corpses := classify(panes, tabID)
	if live != "" {
		applyWidth(d, live, tabID)
		return nil
	}
	for _, id := range corpses {
		d.logf("closing dead pasture pane %s", id)
		if err := d.Client.Close(id); err != nil {
			d.logf("close %s: %v", id, err)
		}
	}
	if len(corpses) > 0 {
		if panes, err = d.Client.PaneList(); err != nil {
			return err
		}
	}
	if snoozed(d, tabID) {
		d.logf("tab %s is snoozed; skipping", tabID)
		return nil
	}
	anchor, layout, ok := leftmost(d, panes, tabID)
	if !ok {
		return nil // leftmost logged why
	}
	env := map[string]string{
		"HERDR_PLUGIN_STATE_DIR":  d.StateDir,
		"HERDR_PLUGIN_CONFIG_DIR": config.Dir(),
	}
	// A pane shell inherits HERDR_PANE_ID and friends but not HERDR_BIN_PATH,
	// so without this the UI depends on `herdr` being on the interactive
	// shell's PATH. If that lookup fails it cannot stamp its token either, and
	// every focus event would reap the pane and split another one.
	if bin := os.Getenv("HERDR_BIN_PATH"); bin != "" {
		env["HERDR_BIN_PATH"] = bin
	}
	// WidthRatio goes to --ratio unchanged. Verified against herdr 0.8.0:
	// --ratio is the share kept by the ORIGINAL pane (split w1:p1 --ratio 0.25
	// leaves w1:p1 at 26% and puts the new w1:p2 at 74% on the right), and
	// `pane swap` exchanges the two panes' POSITIONS while each slot keeps its
	// size (after the swap w1:p2 sits in the 26% left slot). So splitting with
	// WidthRatio and swapping the new pane leftwards lands the dock in a slot
	// exactly WidthRatio wide. Do not "fix" this to 1-WidthRatio.
	//
	// Both settings describe the dock's share of the TAB, so they are turned
	// into columns first and then into a ratio of the anchor, which is what
	// --ratio actually measures.
	ratio := d.Cfg.WidthRatio
	if aw := anchorWidth(layout, anchor.PaneID); aw > 0 && layout.Area.Width > 0 {
		want := d.Cfg.WidthColumns
		if want <= 0 {
			want = int(float64(layout.Area.Width)*d.Cfg.WidthRatio + 0.5)
		}
		ratio = splitRatio(want, aw)
	}
	newID, err := d.Client.Split(anchor.PaneID, ratio, anchor.Cwd, env)
	if err != nil {
		return err
	}
	// Label the pane before doing anything else with it. Only Label and Token
	// make a pane recognisably ours, so a pane that dies between Split and
	// Rename is an orphan no later Ensure would ever reap, and every following
	// event would split yet another one.
	if err := d.Client.Rename(newID, Label); err != nil {
		d.logf("rename %s: %v; closing it rather than leaving an unreapable pane", newID, err)
		reap(d, newID)
		return err
	}
	// The anchor goes in as the SOURCE, the dock as the target. `pane swap`
	// exchanges the two positions whichever way round they are handed over, but
	// it also focuses the source pane, so naming the dock there threw away the
	// split's --no-focus: on a tab created by a "new workspace + agent"
	// keybinding the user's keystrokes landed in the list instead of the agent.
	// Passing the anchor keeps focus where the user already was.
	if err := d.Client.Swap(anchor.PaneID, newID); err != nil {
		reap(d, newID)
		return err
	}
	if err := d.Client.RunInPane(newID, shellQuote(d.Bin)+" ui"); err != nil {
		reap(d, newID)
		return err
	}
	if waitForToken(d, newID) {
		return nil
	}
	d.logf("pane %s never stamped its %q token within %s; it carries the label, so the next ensure reaps it",
		newID, Token, time.Duration(tokenRetries)*tokenWait)
	return nil
}

// reap closes a pane we created but could not finish setting up.
func reap(d Deps, paneID string) {
	if err := d.Client.Close(paneID); err != nil {
		d.logf("close half-built pasture pane %s: %v", paneID, err)
	}
}

// minColumns is the narrowest the list stays readable at. The UI applies the
// same floor when it has no size yet.
const minColumns = 22

// dockColumns clamps a wanted dock width to what the pane being split can give
// it: never below minColumns, and never more than half that pane, so the pane
// the dock is carved out of stays usable. On an anchor too narrow for both
// rules the floor wins and the dock takes more than half rather than becoming
// unreadable.
func dockColumns(want, anchorWidth int) int {
	limit := anchorWidth / 2
	if limit < minColumns {
		limit = minColumns
	}
	if want > limit {
		return limit
	}
	if want < minColumns {
		return minColumns
	}
	return want
}

// splitRatio turns a wanted dock width in columns into the --ratio to hand
// herdr. The ratio is a share of the PANE BEING SPLIT, not of the tab: a tab
// already split in half gives its leftmost pane only half the width, and asking
// for 0.25 there produced a dock a quarter of that pane, an eighth of the tab.
// Verified live on a 348-column tab split in half: the dock came out 44 columns
// where width_ratio 0.25 promised 87.
func splitRatio(wantColumns, anchorWidth int) float64 {
	if anchorWidth <= 0 {
		return 0
	}
	return float64(dockColumns(wantColumns, anchorWidth)) / float64(anchorWidth)
}

// anchorWidth returns the width of paneID in layout, or 0 when the layout does
// not mention it.
func anchorWidth(layout snapshot.Layout, paneID string) int {
	for _, lp := range layout.Panes {
		if lp.PaneID == paneID {
			return lp.Rect.Width
		}
	}
	return 0
}

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

// Toggle closes the tab's live pasture pane (and snoozes the tab) or opens one
// (clearing the snooze). It deliberately skips the auto_open gate that Ensure
// applies: auto_open governs only automatic docking, so an explicit toggle must
// still work for a user who turned automatic docking off. The whole
// decide-and-act sequence runs under the tab's lock.
func Toggle(d Deps, tabID string) error {
	d = d.normalize()
	if tabID == "" {
		panes, err := d.Client.PaneList()
		if err != nil {
			return err
		}
		tabID = focusedTab(panes)
	}
	if tabID == "" {
		return errors.New("toggle: no focused tab")
	}
	unlock, ok := acquireLock(d, tabID)
	if !ok {
		// A toggle is an explicit keypress, so say so rather than doing
		// nothing quietly.
		return fmt.Errorf("toggle: another pasture command holds tab %s", tabID)
	}
	defer unlock()

	panes, err := d.Client.PaneList()
	if err != nil {
		return err
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
	// openLocked, not open: this function already holds tabID's lock.
	return openLocked(d, tabID)
}

// Redeploy clears every snooze, then closes each pasture pane (live or dead) and
// builds a fresh one in its place, so every dock ends up on the current build.
// It takes each affected tab's lock in turn; a tab whose lock is held is left
// alone and named in the returned error. A builder holds the lock for at most
// tokenRetries * tokenWait, so the right recovery is to run redeploy again in a
// moment, and naming the tabs tells the user which ones still need it.
//
// It rebuilds rather than waiting for a focus event to do it. herdr 0.9 does not
// publish a plugin event for the user moving around the TUI: verified on 0.9.0,
// switching workspace or tab logs `workspace.focus` and `tab.focus` in the server
// and invokes no hook at all, while the same focus issued over the API invokes
// both `tab.focused` and `pane.focused`. So "the next focus event respawns them"
// could mean never, and a redeploy left a whole session dockless until something
// happened to create a tab. Rebuilding here also makes the documented update
// sequence land on the new build with nothing further to press.
//
// Cost: the rebuild waits for each new pane to stamp its token, so a session with
// many docks takes a few seconds per tab. Redeploy runs from an action, off the
// UI thread, and is rare.
func Redeploy(d Deps) error {
	d = d.normalize()
	panes, err := d.Client.PaneList()
	if err != nil {
		return err
	}
	var tabs []string
	byTab := map[string][]string{}
	for _, p := range panes {
		if !isPasture(p) {
			continue
		}
		if _, seen := byTab[p.TabID]; !seen {
			tabs = append(tabs, p.TabID)
		}
		byTab[p.TabID] = append(byTab[p.TabID], p.PaneID)
	}
	// Snoozes go first: openLocked skips a snoozed tab, so clearing them after
	// the loop would leave exactly the tabs a redeploy is meant to refresh
	// without a dock. They are cleared even when some tabs turn out to be busy.
	if err := os.RemoveAll(filepath.Join(d.StateDir, "snooze")); err != nil {
		return err
	}
	// Each rebuild's `pane swap` focuses the tab it happens in, so a plain
	// left-to-right pass ends with the user sitting in whichever tab came last.
	// Verified on 0.9.0: a redeploy over seven tabs moved both the server focus
	// and the client's view to the last one. Rebuild the tab the user is in last
	// so the focus comes to rest there, and say so again afterwards for the case
	// where that tab has no dock to rebuild.
	home := focusedTab(panes)
	tabs = focusedTabLast(tabs, home)
	var busy []string
	for _, tab := range tabs {
		unlock, ok := acquireLock(d, tab)
		if !ok {
			d.logf("tab %s is busy; leaving its pasture pane(s) in place", tab)
			busy = append(busy, tab)
			continue
		}
		for _, id := range byTab[tab] {
			if err := d.Client.Close(id); err != nil {
				d.logf("close %s: %v", id, err)
			}
		}
		// openLocked, not open: this loop already holds tab's lock. A tab that
		// cannot be rebuilt (its last pane was the dock, say) is logged and the
		// rest carry on; auto_open is not consulted, for the same reason Startup
		// does not consult it — putting back a dock the user had is not the same
		// decision as docking a tab that never had one.
		if err := openLocked(d, tab); err != nil {
			d.logf("rebuilding the dock in tab %s: %v", tab, err)
		}
		unlock()
	}
	if len(tabs) > 0 && home != "" {
		if err := d.Client.FocusTab(home); err != nil {
			d.logf("returning the focus to tab %s: %v", home, err)
		}
	}
	if len(busy) > 0 {
		return fmt.Errorf("redeploy: %d busy tab(s) left undisturbed: %s", len(busy), strings.Join(busy, ", "))
	}
	return nil
}

// focusedTabLast returns tabs with home moved to the end, order otherwise
// preserved. A home that is not in tabs (the user's tab has no dock to rebuild)
// leaves the order alone.
func focusedTabLast(tabs []string, home string) []string {
	if home == "" {
		return tabs
	}
	out := make([]string, 0, len(tabs))
	found := false
	for _, t := range tabs {
		if t == home {
			found = true
			continue
		}
		out = append(out, t)
	}
	if !found {
		return tabs
	}
	return append(out, home)
}

// isPasture reports whether a pane is one of ours. It trusts the label, so a
// user pane manually renamed "pasture" would be adopted (and closed) as ours;
// that is accepted as unlikely rather than defended against.
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

// leftmost picks the pane to split: smallest X, then tallest, in tabID. When
// the layout is unavailable it reports false rather than guessing: docking is
// the whole point of knowing which pane is on the left edge, and a dock spliced
// into the middle of a tab is worse than no dock until the next event.
func leftmost(d Deps, panes []snapshot.Pane, tabID string) (snapshot.Pane, snapshot.Layout, bool) {
	byID := map[string]snapshot.Pane{}
	var sample snapshot.Pane
	for _, p := range panes {
		if p.TabID == tabID {
			byID[p.PaneID] = p
			sample = p
		}
	}
	if len(byID) == 0 {
		d.logf("tab %s has no panes; skipping", tabID)
		return snapshot.Pane{}, snapshot.Layout{}, false
	}
	layout, err := d.Client.Layout(sample.PaneID)
	if err != nil {
		d.logf("layout of tab %s unavailable (%v); not docking, the next event retries", tabID, err)
		return snapshot.Pane{}, snapshot.Layout{}, false
	}
	if len(layout.Panes) == 0 {
		d.logf("layout of tab %s lists no panes; not docking, the next event retries", tabID)
		return snapshot.Pane{}, snapshot.Layout{}, false
	}
	best := layout.Panes[0]
	for _, lp := range layout.Panes[1:] {
		if lp.Rect.X < best.Rect.X || (lp.Rect.X == best.Rect.X && lp.Rect.Height > best.Rect.Height) {
			best = lp
		}
	}
	p, ok := byID[best.PaneID]
	if !ok {
		d.logf("leftmost pane %s of tab %s is missing from the pane list; not docking", best.PaneID, tabID)
		return snapshot.Pane{}, snapshot.Layout{}, false
	}
	return p, layout, true
}

// waitForToken blocks until paneID's UI stamps its token, up to
// tokenRetries * tokenWait. It sleeps before each check: the UI cannot have
// stamped itself in the microseconds since `pane run` typed the command, and
// sleeping after the last check would only delay releasing the lock.
func waitForToken(d Deps, paneID string) bool {
	for i := 0; i < tokenRetries; i++ {
		d.Sleep(tokenWait)
		if hasToken(d, paneID) {
			return true
		}
	}
	return false
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

// sanitizeID turns a herdr id into one safe filename component: everything
// outside [A-Za-z0-9_-] becomes "_", so the ":" and "/" herdr puts in its ids
// cannot survive, and filepath.Base is applied as a second guard against a
// path ever escaping the state dir. Both the lock and the snooze file are
// named through it.
func sanitizeID(tabID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, tabID)
	if safe == "" {
		// filepath.Base("") is ".", which would make snoozePath name the snooze
		// directory itself.
		safe = "_"
	}
	return filepath.Base(safe)
}

func lockPath(d Deps, tabID string) string {
	return filepath.Join(d.StateDir, "ensure."+sanitizeID(tabID)+".lock")
}

// acquireLock creates StateDir/ensure.<tab>.lock. The lock is per tab so that
// building a dock in one tab (which can hold the lock for seconds while the UI
// starts) never silently drops the ensure for another tab. A lock older than
// lockStale is treated as abandoned and taken over.
func acquireLock(d Deps, tabID string) (release func(), ok bool) {
	path := lockPath(d, tabID)
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
		d.logf("stealing lock %s, abandoned for more than %s", path, lockStale)
		if rmErr := os.RemoveAll(path); rmErr != nil {
			d.logf("remove stale lock: %v", rmErr)
			return nil, false
		}
		// Mkdir is atomic, so of several processes that saw the same stale lock
		// exactly one gets the replacement.
		err = os.Mkdir(path, 0o755)
	}
	if err != nil {
		d.logf("acquire lock %s: %v", path, err)
		return nil, false
	}
	nonce, err := newNonce()
	if err == nil {
		err = os.WriteFile(filepath.Join(path, ownerFile), []byte(nonce), 0o644)
	}
	if err != nil {
		d.logf("stamp lock %s: %v", path, err)
		if rmErr := os.RemoveAll(path); rmErr != nil {
			d.logf("remove unstamped lock: %v", rmErr)
		}
		return nil, false
	}
	return func() {
		// Only remove the lock if it is still ours. A process whose lock was
		// declared stale and stolen must not delete the thief's lock, or a
		// third process could acquire it mid-split.
		got, readErr := os.ReadFile(filepath.Join(path, ownerFile))
		if readErr != nil {
			d.logf("lock %s: owner unreadable (%v); leaving it to go stale", path, readErr)
			return
		}
		if string(got) != nonce {
			d.logf("lock %s was taken over by another process; not releasing it", path)
			return
		}
		if err := os.RemoveAll(path); err != nil {
			d.logf("release lock %s: %v", path, err)
		}
	}, true
}

func newNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func snoozePath(d Deps, tabID string) string {
	return filepath.Join(d.StateDir, "snooze", sanitizeID(tabID))
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
