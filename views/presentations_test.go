package views

import (
	"encoding/json"
	"regexp"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// rawRefRe matches a backend reference rendered as text ("task:86218e2dfd81").
var rawRefRe = regexp.MustCompile(`\b[a-z][a-z.]*:[0-9a-f]{8,}\b`)

func noRawRefs(t *testing.T, what string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	// The Ref itself is the typed identity; only display text is checked.
	var shown struct {
		Label, Text, Title string
		Props              map[string]any
	}
	_ = json.Unmarshal(b, &shown)
	for _, s := range []string{shown.Label, shown.Text, shown.Title, data.Text(shown.Props["summary"])} {
		if rawRefRe.MatchString(s) {
			t.Errorf("%s shows a raw reference: %q", what, s)
		}
	}
}

func TestPresenters(t *testing.T) {
	ref := core.Ref{Kind: data.KindTask, ID: "86218e2dfd81"}
	p := data.ResolvedPresentation{Ref: ref, Form: data.FormCard, Icon: "▶", Label: "Ship zero-field", Subtitle: "the chips",
		Tone: "state.working", Meta: []string{"P1"}, Badges: []data.PresentationBadge{{Text: "Active", Tone: "state.working"}}}

	chip := PresentChipRun(p)
	if len(chip) != 1 || chip[0].Kind != "badge" || chip[0].Label != "▶ Ship zero-field" || chip[0].Tone != "state.working" || *chip[0].Ref != ref {
		t.Fatalf("chip = %+v", chip)
	}
	link := PresentInlineRun(p)
	if link.Kind != "link" || link.Label != "▶ Ship zero-field" || *link.Ref != ref {
		t.Fatalf("inline = %+v", link)
	}
	card := PresentCardNode("c1", p)
	if card.Kind != ui.KindCard || !card.Focusable || card.Title != "▶ Ship zero-field" || *card.Ref != ref || card.Scope != data.KindTask ||
		card.Props["summary"] != "the chips  ·  P1" || len(card.Badges) != 1 || card.Badges[0].Text != "Active" {
		t.Fatalf("card = %+v", card)
	}
	header := PresentDetailHeader("show.header", p)
	if header.ID != "show.header" || header.Kind != ui.KindCard || header.Facts["detail"] != true || *header.Ref != ref {
		t.Fatalf("detail = %+v", header)
	}
	for what, v := range map[string]any{"chip": chip[0], "inline": link, "card": card, "detail": header} {
		noRawRefs(t, what, v)
	}

	missing := data.MissingPresentation(ref, data.FormChip)
	gone := PresentChipRun(missing)
	if gone[0].Label != "(missing task)" || gone[0].Tone != "muted" || *gone[0].Ref != ref {
		t.Fatalf("missing chip = %+v", gone)
	}
	if card := PresentCardNode("c2", missing); card.Facts["missing"] != true || card.Title != "(missing task)" {
		t.Fatalf("missing card = %+v", card)
	}
	noRawRefs(t, "missing chip", gone[0])
}

func TestShowDeclarativePresentationsHideDeadRefIDs(t *testing.T) {
	dead := core.Ref{Kind: data.KindTask, ID: "2b161fe055c9"}
	pane, _ := data.DefaultSchemas().Get(data.KindPane)
	snap := data.ShowSnapshot{
		Record: data.Record{Kind: data.KindPane, ID: "p1", Fields: map[string]any{"title": "worker", "task": dead}},
		Schema: pane,
		FieldLinks: map[string]data.ShowLink{"task": {Ref: dead, Schema: pane,
			Presentation: data.MissingPresentation(dead, data.FormChip)}},
	}
	field := func(opts ShowOptions) string {
		root := ShowViewWith(snap, "", opts)
		n := root.Find("show.field.task")
		if n == nil {
			t.Fatal("no show.field.task")
		}
		return data.Text(n.Props["text"]) + "|" + n.Title
	}
	on := DefaultShowOptions()
	if got := field(on); rawRefRe.MatchString(got) || !regexp.MustCompile(`\(missing task\)`).MatchString(got) {
		t.Fatalf("declarative field = %q", got)
	}
	off := on
	off.DeclarativePresentations = false
	if got := field(off); !rawRefRe.MatchString(got) {
		t.Fatalf("opt-out field = %q, want the raw reference kept", got)
	}
}
