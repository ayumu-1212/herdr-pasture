package snapshot

import "testing"

const snapshotJSON = `{"id":"cli:api:snapshot","result":{"snapshot":{
 "focused_pane_id":"w6:p1",
 "focused_workspace_id":"w6",
 "focused_tab_id":"w6:t1",
 "layouts":[{"area":{"height":28,"width":45,"x":0,"y":0},"focused_pane_id":"w6:p1","panes":[{"focused":false,"pane_id":"w6:p2","rect":{"height":28,"width":19,"x":0,"y":0}},{"focused":true,"pane_id":"w6:p1","rect":{"height":28,"width":26,"x":19,"y":0}}],"tab_id":"w6:t1","workspace_id":"w6","zoomed":true}],
 "panes":[
  {"agent":"claude","agent_status":"working","cwd":"/r/a","focused":true,"pane_id":"w6:p1","tab_id":"w6:t1","terminal_title_stripped":"task one","workspace_id":"w6"},
  {"agent":null,"agent_status":"idle","cwd":"/r/a","focused":false,"pane_id":"w6:p2","tab_id":"w6:t1","terminal_title_stripped":"zsh","workspace_id":"w6","label":"pasture","tokens":{"pasture":"123"}}
 ],
 "workspaces":[{"workspace_id":"w6","number":4,"label":"a","active_tab_id":"w6:t1"}]
}}}`

func TestDecodeSnapshot(t *testing.T) {
	s, err := Decode([]byte(snapshotJSON))
	if err != nil {
		t.Fatal(err)
	}
	if s.FocusedPaneID != "w6:p1" || len(s.Panes) != 2 || len(s.Workspaces) != 1 {
		t.Fatalf("got %+v", s)
	}
	if s.FocusedWorkspaceID != "w6" {
		t.Fatalf("FocusedWorkspaceID = %q", s.FocusedWorkspaceID)
	}
	if s.Panes[0].Agent != "claude" || s.Panes[0].Title != "task one" {
		t.Fatalf("pane0 = %+v", s.Panes[0])
	}
	if s.Panes[1].Agent != "" {
		t.Fatalf("null agent should decode to empty string, got %q", s.Panes[1].Agent)
	}
	if s.Panes[1].Label != "pasture" || s.Panes[1].Tokens["pasture"] != "123" {
		t.Fatalf("pane1 = %+v", s.Panes[1])
	}
	if s.Workspaces[0].Number != 4 || s.Workspaces[0].ActiveTabID != "w6:t1" {
		t.Fatalf("workspace = %+v", s.Workspaces[0])
	}
	if s.FocusedTabID != "w6:t1" {
		t.Fatalf("FocusedTabID = %q", s.FocusedTabID)
	}
	l, ok := s.Layout("w6:t1")
	if !ok || !l.Zoomed || l.FocusedPaneID != "w6:p1" || l.Area.Width != 45 || len(l.Panes) != 2 {
		t.Fatalf("layout = %+v, %v", l, ok)
	}
	if _, ok := s.Layout("w9:t1"); ok {
		t.Fatal("Layout of an unknown tab should report false")
	}
}

func TestDecodePaneList(t *testing.T) {
	panes, err := DecodePaneList([]byte(`{"id":"cli:pane:list","result":{"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","cwd":"/x"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 1 || panes[0].PaneID != "w1:p1" {
		t.Fatalf("got %+v", panes)
	}
}

func TestDecodeLayout(t *testing.T) {
	l, err := DecodeLayout([]byte(`{"id":"cli:pane:layout","result":{"layout":{"area":{"height":96,"width":348,"x":26,"y":1},"focused_pane_id":"w5:p3","panes":[{"focused":false,"pane_id":"w5:p1","rect":{"height":96,"width":174,"x":26,"y":1}},{"focused":true,"pane_id":"w5:p3","rect":{"height":96,"width":174,"x":200,"y":1}}],"tab_id":"w5:t1","workspace_id":"w5"},"type":"pane_layout"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if l.TabID != "w5:t1" || len(l.Panes) != 2 || l.Panes[1].Rect.X != 200 || l.Area.Width != 348 || l.FocusedPaneID != "w5:p3" {
		t.Fatalf("got %+v", l)
	}
}

func TestDecodeSplit(t *testing.T) {
	id, err := DecodeSplit([]byte(`{"id":"cli:pane:split","result":{"pane":{"pane_id":"w6:p9","tab_id":"w6:t1"}}}`))
	if err != nil || id != "w6:p9" {
		t.Fatalf("got %q, %v", id, err)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := Decode([]byte("not json")); err == nil {
		t.Fatal("expected error")
	}
	if _, err := DecodeSplit([]byte(`{"result":{}}`)); err == nil {
		t.Fatal("expected error for missing pane id")
	}
}
