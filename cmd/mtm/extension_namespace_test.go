package main

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// A catalog's declared namespace names its nouns in mtm, not the name it
// was registered under (cli.CatalogNamespace): the MCP gateway, the palette
// and the CLI say widget.item, and mtm widget reaches the tool.
func TestExtensionMountsUnderDeclaredNamespace(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	catalog := strings.Replace(strings.Replace(fakeCatalog, "VERSION", "1.0", 1), `"name":"o-fake",`, `"name":"o-fake","namespace":"widget",`, 1)
	catalog = strings.Replace(catalog, `{"name":"item",`, `{"name":"item","main":true,"defaultAction":"list",`, 1)
	bin, _ := fakeExtension(t, catalog, "")
	a := registered(t, path, "fake", bin)
	has := func(key string) bool {
		_, ok := a.r.Operations.Find(ochazuke.Key(a.r.CanonicalKey(key)))
		return ok
	}
	if !has("widget.item.done") {
		t.Fatal("widget.item.done is not mounted")
	}
	if has("fake.item.done") {
		t.Fatal("the registered name still names the nouns")
	}
	if n := a.r.Noun("widget.item"); n == nil || !n.Main || n.DefaultAction != "list" {
		t.Fatalf("main and defaultAction were not mounted: %+v", n)
	}
	if got := extensionNamespace(a.extension("fake")); got != "widget" {
		t.Fatalf("peer namespace %q", got)
	}
	out := mustCLI(t, a, "widget", "item", "list")
	if !strings.Contains(out, "one") {
		t.Fatalf("mtm widget item list:\n%s", out)
	}
	out = mustCLI(t, a, "extension", "peers")
	if !strings.Contains(out, `"namespace":"widget"`) || !strings.Contains(out, "widget.item") {
		t.Fatalf("peers:\n%s", out)
	}
}
