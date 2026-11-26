package app

import (
	"fmt"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
)

func TestShowScrollAndFocusFollow(t *testing.T) {
	a, d := demo(t)

	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprintf("- BODY_LINE_%02d: full scrollable note context", i))
	}
	note, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindNote,
		ID:   "long-note",
		Fields: map[string]any{
			"title": "Long scrollable note",
			"body":  strings.Join(lines, "\n"),
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindNote,
		ID:   "short-note",
		Fields: map[string]any{
			"title": "Short note",
			"body":  "Just one line.",
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	// Open the long note's Show page.
	d.Send(core.CommandEvent{Command: core.Command{
		ID:     "record.open",
		Args:   map[string]any{"ref": map[string]any{"kind": note.Kind, "id": note.ID}},
		Source: "test",
		Actor:  User,
	}})
	if cur, ok := a.Showing(); !ok || cur != note.Ref() || a.Focus() != showHeader {
		t.Fatalf("expected Show page for long-note focused on show.header: ok=%v cur=%v focus=%q", ok, cur, a.Focus())
	}

	topFrame := d.Frame()
	if !strings.Contains(topFrame, "BODY_LINE_01") {
		t.Fatalf("expected top of body visible initially:\n%s", topFrame)
	}
	if strings.Contains(topFrame, "BODY_LINE_45") {
		t.Fatalf("expected BODY_LINE_45 below initial fold so scroll is testable:\n%s", topFrame)
	}

	// Explicit single-line scroll: ctrl+e down, ctrl+y up.
	d.Keys("ctrl+e")
	if explicitOff(a.nav.showRelatedOff) != 1 || a.Focus() != showHeader {
		t.Fatalf("ctrl+e expected showRelatedOff=1 focus=show.header, got off=%d focus=%q", explicitOff(a.nav.showRelatedOff), a.Focus())
	}
	d.Keys("ctrl+y")
	if explicitOff(a.nav.showRelatedOff) != 0 {
		t.Fatalf("ctrl+y expected showRelatedOff=0, got %d", explicitOff(a.nav.showRelatedOff))
	}

	// Half-page and full-page scroll on show.header reveal lines deep in the body.
	d.Keys("ctrl+d")
	halfOff := explicitOff(a.nav.showRelatedOff)
	if halfOff <= 0 || a.Focus() != showHeader {
		t.Fatalf("ctrl+d should scroll show.related while staying on show.header: off=%d focus=%q", halfOff, a.Focus())
	}
	d.Keys("ctrl+f", "ctrl+f", "ctrl+f")
	scrolledFrame := d.Frame()
	if !strings.Contains(scrolledFrame, "BODY_LINE_45") {
		t.Fatalf("expected BODY_LINE_45 visible after scrolling down:\n%s", scrolledFrame)
	}
	// Clamped against MaxOff.
	info := a.State().Layout.Scrolls["show.related"]
	if info.MaxOff <= 0 || explicitOff(a.nav.showRelatedOff) > info.MaxOff {
		t.Fatalf("expected 0 < off <= MaxOff (%d), got %d", info.MaxOff, explicitOff(a.nav.showRelatedOff))
	}

	// ctrl+u / ctrl+b scroll back up cleanly.
	d.Keys("ctrl+b", "ctrl+b", "ctrl+b", "ctrl+u", "ctrl+u")
	if explicitOff(a.nav.showRelatedOff) != 0 || !strings.Contains(d.Frame(), "BODY_LINE_01") {
		t.Fatalf("expected scroll back to top: off=%d", explicitOff(a.nav.showRelatedOff))
	}

	// Moving focus into show.related with j follows focus from the stored
	// offset: the focused card ends up fully inside the viewport.
	d.Keys("ctrl+d")
	if explicitOff(a.nav.showRelatedOff) <= 0 {
		t.Fatal("expected non-zero showRelatedOff before j")
	}
	d.Keys("j")
	if a.Focus() == showHeader {
		t.Fatal("j from show.header should move focus to a related section/card")
	}
	d.Frame()
	info = a.State().Layout.Scrolls["show.related"]
	if r, ok := info.ChildRects[a.Focus()]; !ok || r.Y < info.Offset || r.Y+r.H > info.Offset+info.ViewH {
		t.Fatalf("moving focus into show.related should reveal the focused card: rect=%+v off=%d viewH=%d", r, info.Offset, info.ViewH)
	}

	// Navigating to a different Show record resets scroll state.
	d.Keys("k", "ctrl+d")
	if explicitOff(a.nav.showRelatedOff) <= 0 {
		t.Fatal("expected non-zero showRelatedOff before opening other record")
	}
	d.Send(core.CommandEvent{Command: core.Command{
		ID:     "record.open",
		Args:   map[string]any{"ref": map[string]any{"kind": other.Kind, "id": other.ID}},
		Source: "test",
		Actor:  User,
	}})
	if a.nav.showScrollRef != other.Ref() || a.nav.showRelatedOff != nil || a.nav.showPropsOff != nil {
		t.Fatalf("opening another Show record should reset scroll state: ref=%v rel=%d props=%d",
			a.nav.showScrollRef, explicitOff(a.nav.showRelatedOff), explicitOff(a.nav.showPropsOff))
	}
}

// explicitOff reads an explicit Show scroll offset, -1 when none is set.
func explicitOff(off *int) int {
	if off == nil {
		return -1
	}
	return *off
}

func TestShowFileBackedBodyAndContextDetailWrap(t *testing.T) {
	a, d := demo(t)

	const diskPath = "/virtual/docs/architecture.md"
	const diskContent = "# Architecture Spec\n\n- Disk bullet alpha\n- Disk bullet beta"
	a.opts.ReadFile = func(path string) ([]byte, error) {
		if path == diskPath {
			return []byte(diskContent), nil
		}
		return nil, fmt.Errorf("not found: %s", path)
	}

	doc, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindDocument,
		ID:   "file-doc",
		Fields: map[string]any{
			"title":    "Architecture Spec",
			"filePath": diskPath,
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	d.Send(core.CommandEvent{Command: core.Command{
		ID:     "record.open",
		Args:   map[string]any{"ref": map[string]any{"kind": doc.Kind, "id": doc.ID}},
		Source: "test",
		Actor:  User,
	}})
	frame := d.Frame()
	if !strings.Contains(frame, "Disk bullet alpha") || !strings.Contains(frame, "Disk bullet beta") {
		t.Fatalf("expected file-backed document Show page to render full disk body:\n%s", frame)
	}

	// buildProjectContextDetailCard wraps long Notes without FixedH: 1 clipping.
	card := a.buildProjectContextDetailCard(CollectedContextItem{
		Key:    "ctx-1",
		Kind:   "url",
		Source: "curated",
		Label:  "Design Spec",
		Value:  "https://example.com/spec",
		Notes:  "Line one of context notes\nLine two of context notes that must wrap instead of clipping to one line",
	}, "context", false)
	notesNode := card.Find("project.ctx.detail.notes")
	if notesNode == nil || notesNode.Kind != ui.KindText || notesNode.Box.FixedH != 0 || notesNode.Props["wrap"] != true {
		t.Fatalf("expected wrapping KindText (FixedH=0, wrap=true) for project.ctx.detail.notes, got %+v", notesNode)
	}
}

// TestShowRelatedFocusFollowPadding: moving focus through a long right column
// keeps ui.scrolloff rows between the focused card and the viewport edge it
// approaches (bottom edge of the card near the bottom, top edge near the
// top), and the offset stays put while the card is inside the padded window.
func TestShowRelatedFocusFollowPadding(t *testing.T) {
	a, d := demo(t)
	openRelatedHub(t, a, d, 30)
	walkScrollFollow(t, a, d, "show.related")
}

// TestShowExplicitScrollThenFocusMoveKeepsView: after scrolling the related
// column with the scroll keys, moving focus continues from the scrolled
// offset (no snap back): the view moves only as far as the padded reveal of
// the newly focused card needs.
func TestShowExplicitScrollThenFocusMoveKeepsView(t *testing.T) {
	a, d := demo(t)
	openRelatedHub(t, a, d, 30)
	// Walk into the column until it has scrolled once.
	var info layout.ScrollInfo
	for range 40 {
		d.Keys("j")
		d.Frame()
		info = a.State().Layout.Scrolls["show.related"]
		if _, in := info.ChildRects[a.Focus()]; in && info.Offset > 0 {
			break
		}
	}
	if info.Offset == 0 {
		t.Fatal("related column never scrolled")
	}
	d.Keys("ctrl+e", "ctrl+e", "ctrl+e")
	d.Frame()
	explicit := explicitOff(a.nav.showRelatedOff)
	if info = a.State().Layout.Scrolls["show.related"]; explicit <= 0 || info.Offset != explicit {
		t.Fatalf("ctrl+e should scroll the column explicitly: explicit=%d drawn=%d", explicit, info.Offset)
	}
	d.Keys("j")
	if a.nav.showRelatedOff != nil {
		t.Fatalf("a focus move should drop the explicit offset, got %d", explicitOff(a.nav.showRelatedOff))
	}
	d.Frame()
	info = a.State().Layout.Scrolls["show.related"]
	span, ok := scrollSpanOf(info, a.Focus())
	if !ok {
		t.Fatalf("focused card %q not in show.related", a.Focus())
	}
	want := traits.ScrollReveal(traits.ScrollState{OffsetY: explicit, ContentH: info.MaxOff + info.ViewH, ViewH: info.ViewH},
		traits.ScrollConfig{ScrollOff: a.scrollOff()}, span).OffsetY
	if info.Offset != want || info.Offset < explicit {
		t.Fatalf("j after ctrl+e: offset %d, want %d (continuing from explicit %d)", info.Offset, want, explicit)
	}
}

// openRelatedHub creates a hub note with n related notes and opens its Show
// page.
func openRelatedHub(t *testing.T, a *App, d *Driver, n int) {
	t.Helper()
	hub, _, err := a.Store.Create(data.CreateInput{Kind: data.KindNote, ID: "hub-note",
		Fields: map[string]any{"title": "Hub note"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		n, _, err := a.Store.Create(data.CreateInput{Kind: data.KindNote, ID: fmt.Sprintf("rel-%02d", i),
			Fields: map[string]any{"title": fmt.Sprintf("Related note %02d", i)}}, User)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.Store.Relate(data.RelateInput{From: n.Ref(), Kind: data.RelAbout, To: hub.Ref()}, User); err != nil {
			t.Fatal(err)
		}
	}
	d.Send(core.CommandEvent{Command: core.Command{ID: "record.open",
		Args: map[string]any{"ref": map[string]any{"kind": hub.Kind, "id": hub.ID}}, Source: "test", Actor: User}})
	d.Frame()
}
