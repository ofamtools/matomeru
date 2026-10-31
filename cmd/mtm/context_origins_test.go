package main

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/ochazuke"
)

func contextOriginsSetting(t *testing.T, a *App, origins ...string) {
	t.Helper()
	if _, err := a.r.Settings().SetSession(settingContextExtractFrom, origins); err != nil {
		t.Fatal(err)
	}
}

func TestContextExtractionDefaultAndSourceOptIn(t *testing.T) {
	t.Parallel()
	a, _ := cliApp(t)
	f := withContextPanes(a)
	f.print("w1", "https://example.com/tool-output")
	f.commands["w1"] = []daemon.CommandEntry{{Cmdline: "go test ./..."}}
	if !slices.Equal(a.contextExtraction(), []string{"prompt", "response"}) {
		t.Fatal(a.contextExtraction())
	}
	mustCLI(t, a, contextNoun, "extract", "--pane", "w1")
	a.cx.follow.mark("w1")
	_, err := a.r.Execute(ochazuke.Request{Noun: contextNoun, Verb: "follow", Target: contextListID})
	if err != nil {
		t.Fatal(err)
	}
	if f.reads != 0 || len(itemsByText(t, a)) != 0 {
		t.Fatal("default read screen sources")
	}
	contextOriginsSetting(t, a, "command")
	mustCLI(t, a, contextNoun, "extract", "--pane", "w1")
	if f.reads != 0 || itemsByText(t, a)["go test ./..."].Origin() != "command" {
		t.Fatal("command opt-in", itemsByText(t, a))
	}
	contextOriginsSetting(t, a, "scrollback")
	mustCLI(t, a, contextNoun, "extract", "--pane", "w1")
	if f.reads != 1 || itemsByText(t, a)["https://example.com/tool-output"].Origin() != "scrollback" {
		t.Fatal("scrollback opt-in")
	}
	if _, err := a.r.Settings().SetSession(settingContextExtractFrom, []string{"typo"}); err == nil {
		t.Fatal("invalid origin accepted")
	}
}

func TestContextExtractionSettingGatesJournalAndCaptures(t *testing.T) {
	t.Parallel()
	a, _ := cliApp(t)
	contextOriginsSetting(t, a, "response")
	a.journal(activity(1, 1790000000000, "prompt", "https://example.com/excluded-prompt"), activity(2, 1790000001000, "turn_end", "https://example.com/answer"))
	if got := itemsByText(t, a); len(got) != 1 || got["https://example.com/answer"].Origin() != "response" {
		t.Fatalf("%v", got)
	}
	contextOriginsSetting(t, a, "prompt")
	mustCLI(t, a, contextNoun, "turns")
	if got := itemsByText(t, a); len(got) != 2 || got["https://example.com/excluded-prompt"].Origin() != "prompt" {
		t.Fatal(got)
	}
	contextOriginsSetting(t, a, []string{}...)
	mustCLI(t, a, "history", "capture-turn", "--json", `{"id":"disabled","session":"matomeru","window":"w1","harness":"omp","conversation_id":"c1","prompt":"https://example.com/disabled","started_at":1790000100000}`)
	if len(itemsByText(t, a)) != 2 {
		t.Fatal("disabled capture extracted")
	}
	mustCLI(t, a, contextNoun, "add", "--text", "https://example.com/explicit")
	mustCLI(t, a, contextNoun, "create", "--type", "link", "--text", "https://example.com/manual")
	got := itemsByText(t, a)
	if got["https://example.com/explicit"].Origin() != "mcp" || got["https://example.com/manual"].Origin() != "manual" {
		t.Fatal(got)
	}
}

func TestContextTranscriptSeparatesProseFromTools(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	p := a.panes[0]
	transcript := paneTranscript{Entries: []daemon.TranscriptEntry{
		{Role: "user", Kind: "text", Text: "https://example.com/prompt"},
		{Role: "assistant", Kind: "text", Text: "https://example.com/response"},
		{Role: "assistant", Kind: "tool_call", Tool: "Read", Text: "https://example.com/call"},
		{Role: "tool", Kind: "tool_result", Text: "https://example.com/result"},
		{Role: "assistant", Kind: "thinking", Text: "https://example.com/thought"},
		{Role: "assistant", Kind: "text", ToolID: "call", Text: "https://example.com/tagged-tool"},
		{Role: "system", Kind: "note", Text: "https://example.com/system-note"},
	}}
	a.transcriptContext(nil)(p, transcript)
	got := itemsByText(t, a)
	if len(got) != 2 || got["https://example.com/prompt"].Origin() != "prompt" || got["https://example.com/response"].Origin() != "response" {
		t.Fatal(got)
	}
	if got["https://example.com/response"].Sources[0].Kind != "transcript" {
		t.Fatal("lost transcript provenance")
	}
	contextOriginsSetting(t, a, "thinking")
	a.transcriptContext(nil)(p, transcript)
	got = itemsByText(t, a)
	if len(got) != 3 || got["https://example.com/thought"].Origin() != "thinking" {
		t.Fatal(got)
	}
	contextOriginsSetting(t, a, "tool")
	a.transcriptContext(nil)(p, transcript)
	got = itemsByText(t, a)
	if len(got) != 6 || got["https://example.com/call"].Origin() != "tool" || got["https://example.com/result"].Origin() != "tool" || got["https://example.com/tagged-tool"].Origin() != "tool" {
		t.Fatal(got)
	}
	contextOriginsSetting(t, a, "transcript")
	a.transcriptContext(nil)(p, transcript)
	got = itemsByText(t, a)
	if len(got) != 7 || got["https://example.com/system-note"].Origin() != "transcript" {
		t.Fatal(got)
	}
	// Unstructured text is not inferred as conversation, thinking or tool from its headings.
	contextOriginsSetting(t, a, "response", "thinking", "tool")
	a.transcriptContext(nil)(p, paneTranscript{Text: "assistant\nhttps://example.com/ambiguous"})
	if len(itemsByText(t, a)) != 7 {
		t.Fatal("unstructured transcript treated as structured")
	}
}

func TestContextIndependentToolAndThinkingControlsPreserveExistingItems(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	p := a.panes[0]
	if _, err := a.r.Settings().SetSession(settingTurnsThinking, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Settings().SetSession(settingTurnsTools, true); err != nil {
		t.Fatal(err)
	}
	// Default ["prompt","response"] excludes thinking and tool entries even when
	// Turns rendering settings show both.
	a.transcriptContext(nil)(p, paneTranscript{Entries: []daemon.TranscriptEntry{
		{Role: "user", Kind: "text", Text: "https://example.com/p1"},
		{Role: "assistant", Kind: "text", Text: "https://example.com/r1"},
		{Role: "assistant", Kind: "thinking", Text: "https://example.com/th1"},
		{Role: "tool", Kind: "tool_result", Text: "https://example.com/tl1"},
	}})
	got := itemsByText(t, a)
	if len(got) != 2 || got["https://example.com/p1"].Origin() != "prompt" || got["https://example.com/r1"].Origin() != "response" {
		t.Fatalf("default extracted thinking/tool: %v", got)
	}

	// Enable thinking only (tool remains excluded).
	contextOriginsSetting(t, a, "prompt", "response", "thinking")
	a.transcriptContext(nil)(p, paneTranscript{Entries: []daemon.TranscriptEntry{
		{Role: "assistant", Kind: "thinking", Text: "https://example.com/th1"},
		{Role: "tool", Kind: "tool_result", Text: "https://example.com/tl1"},
	}})
	got = itemsByText(t, a)
	if len(got) != 3 || got["https://example.com/th1"].Origin() != "thinking" {
		t.Fatalf("thinking opt-in: %v", got)
	}
	if _, hasTool := got["https://example.com/tl1"]; hasTool {
		t.Fatalf("tool extracted when only thinking enabled: %v", got)
	}

	// Enable tool only: extracts tool, excludes new thinking, retains earlier items.
	contextOriginsSetting(t, a, "tool")
	a.transcriptContext(nil)(p, paneTranscript{Entries: []daemon.TranscriptEntry{
		{Role: "assistant", Kind: "thinking", Text: "https://example.com/th2"},
		{Role: "tool", Kind: "tool_result", Text: "https://example.com/tl1"},
	}})
	got = itemsByText(t, a)
	if _, hasThinking2 := got["https://example.com/th2"]; hasThinking2 {
		t.Fatalf("thinking extracted when only tool enabled: %v", got)
	}
	if len(got) != 4 || got["https://example.com/tl1"].Origin() != "tool" || got["https://example.com/th1"].Origin() != "thinking" {
		t.Fatalf("tool opt-in or old item lost: %v", got)
	}
}

func TestContextPickerOriginFiltersAndGenericProperty(t *testing.T) {
	t.Parallel()
	a, _, live, _ := liveContextApp(t)
	for _, src := range []contextitems.Source{{Kind: "turn", EntryKind: "response-completed"}, {Kind: "scrollback"}} {
		if _, _, err := a.contexts.Merge([]contextitems.Item{{Kind: "link", Text: "https://example.com/both", Window: live.ID, Sources: []contextitems.Source{src}}}, contextitems.Owner{}, 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := a.contexts.Merge([]contextitems.Item{{Kind: "link", Text: "https://example.com/tool-only", Window: live.ID, Sources: []contextitems.Source{{Kind: "scrollback"}}}}, contextitems.Owner{}, 2); err != nil {
		t.Fatal(err)
	}
	promptTurns(t, a, live, []string{"my prompt"}, []string{"answer"})
	press(t, a, "ctrl+a", "c")
	c := a.r.Chooser(contextPicker)
	for _, tc := range []struct {
		name  string
		count int
	}{{"conversation", 2}, {"tools", 1}, {"all", 7}} {
		if err := c.ApplyFilter(tc.name); err != nil {
			t.Fatal(err)
		}
		if len(c.Matches()) != tc.count {
			t.Fatalf("%s: %v", tc.name, pickerTexts(t, a))
		}
	}
	a.Update(keyMsg("alt+backspace"))
	// ctrl+f opens the prop filter form (list.filter-form, default on);
	// origin's chips start prompt, response.
	press(t, a, "ctrl+f")
	// j walks the form in drawing order (a field's chips may wrap onto
	// several lines): category created exists id origin.
	for i := 0; i < 20 && !strings.HasSuffix(a.r.Focus, "/origin"); i++ {
		press(t, a, "j")
	}
	if !strings.HasSuffix(a.r.Focus, "/origin") {
		t.Fatalf("filter form focus %s:\n%s", a.r.Focus, frame(a))
	}
	press(t, a, "l", "space", "esc")
	if got := pickerTexts(t, a); len(got) != 1 || !strings.Contains(got[0], "/both") {
		t.Fatal(got)
	}
	if _, err := a.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}

// A source update must refresh an open picker even if it adds no item.
func TestContextPickerUpdatesWhenExistingItemBecomesConversational(t *testing.T) {
	t.Parallel()
	a, _, live, _ := liveContextApp(t)
	text := "https://example.com/reclassified"
	if _, _, err := a.contexts.Merge([]contextitems.Item{{Kind: "link", Text: text, Window: live.ID, Sources: []contextitems.Source{{Kind: "scrollback"}}}}, contextitems.Owner{}, 1); err != nil {
		t.Fatal(err)
	}
	press(t, a, "ctrl+a", "c")
	c := a.r.Chooser(contextPicker)
	if err := c.ApplyFilter("conversation"); err != nil {
		t.Fatal(err)
	}
	if len(c.Matches()) != 0 {
		t.Fatal("tool item in conversation")
	}
	a.transcriptContext(nil)(live, paneTranscript{Entries: []daemon.TranscriptEntry{{Role: "assistant", Kind: "text", Text: text}}})
	a.historyChanged()
	if got := pickerTexts(t, a); len(got) != 1 || got[0] != text {
		t.Fatal(got)
	}
}

func TestContextExtractionSettingChangesWhileJournalReads(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 20 {
			a.journal(activity(uint64(i*2+1), 1790000000000+int64(i*10000), "prompt", "https://example.com/prompt"), activity(uint64(i*2+2), 1790000000001+int64(i*10000), "turn_end", "https://example.com/response"))
		}
	}()
	for range 20 {
		contextOriginsSetting(t, a, "prompt")
		contextOriginsSetting(t, a, "response")
	}
	<-done
}

func TestContextExtractionSettingPersistsAndReloads(t *testing.T) {
	t.Parallel()
	a, _ := cliApp(t)
	mustCLI(t, a, "settings", "set", settingContextExtractFrom, `["scrollback","command"]`, "-y")
	b := openWith(t, Options{Data: a.dataPath})
	if !slices.Equal(b.contextExtraction(), []string{"scrollback", "command"}) {
		t.Fatal(b.contextExtraction())
	}
	mustCLI(t, a, "settings", "set", settingContextExtractFrom, `[]`, "-y")
	if problems := b.r.Settings().Reload(); len(problems) > 0 {
		t.Fatal(problems)
	}
	if len(b.contextExtraction()) != 0 {
		t.Fatal("reload did not update extraction policy", b.contextExtraction())
	}
}
