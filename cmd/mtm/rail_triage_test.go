package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/matomeru/internal/panesignals"
)

// The rail's Needs you lists the pane's open items oldest first, each
// labelled by what it asks (never the pane's name alone), with 100 copies
// of one finished turn folded to one row with its count; the read ones
// sit under Resolved below.
func TestRailNeedsYouFoldsByLabelOldestFirst(t *testing.T) {
	t.Parallel()
	a, _, live, _ := liveContextApp(t)
	clock := int64(1_000_000)
	a.inbox.items.SetClock(func() int64 { return clock })
	add := func(key, kind, why string, read bool) {
		t.Helper()
		it, _, err := a.inbox.items.Add(actionitems.Spec{Source: panesignals.Source, Key: key, Kind: kind, Title: live.label(), Why: why,
			Window: live.ID, PaneLabel: live.label()})
		if err != nil {
			t.Fatal(err)
		}
		if read {
			if _, err := a.inbox.items.SetRead(it.ID, true); err != nil {
				t.Fatal(err)
			}
		}
		clock += 60_000
	}
	add("input", panesignals.NeedsInput, panesignals.Why(panesignals.NeedsInput), false)
	for i := range 100 {
		add(fmt.Sprintf("done-%d", i), panesignals.Done, panesignals.Why(panesignals.Done), false)
	}
	add("seen", panesignals.Errored, panesignals.Why(panesignals.Errored), true)
	a.rail.Stack().Refill()
	text := railText(a, 240, 50)
	input := strings.Index(text, "! waits for your input · ")
	done := strings.Index(text, "! turn finished ×100 · ")
	if input < 0 || done < 0 || input > done {
		t.Fatalf("Needs you is not the open items oldest first, folded:\n%s", text)
	}
	if strings.Count(text, "turn finished") != 1 {
		t.Fatalf("the 100 finished turns are not one row:\n%s", text)
	}
	resolved := strings.Index(text, "Resolved (1)")
	if resolved < done || strings.Contains(text, "agent errored") {
		t.Fatalf("the read item is not folded under Resolved below Needs you:\n%s", text)
	}
}

// Starred items stay on the rail; dismissed ones leave it (rail.dismissed
// hide) or fold under one Dismissed heading (collapsed); the rest are New.
func TestRailTriageSectionsByDismissedSetting(t *testing.T) {
	t.Parallel()
	a, _, live, _ := liveContextApp(t)
	ids := map[string]string{}
	for _, it := range contextItems(t, a) {
		if it.Window == live.ID {
			ids[it.Kind] = it.ID
		}
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: contextNoun, Verb: "star", Args: ochazuke.Arguments{"id": ids["link"], "value": true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: contextNoun, Verb: "dismiss", Args: ochazuke.Arguments{"id": ids["file"]}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		value string
		want  []string
		not   []string
	}{
		{railDismissedHide, []string{"▾ Starred (1)", "https://example.com/older-here"}, []string{"Dismissed", "New (", "main.go"}},
		{railDismissedFold, []string{"▾ Starred (1)", "▸ Dismissed (1)"}, []string{"New (", "main.go"}},
	} {
		if err := a.r.Settings().Set(settingRailDismissed, c.value); err != nil {
			t.Fatal(err)
		}
		a.rail.Stack().Refill()
		text := railText(a, 240, 40)
		for _, want := range c.want {
			if !strings.Contains(text, want) {
				t.Fatalf("rail.dismissed %s: rail misses %q:\n%s", c.value, want, text)
			}
		}
		for _, not := range c.not {
			if strings.Contains(text, not) {
				t.Fatalf("rail.dismissed %s: rail shows %q:\n%s", c.value, not, text)
			}
		}
	}
}

// A bulk dismissal writes every matching item with one stamp; undismiss
// over that stamp puts them back.
func TestContextDismissBulkIsOneBatch(t *testing.T) {
	t.Parallel()
	a, _, _, _ := liveContextApp(t)
	out := executeA(t, a, contextNoun, "dismiss", "", ochazuke.Arguments{"all": true})
	res, _ := out.(map[string]any)
	stamp, _ := res["dismissed-at"].(int64)
	all := contextItems(t, a)
	if res["count"] != len(all) || stamp == 0 {
		t.Fatalf("dismiss --all answered %v over %d items", out, len(all))
	}
	for _, it := range all {
		if !it.Dismissed || it.DismissedAt != stamp {
			t.Fatalf("item %s not dismissed in the batch: %+v", it.ID, it)
		}
	}
	executeA(t, a, contextNoun, "undismiss", "", ochazuke.Arguments{"filter": fmt.Sprintf("dismissed-at=%d", stamp)})
	for _, it := range contextItems(t, a) {
		if it.Dismissed {
			t.Fatalf("item %s still dismissed after undismiss of the batch", it.ID)
		}
	}
}

// Every glyph a rail row starts with is one cell wide, in every style.
func TestRailGlyphsAreOneCell(t *testing.T) {
	t.Parallel()
	for kind, g := range railGlyphs {
		for style, glyph := range map[string]string{railIconsSymbols: g.symbol, railIconsNerd: g.nerd, railIconsLetters: g.letter} {
			if w := ansi.StringWidth(glyph); w != 1 {
				t.Errorf("%s %s glyph %q is %d cells", kind, style, glyph, w)
			}
		}
	}
}

// contextItems are every stored context item.
func contextItems(t *testing.T, a *App) []contextitems.Item {
	t.Helper()
	all, err := a.contexts.All()
	if err != nil {
		t.Fatal(err)
	}
	return all
}
