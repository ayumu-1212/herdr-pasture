package dock

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ayumu-1212/herdr-pasture/internal/config"
	"github.com/ayumu-1212/herdr-pasture/internal/herdr"
	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

// The real client must satisfy Client: cmd/pasture passes a *herdr.Client here.
var _ Client = (*herdr.Client)(nil)

type call struct {
	Name string
	Args []string
}

// fakeClient serves a mutable pane list and records mutations.
type fakeClient struct {
	panes  []snapshot.Pane
	layout snapshot.Layout
	// layouts holds a layout per tab id for the tests that span more than one
	// tab; when a tab is absent here the single `layout` above is used.
	layouts map[string]snapshot.Layout
	calls   []call
	nextID  string
	// errs maps a call name ("list", "layout", "split", "swap", "run",
	// "rename", "close") to the error that call returns. The attempt is still
	// recorded, but the fake's world is left unchanged.
	errs map[string]error
	// stampAfterList stamps Token on nextID from the stampAfterList'th PaneList
	// call onwards, simulating the UI process stamping itself. 0 never stamps:
	// the UI never came up.
	stampAfterList int
	// ran records the panes RunInPane started the UI in; those are the panes
	// stampAfterList can stamp.
	ran map[string]bool
	// listN counts PaneList calls; beforeList runs at the start of the listN'th
	// call, so a test can inspect or mutate the world between two reads the way
	// a concurrent `pasture ensure` process would.
	listN      int
	beforeList func(f *fakeClient, n int)
}

func (f *fakeClient) rec(name string, args ...string) { f.calls = append(f.calls, call{name, args}) }

func (f *fakeClient) PaneList() ([]snapshot.Pane, error) {
	f.listN++
	if f.beforeList != nil {
		f.beforeList(f, f.listN)
	}
	if err := f.errs["list"]; err != nil {
		return nil, err
	}
	// Model what really happens: `pasture ui`, once run in a pane, stamps that
	// pane's token. Keying on the panes RunInPane touched (rather than on the
	// pane a split created) is what lets a revived pane stamp too.
	if f.stampAfterList > 0 && f.listN >= f.stampAfterList {
		for i := range f.panes {
			if f.ran[f.panes[i].PaneID] {
				f.panes[i].Tokens = map[string]string{Token: "1"}
			}
		}
	}
	return append([]snapshot.Pane(nil), f.panes...), nil
}

func (f *fakeClient) Layout(paneID string) (snapshot.Layout, error) {
	if err := f.errs["layout"]; err != nil {
		return snapshot.Layout{}, err
	}
	// Refuse a pane from a tab this fake has no layout for, rather than
	// silently handing back the wrong geometry and letting a test pass on it.
	for _, p := range f.panes {
		if p.PaneID != paneID {
			continue
		}
		if l, ok := f.layouts[p.TabID]; ok {
			return l, nil
		}
		if f.layout.TabID != "" && p.TabID != f.layout.TabID {
			return snapshot.Layout{}, fmt.Errorf("fake has no layout for tab %s", p.TabID)
		}
	}
	return f.layout, nil
}

func (f *fakeClient) Split(paneID string, ratio float64, cwd string, env map[string]string) (string, error) {
	f.rec("split", paneID, strconv.FormatFloat(ratio, 'f', 2, 64), cwd)
	if err := f.errs["split"]; err != nil {
		return "", err
	}
	tab := ""
	for _, p := range f.panes {
		if p.PaneID == paneID {
			tab = p.TabID
		}
	}
	f.panes = append(f.panes, snapshot.Pane{PaneID: f.nextID, TabID: tab, Cwd: cwd})
	return f.nextID, nil
}

func (f *fakeClient) Swap(source, target string) error {
	f.rec("swap", source, target)
	return f.errs["swap"]
}

func (f *fakeClient) FocusTab(tabID string) error {
	f.rec("focustab", tabID)
	return f.errs["focustab"]
}

func (f *fakeClient) RunInPane(paneID, command string) error {
	f.rec("run", paneID, command)
	if err := f.errs["run"]; err != nil {
		return err
	}
	if f.ran == nil {
		f.ran = map[string]bool{}
	}
	f.ran[paneID] = true
	return nil
}

// Rename records the call and, like herdr, actually labels the pane, so a pane
// abandoned after Rename is a reapable corpse in later reads.
func (f *fakeClient) Rename(paneID, label string) error {
	f.rec("rename", paneID, label)
	if err := f.errs["rename"]; err != nil {
		return err
	}
	for i := range f.panes {
		if f.panes[i].PaneID == paneID {
			f.panes[i].Label = label
		}
	}
	return nil
}

func (f *fakeClient) Close(paneID string) error {
	f.rec("close", paneID)
	if err := f.errs["close"]; err != nil {
		return err
	}
	// A fresh slice, not f.panes[:0]: aliasing the backing array would corrupt
	// every list PaneList already handed out.
	kept := make([]snapshot.Pane, 0, len(f.panes))
	for _, p := range f.panes {
		if p.PaneID != paneID {
			kept = append(kept, p)
		}
	}
	f.panes = kept
	return nil
}

func (f *fakeClient) Resize(paneID string, amount float64) error {
	f.rec("resize", paneID, strconv.FormatFloat(amount, 'f', 4, 64))
	return f.errs["resize"]
}

func (f *fakeClient) has(paneID string) bool {
	for _, p := range f.panes {
		if p.PaneID == paneID {
			return true
		}
	}
	return false
}

func names(calls []call) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Name
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func oneTab() *fakeClient {
	return &fakeClient{
		nextID: "w1:p9",
		panes: []snapshot.Pane{
			{PaneID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1", Cwd: "/r", Agent: "claude", Focused: true},
			{PaneID: "w1:p2", TabID: "w1:t1", WorkspaceID: "w1", Cwd: "/r"},
		},
		layout: snapshot.Layout{TabID: "w1:t1", Panes: []snapshot.LayoutPane{
			{PaneID: "w1:p2", Rect: snapshot.Rect{X: 100, Y: 1, Width: 100, Height: 40}},
			{PaneID: "w1:p1", Rect: snapshot.Rect{X: 0, Y: 1, Width: 100, Height: 40}},
		}},
		stampAfterList: 1,
	}
}

func deps(t *testing.T, c *fakeClient) Deps {
	t.Helper()
	return Deps{
		Client:   c,
		StateDir: t.TempDir(),
		Bin:      "/opt/pasture",
		Cfg:      config.Default(),
		Now:      time.Now,
		Sleep:    func(time.Duration) {},
		Log:      io.Discard,
	}
}

// lockDir is the lock the dock takes for the one tab the fixtures use.
func lockDir(d Deps) string { return filepath.Join(d.StateDir, "ensure.w1_t1.lock") }

func TestEnsureOpensDockOnLeftOfLeftmostPane(t *testing.T) {
	c := oneTab()
	if err := Ensure(deps(t, c), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	// Rename comes second so that a failure in any later step leaves a labelled
	// pane the next Ensure can recognise and reap.
	want := []string{"split", "rename", "swap", "run"}
	if !equal(names(c.calls), want) {
		t.Fatalf("calls = %v, want %v", names(c.calls), want)
	}
	if c.calls[0].Args[0] != "w1:p1" {
		t.Fatalf("should split the leftmost pane, split %v", c.calls[0].Args)
	}
	// --ratio is the share kept by the ORIGINAL pane, but `pane swap` moves the
	// dock into that same left slot without resizing it, so width_ratio goes
	// through unchanged and the dock ends up width_ratio wide.
	if c.calls[0].Args[1] != "0.25" {
		t.Fatalf("split ratio = %q, want %q (width_ratio, unchanged)", c.calls[0].Args[1], "0.25")
	}
	if c.calls[0].Args[2] != "/r" {
		t.Fatalf("split cwd = %q, want the anchor pane's cwd %q", c.calls[0].Args[2], "/r")
	}
	if c.calls[1].Args[0] != "w1:p9" || c.calls[1].Args[1] != Label {
		t.Fatalf("rename args = %v", c.calls[1].Args)
	}
	if c.calls[3].Args[1] != "'/opt/pasture' ui" {
		t.Fatalf("run command = %q", c.calls[3].Args[1])
	}
}

// `herdr pane swap` focuses whichever pane it is handed as --source-pane, so the
// ANCHOR has to be the source. Naming the dock there undoes the split's
// --no-focus and leaves the user typing into the list: on a workspace created
// with --focus (a "new workspace + agent" keybinding) the dock, not the agent,
// came up active. Verified against herdr 0.9.0 on a live session: swapping an
// unfocused pane in as the source moved focused_pane_id onto it, while a focused
// pane named as the source kept focus even though the swap moved it to the other
// slot. The geometry is symmetric, so the dock lands on the left either way.
func TestEnsureSwapsTheAnchorInAsSourceSoTheDockNeverTakesFocus(t *testing.T) {
	c := oneTab()
	if err := Ensure(deps(t, c), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	var swap call
	found := false
	for _, got := range c.calls {
		if got.Name == "swap" {
			swap, found = got, true
		}
	}
	if !found {
		t.Fatalf("no swap among %v", names(c.calls))
	}
	if swap.Args[0] != "w1:p1" {
		t.Fatalf("swap source = %q, want the anchor %q; herdr focuses the source pane", swap.Args[0], "w1:p1")
	}
	if swap.Args[1] != "w1:p9" {
		t.Fatalf("swap target = %q, want the new dock pane %q", swap.Args[1], "w1:p9")
	}
}

func TestEnsureNoopWhenLiveDockExists(t *testing.T) {
	c := oneTab()
	c.panes = append(c.panes, snapshot.Pane{PaneID: "w1:p5", TabID: "w1:t1", Label: Label, Tokens: map[string]string{Token: "77"}})
	if err := Ensure(deps(t, c), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 {
		t.Fatalf("expected no calls, got %v", names(c.calls))
	}
}

// A dock in another tab is not this tab's dock, however live it looks.
func TestEnsureIgnoresLiveDockInAnotherTab(t *testing.T) {
	c := oneTab()
	c.panes = append(c.panes, snapshot.Pane{PaneID: "w2:p5", TabID: "w2:t1", Label: Label, Tokens: map[string]string{Token: "77"}})
	if live, corpses := classify(c.panes, "w1:t1"); live != "" || corpses != nil {
		t.Fatalf("classify(w1:t1) = %q, %v; another tab's dock must not count", live, corpses)
	}
	if err := Ensure(deps(t, c), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if !equal(names(c.calls), []string{"split", "rename", "swap", "run"}) {
		t.Fatalf("calls = %v, want this tab to get its own dock", names(c.calls))
	}
}

func TestEnsureReplacesCorpse(t *testing.T) {
	c := oneTab()
	c.panes = append(c.panes, snapshot.Pane{PaneID: "w1:p5", TabID: "w1:t1", Label: Label})
	if err := Ensure(deps(t, c), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"close", "split", "rename", "swap", "run"}
	if !equal(names(c.calls), want) || c.calls[0].Args[0] != "w1:p5" {
		t.Fatalf("calls = %v", c.calls)
	}
}

func TestEnsureRespectsAutoOpenOff(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	d.Cfg.AutoOpen = false
	if err := Ensure(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 {
		t.Fatalf("expected no calls, got %v", names(c.calls))
	}
}

func TestEnsureRespectsSnooze(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	if err := os.MkdirAll(filepath.Join(d.StateDir, "snooze"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.StateDir, "snooze", "w1_t1"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 {
		t.Fatalf("expected no calls, got %v", names(c.calls))
	}
	// A snoozed tab must not even contend for the lock.
	if c.listN != 0 {
		t.Fatalf("PaneList called %d time(s); a snoozed tab should be settled from disk alone", c.listN)
	}
}

func TestEnsureFallsBackToFocusedTab(t *testing.T) {
	c := oneTab()
	if err := Ensure(deps(t, c), ""); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) == 0 || c.calls[0].Args[0] != "w1:p1" {
		t.Fatalf("calls = %v", c.calls)
	}
}

// Each herdr event runs `pasture ensure` as its own OS process, so a decision
// made from a pane list read BEFORE the lock is stale by the time the lock is
// held: the process that held the lock first may already have docked this tab.
// The list that decides must therefore be read under the lock.
func TestEnsureDecidesFromPaneListReadUnderTheLock(t *testing.T) {
	c := oneTab()
	// Between the pre-lock read that resolves the focused tab and the read
	// taken under the lock, another pasture process wins the race and docks
	// this tab.
	c.beforeList = func(f *fakeClient, n int) {
		if n == 2 {
			f.panes = append(f.panes, snapshot.Pane{
				PaneID: "w1:p7", TabID: "w1:t1", Label: Label,
				Tokens: map[string]string{Token: "77"},
			})
		}
	}
	if err := Ensure(deps(t, c), ""); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 {
		t.Fatalf("another process already docked this tab; expected no calls, got %v", names(c.calls))
	}
	if c.listN < 2 {
		t.Fatalf("PaneList called %d time(s); the deciding read must happen under the lock", c.listN)
	}
}

// The invariant behind the fix above: with the tab known up front, every single
// pane list Ensure takes is read while holding that tab's lock.
func TestEnsureReadsEveryPaneListUnderTheLock(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	lock := lockDir(d)
	var held []bool
	c.beforeList = func(f *fakeClient, n int) {
		_, err := os.Stat(lock)
		held = append(held, err == nil)
	}
	if err := Ensure(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if len(held) == 0 {
		t.Fatal("PaneList was never called")
	}
	for i, ok := range held {
		if !ok {
			t.Fatalf("PaneList call %d of %d ran without %s held", i+1, len(held), lock)
		}
	}
}

func TestEnsureSkipsWhenLocked(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	if err := os.Mkdir(lockDir(d), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 {
		t.Fatalf("expected no calls under lock, got %v", names(c.calls))
	}
	if _, err := os.Stat(lockDir(d)); err != nil {
		t.Fatalf("a fresh lock held by someone else must survive: %v", err)
	}
}

// The lock is per tab: a dock being built in one tab can hold its lock for
// seconds, and must not drop another tab's ensure on the floor.
func TestEnsureLocksPerTabNotGlobally(t *testing.T) {
	c := oneTab()
	c.panes = append(c.panes,
		snapshot.Pane{PaneID: "w1:p3", TabID: "w1:t2", WorkspaceID: "w1", Cwd: "/r"},
	)
	c.layout = snapshot.Layout{TabID: "w1:t2", Panes: []snapshot.LayoutPane{
		{PaneID: "w1:p3", Rect: snapshot.Rect{X: 0, Y: 1, Width: 200, Height: 40}},
	}}
	d := deps(t, c)
	// Tab w1:t1 is mid-build elsewhere.
	if err := os.Mkdir(lockDir(d), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(d, "w1:t2"); err != nil {
		t.Fatal(err)
	}
	if !equal(names(c.calls), []string{"split", "rename", "swap", "run"}) {
		t.Fatalf("calls = %v; another tab's lock must not block this one", names(c.calls))
	}
	if c.calls[0].Args[0] != "w1:p3" {
		t.Fatalf("split anchor = %v, want the pane of tab w1:t2", c.calls[0].Args)
	}
}

func TestEnsureStealsStaleLock(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	if err := os.Mkdir(lockDir(d), 0o755); err != nil {
		t.Fatal(err)
	}
	d.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if err := Ensure(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) == 0 {
		t.Fatal("stale lock should have been stolen")
	}
	if _, err := os.Stat(lockDir(d)); err == nil {
		t.Fatal("lock should be released after Ensure")
	}
}

// A holder that was declared stale and stolen from must not delete the thief's
// lock on its way out, or a third process could acquire it mid-split.
func TestReleaseLeavesALockItNoLongerOwns(t *testing.T) {
	d := deps(t, oneTab())
	release, ok := acquireLock(d, "w1:t1")
	if !ok {
		t.Fatal("first acquire failed")
	}
	// A second process decides the lock is stale and takes it over.
	thief := deps(t, oneTab())
	thief.StateDir = d.StateDir
	thief.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	thiefRelease, ok := acquireLock(thief, "w1:t1")
	if !ok {
		t.Fatal("stale lock should have been stealable")
	}
	release() // the slow original finally finishes
	if _, err := os.Stat(lockDir(d)); err != nil {
		t.Fatalf("the thief still holds the lock, it must survive the original's release: %v", err)
	}
	thiefRelease()
	if _, err := os.Stat(lockDir(d)); err == nil {
		t.Fatal("the owner's release should have removed the lock")
	}
}

// A tab id that looks like a path must not let a lock or snooze file escape the
// state dir.
func TestSanitizeIDCannotEscapeTheStateDir(t *testing.T) {
	d := deps(t, oneTab())
	state := filepath.Clean(d.StateDir)
	snoozeDir := filepath.Join(state, "snooze")
	for _, tabID := range []string{"w1:t1", "../../etc/passwd", "..", ".", "/", "a/b/c", "", "w1:t1/../../x"} {
		if key := sanitizeID(tabID); key == "." || key == ".." || strings.ContainsRune(key, filepath.Separator) {
			t.Fatalf("sanitizeID(%q) = %q, not a safe single component", tabID, key)
		}
		if got := filepath.Dir(filepath.Clean(lockPath(d, tabID))); got != state {
			t.Fatalf("lockPath(%q) sits in %q, want %q", tabID, got, state)
		}
		if got := filepath.Dir(filepath.Clean(snoozePath(d, tabID))); got != snoozeDir {
			t.Fatalf("snoozePath(%q) sits in %q, want %q", tabID, got, snoozeDir)
		}
	}
	// The happy path keeps the readable name the rest of the tests rely on.
	if got := sanitizeID("w1:t1"); got != "w1_t1" {
		t.Fatalf("sanitizeID(\"w1:t1\") = %q, want %q", got, "w1_t1")
	}
}

// A pane that exists but is not yet labelled is invisible to isPasture, so
// anything that goes wrong after Split must close the pane rather than strand
// an orphan that no later Ensure would ever reap.
func TestEnsureClosesTheNewPaneWhenSwapFails(t *testing.T) {
	c := oneTab()
	boom := errors.New("swap exploded")
	c.errs = map[string]error{"swap": boom}
	err := Ensure(deps(t, c), "w1:t1")
	if !errors.Is(err, boom) {
		t.Fatalf("Ensure err = %v, want %v", err, boom)
	}
	if !equal(names(c.calls), []string{"split", "rename", "swap", "close"}) {
		t.Fatalf("calls = %v", names(c.calls))
	}
	if c.calls[3].Args[0] != "w1:p9" {
		t.Fatalf("closed %q, want the half-built pane w1:p9", c.calls[3].Args[0])
	}
	if c.has("w1:p9") {
		t.Fatal("the half-built pane was left behind")
	}
}

func TestEnsureClosesTheNewPaneWhenRunFails(t *testing.T) {
	c := oneTab()
	boom := errors.New("run exploded")
	c.errs = map[string]error{"run": boom}
	err := Ensure(deps(t, c), "w1:t1")
	if !errors.Is(err, boom) {
		t.Fatalf("Ensure err = %v, want %v", err, boom)
	}
	if !equal(names(c.calls), []string{"split", "rename", "swap", "run", "close"}) {
		t.Fatalf("calls = %v", names(c.calls))
	}
	if c.has("w1:p9") {
		t.Fatal("the half-built pane was left behind")
	}
}

// Layout is how we know which pane is on the left edge; without it a dock would
// land somewhere arbitrary, which is worse than waiting for the next event.
func TestEnsureSkipsWhenLayoutUnavailable(t *testing.T) {
	c := oneTab()
	c.errs = map[string]error{"layout": errors.New("no layout")}
	var log bytes.Buffer
	d := deps(t, c)
	d.Log = &log
	if err := Ensure(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 {
		t.Fatalf("expected no calls without a layout, got %v", names(c.calls))
	}
	if !strings.Contains(log.String(), "layout of tab w1:t1 unavailable") {
		t.Fatalf("the skip must be logged, log = %q", log.String())
	}
}

// One sleep before each token check and none after the last.
func TestEnsureSleepsBeforeEachTokenCheck(t *testing.T) {
	c := oneTab()
	c.stampAfterList = 3 // the UI stamps itself only by the third pane list
	d := deps(t, c)
	sleeps := 0
	d.Sleep = func(time.Duration) { sleeps++ }
	if err := Ensure(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if sleeps != 2 {
		t.Fatalf("sleeps = %d, want 2 (one before each check, none after the successful one)", sleeps)
	}
}

func TestEnsureGivesUpWhenTokenNeverStampedAndLeavesAReapablePane(t *testing.T) {
	c := oneTab()
	c.stampAfterList = 0 // the UI never comes up
	d := deps(t, c)
	var log bytes.Buffer
	d.Log = &log
	sleeps := 0
	d.Sleep = func(time.Duration) { sleeps++ }
	if err := Ensure(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if sleeps != tokenRetries {
		t.Fatalf("sleeps = %d, want the full bound of %d", sleeps, tokenRetries)
	}
	if !strings.Contains(log.String(), "never stamped") {
		t.Fatalf("giving up must be logged, log = %q", log.String())
	}
	// The pane was labelled before the wait, so the next ensure reaps it
	// instead of splitting a second one beside it.
	c.calls = nil
	if err := Ensure(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) == 0 || c.calls[0].Name != "close" || c.calls[0].Args[0] != "w1:p9" {
		t.Fatalf("calls = %v, want the abandoned pane reaped first", c.calls)
	}
}

func TestToggleClosesLiveDockAndSnoozes(t *testing.T) {
	c := oneTab()
	c.panes = append(c.panes, snapshot.Pane{PaneID: "w1:p5", TabID: "w1:t1", Label: Label, Tokens: map[string]string{Token: "77"}})
	d := deps(t, c)
	if err := Toggle(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if !equal(names(c.calls), []string{"close"}) || c.calls[0].Args[0] != "w1:p5" {
		t.Fatalf("calls = %v", c.calls)
	}
	if _, err := os.Stat(filepath.Join(d.StateDir, "snooze", "w1_t1")); err != nil {
		t.Fatalf("snooze file should exist: %v", err)
	}
}

// Closing and snoozing must not race an in-flight Ensure, or the tab ends up
// both docked and snoozed: the opposite of what the user pressed.
func TestToggleHoldsTheLockAcrossTheCloseBranch(t *testing.T) {
	c := oneTab()
	c.panes = append(c.panes, snapshot.Pane{PaneID: "w1:p5", TabID: "w1:t1", Label: Label, Tokens: map[string]string{Token: "77"}})
	d := deps(t, c)
	lock := lockDir(d)
	var held []bool
	c.beforeList = func(f *fakeClient, n int) {
		_, err := os.Stat(lock)
		held = append(held, err == nil)
	}
	if err := Toggle(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if len(held) == 0 || !held[0] {
		t.Fatalf("the deciding pane list ran without the lock held (held = %v)", held)
	}
	if _, err := os.Stat(lock); err == nil {
		t.Fatal("Toggle should release the lock")
	}
}

func TestToggleReportsABusyTab(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	if err := os.Mkdir(lockDir(d), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Toggle(d, "w1:t1")
	if err == nil {
		t.Fatal("a toggle that cannot run must say so, not fail silently")
	}
	if !strings.Contains(err.Error(), "w1:t1") {
		t.Fatalf("err = %v, want it to name the tab", err)
	}
	if len(c.calls) != 0 {
		t.Fatalf("expected no calls, got %v", names(c.calls))
	}
}

func TestToggleOpensAndClearsSnooze(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	if err := os.MkdirAll(filepath.Join(d.StateDir, "snooze"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.StateDir, "snooze", "w1_t1"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Toggle(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if !equal(names(c.calls), []string{"split", "rename", "swap", "run"}) {
		t.Fatalf("calls = %v", names(c.calls))
	}
	if _, err := os.Stat(filepath.Join(d.StateDir, "snooze", "w1_t1")); err == nil {
		t.Fatal("snooze file should be removed")
	}
}

// auto_open governs automatic docking only; an explicit toggle must still work.
func TestToggleOpensEvenWithAutoOpenOff(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	d.Cfg.AutoOpen = false
	if err := Toggle(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if !equal(names(c.calls), []string{"split", "rename", "swap", "run"}) {
		t.Fatalf("calls = %v", names(c.calls))
	}
}

// Redeploy rebuilds each dock it closed, under the same lock, rather than
// leaving it to the next event. herdr 0.9 publishes no plugin event when the
// user moves around the TUI — verified on 0.9.0: switching workspace logs
// `workspace.focus` and `tab.focus` in the server but invokes no hook, while the
// same focus issued over the API does — so "the next focus event respawns them"
// could mean never, and a redeploy left the session with no docks at all until
// something created a tab.
//
// w2:t1 holds nothing but its corpse, so closing it empties the tab and the
// rebuild there finds no pane to split: one close, no split.
func TestRedeployRebuildsEachDockItClosedAndClearsSnooze(t *testing.T) {
	c := oneTab()
	c.panes = append(c.panes,
		snapshot.Pane{PaneID: "w1:p5", TabID: "w1:t1", Label: Label, Tokens: map[string]string{Token: "77"}},
		snapshot.Pane{PaneID: "w2:p3", TabID: "w2:t1", Label: Label},
	)
	d := deps(t, c)
	if err := os.MkdirAll(filepath.Join(d.StateDir, "snooze"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.StateDir, "snooze", "w1_t1"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Redeploy(d); err != nil {
		t.Fatal(err)
	}
	// w2:t1 goes first because the focused tab is rebuilt last, and its close is
	// all it gets. Then w1:t1: close the live dock, build a fresh one.
	want := []string{"close", "close", "split", "rename", "swap", "run", "focustab"}
	if !equal(names(c.calls), want) {
		t.Fatalf("calls = %v, want %v", names(c.calls), want)
	}
	if c.calls[0].Args[0] != "w2:p3" || c.calls[1].Args[0] != "w1:p5" {
		t.Fatalf("closed %q then %q, want w2:p3 then w1:p5", c.calls[0].Args[0], c.calls[1].Args[0])
	}
	if c.calls[2].Args[0] != "w1:p1" {
		t.Fatalf("split %v, want the leftmost pane w1:p1", c.calls[2].Args)
	}
	if _, err := os.Stat(filepath.Join(d.StateDir, "snooze")); err == nil {
		t.Fatal("snooze dir should be removed")
	}
	if _, err := os.Stat(lockDir(d)); err == nil {
		t.Fatal("Redeploy should release the locks it took")
	}
}

// Every rebuild's `pane swap` focuses the tab it happens in, so the order of the
// pass decides where the user ends up. Verified on herdr 0.9.0: a redeploy over
// seven tabs left both the server focus and the client's view in the last tab it
// touched, nowhere near where the user had been.
func TestRedeployLeavesTheFocusInTheTabTheUserWasIn(t *testing.T) {
	c := oneTab()
	// The user is in w2:t1; w1:t1 is some other tab that also carries a dock.
	c.panes[0].Focused = false
	c.panes = append(c.panes,
		snapshot.Pane{PaneID: "w1:p5", TabID: "w1:t1", Label: Label, Tokens: map[string]string{Token: "77"}},
		snapshot.Pane{PaneID: "w2:p1", TabID: "w2:t1", WorkspaceID: "w2", Cwd: "/r", Focused: true},
		snapshot.Pane{PaneID: "w2:p3", TabID: "w2:t1", Label: Label, Tokens: map[string]string{Token: "78"}},
	)
	c.layouts = map[string]snapshot.Layout{
		"w1:t1": c.layout,
		"w2:t1": {TabID: "w2:t1", Panes: []snapshot.LayoutPane{
			{PaneID: "w2:p1", Rect: snapshot.Rect{X: 0, Y: 1, Width: 100, Height: 40}},
		}},
	}
	d := deps(t, c)
	if err := Redeploy(d); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, got := range c.calls {
		if got.Name == "split" {
			order = append(order, got.Args[0])
		}
	}
	if !equal(order, []string{"w1:p1", "w2:p1"}) {
		t.Fatalf("split order = %v, want w1:t1 rebuilt before the focused tab w2:t1", order)
	}
	last := c.calls[len(c.calls)-1]
	if last.Name != "focustab" || last.Args[0] != "w2:t1" {
		t.Fatalf("last call = %v %v, want focustab w2:t1", last.Name, last.Args)
	}
}

// Nothing to redeploy means nothing to put the focus back to, so the focus is
// left strictly alone.
func TestRedeployWithNoDocksTouchesNothing(t *testing.T) {
	c := oneTab()
	if err := Redeploy(deps(t, c)); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 {
		t.Fatalf("calls = %v, want none", names(c.calls))
	}
}

// The snoozes have to go before the rebuild, not after it: openLocked skips a
// snoozed tab, so clearing them last would leave exactly the tabs a redeploy is
// meant to refresh with no dock at all.
func TestRedeployClearsSnoozeBeforeRebuilding(t *testing.T) {
	c := oneTab()
	c.panes = append(c.panes, snapshot.Pane{PaneID: "w1:p5", TabID: "w1:t1", Label: Label, Tokens: map[string]string{Token: "77"}})
	d := deps(t, c)
	if err := os.MkdirAll(filepath.Join(d.StateDir, "snooze"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.StateDir, "snooze", "w1_t1"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Redeploy(d); err != nil {
		t.Fatal(err)
	}
	want := []string{"close", "split", "rename", "swap", "run", "focustab"}
	if !equal(names(c.calls), want) {
		t.Fatalf("calls = %v, want %v: a snoozed tab must still be rebuilt", names(c.calls), want)
	}
}

// A pane another process is still building must not be closed by Redeploy: it
// would come back live on the old binary, which is what redeploy exists to
// prevent.
func TestRedeployLeavesBusyTabsAloneAndSaysSo(t *testing.T) {
	c := oneTab()
	c.panes = append(c.panes,
		snapshot.Pane{PaneID: "w1:p5", TabID: "w1:t1", Label: Label, Tokens: map[string]string{Token: "77"}},
		snapshot.Pane{PaneID: "w2:p3", TabID: "w2:t1", Label: Label},
	)
	d := deps(t, c)
	if err := os.Mkdir(lockDir(d), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Redeploy(d)
	if err == nil || !strings.Contains(err.Error(), "w1:t1") {
		t.Fatalf("err = %v, want it to name the skipped tab w1:t1", err)
	}
	// The idle tab's pane is closed (and its tab has nothing left to split), and
	// the focus still goes back to where the user was even though their own tab
	// was the busy one.
	if !equal(names(c.calls), []string{"close", "focustab"}) || c.calls[0].Args[0] != "w2:p3" {
		t.Fatalf("calls = %v, want only the idle tab's pane closed", c.calls)
	}
}

func TestShellQuote(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/opt/pasture", "'/opt/pasture'"},
		{"/Applications/My Tools/pasture", "'/Applications/My Tools/pasture'"},
		{"/home/o'brien/bin/pasture", `'/home/o'\''brien/bin/pasture'`},
		{"/tmp/it's here/pasture", `'/tmp/it'\''s here/pasture'`},
	} {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Nothing should panic when a caller leaves the optional hooks nil.
func TestNilNowAndSleepDoNotPanic(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	d.Now = nil
	d.Sleep = nil
	if err := Ensure(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) == 0 {
		t.Fatalf("calls = %v, want the dock to be built", names(c.calls))
	}
}

// widthDeps builds Deps with a column target so the ratio and resize
// arithmetic can be asserted exactly.
func widthDeps(t *testing.T, c *fakeClient, columns int) Deps {
	t.Helper()
	d := deps(t, c)
	d.Cfg.WidthColumns = columns
	return d
}

func argOf(calls []call, name string, i int) string {
	for _, c := range calls {
		if c.Name == name {
			return c.Args[i]
		}
	}
	return ""
}

func TestEnsureSplitsToTheColumnTarget(t *testing.T) {
	c := oneTab()
	c.layout.Area.Width = 200
	if err := Ensure(widthDeps(t, c, 50), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	// oneTab's anchor holds 100 of the tab's 200 columns, and --ratio measures
	// the anchor, so 50 columns of dock is half of it.
	if got := argOf(c.calls, "split", 1); got != "0.50" {
		t.Fatalf("split ratio = %q, want 0.50 (50 of the anchor's 100 columns)", got)
	}
}

func TestEnsureResizesAnExistingDockThatDrifted(t *testing.T) {
	c := oneTab()
	c.layout.Area.Width = 200
	c.panes = append(c.panes, snapshot.Pane{
		PaneID: "w1:p5", TabID: "w1:t1", Label: Label,
		Tokens: map[string]string{Token: "77"},
	})
	c.layout.Panes = append(c.layout.Panes, snapshot.LayoutPane{
		PaneID: "w1:p5", Rect: snapshot.Rect{X: 0, Y: 1, Width: 20, Height: 40},
	})
	if err := Ensure(widthDeps(t, c, 50), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if got := argOf(c.calls, "resize", 1); got != "0.1500" {
		t.Fatalf("resize amount = %q, want 0.1500 ((50-20)/200)", got)
	}
	if got := argOf(c.calls, "resize", 0); got != "w1:p5" {
		t.Fatalf("resized %q, want the dock pane", got)
	}
	if argOf(c.calls, "split", 0) != "" {
		t.Fatalf("a live dock must not be split again: %v", c.calls)
	}
}

func TestEnsureLeavesAWidthWithinOneColumnAlone(t *testing.T) {
	c := oneTab()
	c.layout.Area.Width = 200
	c.panes = append(c.panes, snapshot.Pane{
		PaneID: "w1:p5", TabID: "w1:t1", Label: Label,
		Tokens: map[string]string{Token: "77"},
	})
	c.layout.Panes = append(c.layout.Panes, snapshot.LayoutPane{
		PaneID: "w1:p5", Rect: snapshot.Rect{X: 0, Y: 1, Width: 49, Height: 40},
	})
	if err := Ensure(widthDeps(t, c, 50), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if argOf(c.calls, "resize", 0) != "" {
		t.Fatalf("a one-column drift must not resize: %v", c.calls)
	}
}

func TestEnsureWithoutAColumnTargetNeverResizes(t *testing.T) {
	c := oneTab()
	c.layout.Area.Width = 200
	c.panes = append(c.panes, snapshot.Pane{
		PaneID: "w1:p5", TabID: "w1:t1", Label: Label,
		Tokens: map[string]string{Token: "77"},
	})
	c.layout.Panes = append(c.layout.Panes, snapshot.LayoutPane{
		PaneID: "w1:p5", Rect: snapshot.Rect{X: 0, Y: 1, Width: 20, Height: 40},
	})
	if err := Ensure(deps(t, c), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if argOf(c.calls, "resize", 0) != "" {
		t.Fatalf("width_columns unset must not resize: %v", c.calls)
	}
}

func TestTargetColumnsClampsToTheFloorAndHalfTheTab(t *testing.T) {
	cases := []struct{ columns, tab, want int }{
		{50, 200, 50},
		{5, 200, 22},    // below the readable floor
		{150, 200, 100}, // more than half the tab
		{30, 30, 22},    // tab too narrow for both rules; the floor wins
	}
	for _, tc := range cases {
		if got := targetColumns(tc.columns, tc.tab); got != tc.want {
			t.Errorf("targetColumns(%d, %d) = %d, want %d", tc.columns, tc.tab, got, tc.want)
		}
	}
}

// A herdr server restart (which an upgrade forces) restores the pane but not
// the process inside it, and herdr fires no events for the restore, so nothing
// heals the dock until the user next changes focus. Startup is the [[startup]]
// hook that closes those corpses and re-docks their tabs.
func TestStartupHealsEveryTabThatHadADock(t *testing.T) {
	c := oneTab()
	c.layout.Area.Width = 200
	c.panes = append(c.panes,
		snapshot.Pane{PaneID: "w1:p5", TabID: "w1:t1", WorkspaceID: "w1", Cwd: "/r", Label: Label},
		snapshot.Pane{PaneID: "w2:p1", TabID: "w2:t1", WorkspaceID: "w2", Cwd: "/r2"},
		snapshot.Pane{PaneID: "w2:p5", TabID: "w2:t1", WorkspaceID: "w2", Cwd: "/r2", Label: Label},
	)
	c.layouts = map[string]snapshot.Layout{
		"w1:t1": c.layout,
		"w2:t1": {TabID: "w2:t1", Area: snapshot.Rect{Width: 200, Height: 40}, Panes: []snapshot.LayoutPane{
			{PaneID: "w2:p1", Rect: snapshot.Rect{X: 0, Y: 1, Width: 100, Height: 40}},
			{PaneID: "w2:p5", Rect: snapshot.Rect{X: 100, Y: 1, Width: 100, Height: 40}},
		}},
	}
	if err := Startup(deps(t, c)); err != nil {
		t.Fatal(err)
	}
	// Reviving in place: the UI is re-run in the pane the restart left behind,
	// so neither tab is closed or split.
	revived := map[string]bool{}
	for _, call := range c.calls {
		switch call.Name {
		case "run":
			revived[call.Args[0]] = true
		case "close", "split":
			t.Fatalf("a revivable dock must not be rebuilt, calls = %v", c.calls)
		}
	}
	if !revived["w1:p5"] || !revived["w2:p5"] {
		t.Fatalf("both docks must be revived, calls = %v", c.calls)
	}
}

// When the revived pane never stamps its token the pane is genuinely dead, so
// fall back to closing it and building a fresh dock.
func TestStartupRebuildsWhenTheRevivedPaneStaysDead(t *testing.T) {
	c := oneTab()
	c.layout.Area.Width = 200
	c.stampAfterList = 0 // the UI never comes up, in the corpse or its successor
	c.panes = append(c.panes, snapshot.Pane{
		PaneID: "w1:p5", TabID: "w1:t1", WorkspaceID: "w1", Cwd: "/r", Label: Label,
	})
	if err := Startup(deps(t, c)); err != nil {
		t.Fatal(err)
	}
	if argOf(c.calls, "close", 0) != "w1:p5" {
		t.Fatalf("the dead pane must be closed, calls = %v", c.calls)
	}
	if argOf(c.calls, "split", 0) == "" {
		t.Fatalf("a fresh dock must be built, calls = %v", names(c.calls))
	}
}

// A tab whose dock is alive needs no healing, and a tab that never had one must
// not gain a dock just because the server restarted.
func TestStartupLeavesLiveAndUndockedTabsAlone(t *testing.T) {
	c := oneTab()
	c.layout.Area.Width = 200
	c.panes = append(c.panes, snapshot.Pane{
		PaneID: "w1:p5", TabID: "w1:t1", Label: Label,
		Tokens: map[string]string{Token: "77"},
	})
	if err := Startup(deps(t, c)); err != nil {
		t.Fatal(err)
	}
	for _, call := range c.calls {
		if call.Name == "split" || call.Name == "close" {
			t.Fatalf("nothing to heal, but got %v", c.calls)
		}
	}
}

// auto_open governs automatic docking on focus. Restoring a dock the user
// already had is not that, so a restart must bring it back either way.
func TestStartupHealsEvenWithAutoOpenOff(t *testing.T) {
	c := oneTab()
	c.layout.Area.Width = 200
	c.panes = append(c.panes, snapshot.Pane{
		PaneID: "w1:p5", TabID: "w1:t1", WorkspaceID: "w1", Cwd: "/r", Label: Label,
	})
	d := deps(t, c)
	d.Cfg.AutoOpen = false
	if err := Startup(d); err != nil {
		t.Fatal(err)
	}
	if argOf(c.calls, "run", 0) != "w1:p5" {
		t.Fatalf("the dock should be restored, calls = %v", c.calls)
	}
}

// --ratio is a share of the pane being split, not of the tab, so a tab whose
// leftmost pane is only part of the width used to give a dock far narrower than
// width_ratio promised. Reproduced live: a 348-column tab split in half docked
// at 44 columns instead of 87.
func TestEnsureSizesTheDockAgainstTheAnchorNotTheTab(t *testing.T) {
	c := oneTab()
	// A tab 200 columns wide whose leftmost pane holds only half of it.
	c.layout = snapshot.Layout{TabID: "w1:t1", Area: snapshot.Rect{Width: 200, Height: 40},
		Panes: []snapshot.LayoutPane{
			{PaneID: "w1:p1", Rect: snapshot.Rect{X: 0, Y: 1, Width: 100, Height: 40}},
			{PaneID: "w1:p2", Rect: snapshot.Rect{X: 100, Y: 1, Width: 100, Height: 40}},
		}}
	if err := Ensure(deps(t, c), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	// 25% of the 200-column tab is 50 columns, which is half of the 100-column
	// anchor, so the ratio handed to herdr must be 0.50, not 0.25.
	if got := argOf(c.calls, "split", 1); got != "0.50" {
		t.Fatalf("split ratio = %q, want 0.50 (50 of the anchor's 100 columns)", got)
	}
}

func TestEnsureSizesAColumnTargetAgainstTheAnchor(t *testing.T) {
	c := oneTab()
	c.layout = snapshot.Layout{TabID: "w1:t1", Area: snapshot.Rect{Width: 200, Height: 40},
		Panes: []snapshot.LayoutPane{
			{PaneID: "w1:p1", Rect: snapshot.Rect{X: 0, Y: 1, Width: 100, Height: 40}},
			{PaneID: "w1:p2", Rect: snapshot.Rect{X: 100, Y: 1, Width: 100, Height: 40}},
		}}
	d := deps(t, c)
	d.Cfg.WidthColumns = 30
	if err := Ensure(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if got := argOf(c.calls, "split", 1); got != "0.30" {
		t.Fatalf("split ratio = %q, want 0.30 (30 of the anchor's 100 columns)", got)
	}
}

// The dock must never take so much of the anchor that the pane it split from is
// unusable, however wide the tab makes the target look.
func TestDockColumnsNeverSwallowTheAnchor(t *testing.T) {
	// A 400-column tab wants 100 columns of dock, but the anchor is only 60.
	if got := dockColumns(100, 60); got > 30 {
		t.Fatalf("dockColumns(100, 60) = %d, want at most half the anchor", got)
	}
	// A comfortable anchor gets exactly what was asked for.
	if got := dockColumns(50, 200); got != 50 {
		t.Fatalf("dockColumns(50, 200) = %d, want 50", got)
	}
	// Below the readable floor the floor wins, even on a narrow anchor.
	if got := dockColumns(5, 200); got != minColumns {
		t.Fatalf("dockColumns(5, 200) = %d, want the %d-column floor", got, minColumns)
	}
}
