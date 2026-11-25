package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func seedComposerContextFixtures(t *testing.T) (*data.Store, data.Record, data.Record, data.Record, data.Record) {
	t.Helper()
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}

	proj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "proj-ctx",
		Fields: map[string]any{"title": "Project Context Feature"},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	sub, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "sub-ctx",
		Fields: map[string]any{"title": "Wire composer", "parent": proj.Ref()},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	gdoc, _, err := store.AddOfficialProjectContext(data.OfficialContextInput{
		ProjectID: proj.ID,
		Title:     "Design Doc",
		Value:     "https://docs.google.com/document/d/design-123/edit",
		Notes:     "Canonical architecture specification",
		Pinned:    true,
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	sheet, _, err := store.AddOfficialProjectContext(data.OfficialContextInput{
		ProjectID: proj.ID,
		Title:     "Metrics Sheet",
		Value:     "https://docs.google.com/spreadsheets/d/metrics-456/edit",
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	scraped, _, err := store.Create(data.CreateInput{
		Kind: data.KindContext,
		ID:   "ctx-scraped",
		Fields: map[string]any{
			"title":       "Scraped Debug URL",
			"value":       "https://example.com/debug-trace",
			"contextKind": "url",
			"source":      "screen",
			"task":        sub.Ref(),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	// Attach pane to the child subtask without promptedAt to test root project
	// resolution and first-prompt detection.
	pane, _, err := store.Create(data.CreateInput{
		Kind: data.KindPane,
		ID:   "pane-ctx",
		Fields: map[string]any{
			"title":     "claude: composer",
			"harness":   "claude",
			"runStatus": "running",
			"task":      sub.Ref(),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	return store, *pane, gdoc, sheet, *scraped
}

func TestComposerOfficialProjectContextAutoSelectToggleAndCherryPick(t *testing.T) {
	store, pane, gdoc, sheet, scraped := seedComposerContextFixtures(t)
	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 160, 40)

	// 1. Opening composer auto-selects ONLY the 2 official items (never scraped!).
	d.Keys("c")
	cm := a.activeComposer()
	if cm == nil || cm.projCtx == nil {
		t.Fatalf("expected active composer with projCtx, got cm=%v", cm)
	}
	if !cm.projCtx.enabled || !cm.projCtx.firstPrompt {
		t.Fatalf("expected enabled=true firstPrompt=true, got enabled=%v firstPrompt=%v", cm.projCtx.enabled, cm.projCtx.firstPrompt)
	}
	sel := cm.selectedProjectContext()
	if len(sel) != 2 || sel[0].ID != gdoc.ID || sel[1].ID != sheet.ID {
		t.Fatalf("auto-selected context = %v, want [%s %s] (scraped %s excluded)", sel, gdoc.ID, sheet.ID, scraped.ID)
	}
	banner := cm.projCtx.bannerNode()
	if banner == nil || banner.Tone != "accent" {
		t.Fatalf("expected accent banner node, got %+v", banner)
	}
	bannerText, _ := banner.Props["text"].(string)
	for _, want := range []string{"★ Project Context (2/2 official)", "Design Doc", "Metrics Sheet", "first prompt", "ctrl+g off · ctrl+o pick"} {
		if !strings.Contains(bannerText, want) {
			t.Fatalf("banner missing %q: %q", want, bannerText)
		}
	}
	if strings.Contains(bannerText, "Scraped Debug URL") {
		t.Fatalf("banner must not include scraped item by default: %q", bannerText)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Project Context (2/2 official)") {
		t.Fatalf("rendered composer frame missing official context banner:\n%s", frame)
	}

	// Submit prompt with default official context -> delivered payload contains
	// ## Official Project Context with gdoc + sheet, excluding scraped URL.
	d.Type("Implement phase 2")
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("ctrl+s did not close composer: modal=%q", a.Modal())
	}
	delivered1 := strings.Join(a.termOf(pane.ID).lines, "\n")
	for _, want := range []string{"Implement phase 2", "## Official Project Context", "[gdoc] **Design Doc**", "[sheet] **Metrics Sheet**"} {
		if !strings.Contains(delivered1, want) {
			t.Fatalf("first delivered prompt missing %q:\n%s", want, delivered1)
		}
	}
	if strings.Contains(delivered1, scraped.Text("value")) {
		t.Fatalf("first delivered prompt must not contain scraped item:\n%s", delivered1)
	}

	// 2. ctrl+g opts out cleanly and pressing ctrl+g again re-enables.
	a.termOf(pane.ID).lines = nil
	d.Keys("c")
	cm = a.activeComposer()
	if cm == nil || cm.projCtx == nil {
		t.Fatal("expected composer reopened")
	}
	if cm.projCtx.firstPrompt {
		t.Fatal("second prompt after RecordPaneInput should have firstPrompt=false")
	}
	d.Keys("ctrl+g")
	if cm.projCtx.enabled || len(cm.selectedProjectContext()) != 0 {
		t.Fatalf("ctrl+g did not opt out: enabled=%v sel=%d", cm.projCtx.enabled, len(cm.selectedProjectContext()))
	}
	offBanner := cm.projCtx.bannerNode()
	if offBanner == nil || offBanner.Tone != "muted" {
		t.Fatalf("expected muted banner when opted out, got %+v", offBanner)
	}
	if txt, _ := offBanner.Props["text"].(string); !strings.Contains(txt, "○ Project Context: off (2 official available)") || !strings.Contains(txt, "ctrl+g on · ctrl+o pick") {
		t.Fatalf("unexpected opted-out banner text: %q", txt)
	}

	// Press ctrl+g again -> re-enables official set.
	d.Keys("ctrl+g")
	if !cm.projCtx.enabled || len(cm.selectedProjectContext()) != 2 {
		t.Fatalf("second ctrl+g did not re-enable official context: enabled=%v sel=%d", cm.projCtx.enabled, len(cm.selectedProjectContext()))
	}

	// Opt out once more and submit -> delivered prompt omits ## Official Project Context.
	d.Keys("ctrl+g")
	d.Type("Quick status check")
	d.Keys("ctrl+s")
	delivered2 := strings.Join(a.termOf(pane.ID).lines, "\n")
	if !strings.Contains(delivered2, "Quick status check") {
		t.Fatalf("opted-out prompt text missing:\n%s", delivered2)
	}
	if strings.Contains(delivered2, "## Official Project Context") {
		t.Fatalf("opted-out prompt must omit Official Project Context:\n%s", delivered2)
	}

	// 3. Cherry-pick via ctrl+o picker: drop one official item (Design Doc) and
	// add the scraped item for this single prompt.
	a.termOf(pane.ID).lines = nil
	d.Keys("c")
	cm = a.activeComposer()
	d.Keys("ctrl+o")
	if a.Modal() != composerContextPickerID || a.Mode() != ModePicker {
		t.Fatalf("ctrl+o did not open context picker: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	pickerFrame := d.Frame()
	for _, want := range []string{"[✓] ★ Design Doc (gdoc)", "[✓] ★ Metrics Sheet (sheet)", "[ ] Scraped Debug URL (url)"} {
		if !strings.Contains(pickerFrame, want) {
			t.Fatalf("picker frame missing %q:\n%s", want, pickerFrame)
		}
	}

	// Toggle off Design Doc (row 0) with enter.
	d.Keys("enter")
	if a.Modal() != composerContextPickerID {
		t.Fatalf("picker should stay open for multi-toggle, modal=%q", a.Modal())
	}
	if !strings.Contains(d.Frame(), "[ ] ★ Design Doc (gdoc)") {
		t.Fatalf("enter did not uncheck Design Doc:\n%s", d.Frame())
	}

	// Navigate down to Scraped Debug URL (row 2) and toggle on with space.
	d.Keys("j", "j", "space")
	if !strings.Contains(d.Frame(), "[✓] Scraped Debug URL (url)") {
		t.Fatalf("space did not check Scraped Debug URL:\n%s", d.Frame())
	}

	// Close picker and return to composer -> banner reflects customized selection.
	d.Keys("q")
	if a.Modal() != "composer" || a.Mode() != ModeComposer {
		t.Fatalf("closing picker did not return to composer: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	custBanner := cm.projCtx.bannerNode()
	if custBanner == nil || custBanner.Tone != "accent" {
		t.Fatalf("expected accent customized banner, got %+v", custBanner)
	}
	if txt, _ := custBanner.Props["text"].(string); !strings.Contains(txt, "★ Project Context (2 selected)") || !strings.Contains(txt, "Metrics Sheet") || !strings.Contains(txt, "Scraped Debug URL") || strings.Contains(txt, "Design Doc") {
		t.Fatalf("unexpected customized banner: %q", txt)
	}

	d.Type("Investigate trace")
	d.Keys("ctrl+s")
	delivered3 := strings.Join(a.termOf(pane.ID).lines, "\n")
	if !strings.Contains(delivered3, "## Official Project Context") || !strings.Contains(delivered3, "Metrics Sheet") || !strings.Contains(delivered3, "Scraped Debug URL") {
		t.Fatalf("cherry-picked prompt missing expected context:\n%s", delivered3)
	}
	if strings.Contains(delivered3, "Design Doc") {
		t.Fatalf("cherry-picked prompt must exclude dropped Design Doc:\n%s", delivered3)
	}

	// Reopening composer resets to default official items (single-prompt scope).
	d.Keys("c")
	cm = a.activeComposer()
	resetSel := cm.selectedProjectContext()
	if len(resetSel) != 2 || resetSel[0].ID != gdoc.ID || resetSel[1].ID != sheet.ID {
		t.Fatalf("reopened composer did not reset to official set: %v", resetSel)
	}
}

func TestComposerProjectContextAutoInjectSettingDisabled(t *testing.T) {
	store, pane, gdoc, sheet, scraped := seedComposerContextFixtures(t)
	cfgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte(`{"context.autoInjectProjectContext": false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{ConfigDir: cfgDir, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 160, 40)

	// Starts opted out by default when SetContextAutoInject is false.
	d.Keys("c")
	cm := a.activeComposer()
	if cm == nil || cm.projCtx == nil {
		t.Fatal("expected active composer with projCtx")
	}
	if cm.projCtx.enabled || len(cm.selectedProjectContext()) != 0 {
		t.Fatalf("expected opted out when autoInject=false, got enabled=%v sel=%d", cm.projCtx.enabled, len(cm.selectedProjectContext()))
	}
	d.Type("Plain prompt")
	d.Keys("ctrl+s")
	delivered := strings.Join(a.termOf(pane.ID).lines, "\n")
	if strings.Contains(delivered, "## Official Project Context") {
		t.Fatalf("autoInject=false should not inject context:\n%s", delivered)
	}

	// Reopen and explicitly cherry-pick a single scraped item via toggleItem.
	d.Keys("c")
	cm = a.activeComposer()
	if !cm.projCtx.toggleItem(scraped.ID) {
		t.Fatal("toggleItem(scraped) should enable scraped item")
	}
	got := cm.selectedProjectContext()
	if len(got) != 1 || got[0].ID != scraped.ID {
		t.Fatalf("selected after single toggleItem = %v, want [%s]", got, scraped.ID)
	}

	// Drop the single item -> enabled becomes false; ctrl+g restores official set.
	if cm.projCtx.toggleItem(scraped.ID) {
		t.Fatal("second toggleItem(scraped) should deselect it")
	}
	if cm.projCtx.enabled {
		t.Fatal("deselecting last item should disable project context")
	}
	d.Keys("ctrl+g")
	got = cm.selectedProjectContext()
	if !cm.projCtx.enabled || len(got) != 2 || got[0].ID != gdoc.ID || got[1].ID != sheet.ID {
		t.Fatalf("ctrl+g after empty selection should restore official set, got enabled=%v items=%v", cm.projCtx.enabled, got)
	}
}

func TestFormatPromptWithContext(t *testing.T) {
	rec := data.Record{
		ID:   "ctx-1",
		Kind: data.KindContext,
		Fields: map[string]any{
			"title":       "Design Doc",
			"value":       "https://docs.google.com/document/d/abc/edit",
			"contextKind": "gdoc",
		},
	}
	att := data.Attachment{Kind: "file", Path: "/tmp/spec.md", Label: "spec.md"}

	if got := formatPromptWithContext("hello", nil, nil); got != "hello" {
		t.Fatalf("plain = %q, want hello", got)
	}
	got := formatPromptWithContext("hello\n", []data.Attachment{att}, []data.Record{rec})
	wantSubstrings := []string{
		"hello\n\n## Official Project Context",
		"- [gdoc] **Design Doc**: https://docs.google.com/document/d/abc/edit",
		"\n\n[Attachments]\n- /tmp/spec.md",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Fatalf("formatPromptWithContext missing %q:\n%s", want, got)
		}
	}
}
