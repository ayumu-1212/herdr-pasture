package mobile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

func TestPhone(t *testing.T) {
	for _, tc := range []struct {
		x, width, threshold int
		want                bool
	}{
		{0, 45, 64, true},
		{0, 64, 64, true},
		{0, 65, 64, false},
		{26, 50, 64, false}, // desktop with herdr's sidebar out: 76 columns
		{0, 0, 64, false},   // size unknown
		{0, 45, 0, false},   // feature off
		{0, 45, -1, false},  // feature off
	} {
		l := snapshot.Layout{Area: snapshot.Rect{X: tc.x, Width: tc.width}}
		if got := Phone(l, tc.threshold); got != tc.want {
			t.Errorf("Phone(x=%d width=%d, %d) = %v, want %v", tc.x, tc.width, tc.threshold, got, tc.want)
		}
	}
}

func TestMarksRoundTrip(t *testing.T) {
	m := Marks{Dir: t.TempDir()}
	if _, ok := m.Get("w1:t1"); ok {
		t.Fatal("fresh tab should be unmarked")
	}
	if err := m.Set("w1:t1", Mark{Back: "w1:p1", Width: 0.42}); err != nil {
		t.Fatal(err)
	}
	if mk, ok := m.Get("w1:t1"); !ok || mk != (Mark{Back: "w1:p1", Width: 0.42}) {
		t.Fatalf("Get = %+v, %v", mk, ok)
	}
	if err := m.Set("w1:t1", Mark{}); err != nil {
		t.Fatal(err)
	}
	if mk, ok := m.Get("w1:t1"); !ok || mk != (Mark{}) {
		t.Fatalf("an empty mark must still mark the tab: %+v, %v", mk, ok)
	}
	if _, ok := m.Get("w2:t1"); ok {
		t.Fatal("marks must be per tab")
	}
	if err := m.Clear("w1:t1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Get("w1:t1"); ok {
		t.Fatal("Clear left the mark")
	}
	if err := m.Clear("w1:t1"); err != nil {
		t.Fatalf("clearing an unmarked tab: %v", err)
	}
}

func TestMarksReadsTheOldPlainFormat(t *testing.T) {
	m := Marks{Dir: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(m.Dir, "zoomed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Dir, "zoomed", "w1_t1"), []byte("w1:p2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if mk, ok := m.Get("w1:t1"); !ok || mk != (Mark{Back: "w1:p2"}) {
		t.Fatalf("Get = %+v, %v", mk, ok)
	}
}

func TestRememberKeepsTheFirstWidth(t *testing.T) {
	m := Marks{Dir: t.TempDir()}
	desk := phoneTab(20, false, "agent")
	if err := m.Remember("w1:t1", "agent", desk, "dock"); err != nil {
		t.Fatal(err)
	}
	strip := phoneTab(5, false, "agent")
	if err := m.Remember("w1:t1", "", strip, "dock"); err != nil {
		t.Fatal(err)
	}
	mk, _ := m.Get("w1:t1")
	if mk.Back != "agent" || mk.Width != 20.0/45 {
		t.Fatalf("mark = %+v; want back kept and the first width 20/45", mk)
	}
}

// fakeHerdr records calls as "zoom PANE on|off" and "resize PANE AMOUNT".
type fakeHerdr struct {
	calls []string
	fail  string // a call prefix that returns an error
}

func (f *fakeHerdr) record(c string) error {
	f.calls = append(f.calls, c)
	if f.fail != "" && strings.HasPrefix(c, f.fail) {
		return errors.New("boom")
	}
	return nil
}

func (f *fakeHerdr) Zoom(p string, on bool) error {
	state := "off"
	if on {
		state = "on"
	}
	return f.record("zoom " + p + " " + state)
}

func (f *fakeHerdr) Resize(p string, amount float64) error {
	return f.record(fmt.Sprintf("resize %s %.3f", p, amount))
}

// phoneTab is a 45-column tab: the dock dockWidth columns wide, then "agent".
func phoneTab(dockWidth int, zoomed bool, focused string) snapshot.Layout {
	return snapshot.Layout{
		TabID: "w1:t1",
		Area:  snapshot.Rect{Width: 45, Height: 20},
		Panes: []snapshot.LayoutPane{
			{PaneID: "dock", Rect: snapshot.Rect{Width: dockWidth, Height: 20}},
			{PaneID: "agent", Rect: snapshot.Rect{X: dockWidth, Width: 45 - dockWidth, Height: 20}},
		},
		Zoomed:        zoomed,
		FocusedPaneID: focused,
	}
}

func TestOpenRemembersTheFocusAndZoomsTheDock(t *testing.T) {
	m, h := Marks{Dir: t.TempDir()}, &fakeHerdr{}
	if err := Open(h, m, phoneTab(20, false, "agent"), "w1:t1", "dock"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.calls, ","); got != "zoom dock on" {
		t.Fatalf("calls = %s", got)
	}
	if mk, _ := m.Get("w1:t1"); mk.Back != "agent" || mk.Width != 20.0/45 {
		t.Fatalf("mark = %+v", mk)
	}
}

func TestCloseSqueezesTheDockAndFocusesTheTarget(t *testing.T) {
	m, h := Marks{Dir: t.TempDir()}, &fakeHerdr{}
	if err := Close(h, m, phoneTab(20, true, "dock"), "w1:t1", "dock", "agent"); err != nil {
		t.Fatal(err)
	}
	want := "resize dock -0.900,zoom agent on,zoom agent off"
	if got := strings.Join(h.calls, ","); got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
	if mk, ok := m.Get("w1:t1"); !ok || mk.Back != "agent" {
		t.Fatalf("mark = %+v, %v", mk, ok)
	}
}

func TestCloseLeavesAStripAlone(t *testing.T) {
	m, h := Marks{Dir: t.TempDir()}, &fakeHerdr{}
	if err := Close(h, m, phoneTab(5, false, "dock"), "w1:t1", "dock", "agent"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.calls, ","); got != "zoom agent on,zoom agent off" {
		t.Fatalf("calls = %s", got)
	}
}

func TestCloseWithoutADockOnlyMarks(t *testing.T) {
	m, h := Marks{Dir: t.TempDir()}, &fakeHerdr{}
	l := snapshot.Layout{TabID: "w1:t1", Area: snapshot.Rect{Width: 45}}
	if err := Close(h, m, l, "w1:t1", "", ""); err != nil {
		t.Fatal(err)
	}
	if len(h.calls) != 0 {
		t.Fatalf("calls = %v", h.calls)
	}
	if _, ok := m.Get("w1:t1"); !ok {
		t.Fatal("the tab must be marked so its dock starts closed")
	}
}

func TestSqueeze(t *testing.T) {
	h := &fakeHerdr{}
	if err := Squeeze(h, phoneTab(5, false, "agent"), "dock"); err != nil || len(h.calls) != 0 {
		t.Fatalf("a strip was squeezed: %v %v", h.calls, err)
	}
	if err := Squeeze(h, phoneTab(22, false, "agent"), "dock"); err != nil || strings.Join(h.calls, ",") != "resize dock -0.900" {
		t.Fatalf("calls = %v, %v", h.calls, err)
	}
}

func TestWide(t *testing.T) {
	for _, tc := range []struct {
		dock int
		want bool
	}{{5, false}, {9, false}, {10, true}, {22, true}} {
		if got := Wide(phoneTab(tc.dock, false, ""), "dock"); got != tc.want {
			t.Errorf("Wide(%d of 45) = %v, want %v", tc.dock, got, tc.want)
		}
	}
	if Wide(phoneTab(22, false, ""), "gone") {
		t.Error("a dock missing from the layout is not wide")
	}
}

// desktopTab is the same tab seen from a 120-column desktop.
func desktopTab(dockWidth int, zoomed bool, focused string) snapshot.Layout {
	l := phoneTab(dockWidth, zoomed, focused)
	l.Area.Width = 120
	return l
}

func TestRestoreUnzoomsWidensAndClears(t *testing.T) {
	m, h := Marks{Dir: t.TempDir()}, &fakeHerdr{}
	_ = m.Set("w1:t1", Mark{Back: "agent", Width: 50.0 / 120})
	if err := Restore(h, m, desktopTab(12, true, "dock"), "w1:t1", "dock", 0); err != nil {
		t.Fatal(err)
	}
	want := "zoom dock off,resize dock 0.317"
	if got := strings.Join(h.calls, ","); got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
	if _, ok := m.Get("w1:t1"); ok {
		t.Fatal("the mark must be cleared")
	}
}

func TestRestorePrefersTheConfiguredColumns(t *testing.T) {
	m, h := Marks{Dir: t.TempDir()}, &fakeHerdr{}
	_ = m.Set("w1:t1", Mark{Width: 19.0 / 45}) // rounded on the phone: 51 of 120
	if err := Restore(h, m, desktopTab(12, false, "agent"), "w1:t1", "dock", 40); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.calls, ","); got != "resize dock 0.233" {
		t.Fatalf("calls = %s", got)
	}
}

func TestRestoreWithNoWidthToGoBackToLeavesTheSplit(t *testing.T) {
	m, h := Marks{Dir: t.TempDir()}, &fakeHerdr{}
	_ = m.Set("w1:t1", Mark{})
	if err := Restore(h, m, desktopTab(12, false, "agent"), "w1:t1", "dock", 0); err != nil {
		t.Fatal(err)
	}
	if len(h.calls) != 0 {
		t.Fatalf("calls = %v", h.calls)
	}
}

func TestRestoreKeepsTheMarkWhenHerdrFails(t *testing.T) {
	m, h := Marks{Dir: t.TempDir()}, &fakeHerdr{fail: "zoom"}
	_ = m.Set("w1:t1", Mark{Width: 0.4})
	if err := Restore(h, m, desktopTab(12, true, "dock"), "w1:t1", "dock", 0); err == nil {
		t.Fatal("want the zoom error")
	}
	if _, ok := m.Get("w1:t1"); !ok {
		t.Fatal("a failed restore must keep the mark for the next try")
	}
}

func layout(panes ...snapshot.LayoutPane) snapshot.Layout {
	return snapshot.Layout{TabID: "w1:t1", Panes: panes}
}

func pane(id string, w, h int) snapshot.LayoutPane {
	return snapshot.LayoutPane{PaneID: id, Rect: snapshot.Rect{Width: w, Height: h}}
}

func TestReturnPanePrefersTheRememberedPane(t *testing.T) {
	l := layout(pane("dock", 19, 28), pane("big", 20, 28), pane("small", 6, 28))
	if got, ok := ReturnPane(l, "dock", "small"); !ok || got != "small" {
		t.Fatalf("got %q, %v", got, ok)
	}
}

func TestReturnPaneFallsBackToTheLargestNonDockPane(t *testing.T) {
	l := layout(pane("dock", 40, 28), pane("small", 6, 28), pane("big", 20, 28))
	for _, preferred := range []string{"", "gone", "dock"} {
		if got, ok := ReturnPane(l, "dock", preferred); !ok || got != "big" {
			t.Fatalf("preferred %q: got %q, %v", preferred, got, ok)
		}
	}
}

func TestReturnPaneReportsFalseWhenOnlyTheDockIsLeft(t *testing.T) {
	if got, ok := ReturnPane(layout(pane("dock", 45, 28)), "dock", ""); ok {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeID(t *testing.T) {
	for in, want := range map[string]string{"w1:t1": "w1_t1", "../x": "___x", "": "_"} {
		if got := SanitizeID(in); got != want {
			t.Errorf("SanitizeID(%q) = %q, want %q", in, got, want)
		}
	}
}
