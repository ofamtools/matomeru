package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	osetup "ofam.dev/ochazuke/setup"
)

// setupCatalog is fakeCatalog with ochazuke's shared setup noun, its verbs
// marked builtin as a real tool's catalog marks them.
func setupCatalog(name string) string {
	setupNoun := `{"name":"ochazuke.setup","verbs":[
  {"name":"status","kind":"application","builtin":true},
  {"name":"plan","kind":"application","builtin":true},
  {"name":"install","kind":"application","builtin":true,"confirm":"Apply setup install?","agent":"confirm"},
  {"name":"uninstall","kind":"application","builtin":true,"confirm":"Apply setup uninstall?","agent":"confirm"}]},
 {"name":"app"`
	return strings.Replace(toolCatalog(name, "1.0"), `{"name":"app"`, setupNoun, 1)
}

// setupTool writes an executable o-NAME into dir whose catalog is
// setupCatalog when withSetup, else the plain one. Its setup plan reads
// dir/NAME.setup: "missing" (one of two items missing, one installed),
// "installed" (both installed) or "error" (the plan fails). install
// writes "installed" there, uninstall "missing". Every call is logged to
// dir/NAME.log.
func setupTool(t *testing.T, dir, name, state string, withSetup bool) string {
	t.Helper()
	catalog := toolCatalog(name, "1.0")
	if withSetup {
		catalog = setupCatalog(name)
	}
	bin := fakeTool(t, dir, name, catalog)
	stateFile, log := filepath.Join(dir, name+".setup"), filepath.Join(dir, name+".log")
	if err := os.WriteFile(stateFile, []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(dir, "home", ".harness", name+".json")
	plan := func(first string) string {
		return `{"operation":"install","items":[` +
			`{"id":"hook","name":"Harness hook","description":"Adds the ` + name + ` hook to the harness","kind":"json","path":"` + hook + `","group":"harness","state":"` + first + `","next":"o-` + name + ` setup install hook"},` +
			`{"id":"shell","name":"Shell block","kind":"block","path":"` + filepath.Join(dir, "home", ".zshrc") + `","state":"installed"},` +
			`{"id":"other","name":"Other harness","kind":"check","state":"n/a","reason":"requires dir:~/.other"}],` +
			`"writes":[{"path":"` + hook + `","content":"{}"}],"commands":[]}`
	}
	script := `#!/bin/sh
echo "$*" >> '` + log + `'
case "$1 $2" in
"catalog ") cat '` + filepath.Join(dir, name+".catalog") + `' ;;
"item "*) echo '[{"id":"i1","label":"one","cells":["one","open"]}]' ;;
"ochazuke.setup plan")
  case "$(cat '` + stateFile + `')" in
  missing) echo '` + plan("missing") + `' ;;
  installed) echo '` + plan("installed") + `' ;;
  *) echo "setup.json: broken" >&2; exit 1 ;;
  esac ;;
"ochazuke.setup install") echo installed > '` + stateFile + `'; echo '[{"id":"hook","name":"Harness hook","kind":"json","state":"installed"}]' ;;
"ochazuke.setup uninstall") echo missing > '` + stateFile + `'; echo '[{"id":"hook","name":"Harness hook","kind":"json","state":"missing"}]' ;;
*) echo "unknown $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// setupApp registers the given tools (all in dir) and returns a fresh mtm
// on their data, PATH holding only dir.
func setupApp(t *testing.T, dir string, names ...string) *App {
	t.Helper()
	path := seeded(t)
	cliApp := openAt(t, path)
	for _, name := range names {
		mustCLI(t, cliApp, "extension", "register", name, "--bin", filepath.Join(dir, "o-"+name))
	}
	a := openAt(t, path)
	a.getenv = func(k string) string {
		if k == "PATH" {
			return dir
		}
		return os.Getenv(k)
	}
	return a
}

// The four setup states, from one plan each: complete, needs setup (N of
// the M items that apply; n/a items do not count), no setup (the catalog
// has no setup noun, and the tool is never asked) and unknown (the plan
// failed, its error kept).
func TestExtensionSetupStatusStates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	setupTool(t, dir, "done", "installed", true)
	setupTool(t, dir, "todo", "missing", true)
	setupTool(t, dir, "plain", "missing", false)
	setupTool(t, dir, "broken", "error", true)
	a := setupApp(t, dir, "done", "todo", "plain", "broken")
	var infos []extSetupInfo
	if err := json.Unmarshal([]byte(mustCLI(t, a, "extension", "setup-status")), &infos); err != nil {
		t.Fatal(err)
	}
	got := map[string]extSetupInfo{}
	for _, info := range infos {
		got[info.Name] = info
	}
	if s := got["done"]; s.State != setupComplete || s.Missing != 0 || s.Total != 2 {
		t.Fatalf("done: %+v", s)
	}
	if s := got["todo"]; s.State != setupNeeded || s.Missing != 1 || s.Total != 2 || len(s.Items) != 3 {
		t.Fatalf("todo: %+v", s)
	}
	if s := got["plain"]; s.State != setupNone {
		t.Fatalf("plain: %+v", s)
	}
	if s := got["broken"]; s.State != setupUnknown || !strings.Contains(s.Error, "setup.json: broken") {
		t.Fatalf("broken: %+v", s)
	}
	for _, call := range toolCalls(t, dir, "plain") {
		if strings.HasPrefix(call, "ochazuke.setup") {
			t.Fatalf("a tool without setup was asked %q", call)
		}
	}
	// One extension by name.
	if err := json.Unmarshal([]byte(mustCLI(t, a, "extension", "setup-status", "todo")), &infos); err != nil || len(infos) != 1 || infos[0].Name != "todo" {
		t.Fatalf("setup-status todo: %+v %v", infos, err)
	}
}

func TestSummarizeSetupCounts(t *testing.T) {
	t.Parallel()
	plan := osetup.Plan{Items: []osetup.Status{
		{Item: osetup.Item{ID: "a"}, State: osetup.Missing}, {Item: osetup.Item{ID: "b"}, State: osetup.Outdated},
		{Item: osetup.Item{ID: "c"}, State: osetup.Installed}, {Item: osetup.Item{ID: "d"}, State: osetup.Skipped},
		{Item: osetup.Item{ID: "e"}, State: osetup.Manual},
	}}
	info := summarizeSetup("x", plan)
	if info.State != setupNeeded || info.Missing != 2 || info.Total != 4 {
		t.Fatalf("%+v", info)
	}
	if got := setupCell(info, true); got != "needs 2 of 4" {
		t.Fatalf("cell %q", got)
	}
	if got := summarizeSetup("x", osetup.Plan{}).State; got != setupComplete {
		t.Fatalf("no items: %s", got)
	}
}

// Agents never run setup or its undo (the verbs are declared never: the
// owner confirms installs); the plan and status stay theirs to read.
func TestExtensionSetupRefusedForAgents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	setupTool(t, dir, "todo", "missing", true)
	a := setupApp(t, dir, "todo")
	a.getenv = envOf(map[string]string{actorEnv: `{"kind":"agent","harness":"claude","pane":"w1"}`})
	for _, verb := range []string{"setup", "setup-undo"} {
		_, errOut, err := cli(a, "", "extension", verb, "todo", "--yes")
		if err == nil || !strings.Contains(errOut+err.Error(), "agents may never run") {
			t.Fatalf("%s as an agent: %v %q", verb, err, errOut)
		}
	}
	for _, call := range toolCalls(t, dir, "todo") {
		if strings.Contains(call, "install") {
			t.Fatalf("the tool was asked %q", call)
		}
	}
	if out, errOut, err := cli(a, "", "extension", "setup-plan", "todo"); err != nil || !strings.Contains(out, "Harness hook") {
		t.Fatalf("setup-plan as an agent: %v %q %q", err, out, errOut)
	}
}

// The owner's CLI: setup installs with --yes (the tool's own install,
// answered), the next status is complete; setup-undo removes it again.
func TestExtensionSetupCLI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	setupTool(t, dir, "todo", "missing", true)
	a := setupApp(t, dir, "todo")
	mustCLI(t, a, "extension", "setup", "todo", "--group", "harness", "--yes")
	calls := toolCalls(t, dir, "todo")
	if last := calls[len(calls)-1]; last != `ochazuke.setup install --json {"group":"harness"} --yes` {
		t.Fatalf("install call %q", last)
	}
	if !strings.Contains(mustCLI(t, a, "extension", "setup-status", "todo"), `"state": "complete"`) {
		t.Fatal("not complete after setup")
	}
	mustCLI(t, a, "extension", "setup-undo", "todo", "--yes")
	if !strings.Contains(mustCLI(t, a, "extension", "setup-status", "todo"), `"state": "needs setup"`) {
		t.Fatal("still complete after setup-undo")
	}
	if _, errOut, err := cli(a, "", "extension", "setup", "nope", "--yes"); err == nil {
		t.Fatalf("an unregistered extension: %q", errOut)
	}
}

// On the Extensions page: the setup column reads each registered row's
// state; the detail lists the items (name, state, target, what each
// changes, the next step) and the summary line counts the rows that need
// setup. s, on a row that needs setup, asks first with the plan's
// summary; y installs and the row reads complete. s does not answer on a
// complete row. The setting turns the column, the summary and the key
// off.
func TestExtensionsPageSetup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	setupTool(t, dir, "done", "installed", true)
	setupTool(t, dir, "todo", "missing", true)
	setupTool(t, dir, "plain", "missing", false)
	a := setupApp(t, dir, "done", "todo", "plain")
	if _, err := a.r.Settings().SetSession(settingExtAutoRefresh, false); err != nil {
		t.Fatal(err)
	}
	openExtPage(t, a, extensionsPage)
	cell := func(name string) string {
		for _, item := range a.extList.Items() {
			if item.Label == name {
				return item.Cells[2]
			}
		}
		return "?"
	}
	if cell("done") != setupComplete || cell("todo") != "needs 1 of 2" || cell("plain") != setupNone {
		t.Fatalf("cells done %q todo %q plain %q", cell("done"), cell("todo"), cell("plain"))
	}
	if err := a.r.SetFocus(extListID + "/todo"); err != nil {
		t.Fatal(err)
	}
	f := frame(a)
	for _, want := range []string{"setup", "1 extension needs setup (s on a row sets it up)", "setup  1 of 2 items missing (s sets it up)", "Harness hook", "Adds the todo hook", "next: o-todo setup install hook"} {
		if !strings.Contains(f, want) {
			t.Fatalf("frame lacks %q:\n%s", want, f)
		}
	}
	writeFrame(t, "extensions-setup.txt", f)
	pressJob(t, a, "s")
	if a.r.TopModal() == "" {
		t.Fatalf("s did not ask:\n%s", frame(a))
	}
	if f := frame(a); !strings.Contains(f, "Set up extension todo?") || !strings.Contains(f, "Harness hook") {
		t.Fatalf("confirm:\n%s", f)
	}
	writeFrame(t, "extensions-setup-confirm.txt", frame(a))
	pressJob(t, a, "y")
	if cell("todo") != setupComplete {
		t.Fatalf("after y: %q\n%s", cell("todo"), frame(a))
	}
	if strings.Contains(frame(a), "need setup") || strings.Contains(frame(a), "needs setup (") {
		t.Fatalf("summary stays:\n%s", frame(a))
	}
	// s on a complete row does nothing, and no hint offers it.
	if key := a.keyFor(extListID+"/todo", "extension.setup"); key != "" {
		t.Fatalf("a complete row hints %q", key)
	}
	pressJob(t, a, "s")
	if a.r.TopModal() != "" {
		t.Fatalf("s on a complete row opened %s", a.r.TopModal())
	}
	// Off: no column, no summary, no key.
	if err := os.WriteFile(filepath.Join(dir, "todo.setup"), []byte("missing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Settings().SetSession(settingExtSetupStatus, false); err != nil {
		t.Fatal(err)
	}
	openExtPage(t, a, runsPage) // the Settings page, in use
	openExtPage(t, a, extensionsPage)
	for _, item := range a.extList.Items() {
		if len(item.Cells) != len(extColumns) {
			t.Fatalf("setting off: cells %v", item.Cells)
		}
	}
	pressJob(t, a, "s")
	if a.r.TopModal() != "" {
		t.Fatalf("setting off: s opened %s", a.r.TopModal())
	}
}

// writeFrame keeps a frame for the lane's report when MTM_FRAMES names a
// directory.
func writeFrame(t *testing.T, name, f string) {
	t.Helper()
	if dir := os.Getenv("MTM_FRAMES"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(f), 0o644)
	}
}
