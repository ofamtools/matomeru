package app

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/actions"
	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func latestRunCommand(t *testing.T, a *App) string {
	t.Helper()
	res, err := a.Store.Query(data.Query{Kind: data.KindRun, Limit: 50})
	if err != nil {
		t.Fatalf("query runs: %v", err)
	}
	if len(res.Items) == 0 {
		t.Fatal("expected at least one run record, got 0")
	}
	latest := res.Items[0]
	for _, it := range res.Items[1:] {
		if it.CreatedAt.After(latest.CreatedAt) || (it.CreatedAt.Equal(latest.CreatedAt) && it.ID > latest.ID) {
			latest = it
		}
	}
	return latest.Text("command")
}

func TestCustomActionsPickerAndGoogleSearch(t *testing.T) {
	a, d := demo(t)

	d.Keys("g", "a")
	if a.Modal() != customActionsModalID {
		t.Fatalf("g a modal = %q, want %q (status=%q)", a.Modal(), customActionsModalID, a.Status())
	}
	ids := pickerIDs(a, customActionsModalID)
	for _, want := range []string{actions.BuiltinGoogleSearchID, actions.BuiltinWebOpenMDID, actions.BuiltinUpgradePreviewID} {
		if !slices.Contains(ids, want) {
			t.Fatalf("custom actions picker missing %q: got %v", want, ids)
		}
	}

	// Pick Google Search -> Fill-in-the-Blank Auto-Form opens in edit mode.
	d.Keys("enter")
	if a.Modal() != customActionFormModalID {
		t.Fatalf("selecting Google Search opened %q, want %q", a.Modal(), customActionFormModalID)
	}
	if a.Mode() != ModeFormEdit {
		t.Fatalf("single-string custom action form mode = %q, want %q", a.Mode(), ModeFormEdit)
	}
	d.Type("golang slices & maps")
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("form did not close after ctrl+s: modal=%q status=%q", a.Modal(), a.Status())
	}
	cmd := latestRunCommand(t, a)
	wantURL := "https://www.google.com/search?q=golang+slices+%26+maps"
	if !strings.Contains(cmd, wantURL) {
		t.Fatalf("Google Search run command = %q, want substring %q", cmd, wantURL)
	}

	// Also verify submitting with enter (stop edit) + enter (submit single-field form).
	d.Keys("g", "a", "enter")
	d.Type("ochazuke tui")
	d.Keys("enter", "enter")
	if a.Modal() != "" {
		t.Fatalf("single-field form did not submit on enter+enter: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	cmd2 := latestRunCommand(t, a)
	if !strings.Contains(cmd2, "https://www.google.com/search?q=ochazuke+tui") {
		t.Fatalf("enter+enter run command = %q", cmd2)
	}
}

func TestCustomActionsWebOpenMarkdownAndPathPicker(t *testing.T) {
	a, d := demo(t)
	tmpDir := t.TempDir()
	mdFile := filepath.Join(tmpDir, "design plan.md")
	if err := os.WriteFile(mdFile, []byte("# Design Plan\n\nHello mdweb!\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create a document pointing at mdFile and focus it in Work -> Documents.
	if _, _, err := a.Store.Create(data.CreateInput{
		Kind:   data.KindDocument,
		Fields: map[string]any{"title": "Design Plan", "filePath": mdFile},
	}, User); err != nil {
		t.Fatal(err)
	}
	d.Keys("2", "g", "n", "/")
	d.Type("documents")
	d.Keys("enter", "G")
	_ = d.Frame()

	// Configure a live mdweb daemon base URL.
	a.rt.live.telemetryMu.Lock()
	a.rt.live.mcpURL = "http://127.0.0.1:4173"
	a.rt.live.telemetryMu.Unlock()

	// Open Custom Actions picker, filter for Markdown viewer, confirm.
	d.Keys("g", "a", "/")
	d.Type("markdown")
	d.Keys("enter")
	if a.Modal() != customActionFormModalID {
		t.Fatalf("expected custom action form modal, got %q", a.Modal())
	}
	// Verify focused document's path was pre-filled!
	if frame := d.Frame(); !strings.Contains(frame, "design plan.md") {
		t.Fatalf("expected focused document path pre-filled in form:\n%s", frame)
	}

	// Leave edit mode and press p -> opens path picker modal for ParamPath!
	d.Keys("esc", "p")
	if a.Modal() != "path" {
		t.Fatalf("p on path param did not open path picker: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	// Close path picker and submit the pre-filled form.
	d.Keys("esc", "esc", "ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("web open form did not close on ctrl+s: modal=%q status=%q", a.Modal(), a.Status())
	}

	cmd := latestRunCommand(t, a)
	wantURL := "http://127.0.0.1:4173/md/view?file=" + url.QueryEscape(mdFile)
	if !strings.Contains(cmd, wantURL) {
		t.Fatalf("web.open command = %q, want substring %q", cmd, wantURL)
	}

	// Without daemon HTTP server running, web.open reports cleanly without crashing.
	a.rt.live.telemetryMu.Lock()
	a.rt.live.mcpURL = ""
	a.rt.live.telemetryMu.Unlock()
	t.Setenv("MTM_MCP_URL", "")
	_, _ = a.Shell.Update(core.CommandEvent{Command: core.Command{
		ID:     CmdWebOpen,
		Args:   map[string]any{"file": mdFile},
		Source: "custom.actions",
		Actor:  User,
	}})
	if !strings.Contains(a.Status(), "mdweb daemon not running") {
		t.Fatalf("expected graceful offline message, got %q", a.Status())
	}
}

func TestCustomActionsUpgradePreviewImmediateExec(t *testing.T) {
	a, d := demo(t)

	prevBuilder := upgradePreviewBuilder
	t.Cleanup(func() { upgradePreviewBuilder = prevBuilder })
	upgradePreviewBuilder = func(_ *App) UpgradePreviewInfo {
		return UpgradePreviewInfo{
			Running: UpgradeBinaryInfo{
				Version:         "v1.3.0",
				Commit:          "aaa1111222233",
				ShortSHA:        "aaa1111",
				OchazukeVersion: "v1.21.0",
			},
			Installed: UpgradeBinaryInfo{
				Version:         "v1.4.0",
				Commit:          "bbb2222333344",
				ShortSHA:        "bbb2222",
				OchazukeVersion: "v1.22.0",
				Path:            "/Users/test/bin/mtm",
			},
			DaemonRunning: true,
			DaemonVersion: "v1.4.0",
			DaemonCommit:  "bbb2222",
			DaemonMatch:   false,
			HasUpgrade:    true,
			StatusSummary: "Upgrade ready: v1.3.0 (aaa1111) -> v1.4.0 (bbb2222)",
			Commits: []string{
				"bbb2222 feat: generic custom actions picker",
				"ccc3333 perf: zero-alloc viewport render",
			},
		}
	}

	// Trigger Preview g U Pending Upgrade via g a picker (0 required params -> immediate!).
	d.Keys("g", "a", "/")
	d.Type("upgrade")
	d.Keys("enter")
	if a.Modal() != upgradePreviewModalID {
		t.Fatalf("upgrade preview modal = %q, want %q (status=%q)", a.Modal(), upgradePreviewModalID, a.Status())
	}
	frame := d.Frame()
	for _, want := range []string{"v1.3.0", "v1.4.0", "bbb2222", "g U"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("upgrade preview frame missing %q:\n%s", want, frame)
		}
	}
	d.Keys("q")
	if a.Modal() != "" {
		t.Fatalf("q did not close upgrade preview modal: %q", a.Modal())
	}

	// Verify binary probe helper against fake scripts (version --json and --version fallback).
	binDir := t.TempDir()
	jsonBin := filepath.Join(binDir, "mtm-json")
	if err := os.WriteFile(jsonBin, []byte("#!/bin/sh\necho '{\"version\":\"v1.9.0\",\"commit\":\"deadbeef1234\",\"shortSha\":\"deadbee\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gotJSON := probeInstalledMTMBinary(context.Background(), jsonBin)
	if gotJSON.Version != "v1.9.0" || gotJSON.ShortSHA != "deadbee" || gotJSON.ProbeErr != "" {
		t.Fatalf("probeInstalledMTMBinary(json) = %+v", gotJSON)
	}

	textBin := filepath.Join(binDir, "mtm-text")
	if err := os.WriteFile(textBin, []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'mtm v1.2.0'; exit 0; fi\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gotText := probeInstalledMTMBinary(context.Background(), textBin)
	if gotText.Version != "v1.2.0" || gotText.ProbeErr != "" {
		t.Fatalf("probeInstalledMTMBinary(text) = %+v", gotText)
	}
}

func TestCustomActionsUserConfigOptOutAndLeaderMirror(t *testing.T) {
	cfgDir := t.TempDir()
	actionsJSON := `[
		{
			"noun": "task",
			"verb": "ping",
			"description": "Legacy record action still works",
			"command": ["echo", "ping:{{title}}"]
		},
		{
			"id": "custom.grep_docs",
			"title": "Grep Docs",
			"description": "Search docs for a keyword",
			"category": "custom",
			"params": [
				{"name": "term", "label": "Search term", "type": "string", "required": true},
				{"name": "mode", "type": "enum", "enum": ["fixed", "regex"], "default": "fixed"}
			],
			"exec": "rg --{{mode}} {{term|shell}} docs/"
		},
		{
			"id": "builtin.google_search",
			"disabled": true
		}
	]`
	if err := os.WriteFile(filepath.Join(cfgDir, actions.ActionsFile), []byte(actionsJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	a, d := configured(t, cfgDir)

	// Legacy actions.json entry still registered in CommandSpecs/Keymap.
	if _, ok := a.bind.Keymap.Commands.Lookup("task.ping"); !ok {
		t.Fatal("legacy actions.json command task.ping was not registered")
	}

	// Custom actions picker honors disabled built-in and includes custom.grep_docs.
	d.Keys("g", "a")
	ids := pickerIDs(a, customActionsModalID)
	if slices.Contains(ids, actions.BuiltinGoogleSearchID) {
		t.Fatalf("disabled builtin.google_search still shown: %v", ids)
	}
	if !slices.Contains(ids, "custom.grep_docs") || !slices.Contains(ids, actions.BuiltinWebOpenMDID) {
		t.Fatalf("expected custom.grep_docs and builtin.web_open_markdown, got %v", ids)
	}

	// Select custom.grep_docs, fill term, submit with ctrl+s -> expands shell filter cleanly.
	d.Keys("/")
	d.Type("grep")
	d.Keys("enter")
	if a.Modal() != customActionFormModalID {
		t.Fatalf("expected custom action form modal for grep_docs, got %q", a.Modal())
	}
	d.Type("hello world")
	d.Keys("ctrl+s")
	cmd := latestRunCommand(t, a)
	if !strings.Contains(cmd, "rg --fixed") || !strings.Contains(cmd, "hello world") {
		t.Fatalf("custom.grep_docs command = %q", cmd)
	}

	// Opt-out setting ui.builtinCustomActions = false hides all built-in recipes immediately.
	a.cfg.userSettings[SetBuiltinCustomActions] = false
	a.reloadSettings()
	d.Keys("g", "a")
	idsOptOut := pickerIDs(a, customActionsModalID)
	if len(idsOptOut) != 1 || idsOptOut[0] != "custom.grep_docs" {
		t.Fatalf("with ui.builtinCustomActions=false, got %v, want [custom.grep_docs]", idsOptOut)
	}
	d.Keys("esc", "esc")

	// Leader mirroring: inside live terminal (ModeTerminal), ctrl+a g a opens Custom Actions picker!
	a.Shell.State = withMode(a.State(), ModeTerminal)
	d.Keys("ctrl+a", "g", "a")
	if a.Modal() != customActionsModalID {
		t.Fatalf("ctrl+a g a in ModeTerminal did not open custom actions picker: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
}
