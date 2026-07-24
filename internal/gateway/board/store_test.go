package board

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func intPtr(v int) *int { return &v }

func mustApply(t *testing.T, s *Store, sessionKey string, ops ...Op) Snapshot {
	t.Helper()
	snapshot, err := s.ApplyOps(sessionKey, ops)
	if err != nil {
		t.Fatalf("apply ops: %v", err)
	}
	return snapshot
}

func TestEmptySnapshotAndRevisionCounter(t *testing.T) {
	s := NewStore()
	snap := s.GetSnapshot("sess-1")
	if snap.SessionKey != "sess-1" || snap.Revision != 0 || len(snap.Tabs) != 0 || len(snap.Widgets) != 0 {
		t.Fatalf("unexpected empty snapshot: %+v", snap)
	}
	// Zero ops do not bump the revision.
	snap = mustApply(t, s, "sess-1")
	if snap.Revision != 0 {
		t.Fatalf("no-op update must not bump revision: %+v", snap)
	}
	snap = mustApply(t, s, "sess-1", Op{Kind: "tab_create", TabID: "main", Title: "Main"})
	if snap.Revision != 1 || len(snap.Tabs) != 1 || snap.Tabs[0].ChatDock != "right" {
		t.Fatalf("unexpected snapshot after tab_create: %+v", snap)
	}
	snap = mustApply(t, s, "sess-1", Op{Kind: "tab_update", TabID: "main", Title: "Renamed", ChatDock: "bottom"})
	if snap.Revision != 2 || snap.Tabs[0].Title != "Renamed" || snap.Tabs[0].ChatDock != "bottom" {
		t.Fatalf("unexpected snapshot after tab_update: %+v", snap)
	}
}

func TestTabLifecycleAndReorder(t *testing.T) {
	s := NewStore()
	mustApply(t, s, "sess",
		Op{Kind: "tab_create", TabID: "a", Title: "A"},
		Op{Kind: "tab_create", TabID: "b", Title: "B"},
		Op{Kind: "tab_create", TabID: "c", Title: "C"},
	)
	if _, err := s.ApplyOps("sess", []Op{{Kind: "tab_create", TabID: "a", Title: "Dup"}}); err == nil {
		t.Fatal("expected conflict for duplicate tab")
	}
	snap := mustApply(t, s, "sess", Op{Kind: "tabs_reorder", TabIDs: []string{"c", "a", "b"}})
	got := []string{snap.Tabs[0].TabID, snap.Tabs[1].TabID, snap.Tabs[2].TabID}
	if strings.Join(got, ",") != "c,a,b" {
		t.Fatalf("unexpected tab order: %v", got)
	}
	if _, err := s.ApplyOps("sess", []Op{{Kind: "tabs_reorder", TabIDs: []string{"c", "a"}}}); err == nil {
		t.Fatal("expected reorder error for incomplete tab list")
	}
	snap = mustApply(t, s, "sess", Op{Kind: "tab_update", TabID: "b", Position: intPtr(0)})
	if snap.Tabs[0].TabID != "b" {
		t.Fatalf("expected b first, got %+v", snap.Tabs)
	}
	// Deleting a tab moves its widgets to the first remaining tab.
	if _, err := s.PutWidget(PutParams{SessionKey: "sess", Name: "w1", Content: PutContent{Kind: ContentKindHTML, HTML: "<p>hi</p>"}, Placement: &PutPlacement{TabID: "b"}}); err != nil {
		t.Fatalf("put widget: %v", err)
	}
	snap, err := s.ApplyOps("sess", []Op{{Kind: "tab_delete", TabID: "b"}})
	if err != nil {
		t.Fatalf("tab_delete: %v", err)
	}
	if len(snap.Tabs) != 2 || len(snap.Widgets) != 1 {
		t.Fatalf("unexpected snapshot after delete: %+v", snap)
	}
	if snap.Widgets[0].TabID != snap.Tabs[0].TabID {
		t.Fatalf("orphaned widget was not moved to first tab: %+v", snap)
	}
}

func TestBoardDeletedWhenEmpty(t *testing.T) {
	s := NewStore()
	mustApply(t, s, "sess", Op{Kind: "tab_create", TabID: "main", Title: "Main"})
	snap := mustApply(t, s, "sess", Op{Kind: "tab_delete", TabID: "main"})
	if len(snap.Tabs) != 0 {
		t.Fatalf("expected no tabs: %+v", snap)
	}
	// Board is gone: revision restarts from zero.
	if got := s.GetSnapshot("sess"); got.Revision != 0 {
		t.Fatalf("expected fresh board after emptying, got %+v", got)
	}
}

func TestWidgetPutDefaultsAndMove(t *testing.T) {
	s := NewStore()
	snap, err := s.PutWidget(PutParams{SessionKey: "sess", Name: "chart", Content: PutContent{Kind: ContentKindHTML, HTML: "<p>1</p>"}})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if len(snap.Tabs) != 1 || snap.Tabs[0].TabID != "main" {
		t.Fatalf("expected default main tab: %+v", snap.Tabs)
	}
	w := snap.Widgets[0]
	if w.SizeW != 6 || w.SizeH != 4 || w.Revision != 1 || w.GrantState != GrantNone || w.InstanceID == "" {
		t.Fatalf("unexpected widget defaults: %+v", w)
	}
	// Re-put bumps widget revision and board revision, preserves size.
	snap, err = s.PutWidget(PutParams{SessionKey: "sess", Name: "chart", Content: PutContent{Kind: ContentKindHTML, HTML: "<p>2</p>"}})
	if err != nil {
		t.Fatalf("re-put: %v", err)
	}
	if snap.Widgets[0].Revision != 2 || snap.Revision != 2 {
		t.Fatalf("expected bumped revisions: %+v", snap)
	}
	// widget_move with an anchor.
	if _, err = s.PutWidget(PutParams{SessionKey: "sess", Name: "table", Content: PutContent{Kind: ContentKindHTML, HTML: "<p>t</p>"}}); err != nil {
		t.Fatalf("put table: %v", err)
	}
	snap = mustApply(t, s, "sess", Op{Kind: "widget_move", Name: "chart", After: "table"})
	if snap.Widgets[0].Name != "table" || snap.Widgets[1].Name != "chart" {
		t.Fatalf("unexpected order after move: %+v", snap.Widgets)
	}
	// widget_resize pins heightMode.
	snap = mustApply(t, s, "sess", Op{Kind: "widget_resize", Name: "chart", SizeW: 40, SizeH: 1})
	var chart Widget
	for _, candidate := range snap.Widgets {
		if candidate.Name == "chart" {
			chart = candidate
		}
	}
	if chart.SizeW != 12 || chart.SizeH != 1 || chart.HeightMode != "fixed" {
		t.Fatalf("unexpected resize result: %+v", chart)
	}
	// widget_remove drops the widget and its document.
	snap = mustApply(t, s, "sess", Op{Kind: "widget_remove", Name: "chart"})
	if len(snap.Widgets) != 1 || snap.Widgets[0].Name != "table" {
		t.Fatalf("unexpected widgets after remove: %+v", snap.Widgets)
	}
	if s.HasWidget("sess", "chart") {
		t.Fatal("removed widget still present")
	}
}

func TestWidgetLimitAndValidation(t *testing.T) {
	s := NewStore()
	for i := 0; i < MaxWidgets; i++ {
		if _, err := s.PutWidget(PutParams{SessionKey: "sess", Name: fmt.Sprintf("w%d", i), Content: PutContent{Kind: ContentKindHTML, HTML: "<p></p>"}}); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
	if _, err := s.PutWidget(PutParams{SessionKey: "sess", Name: "overflow", Content: PutContent{Kind: ContentKindHTML, HTML: "<p></p>"}}); err == nil {
		t.Fatal("expected widget limit error")
	}
	if _, err := s.PutWidget(PutParams{SessionKey: "s2", Name: "Bad Name", Content: PutContent{Kind: ContentKindHTML, HTML: "<p></p>"}}); err == nil {
		t.Fatal("expected name validation error")
	}
	if _, err := s.PutWidget(PutParams{SessionKey: "s2", Name: "big", Content: PutContent{Kind: ContentKindHTML, HTML: strings.Repeat("x", MaxWidgetHTMLBytes+1)}}); err == nil {
		t.Fatal("expected html size error")
	}
	if _, err := s.PutWidget(PutParams{SessionKey: "s2", Name: "p", Content: PutContent{Kind: ContentKindPlugin, PluginKind: "core:notes"}, Declared: &Declared{Tools: []string{"x"}}}); err == nil {
		t.Fatal("expected plugin declared rejection")
	}
	if _, err := s.PutWidget(PutParams{SessionKey: "s2", Name: "u", Content: PutContent{Kind: "mcp-app"}}); err == nil {
		t.Fatal("expected unsupported content kind error")
	}
}

func TestGrantFlow(t *testing.T) {
	s := NewStore()
	snap, err := s.PutWidget(PutParams{
		SessionKey: "sess", Name: "api",
		Content:  PutContent{Kind: ContentKindHTML, HTML: "<p>net</p>"},
		Declared: &Declared{NetOrigins: []string{"https://api.example.com"}, Tools: []string{"prompt"}},
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	w := snap.Widgets[0]
	if w.GrantState != GrantPending || len(w.DeclaredSummary) != 2 {
		t.Fatalf("expected pending grant with summary: %+v", w)
	}
	// Wrong revision conflicts.
	if _, err := s.Grant("sess", "api", GrantGranted, w.Revision+1, w.InstanceID); err == nil {
		t.Fatal("expected revision conflict")
	}
	// Wrong instance conflicts.
	if _, err := s.Grant("sess", "api", GrantGranted, w.Revision, "bogus"); err == nil {
		t.Fatal("expected instance conflict")
	}
	snap, err = s.Grant("sess", "api", GrantGranted, w.Revision, w.InstanceID)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if snap.Widgets[0].GrantState != GrantGranted || snap.Revision != 2 {
		t.Fatalf("unexpected granted snapshot: %+v", snap)
	}
	// Granting again is invalid: not pending anymore.
	if _, err := s.Grant("sess", "api", GrantGranted, snap.Widgets[0].Revision, snap.Widgets[0].InstanceID); err == nil {
		t.Fatal("expected not-pending error")
	}

	// Identical bytes + subset declaration preserves the grant.
	snap, err = s.PutWidget(PutParams{
		SessionKey: "sess", Name: "api",
		Content:  PutContent{Kind: ContentKindHTML, HTML: "<p>net</p>"},
		Declared: &Declared{NetOrigins: []string{"https://api.example.com"}},
	})
	if err != nil {
		t.Fatalf("re-put: %v", err)
	}
	if snap.Widgets[0].GrantState != GrantGranted {
		t.Fatalf("expected preserved grant: %+v", snap.Widgets[0])
	}
	// Widened declaration returns to pending.
	snap, err = s.PutWidget(PutParams{
		SessionKey: "sess", Name: "api",
		Content:  PutContent{Kind: ContentKindHTML, HTML: "<p>net</p>"},
		Declared: &Declared{NetOrigins: []string{"https://api.example.com", "https://evil.example.com"}},
	})
	if err != nil {
		t.Fatalf("re-put widened: %v", err)
	}
	if snap.Widgets[0].GrantState != GrantPending {
		t.Fatalf("expected pending after widening: %+v", snap.Widgets[0])
	}
	// Changed content returns to pending too.
	if _, err := s.Grant("sess", "api", GrantGranted, snap.Widgets[0].Revision, snap.Widgets[0].InstanceID); err != nil {
		t.Fatalf("grant widened: %v", err)
	}
	snap, err = s.PutWidget(PutParams{
		SessionKey: "sess", Name: "api",
		Content:  PutContent{Kind: ContentKindHTML, HTML: "<p>changed</p>"},
		Declared: &Declared{NetOrigins: []string{"https://api.example.com"}},
	})
	if err != nil {
		t.Fatalf("re-put changed bytes: %v", err)
	}
	if snap.Widgets[0].GrantState != GrantPending {
		t.Fatalf("expected pending after content change: %+v", snap.Widgets[0])
	}
}

func TestSnapshotIsolation(t *testing.T) {
	s := NewStore()
	if _, err := s.PutWidget(PutParams{SessionKey: "sess", Name: "w", Content: PutContent{Kind: ContentKindHTML, HTML: "<p></p>"}, Declared: &Declared{Tools: []string{"prompt"}}}); err != nil {
		t.Fatalf("put: %v", err)
	}
	snap := s.GetSnapshot("sess")
	snap.Widgets[0].GrantState = "granted"
	snap.Widgets[0].Declared.Tools[0] = "mutated"
	snap.Tabs[0].Title = "mutated"
	fresh := s.GetSnapshot("sess")
	if fresh.Widgets[0].GrantState != GrantPending || fresh.Widgets[0].Declared.Tools[0] != "prompt" || fresh.Tabs[0].Title == "mutated" {
		t.Fatalf("store state leaked mutation: %+v", fresh)
	}
}

func TestNoticeDeduperAndLimits(t *testing.T) {
	d := NewNoticeDeduper()
	now := time.Now()
	notice, ok, err := d.Render("sess", "chart", json.RawMessage(`{"clicked": 1}`), now)
	if err != nil || !ok {
		t.Fatalf("render: ok=%v err=%v", ok, err)
	}
	if notice != `[dashboard] {"clicked":1} on widget chart` {
		t.Fatalf("unexpected notice: %q", notice)
	}
	// Identical payload within window dedupes.
	if _, ok, _ := d.Render("sess", "chart", json.RawMessage(`{"clicked":1}`), now.Add(time.Second)); ok {
		t.Fatal("expected dedupe")
	}
	// Different widget is independent.
	if _, ok, _ := d.Render("sess", "other", json.RawMessage(`{"clicked":1}`), now.Add(time.Second)); !ok {
		t.Fatal("expected different widget to pass")
	}
	// Same payload after the window passes again.
	if _, ok, _ := d.Render("sess", "chart", json.RawMessage(`{"clicked":1}`), now.Add(6*time.Second)); !ok {
		t.Fatal("expected replay after window")
	}
	// Oversized payloads error.
	big := json.RawMessage(`"` + strings.Repeat("x", EventMaxBytes) + `"`)
	if _, _, err := d.Render("sess", "chart", big, now); err == nil {
		t.Fatal("expected size error")
	}
	// Invalid JSON errors.
	if _, _, err := d.Render("sess", "chart", json.RawMessage(`{oops`), now); err == nil {
		t.Fatal("expected serialization error")
	}
	// Long summaries are clipped with the widget suffix intact.
	long, ok, err := d.Render("sess", "chart", json.RawMessage(`"`+strings.Repeat("a", 600)+`"`), now.Add(10*time.Second))
	if err != nil || !ok {
		t.Fatalf("long render: ok=%v err=%v", ok, err)
	}
	if len(long) > 510 || !strings.HasSuffix(long, " on widget chart") || !strings.HasPrefix(long, "[dashboard] ") {
		t.Fatalf("unexpected clipped notice (len=%d): %q", len(long), long)
	}
}

func TestStoreConcurrency(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sessionKey := fmt.Sprintf("sess-%d", i%2)
			for j := 0; j < 25; j++ {
				name := fmt.Sprintf("w%d-%d", i, j%3)
				_, _ = s.PutWidget(PutParams{SessionKey: sessionKey, Name: name, Content: PutContent{Kind: ContentKindHTML, HTML: "<p></p>"}})
				_ = s.GetSnapshot(sessionKey)
				_, _ = s.ApplyOps(sessionKey, []Op{{Kind: "widget_resize", Name: name, SizeW: 4, SizeH: 4}})
			}
		}(i)
	}
	wg.Wait()
	for _, sessionKey := range []string{"sess-0", "sess-1"} {
		snap := s.GetSnapshot(sessionKey)
		if snap.Revision == 0 || len(snap.Widgets) == 0 {
			t.Fatalf("expected populated board for %s: %+v", sessionKey, snap)
		}
	}
}
