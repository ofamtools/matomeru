package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
)

// contextIDOf is the id of the stored item with that text.
func contextIDOf(t *testing.T, a *App, text string) string {
	t.Helper()
	items, err := a.contexts.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Text == text {
			return it.ID
		}
	}
	t.Fatalf("no item %q", text)
	return ""
}

// The context item's verbs: the standard ones its props and store give
// (no rename: the text is the item, written by create only), forget a
// lasting alias of delete, and the named ones beside them.
func TestContextItemVerbsAreStandard(t *testing.T) {
	t.Parallel()
	a, _ := contextApp(t)
	for verb, standard := range map[string]string{
		"get": "get", "list": "list", "create": "create", "set": "set", "delete": "delete", "watch": "watch",
		"move": "move", "duplicate": "duplicate", "star": "set", // star: the alias of set {prop: starred, toggle: true}
		"add": "", "copy": "", "open": "", "reveal": "", "associate": "", "choose-move": "",
		"extract": "", "turns": "", "follow": "", "kind": "", "search": "", "pick": "", "copy-prompt": "",
	} {
		op, ok := a.r.Operation(contextNoun + "." + verb)
		if !ok || op.Standard != standard {
			t.Errorf("%s.%s: ok %v standard %q, want %q", contextNoun, verb, ok, op.Standard, standard)
		}
	}
	if _, ok := a.r.Operation(contextNoun + ".rename"); ok {
		t.Fatal("the text is create-only, so no rename")
	}
	var forget *ochazuke.ActionAlias
	for _, alias := range a.r.AliasesOf(contextNoun + ".delete") {
		if alias.Alias == contextNoun+".forget" {
			forget = &alias
		}
	}
	if forget == nil || forget.Deprecated() {
		t.Fatalf("forget: %+v", forget)
	}
}

// Targeted keys on a context row: * stars through the standard set (and
// undoes), m moves through the chooser's standard move, and d d asks
// "Forget ...?" and deletes the record; undo puts the row back as it was,
// sightings and all.
func TestContextItemKeysStarMoveForget(t *testing.T) {
	t.Parallel()
	a, _ := contextApp(t)
	link := contextIDOf(t, a, "https://example.com/ochazuke")
	selectContext(t, a, "https://example.com/ochazuke")
	press(t, a, "*")
	if it, _ := a.contexts.Get(link); !it.Starred {
		t.Fatal("* did not star")
	}
	executeA(t, a, "history", "undo", "", nil)
	if it, _ := a.contexts.Get(link); it.Starred {
		t.Fatal("undo of * kept the star")
	}
	selectContext(t, a, "https://example.com/ochazuke")
	press(t, a, "m")
	if a.r.Chooser(moveChooser) == nil {
		t.Fatalf("m opened %q", a.r.TopModal())
	}
	typeText(t, a, "matom")
	press(t, a, "enter")
	if it, _ := a.contexts.Get(link); it.ProjectID != "matomeru" {
		t.Fatalf("m: %+v", it)
	}
	commit := contextIDOf(t, a, "1ba025f")
	before, _ := a.contexts.Get(commit)
	selectContext(t, a, "1ba025f")
	press(t, a, "d", "d")
	if fr := frame(a); a.r.TopModal() == "" || !strings.Contains(fr, "Forget 1ba025f?") {
		t.Fatalf("d d: modal %q\n%s", a.r.TopModal(), fr)
	}
	press(t, a, "y")
	if _, err := a.contexts.Get(commit); err == nil {
		t.Fatal("y kept the item")
	}
	executeA(t, a, "history", "undo", "", nil)
	if back, err := a.contexts.Get(commit); err != nil || !reflect.DeepEqual(back, before) {
		t.Fatalf("undo of the delete: %+v %v, want %+v", back, err, before)
	}
}

// The owner props: a task brings its project (stored task-only, as
// extraction stores it); a new project drops a task of another project; an
// unknown owner is saved unassociated with a notice, never refused.
func TestContextItemOwnerProps(t *testing.T) {
	t.Parallel()
	a, _ := contextApp(t)
	id := contextIDOf(t, a, "1ba025f")
	thing, _ := executeA(t, a, contextNoun, "set", "", ochazuke.Arguments{"id": id, "prop": "task", "value": "t1"}).(map[string]any)
	if thing["task"] != "t1" || thing["project"] != "matomeru" {
		t.Fatalf("set task: %v", thing)
	}
	if it, _ := a.contexts.Get(id); it.TaskID != "t1" || it.ProjectID != "" {
		t.Fatalf("stored: %+v", it)
	}
	thing, _ = executeA(t, a, contextNoun, "move", "", ochazuke.Arguments{"id": id, "to": "matomeru"}).(map[string]any)
	if thing["task"] != "t1" {
		t.Fatalf("a move within the task's project keeps the task: %v", thing)
	}
	thing, _ = executeA(t, a, contextNoun, "move", "", ochazuke.Arguments{"id": id, "to": "ochazuke"}).(map[string]any)
	if thing["task"] != "" || thing["project"] != "ochazuke" {
		t.Fatalf("move to another project: %v", thing)
	}
	thing, _ = executeA(t, a, contextNoun, "move", "", ochazuke.Arguments{"id": id, "to": "nowhere"}).(map[string]any)
	if thing["project"] != "" {
		t.Fatalf("an unknown project: %v", thing)
	}
	if !strings.Contains(a.m.Status+frame(a), "unknown project nowhere") {
		t.Fatalf("no notice for the unknown project:\n%s", frame(a))
	}
}

// From the CLI: list answers the items as things (--kind and --where
// type= narrow, starred first), create keeps a new item of its own (never
// merged into one already kept), star and forget work as before, and
// associate's old forms say what replaces them.
func TestContextItemStandardVerbsFromTheCLI(t *testing.T) {
	t.Parallel()
	a, _ := cliApp(t)
	if _, _, err := a.contexts.Merge([]contextitems.Item{{Kind: "branch", Text: "lane/context"}, {Kind: "file", Text: "/tmp/x.go"}}, contextitems.Owner{}, 1); err != nil {
		t.Fatal(err)
	}
	branch := contextIDOf(t, a, "lane/context")
	mustCLI(t, a, contextNoun, "star", branch)
	var rows []map[string]any
	for _, args := range [][]string{{"list", "--kind", "branch"}, {"list", "--where", "type=branch"}} {
		if err := json.Unmarshal([]byte(mustCLI(t, a, append([]string{contextNoun}, args...)...)), &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0]["id"] != branch || rows[0]["label"] != "lane/context" || rows[0]["starred"] != true {
			t.Fatalf("%v: %v", args, rows)
		}
	}
	// value sets instead of toggling (the toggle preset yields); twice is
	// still false.
	for range 2 {
		executeA(t, a, contextNoun, "star", "", ochazuke.Arguments{"id": branch, "value": false})
		if it, _ := a.contexts.Get(branch); it.Starred {
			t.Fatal("star value=false kept the star")
		}
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: contextNoun, Verb: "star", Args: ochazuke.Arguments{"id": branch, "value": true, "toggle": true}}); err == nil {
		t.Fatal("star with value and toggle was not refused")
	}
	var made map[string]any
	cliJSON(t, a, &made, contextNoun, "create", "--type", "branch", "--text", "lane/context", "--project", "matomeru")
	if made["id"] == branch || made["project"] != "matomeru" || made["label"] != nil && made["label"] != "lane/context" {
		t.Fatalf("create: %v", made)
	}
	if it, err := a.contexts.Get(made["id"].(string)); err != nil || it.Kind != "branch" || len(it.Sources) != 1 || it.Sources[0].Kind != contextCreateSource {
		t.Fatalf("created row: %+v %v", it, err)
	}
	if _, _, err := cli(a, "", contextNoun, "create", "--text", "no kind"); err == nil {
		t.Fatal("create without a type succeeded")
	}
	_, errOut, err := cli(a, "", contextNoun, "associate", branch, "--project", "ochazuke")
	if err != nil || !strings.Contains(errOut, "old form of context-item.move") {
		t.Fatalf("associate --project: %v %q", err, errOut)
	}
	if it, _ := a.contexts.Get(branch); it.ProjectID != "ochazuke" {
		t.Fatalf("associate --project: %+v", it)
	}
	mustCLI(t, a, contextNoun, "forget", branch, "--yes")
	if _, err := a.contexts.Get(branch); err == nil {
		t.Fatal("forget ID removes the record")
	}
}

// Known gap (ochazuke v0.55.0): the CLI's request parser applies an
// alias's presets before it reads the flags (cli.Shell.request calls
// CanonicalRequest with no arguments), so the toggle preset does not
// yield to --value: "set takes a value or toggle, not both". The runtime
// and the palette merge after the arguments (WithPresets) and yield.
func TestContextItemStarValueFromTheCLI(t *testing.T) {
	t.Parallel()
	t.Skip("ochazuke v0.55.0: the CLI applies alias presets before the flags; star --value is refused")
	a, _ := cliApp(t)
	if _, _, err := a.contexts.Merge([]contextitems.Item{{Kind: "branch", Text: "lane/x"}}, contextitems.Owner{}, 1); err != nil {
		t.Fatal(err)
	}
	id := contextIDOf(t, a, "lane/x")
	mustCLI(t, a, contextNoun, "star", id, "--value=true")
	if it, _ := a.contexts.Get(id); !it.Starred {
		t.Fatal("star --value=true left it unstarred")
	}
}
