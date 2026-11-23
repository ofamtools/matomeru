package actions

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
)

const v1Snippet = `[
  {
    "noun": "cmd.cmd",
    "verb": "run-term",
    "description": "Run this command in the terminal",
    "command": ["o-cmds", "cmd", "run", "{{label}}"],
    "foreground": true,
    "history": "fetch"
  }
]`

func TestParseV1SnippetVerbatim(t *testing.T) {
	defs, err := Parse([]byte(v1Snippet))
	if err != nil {
		t.Fatalf("Parse v1 snippet: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("len(defs) = %d, want 1", len(defs))
	}
	got := defs[0]
	want := ActionDef{
		Noun:        "cmd.cmd",
		Verb:        "run-term",
		Description: "Run this command in the terminal",
		Command:     []string{"o-cmds", "cmd", "run", "{{label}}"},
		Foreground:  true,
		History:     "fetch",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
	if got.CommandName() != "cmd.cmd.run-term" {
		t.Fatalf("CommandName() = %q, want %q", got.CommandName(), "cmd.cmd.run-term")
	}
}

func TestParseJSONCCommentsAndTrailingCommas(t *testing.T) {
	src := `
// Top-level line comment for actions.json
/* Multi-line
   block comment */
[
  {
    "noun": "gh.pr", // inline comment
    "verb": "open-url",
    "description": "Open https://github.com/owner/repo/*not-a-comment*/",
    "command": [
      "open",
      "https://github.com/{{repo}}/pull/{{id}}", // keep // inside string
    ], // trailing comma before comment
    "dir": "{{worktree}}",
    "env": {
      "MTM_TITLE": "say \"//hello\\\"",
      "MTM_KIND": "gh.pr", /* trailing comma after this */
    },
  }, // trailing comma on array
]
`
	defs, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse JSONC: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("len(defs) = %d, want 1", len(defs))
	}
	d := defs[0]
	if d.CommandName() != "gh.pr.open-url" {
		t.Fatalf("CommandName() = %q, want gh.pr.open-url", d.CommandName())
	}
	if d.Description != "Open https://github.com/owner/repo/*not-a-comment*/" {
		t.Fatalf("Description = %q", d.Description)
	}
	if wantCmd := []string{"open", "https://github.com/{{repo}}/pull/{{id}}"}; !reflect.DeepEqual(d.Command, wantCmd) {
		t.Fatalf("Command = %#v, want %#v", d.Command, wantCmd)
	}
	if d.Dir != "{{worktree}}" {
		t.Fatalf("Dir = %q, want {{worktree}}", d.Dir)
	}
	if d.Env["MTM_TITLE"] != `say "//hello\"` || d.Env["MTM_KIND"] != "gh.pr" {
		t.Fatalf("Env = %#v", d.Env)
	}

	// Object wrapper {"actions": [...]} and comment-only files.
	wrapped, err := Parse([]byte(`{"actions": [` + v1Snippet[1:len(v1Snippet)-1] + `,]}`))
	if err != nil || len(wrapped) != 1 || wrapped[0].CommandName() != "cmd.cmd.run-term" {
		t.Fatalf("Parse wrapper = %#v, err=%v", wrapped, err)
	}
	empty, err := Parse([]byte("// only a comment\n/* and another */\n"))
	if err != nil || empty != nil {
		t.Fatalf("Parse comment-only = %#v, err=%v", empty, err)
	}
}

func TestParseInvalidEntries(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"malformed json", `[{`, "parse"},
		{"non-array root", `"nope"`, "JSON array"},
		{"object missing actions key", `{"other": []}`, `"actions"`},
		{"missing noun", `[{"verb":"run","command":["echo"]}]`, "missing noun"},
		{"invalid noun space", `[{"noun":"my noun","verb":"run","command":["echo"]}]`, "invalid noun"},
		{"invalid noun dots", `[{"noun":"gh..pr","verb":"run","command":["echo"]}]`, "invalid noun"},
		{"missing verb", `[{"noun":"task","command":["echo"]}]`, "missing verb"},
		{"invalid verb dot", `[{"noun":"task","verb":"run.now","command":["echo"]}]`, "invalid verb"},
		{"invalid verb space", `[{"noun":"task","verb":"run now","command":["echo"]}]`, "invalid verb"},
		{"empty command", `[{"noun":"task","verb":"run","command":[]}]`, "missing command"},
		{"blank executable", `[{"noun":"task","verb":"run","command":["   "]}]`, "missing command"},
		{"duplicate command", `[
			{"noun":"task","verb":"run","command":["echo","1"]},
			{"noun":"task","verb":"run","command":["echo","2"]}
		]`, "duplicates"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.src))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.json")
	defs, err := LoadFile(missing)
	if err != nil {
		t.Fatalf("LoadFile missing: err=%v, want nil", err)
	}
	if defs != nil {
		t.Fatalf("LoadFile missing: defs=%#v, want nil", defs)
	}

	p := filepath.Join(dir, ActionsFile)
	if err := os.WriteFile(p, []byte(v1Snippet), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	defs, err = LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile valid: %v", err)
	}
	if len(defs) != 1 || defs[0].CommandName() != "cmd.cmd.run-term" {
		t.Fatalf("LoadFile valid got %#v", defs)
	}
}

func TestCommandName(t *testing.T) {
	if got := (ActionDef{Noun: " cmd.cmd ", Verb: " run-term "}).CommandName(); got != "cmd.cmd.run-term" {
		t.Fatalf("CommandName() = %q, want cmd.cmd.run-term", got)
	}
	for _, bad := range []ActionDef{
		{Noun: "", Verb: "run"},
		{Noun: "task", Verb: ""},
		{Noun: ".task", Verb: "run"},
		{Noun: "task", Verb: "a.b"},
	} {
		if got := bad.CommandName(); got != "" {
			t.Fatalf("CommandName(%#v) = %q, want empty", bad, got)
		}
	}
}

func TestExpandCommand(t *testing.T) {
	def := ActionDef{
		Noun:    "cmd.cmd",
		Verb:    "run-term",
		Command: []string{"o-cmds", "cmd", "run", "{{label}}", "--id={{ id }}", "{{title}}", "{{data}}", "{{branch}}", "{{missing}}", "{{noun}}:{{verb}}"},
		Dir:     "/work/{{repo}}",
		Env:     map[string]string{"MTM_LABEL": "{{label}}", "MTM_DATA": "{{data}}"},
	}
	origCmd := slices.Clone(def.Command)

	// Shell metacharacters and embedded placeholders must stay intact in a single argv element.
	vars := map[string]string{
		"label":  `deploy $(rm -rf /); echo "hi" && {{id}}`,
		"id":     "rec-42",
		"title":  "Deploy staging",
		"data":   `{"env":"staging","args":"a b"}`,
		"branch": "feat/wave-4",
		"repo":   "matomeru",
	}

	got := def.ExpandCommand(vars)
	want := []string{
		"o-cmds",
		"cmd",
		"run",
		`deploy $(rm -rf /); echo "hi" && {{id}}`, // single-pass: {{id}} inside label value is NOT re-expanded
		"--id=rec-42",
		"Deploy staging",
		`{"env":"staging","args":"a b"}`,
		"feat/wave-4",
		"",
		"cmd.cmd:run-term",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExpandCommand:\ngot  %#v\nwant %#v", got, want)
	}
	if !reflect.DeepEqual(def.Command, origCmd) {
		t.Fatalf("ExpandCommand mutated ActionDef.Command: %#v", def.Command)
	}

	if dir := def.ExpandDir(vars); dir != "/work/matomeru" {
		t.Fatalf("ExpandDir = %q, want /work/matomeru", dir)
	}
	env := def.ExpandEnv(vars)
	if env["MTM_LABEL"] != vars["label"] || env["MTM_DATA"] != vars["data"] {
		t.Fatalf("ExpandEnv = %#v", env)
	}
	// Label falls back to title, then id when label is absent.
	fallback := (ActionDef{Command: []string{"echo", "{{label}}", "{{target}}"}}).ExpandCommand(map[string]string{
		"title": "Fallback Title",
		"id":    "t-9",
	})
	if !reflect.DeepEqual(fallback, []string{"echo", "Fallback Title", "t-9"}) {
		t.Fatalf("fallback ExpandCommand = %#v", fallback)
	}
}

func TestToCommandSpecAndRegistry(t *testing.T) {
	defs, err := Parse([]byte(v1Snippet))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	spec := defs[0].ToCommandSpec()

	if spec.ID != "cmd.cmd.run-term" {
		t.Fatalf("ID = %q, want cmd.cmd.run-term", spec.ID)
	}
	if spec.Description != "Run this command in the terminal" {
		t.Fatalf("Description = %q", spec.Description)
	}
	if !spec.HasTag("user") || !spec.HasTag("action") || !spec.HasTag("cmd.cmd") {
		t.Fatalf("Tags = %#v", spec.Tags)
	}
	if isRec, _ := spec.Meta[MetaRecord].(bool); !isRec {
		t.Fatalf("Meta[%q] = %#v, want true", MetaRecord, spec.Meta[MetaRecord])
	}
	if kinds, _ := spec.Meta[MetaKinds].([]string); !reflect.DeepEqual(kinds, []string{"cmd.cmd"}) {
		t.Fatalf("Meta[%q] = %#v, want [cmd.cmd]", MetaKinds, spec.Meta[MetaKinds])
	}
	if scope, _ := spec.Meta[MetaScope].(string); scope != "cmd.cmd" {
		t.Fatalf("Meta[%q] = %q, want cmd.cmd", MetaScope, scope)
	}
	if fg, _ := spec.Meta[MetaForeground].(bool); !fg {
		t.Fatalf("Meta[%q] = %#v, want true", MetaForeground, spec.Meta[MetaForeground])
	}
	if hist, _ := spec.Meta[MetaHistory].(string); hist != "fetch" {
		t.Fatalf("Meta[%q] = %q, want fetch", MetaHistory, hist)
	}
	if src, _ := spec.Meta[MetaSource].(string); src != ActionsFile {
		t.Fatalf("Meta[%q] = %q, want %q", MetaSource, src, ActionsFile)
	}
	if NamespaceOf(spec) != "cmd.cmd" {
		t.Fatalf("NamespaceOf(spec) = %q, want cmd.cmd", NamespaceOf(spec))
	}

	// Registers seamlessly into core.Registry.
	reg := core.DefaultRegistry()
	reg.Register(ToCommandSpecs(defs)...)
	looked, ok := reg.Lookup("cmd.cmd.run-term")
	if !ok || looked.Description != "Run this command in the terminal" {
		t.Fatalf("Registry.Lookup = (%#v, %v)", looked, ok)
	}

	// Generic "record" noun omits Meta["kinds"] so it applies to every focused record.
	genericSpec := (ActionDef{
		Noun:    "record",
		Verb:    "copy-json",
		Command: []string{"pbcopy"},
	}).ToCommandSpec()
	if _, hasKinds := genericSpec.Meta[MetaKinds]; hasKinds {
		t.Fatalf("generic record action should not restrict Meta[kinds]: %#v", genericSpec.Meta)
	}
	if genericSpec.Description != "pbcopy" {
		t.Fatalf("empty description fallback = %q, want pbcopy", genericSpec.Description)
	}
}

func TestNamespaceOf(t *testing.T) {
	cases := []struct {
		spec core.CommandSpec
		want string
	}{
		{core.CommandSpec{ID: "pane.approve"}, "pane"},
		{core.CommandSpec{ID: "panes.split"}, "panes"},
		{core.CommandSpec{ID: "gh.pr.merge"}, "gh.pr"},
		{core.CommandSpec{ID: "gh.open"}, "gh"},
		{core.CommandSpec{ID: "cmd.cmd.run-term"}, "cmd.cmd"},
		{core.CommandSpec{ID: "record.star"}, "record"},
		{core.CommandSpec{ID: "yank"}, "yank"},
		{core.CommandSpec{ID: "open.target"}, "open"},
		{core.CommandSpec{ID: "custom.run", Meta: map[string]any{MetaNoun: "gh.pr"}}, "gh.pr"},
		{core.CommandSpec{ID: "custom.run", Meta: map[string]any{MetaScope: "signal"}}, "signal"},
	}
	for _, tc := range cases {
		if got := NamespaceOf(tc.spec); got != tc.want {
			t.Errorf("NamespaceOf(%q, meta=%v) = %q, want %q", tc.spec.ID, tc.spec.Meta, got, tc.want)
		}
	}
}

func TestGroupForFocusPane(t *testing.T) {
	specs := []core.CommandSpec{
		{ID: "yank.primary", Description: "Yank primary"},
		{ID: "record.star", Description: "Star"},
		{ID: "open.target", Description: "Open target"},
		{ID: "panes.split", Description: "Split pane"},
		{ID: "record.archive", Description: "Archive"},
		{ID: "pane.zoom", Description: "Zoom"},
		{ID: "lane.show", Description: "Show lane", Meta: map[string]any{MetaKinds: []string{"pane", "lane"}}},
		{ID: "pane.approve", Description: "Approve"},
		{ID: "pane.attention.next", Description: "Next attention"},
		{ID: "yank.pick", Description: "Yank pick"},
		{ID: "record.edit", Description: "Edit"},
	}
	origIDs := idsOfSpecs(specs)

	grouped := GroupForFocus("pane", specs)
	if !reflect.DeepEqual(idsOfSpecs(specs), origIDs) {
		t.Fatalf("GroupForFocus mutated input slice")
	}

	wantIDs := []string{
		// Top focused group ("pane"): exact pane.* -> alias panes.* -> sub-namespace pane.attention.*
		"pane.approve",
		"pane.zoom",
		"panes.split",
		"pane.attention.next",
		// Kind-scoped cross-namespace group ("lane")
		"lane.show",
		// Generic record group ("record")
		"record.archive",
		"record.edit",
		"record.star",
		// Remaining namespaces alphabetically ("open", "yank")
		"open.target",
		"yank.pick",
		"yank.primary",
	}
	if gotIDs := idsOfGrouped(grouped); !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("GroupForFocus(pane):\ngot  %v\nwant %v", gotIDs, wantIDs)
	}

	// Verify FocusedMatch and FirstInGroup boundaries.
	sections := Sections(grouped)
	wantGroups := []string{"pane", "lane", "record", "open", "yank"}
	gotGroups := make([]string, len(sections))
	for i, sec := range sections {
		gotGroups[i] = sec.Name
	}
	if !reflect.DeepEqual(gotGroups, wantGroups) {
		t.Fatalf("Sections(pane) groups = %v, want %v", gotGroups, wantGroups)
	}
	if !sections[0].FocusedMatch || len(sections[0].Actions) != 4 {
		t.Fatalf("first section = %+v, want FocusedMatch=true len=4", sections[0])
	}
	for _, sec := range sections[1:] {
		if sec.FocusedMatch {
			t.Fatalf("non-first section %q unexpectedly has FocusedMatch=true", sec.Name)
		}
	}
}

func TestGroupForFocusGHPR(t *testing.T) {
	specs := []core.CommandSpec{
		{ID: "record.star"},
		{ID: "gh.open"},
		{ID: "yank.pick"},
		{ID: "gh.pr.merge"},
		{ID: "gh.browse"},
		{ID: "gh.prs.refresh"},
		{ID: "record.edit"},
		{ID: "gh.pr.checkout"},
		{ID: "open.target"},
		{ID: "gh.pr.comment"},
	}

	grouped := GroupForFocus("gh.pr", specs)
	wantIDs := []string{
		// Exact gh.pr.* + alias gh.prs.* first
		"gh.pr.checkout",
		"gh.pr.comment",
		"gh.pr.merge",
		"gh.prs.refresh",
		// Parent namespace gh.* second
		"gh.browse",
		"gh.open",
		// Generic record.* third
		"record.edit",
		"record.star",
		// Remaining namespaces grouped alphabetically
		"open.target",
		"yank.pick",
	}
	if gotIDs := idsOfGrouped(grouped); !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("GroupForFocus(gh.pr):\ngot  %v\nwant %v", gotIDs, wantIDs)
	}

	sections := Sections(grouped)
	wantGroups := []string{"gh.pr", "gh", "record", "open", "yank"}
	gotGroups := make([]string, len(sections))
	for i, s := range sections {
		gotGroups[i] = s.Name
	}
	if !reflect.DeepEqual(gotGroups, wantGroups) {
		t.Fatalf("Sections(gh.pr) = %v, want %v", gotGroups, wantGroups)
	}
	if !sections[0].FocusedMatch {
		t.Fatalf("gh.pr section should have FocusedMatch=true")
	}
	if sections[1].FocusedMatch {
		t.Fatalf("parent gh section should have FocusedMatch=false")
	}
}

func TestGroupForFocusSignalAndTask(t *testing.T) {
	signalSpecs := []core.CommandSpec{
		{ID: "record.archive"},
		{ID: "yank"},
		{ID: "triage.rule.from-signal", Meta: map[string]any{MetaKinds: []string{"signal"}}},
		{ID: "signal.promote"},
		{ID: "open"},
		{ID: "signal.dismiss"},
		{ID: "signals.triage"},
		{ID: "record.snooze"},
	}
	sigGot := idsOfGrouped(GroupForFocus("signal", signalSpecs))
	sigWant := []string{
		"signal.dismiss",
		"signal.promote",
		"signals.triage",
		"triage.rule.from-signal",
		"record.archive",
		"record.snooze",
		"open",
		"yank",
	}
	if !reflect.DeepEqual(sigGot, sigWant) {
		t.Fatalf("GroupForFocus(signal):\ngot  %v\nwant %v", sigGot, sigWant)
	}

	userTask := (ActionDef{
		Noun:        "task",
		Verb:        "export-md",
		Description: "Export task to Markdown",
		Command:     []string{"echo", "{{title}}"},
	}).ToCommandSpec()

	taskSpecs := []core.CommandSpec{
		{ID: "relate"},
		{ID: "record.edit"},
		{ID: "open.target"},
		{ID: "task.complete"},
		userTask,
		{ID: "yank.pick"},
		{ID: "tasks.prune"},
		{ID: "record.assign"},
		{ID: "work.focus"},
	}
	taskGot := idsOfGrouped(GroupForFocus("task", taskSpecs))
	taskWant := []string{
		"task.complete",
		"task.export-md",
		"tasks.prune",
		"record.assign",
		"record.edit",
		"open.target",
		"relate",
		"work.focus",
		"yank.pick",
	}
	if !reflect.DeepEqual(taskGot, taskWant) {
		t.Fatalf("GroupForFocus(task):\ngot  %v\nwant %v", taskGot, taskWant)
	}

	// Empty focus still groups deterministically (record first, then alphabetical).
	noFocus := idsOfGrouped(GroupForFocus("", []core.CommandSpec{
		{ID: "yank.pick"},
		{ID: "record.star"},
		{ID: "open.target"},
		{ID: "record.archive"},
	}))
	if want := []string{"record.archive", "record.star", "open.target", "yank.pick"}; !reflect.DeepEqual(noFocus, want) {
		t.Fatalf("GroupForFocus(\"\") = %v, want %v", noFocus, want)
	}
}

func idsOfSpecs(specs []core.CommandSpec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.ID
	}
	return out
}

func idsOfGrouped(grouped []GroupedAction) []string {
	out := make([]string, len(grouped))
	for i, g := range grouped {
		out[i] = g.Spec.ID
	}
	return out
}

func TestExpandTemplateFallbackChains(t *testing.T) {
	vars := map[string]string{
		"binary": "/usr/local/bin/o-df",
		"name":   "go-build-cache",
		"size":   "1.2 GB",
		"id":     "rec-1",
		"tricky": "{{id}}|'nope'",
	}
	cases := []struct {
		tpl  string
		want string
	}{
		{"{{sizeHuman|size|'space'}}", "1.2 GB"},
		{"{{missing1|missing2|'space'}}", "space"},
		{`{{missing1|"double fallback"}}`, "double fallback"},
		{"{{name|title|id}}", "go-build-cache"},
		{"{{missing|id}}", "rec-1"},
		{"Free {{sizeHuman|size|'space'}} from {{name|title|id}} via o-df clean --yes?", "Free 1.2 GB from go-build-cache via o-df clean --yes?"},
		// Single-pass: substituted values with {{...}} are never re-expanded.
		{"{{tricky|'fallback'}}", "{{id}}|'nope'"},
		// Whitespace around pipes and quoted literal containing pipe/braces.
		{"{{ missing | 'a|b' }}", "a|b"},
		{"{{missing|'keep}}braces'}}", "keep}}braces"},
	}
	for _, tc := range cases {
		if got := ExpandTemplate(tc.tpl, vars); got != tc.want {
			t.Errorf("ExpandTemplate(%q) = %q, want %q", tc.tpl, got, tc.want)
		}
	}

	argv := ExpandArgv([]string{"{{binary}}", "clean", "{{name|title|id}}", "--yes"}, vars)
	wantArgv := []string{"/usr/local/bin/o-df", "clean", "go-build-cache", "--yes"}
	if !reflect.DeepEqual(argv, wantArgv) {
		t.Fatalf("ExpandArgv = %#v, want %#v", argv, wantArgv)
	}
}

func TestRecordActionSpecToCommandSpecGuardVisibility(t *testing.T) {
	visible := RecordActionSpec{
		ID:          "df.culprit.clean",
		Description: "Clean culprit",
		Kinds:       []string{"df.culprit"},
		Source:      "o-df",
		Binary:      "/bin/o-df",
		Exec:        ExecSpec{Argv: []string{"{{binary}}", "clean", "{{name|title|id}}", "--yes"}},
		Guard:       &GuardSpec{When: "fields.rebuildable == true", RefuseMessage: "not rebuildable"},
	}.ToCommandSpec()
	if _, hasWhen := visible.Meta["when"]; hasWhen {
		t.Fatalf("HideWhenFalse=false should not set Meta[when]: %+v", visible.Meta)
	}
	if got, _ := visible.Meta["cli"].(string); got != "o-df clean {{name|title|id}} --yes" {
		t.Fatalf("Meta[cli] = %q", got)
	}
	if kinds, _ := visible.Meta[MetaKinds].([]string); !reflect.DeepEqual(kinds, []string{"df.culprit"}) {
		t.Fatalf("Meta[kinds] = %#v", visible.Meta[MetaKinds])
	}

	hidden := RecordActionSpec{
		ID:    "df.culprit.hidden",
		Kinds: []string{"df.culprit"},
		Exec:  ExecSpec{Argv: []string{"echo", "ok"}},
		Guard: &GuardSpec{When: "fields.rebuildable == true", HideWhenFalse: true},
	}.ToCommandSpec()
	if got, _ := hidden.Meta["when"].(string); got != "fields.rebuildable == true" {
		t.Fatalf("HideWhenFalse=true Meta[when] = %q, want guard expression", got)
	}
}
