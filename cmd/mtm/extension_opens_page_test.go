package main

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// A catalog verb that opens a page of its tool (opensPage) opens that page
// in mtm when the page is mounted, and never runs the tool: the tool would
// start its own terminal program as a job with no terminal. A page whose
// title another page has is refused, so the tool names its page apart.
func TestExtensionVerbOpensItsMountedPage(t *testing.T) {
	t.Parallel()
	pages := `"pages":[` +
		`{"id":"items","title":"Items","lists":[{"id":"items/list","noun":"item","source":"item.list"}]},` +
		`{"id":"board","title":"Decisions","lists":[{"id":"board/list","noun":"item","source":"item.list"}]},` +
		`{"id":"dup","title":"Review","lists":[{"id":"dup/list","noun":"item","source":"item.list"}]}]`
	cat := strings.Replace(toolCatalog("fake", "1.0"), `"pages":[]`, pages, 1)
	cat = strings.Replace(cat, `{"name":"list","kind":"application","description":"List the items"},`, `{"name":"list","kind":"application","description":"List the items"},{"name":"board","kind":"application","description":"Open the board","opensPage":"board"},`, 1)
	bin, log := fakeExtension(t, cat, "")
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "fake", "--bin", bin)
	a := openAt(t, path)
	if a.r.PageOf("ext/fake/board") == nil {
		t.Fatalf("the Decisions page is not mounted: %v; %v", pageIDs(a), a.m.Notices)
	}
	if a.r.PageOf("ext/fake/dup") != nil || !strings.Contains(strings.Join(a.m.Notices, " "), `title "Review" is already registered`) {
		t.Fatalf("a page with mtm's own title mounted: %v; %v", pageIDs(a), a.m.Notices)
	}
	before := len(calls(t, log))
	if _, err := a.r.Execute(ochazuke.Request{Noun: "fake.item", Verb: "board"}); err != nil {
		t.Fatal(err)
	}
	if a.r.Page != "ext/fake/board" {
		t.Fatalf("page = %q, want ext/fake/board", a.r.Page)
	}
	for _, c := range calls(t, log)[before:] {
		if strings.Contains(c, "item board") {
			t.Fatalf("the tool ran the verb: %q", c)
		}
	}
}
