package app

import (
	"slices"
	"testing"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
)

func seedResponseContext(a *App, pane data.Record, text string) {
	a.indexContextItems(&pane, "", "", contextitems.Extract(text, contextitems.Options{Source: contextitems.SourceResponse}))
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
