package actions

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
)

func TestExpandTemplateFiltersAndFallbacks(t *testing.T) {
	vars := map[string]string{
		"query": "go slices & maps? a=1",
		"file":  "docs/my plan.md",
		"url":   "https://fallback.example.com",
		"word":  "  Hello World  ",
		"quote": "it's ok",
	}
	cases := []struct {
		tpl  string
		want string
	}{
		{"https://www.google.com/search?q={{query|url}}", "https://www.google.com/search?q=go+slices+%26+maps%3F+a%3D1"},
		{"https://example.com/{{file|urlpath}}", "https://example.com/docs%2Fmy%20plan.md"},
		{"{{missing|'hello world'|url}}", "hello+world"},
		{"{{missing|url}}", "https://fallback.example.com"},
		{"{{word|trim|lower}}", "hello world"},
		{"{{word|trim|upper}}", "HELLO WORLD"},
		{"echo {{quote|shell}}", `echo 'it'\''s ok'`},
	}
	for _, tc := range cases {
		if got := ExpandTemplate(tc.tpl, vars); got != tc.want {
			t.Errorf("ExpandTemplate(%q) = %q, want %q", tc.tpl, got, tc.want)
		}
	}
}

func TestParseAllMixedLegacyAndCustomActions(t *testing.T) {
	src := `
	// Mixed actions.json with legacy ActionDef and parameterized CustomActions
	[
	  {
	    "noun": "task",
	    "verb": "ship",
	    "description": "Ship focused task",
	    "command": ["echo", "{{title}}"],
	  },
	  {
	    "id": "custom.ddg",
	    "title": "DuckDuckGo Search",
	    "description": "Search DuckDuckGo",
	    "category": "web",
	    "params": [
	      {"name": "query", "label": "Query", "type": "string", "required": true, "placeholder": "search..."},
	      {"name": "region", "type": "enum", "enum": ["us-en", "wt-wt"], "default": "us-en"},
	      {"name": "safe", "type": "bool", "default": true}
	    ],
	    "exec": "open \"https://duckduckgo.com/?q={{query|url}}&kl={{region}}\""
	  },
	  {
	    "id": "custom.inspect_file",
	    "title": "Inspect File",
	    "category": "files",
	    "params": [{"name": "file", "type": "path", "required": true}],
	    "exec": ["stat", "{{file}}"]
	  },
	  {
	    "id": "builtin.google_search",
	    "disabled": true
	  }
	]
	`
	defs, customs, err := ParseAll([]byte(src))
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(defs) != 1 || defs[0].CommandName() != "task.ship" {
		t.Fatalf("defs = %+v, want 1 task.ship", defs)
	}
	if len(customs) != 3 {
		t.Fatalf("len(customs) = %d, want 3", len(customs))
	}
	if customs[0].ID != "custom.ddg" || !strings.Contains(customs[0].Exec.Shell, "duckduckgo.com") {
		t.Fatalf("customs[0] = %+v", customs[0])
	}
	if !reflect.DeepEqual(customs[1].Exec.Argv, []string{"stat", "{{file}}"}) {
		t.Fatalf("customs[1].Exec.Argv = %#v", customs[1].Exec.Argv)
	}
	if !customs[2].Disabled {
		t.Fatalf("customs[2] should be disabled: %+v", customs[2])
	}

	// Verify ParamSpec conversion.
	specs := customs[0].ToParamSpecs()
	if len(specs) != 3 || specs[0].Type != core.ParamString || !specs[0].Required || specs[1].Type != core.ParamString || len(specs[1].Enum) != 2 || specs[2].Type != core.ParamBool {
		t.Fatalf("ToParamSpecs = %+v", specs)
	}
	fileSpecs := customs[1].ToParamSpecs()
	if len(fileSpecs) != 1 || fileSpecs[0].Type != core.ParamPath {
		t.Fatalf("fileSpecs = %+v", fileSpecs)
	}
}

func TestMergeCustomActionsOverrideDisableAndOptOut(t *testing.T) {
	builtins := BuiltinCustomActions()
	if len(builtins) != 3 {
		t.Fatalf("len(BuiltinCustomActions) = %d, want 3", len(builtins))
	}
	// Opt-out hides built-ins while keeping user custom actions.
	user := []CustomAction{
		{ID: "custom.hello", Title: "Hello", Exec: CustomExecSpec{Shell: "echo hi"}},
		{ID: BuiltinGoogleSearchID, Disabled: true},
		{ID: BuiltinUpgradePreviewID, Title: "Custom Upgrade Check", Exec: CustomExecSpec{Verb: "upgrade.preview"}},
	}
	withoutBuiltins := MergeCustomActions(false, user)
	if len(withoutBuiltins) != 2 || withoutBuiltins[0].ID != "custom.hello" || withoutBuiltins[1].ID != BuiltinUpgradePreviewID {
		t.Fatalf("withoutBuiltins = %+v", withoutBuiltins)
	}

	withBuiltins := MergeCustomActions(true, user)
	ids := make([]string, len(withBuiltins))
	for i, ca := range withBuiltins {
		ids[i] = ca.ID
	}
	wantIDs := []string{BuiltinWebOpenMDID, BuiltinUpgradePreviewID, "custom.hello"}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("withBuiltins IDs = %v, want %v", ids, wantIDs)
	}
	if withBuiltins[1].Title != "Custom Upgrade Check" {
		t.Fatalf("override title = %q, want Custom Upgrade Check", withBuiltins[1].Title)
	}
}

func TestLoadAllFileObjectFormAndValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ActionsFile)
	payload := `{
		"customActions": [
			{
				"id": "custom.open_docs",
				"title": "Open Docs",
				"category": "web",
				"exec": {"url": "https://example.com/docs"}
			}
		]
	}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	defs, customs, err := LoadAllFile(path)
	if err != nil {
		t.Fatalf("LoadAllFile: %v", err)
	}
	if len(defs) != 0 || len(customs) != 1 || customs[0].ID != "custom.open_docs" || customs[0].HasRequiredParams() {
		t.Fatalf("LoadAllFile got defs=%+v customs=%+v", defs, customs)
	}

	// Validation errors: enum without choices, missing exec.
	if _, _, err := ParseAll([]byte(`[{"id":"bad.enum","params":[{"name":"x","type":"enum"}],"exec":"echo 1"}]`)); err == nil || !strings.Contains(err.Error(), "enum") {
		t.Fatalf("expected enum validation error, got %v", err)
	}
	if _, _, err := ParseAll([]byte(`[{"id":"bad.exec","title":"No Exec"}]`)); err == nil || !strings.Contains(err.Error(), "missing exec") {
		t.Fatalf("expected missing exec validation error, got %v", err)
	}
}
