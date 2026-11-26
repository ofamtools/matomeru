package app

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/ochazuke/ui"
)

func seedResponseContext(a *App, pane data.Record, text string) {
	a.indexContextItems(&pane, "", "", contextitems.Extract(text, a.contextExtractOptions(contextitems.SourceResponse, 0)))
}

func TestContextAgentTerminalTextIsNotAutoIndexed(t *testing.T) {
	a, _ := demo(t)
	pane := titled(t, a, data.KindPane, "claude: wave 5B")
	a.harvestPrompt(pane, "Check https://prompt.example.com")
	a.harvestScreenLines(pane, []string{"Working on /tmp/intermediate.go"}, 80, 1)
	for _, doc := range []struct {
		path   string
		source string
	}{
		{path: "/tmp/tool-created.md", source: data.DocumentSourceTool},
		{path: "/tmp/curated-document.md"},
	} {
		mkRec(t, a, data.KindDocument, map[string]any{"title": doc.path, "filePath": doc.path, "source": doc.source})
	}
	items := a.allContextItems()
	hasValue := func(value string) bool {
		return slices.ContainsFunc(items, func(item CollectedContextItem) bool { return item.Value == value })
	}
	for _, value := range []string{"https://prompt.example.com", "/tmp/intermediate.go", "/tmp/tool-created.md"} {
		if hasValue(value) {
			t.Errorf("auto-indexed %s without a final answer", value)
		}
	}
	if !hasValue("/tmp/curated-document.md") {
		t.Error("independently registered document was omitted")
	}
}

func TestContextCodeBlocksSettingOnAndOff(t *testing.T) {
	input := "Check https://example.com/code and run `go test ./...`\n```go\npackage demo\nconst tok = \"ghp_abcdefghijklmnopqrstuvwxyz1234567890\"\n```"

	// Default ON: extracts both fenced and inline code, persists language & hash canonKey, masks secrets.
	aOn, _ := demo(t)
	if !aOn.extractCodeBlocksEnabled() {
		t.Fatal("expected context.extractCodeBlocks to default to true")
	}
	paneOn := mkRec(t, aOn, data.KindPane, map[string]any{"title": "shell-on", "harness": ""})
	aOn.harvestPrompt(paneOn, input)

	itemsOn := aOn.paneContextItems(paneOn.ID)
	var codeItems []CollectedContextItem
	for _, it := range itemsOn {
		if it.Kind == contextitems.KindCode {
			codeItems = append(codeItems, it)
		}
	}
	if len(codeItems) != 2 {
		t.Fatalf("ON: got %d code items, want 2: %+v", len(codeItems), itemsOn)
	}
	foundGo := false
	for _, ci := range codeItems {
		if !strings.HasPrefix(ci.CanonKey, "code:") || len(ci.CanonKey) != len("code:")+16 {
			t.Errorf("code item canonKey = %q, want hash key", ci.CanonKey)
		}
		if strings.Contains(ci.Value, "ghp_abcdefghijklmnopqrstuvwxyz1234567890") {
			t.Errorf("secret was not masked in app code context: %q", ci.Value)
		}
		if ci.Language == "go" {
			foundGo = true
		}
	}
	if !foundGo {
		t.Errorf("expected fenced go code item with Language=go, got %+v", codeItems)
	}

	// Opt-out OFF: skips code items while keeping URL extraction.
	aOff, _ := demo(t)
	aOff.cfg.userSettings[SetContextExtractCodeBlocks] = false
	aOff.reloadSettings()
	if aOff.extractCodeBlocksEnabled() {
		t.Fatal("expected context.extractCodeBlocks=false after setting update")
	}
	paneOff := mkRec(t, aOff, data.KindPane, map[string]any{"title": "shell-off", "harness": ""})
	aOff.harvestPrompt(paneOff, input)

	itemsOff := aOff.paneContextItems(paneOff.ID)
	for _, it := range itemsOff {
		if it.Kind == contextitems.KindCode {
			t.Fatalf("OFF: code item should not have been extracted: %+v", it)
		}
	}
	if !slices.ContainsFunc(itemsOff, func(it CollectedContextItem) bool { return it.Value == "https://example.com/code" }) {
		t.Fatalf("OFF: expected URL still extracted, got %+v", itemsOff)
	}
}

func TestCodeContextSafeOpenChipsAndPreview(t *testing.T) {
	a, _ := demo(t)
	code := "func greet() {\n\tprintln(\"```triple```\")\n}"
	rec := mkRec(t, a, data.KindContext, map[string]any{
		"title":       "go: func greet() {",
		"value":       code,
		"contextKind": "code",
		"language":    "go",
		"source":      "response",
		"canonKey":    data.CodeContextCanonKey(code),
	})

	// 1. targetOf routes code context exclusively to inline MarkdownReader (empty path, never SystemOpen).
	target, ok := a.targetOf(rec)
	if !ok {
		t.Fatal("targetOf should accept code context record")
	}
	if target.path != "" {
		t.Fatalf("code target.path = %q, want empty (inline only)", target.path)
	}
	if !strings.HasPrefix(target.body, "````go\n") || !strings.HasSuffix(target.body, "\n````") {
		t.Fatalf("code target.body = %q, want 4-backtick fenced markdown", target.body)
	}
	if len(target.rules) != 1 || target.rules[0].Name != handlers.MarkdownReader {
		t.Fatalf("code target.rules = %+v, want single MarkdownReader rule", target.rules)
	}

	// 2. contextTargetPath never returns a path for code; openContextValue and revealContextValue open MarkdownReader modal (no shell command).
	if p := contextTargetPath(contextitems.KindCode, code, nil); p != "" {
		t.Fatalf("contextTargetPath(code) = %q, want empty", p)
	}
	beforeRuns := len(latestRunCommands(t, a.Store))
	a.Shell.State = a.openContextValue(a.Shell.State, contextitems.KindCode, code, nil).State
	if a.modals.Top() == nil || a.modals.Top().ID() != "reader" {
		t.Fatalf("openContextValue(code) top modal = %v, want reader", a.modals.Top())
	}
	if after := latestRunCommands(t, a.Store); len(after) != beforeRuns {
		t.Fatalf("openContextValue(code) invoked shell commands %v, want none", after[beforeRuns:])
	}

	a.Shell.State = a.dropModals(a.Shell.State)
	a.Shell.State = a.revealContextValue(a.Shell.State, contextitems.KindCode, code, nil).State
	if a.modals.Top() == nil || a.modals.Top().ID() != "reader" {
		t.Fatalf("revealContextValue(code) top modal = %v, want reader", a.modals.Top())
	}
	if after := latestRunCommands(t, a.Store); len(after) != beforeRuns {
		t.Fatalf("revealContextValue(code) invoked shell commands %v, want none", after[beforeRuns:])
	}

	// 3. contextPointerChips must NOT emit pointer chips for code items.
	item := CollectedContextItem{
		RecordID: rec.ID,
		Kind:     contextitems.KindCode,
		Value:    code,
		Label:    rec.Text("title"),
		Language: "go",
		Source:   "response",
	}
	if chips := a.contextPointerChips(item, rec.Ref()); len(chips) != 0 {
		t.Fatalf("contextPointerChips(code) = %+v, want none", chips)
	}

	// 4. Detail preview card renders code as ui.KindMarkdown.
	card := a.buildProjectContextDetailCard(item, "context", true)
	mdNode := card.Find("project.ctx.detail.code")
	if mdNode == nil || mdNode.Kind != ui.KindMarkdown {
		t.Fatalf("detail card missing KindMarkdown code node: %+v", card)
	}
}
