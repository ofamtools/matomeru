package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
	"ofam.dev/ochazuke/sysstat"
)

// mtm turns on the cpu, proc (its own CPU), ram and disk segments, the
// disk one on the data directory's volume; each has its own opt-out setting and the area one
// more; an extension's catalog segments mount against its proxy verbs.
func TestSegmentsOnByDefaultWithOptOuts(t *testing.T) {
	t.Parallel()
	app, err := New(Options{Data: seeded(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	r := app.r
	r.EnsureConfigSettings()
	// New (not openWith, which turns them off for frame tests) keeps
	// mtm's defaults.
	shown := func() map[string]bool {
		out := map[string]bool{}
		for _, s := range r.Segments() {
			out[s.ID] = s.Shown
		}
		return out
	}
	got := r.Segments()
	if len(got) != 4 || got[0].ID != "cpu" || got[1].ID != "proc" || got[2].ID != "ram" || got[3].ID != "disk" {
		t.Fatalf("segments = %+v", got)
	}
	for _, s := range got {
		if !s.Shown || s.Error != "" {
			t.Fatalf("%s: shown %t error %q", s.ID, s.Shown, s.Error)
		}
	}
	if got[3].Path != app.dataDir() {
		t.Fatalf("disk reads %q, want the data directory %q", got[3].Path, app.dataDir())
	}
	for _, name := range []string{ochazuke.SegmentsSetting, ochazuke.SegmentSetting("cpu"), ochazuke.SegmentSetting("proc"), ochazuke.SegmentSetting("ram"), ochazuke.SegmentSetting("disk"), ochazuke.SegmentOrderSetting, sysstat.ProcScaleSetting} {
		if _, err := r.Settings().Get(name); err != nil {
			t.Fatalf("setting %s: %v", name, err)
		}
	}
	if err := r.Settings().Set(ochazuke.SegmentSetting("cpu"), false); err != nil {
		t.Fatal(err)
	}
	if s := shown(); s["cpu"] || !s["proc"] || !s["ram"] || !s["disk"] {
		t.Fatalf("after turning cpu off: %v", s)
	}
	if err := r.Settings().Set(ochazuke.SegmentsSetting, false); err != nil {
		t.Fatal(err)
	}
	if s := shown(); s["proc"] || s["ram"] || s["disk"] || len(r.SegmentCells()) != 0 {
		t.Fatalf("the area is off: %v", s)
	}

	r.Define("ext-tool.usage").Verb("brief", func(ochazuke.Ctx) ochazuke.Result { return ochazuke.Done("3 runs") }, "Proxy")
	x := &extension{name: "tool", doc: ocli.CatalogDoc{Segments: []ocli.CatalogSegment{{ID: "usage", Label: "ai", Verb: "usage.brief", Every: "30s"}}}}
	app.mountExtensionSegments(x, func(n string) string { return "ext-tool." + n })
	var mounted *ochazuke.SegmentState
	for _, s := range r.Segments() {
		if s.ID == "tool-usage" {
			mounted = &s
		}
	}
	if mounted == nil || mounted.Verb != "ext-tool.usage.brief" || mounted.Error != "" || mounted.Source != "verb" {
		t.Fatalf("extension segment = %+v", mounted)
	}
	if _, err := r.Settings().Get(ochazuke.SegmentSetting("tool-usage")); err != nil {
		t.Fatalf("the extension segment has no setting: %v", err)
	}
}

// With the segments on, mtm's header rule carries the live readings,
// right-aligned, and the body keeps its rows.
func TestSegmentsDrawInTheHeaderRule(t *testing.T) {
	t.Parallel()
	app := openWith(t, Options{Data: seeded(t)})
	app.m.Width, app.m.Height = 100, 24
	before := strings.Split(plainView(app), "\n")
	for _, id := range []string{"cpu", "ram", "disk"} {
		if err := app.r.AddSegment(ochazuke.Segment{ID: id, Enabled: ochazuke.On(true)}); err != nil {
			t.Fatal(err)
		}
	}
	got := make(chan tea.Msg, 16)
	var start func(tea.Cmd)
	start = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			switch msg := c().(type) {
			case tea.BatchMsg:
				for _, c := range msg {
					start(c)
				}
			case ochazuke.SegmentMsg:
				got <- msg
			}
		}()
	}
	_, cmd := app.m.Update(ochazuke.SegmentTickMsg{})
	start(cmd)
	deadline := time.After(5 * time.Second)
	for app.m.SegmentsInFlight() > 0 {
		select {
		case msg := <-got:
			app.m.Update(msg)
		case <-deadline:
			t.Fatal("the segments never answered")
		}
	}
	rows := strings.Split(plainView(app), "\n")
	t.Logf("frame:\n%s", strings.Join(rows[:4], "\n"))
	rule := strings.TrimSpace(rows[2])
	for _, want := range []string{"cpu ", "% · ram ", "G · disk ", "──"} {
		if !strings.Contains(rule, want) {
			t.Fatalf("header rule %q lacks %q", rule, want)
		}
	}
	if !strings.HasSuffix(rule, " ──") || rows[3] != before[3] || len(rows) != len(before) {
		t.Fatalf("the segments moved the body:\n%s", strings.Join(rows, "\n"))
	}
}
