package dock

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
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
	calls  []call
	nextID string
	// stampTokenOnList makes the next PaneList show the token on nextID,
	// simulating the UI process stamping itself.
	stampTokenOnList bool
	// listN counts PaneList calls; beforeList runs at the start of the listN'th
	// call, so a test can mutate the world between two reads the way a
	// concurrent `pasture ensure` process would.
	listN      int
	beforeList func(f *fakeClient, n int)
}

func (f *fakeClient) rec(name string, args ...string) { f.calls = append(f.calls, call{name, args}) }

func (f *fakeClient) PaneList() ([]snapshot.Pane, error) {
	f.listN++
	if f.beforeList != nil {
		f.beforeList(f, f.listN)
	}
	if f.stampTokenOnList {
		for i := range f.panes {
			if f.panes[i].PaneID == f.nextID {
				f.panes[i].Tokens = map[string]string{Token: "1"}
			}
		}
	}
	return append([]snapshot.Pane(nil), f.panes...), nil
}

func (f *fakeClient) Layout(paneID string) (snapshot.Layout, error) { return f.layout, nil }

func (f *fakeClient) Split(paneID string, ratio float64, cwd string, env map[string]string) (string, error) {
	f.rec("split", paneID, strconv.FormatFloat(ratio, 'f', 2, 64), cwd)
	tab := ""
	for _, p := range f.panes {
		if p.PaneID == paneID {
			tab = p.TabID
		}
	}
	f.panes = append(f.panes, snapshot.Pane{PaneID: f.nextID, TabID: tab, Cwd: cwd})
	return f.nextID, nil
}

func (f *fakeClient) Swap(source, target string) error { f.rec("swap", source, target); return nil }
func (f *fakeClient) RunInPane(paneID, command string) error {
	f.rec("run", paneID, command)
	return nil
}
func (f *fakeClient) Rename(paneID, label string) error { f.rec("rename", paneID, label); return nil }

func (f *fakeClient) Close(paneID string) error {
	f.rec("close", paneID)
	kept := f.panes[:0]
	for _, p := range f.panes {
		if p.PaneID != paneID {
			kept = append(kept, p)
		}
	}
	f.panes = kept
	return nil
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
		stampTokenOnList: true,
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

func TestEnsureOpensDockOnLeftOfLeftmostPane(t *testing.T) {
	c := oneTab()
	if err := Ensure(deps(t, c), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"split", "swap", "run", "rename"}
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
	if c.calls[1].Args[0] != "w1:p9" || c.calls[1].Args[1] != "w1:p1" {
		t.Fatalf("swap args = %v", c.calls[1].Args)
	}
	if c.calls[2].Args[1] != "'/opt/pasture' ui" {
		t.Fatalf("run command = %q", c.calls[2].Args[1])
	}
	if c.calls[3].Args[1] != Label {
		t.Fatalf("rename label = %q", c.calls[3].Args[1])
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

func TestEnsureReplacesCorpse(t *testing.T) {
	c := oneTab()
	c.panes = append(c.panes, snapshot.Pane{PaneID: "w1:p5", TabID: "w1:t1", Label: Label})
	if err := Ensure(deps(t, c), "w1:t1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"close", "split", "swap", "run", "rename"}
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

func TestEnsureSkipsWhenLocked(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	if err := os.Mkdir(filepath.Join(d.StateDir, "ensure.lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 {
		t.Fatalf("expected no calls under lock, got %v", names(c.calls))
	}
	if _, err := os.Stat(filepath.Join(d.StateDir, "ensure.lock")); err != nil {
		t.Fatalf("a fresh lock held by someone else must survive: %v", err)
	}
}

func TestEnsureStealsStaleLock(t *testing.T) {
	c := oneTab()
	d := deps(t, c)
	if err := os.Mkdir(filepath.Join(d.StateDir, "ensure.lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	d.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if err := Ensure(d, "w1:t1"); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) == 0 {
		t.Fatal("stale lock should have been stolen")
	}
	if _, err := os.Stat(filepath.Join(d.StateDir, "ensure.lock")); err == nil {
		t.Fatal("lock should be released after Ensure")
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
	if !equal(names(c.calls), []string{"split", "swap", "run", "rename"}) {
		t.Fatalf("calls = %v", names(c.calls))
	}
	if _, err := os.Stat(filepath.Join(d.StateDir, "snooze", "w1_t1")); err == nil {
		t.Fatal("snooze file should be removed")
	}
}

func TestRedeployClosesAllDocksAndClearsSnooze(t *testing.T) {
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
	if !equal(names(c.calls), []string{"close", "close"}) {
		t.Fatalf("calls = %v", names(c.calls))
	}
	if _, err := os.Stat(filepath.Join(d.StateDir, "snooze")); err == nil {
		t.Fatal("snooze dir should be removed")
	}
}
