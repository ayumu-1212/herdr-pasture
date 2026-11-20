package mobile

import (
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
	if err := m.Set("w1:t1", "w1:p1"); err != nil {
		t.Fatal(err)
	}
	if p, ok := m.Get("w1:t1"); !ok || p != "w1:p1" {
		t.Fatalf("Get = %q, %v", p, ok)
	}
	if err := m.Set("w1:t1", ""); err != nil {
		t.Fatal(err)
	}
	if p, ok := m.Get("w1:t1"); !ok || p != "" {
		t.Fatalf("an empty return pane must still mark the tab: %q, %v", p, ok)
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
