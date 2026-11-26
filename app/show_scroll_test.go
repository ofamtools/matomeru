package app

import (
	"fmt"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
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
	if a.nav.showRelatedOff != 1 || a.Focus() != showHeader {
		t.Fatalf("ctrl+e expected showRelatedOff=1 focus=show.header, got off=%d focus=%q", a.nav.showRelatedOff, a.Focus())
	}
	d.Keys("ctrl+y")
	if a.nav.showRelatedOff != 0 {
		t.Fatalf("ctrl+y expected showRelatedOff=0, got %d", a.nav.showRelatedOff)
	}

	// Half-page and full-page scroll on show.header reveal lines deep in the body.
	d.Keys("ctrl+d")
	halfOff := a.nav.showRelatedOff
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
	if info.MaxOff <= 0 || a.nav.showRelatedOff > info.MaxOff {
		t.Fatalf("expected 0 < off <= MaxOff (%d), got %d", info.MaxOff, a.nav.showRelatedOff)
	}

	// ctrl+u / ctrl+b scroll back up cleanly.
	d.Keys("ctrl+b", "ctrl+b", "ctrl+b", "ctrl+u", "ctrl+u")
	if a.nav.showRelatedOff != 0 || !strings.Contains(d.Frame(), "BODY_LINE_01") {
		t.Fatalf("expected scroll back to top: off=%d", a.nav.showRelatedOff)
	}

	// Moving focus into show.related with j follows focus from the stored
	// offset: the focused card ends up fully inside the viewport.
	d.Keys("ctrl+d")
	if a.nav.showRelatedOff == 0 {
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
	if a.nav.showRelatedOff == 0 {
		t.Fatal("expected non-zero showRelatedOff before opening other record")
	}
	d.Send(core.CommandEvent{Command: core.Command{
		ID:     "record.open",
		Args:   map[string]any{"ref": map[string]any{"kind": other.Kind, "id": other.ID}},
		Source: "test",
		Actor:  User,
	}})
	if a.nav.showScrollRef != other.Ref() || a.nav.showRelatedOff != 0 || a.nav.showPropsOff != 0 {
		t.Fatalf("opening another Show record should reset scroll state: ref=%v rel=%d props=%d",
			a.nav.showScrollRef, a.nav.showRelatedOff, a.nav.showPropsOff)
	}
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
	hub, _, err := a.Store.Create(data.CreateInput{Kind: data.KindNote, ID: "hub-note",
		Fields: map[string]any{"title": "Hub note"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 30 {
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

	pad := a.scrollOff()
	type geo struct{ off, top, bottom, viewH, maxOff int }
	look := func() (geo, bool) {
		d.Frame()
		info, ok := a.State().Layout.Scrolls["show.related"]
		r, inside := info.ChildRects[a.Focus()]
		if !ok || !inside {
			return geo{}, false
		}
		return geo{info.Offset, r.Y, r.Y + r.H, info.ViewH, info.MaxOff}, true
	}
	check := func(step string, g geo) {
		t.Helper()
		p := min(pad, max(0, (g.viewH-(g.bottom-g.top))/2))
		if g.off < g.maxOff && g.off+g.viewH-g.bottom < p {
			t.Fatalf("%s: focused card bottom %d is %d rows from viewport bottom %d, want >= %d", step, g.bottom, g.off+g.viewH-g.bottom, g.off+g.viewH, p)
		}
		if g.off > 0 && g.top-g.off < p {
			t.Fatalf("%s: focused card top %d is %d rows from viewport top %d, want >= %d", step, g.top, g.top-g.off, g.off, p)
		}
	}

	var last geo
	scrolledDown := false
	for i := range 40 {
		d.Keys("j")
		g, ok := look()
		if !ok {
			continue
		}
		check(fmt.Sprintf("j #%d", i+1), g)
		last = g
		if g.off > 0 {
			scrolledDown = true
		}
		if scrolledDown && g.off < g.maxOff && g.bottom > 2*g.viewH {
			break
		}
	}
	if !scrolledDown {
		t.Fatalf("related column never scrolled; content too short for the test (last %+v)", last)
	}
	// Moving back up: the offset must not change while the focused card is
	// still inside the padded window, and near the top the top edge is padded.
	for i := range 40 {
		prev := last
		d.Keys("k")
		g, ok := look()
		if !ok {
			break
		}
		check(fmt.Sprintf("k #%d", i+1), g)
		p := min(pad, max(0, (g.viewH-(g.bottom-g.top))/2))
		if g.top-prev.off >= p && g.off != prev.off {
			t.Fatalf("k #%d: offset moved %d -> %d although card top %d was inside the padded window", i+1, prev.off, g.off, g.top)
		}
		last = g
	}
}
