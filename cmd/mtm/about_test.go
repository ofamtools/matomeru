package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/daemon"
)

func TestQuestionCommandContextNounsCLI(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir()
	db := filepath.Join(dir, "mtm.db")
	c := cli{t: t, db: db}

	// 1. question create/list/get/edit/star/archive/restore/answer (standalone + alias q/questions)
	q1 := decode[record](t, c.ok("question", "create", "--title", "Should we deploy tonight?", "--field", "questionKind=decision", "--body", "Waiting on green CI"))
	if q1.Kind != "question" || q1.Fields["prompt"] != "Should we deploy tonight?" || q1.Fields["details"] != "Waiting on green CI" {
		t.Fatalf("question create = %+v", q1)
	}
	if qs := decode[[]record](t, c.ok("q", "list")); len(qs) != 1 || qs[0].ID != q1.ID {
		t.Fatalf("q list = %+v", qs)
	}
	ans1 := decode[record](t, c.ok("question", "answer", q1.ID, "--answer", "Wait until morning"))
	if ans1.Fields["status"] != "answered" || ans1.Fields["answer"] != "Wait until morning" || ans1.Fields["answeredAt"] == "" {
		t.Fatalf("question answer (--answer) = %+v", ans1)
	}

	// 2. question asked via mcp ui.ask -> answered via positional `mtm q answer <id> <answer>` syncs with pane ui.page!
	c.ok("mcp", "call", "ui.ask", `{"pane": "w1", "title": "Pick database?", "choices": [{"id": "sqlite"}, {"id": "postgres"}]}`)
	openQs := decode[[]record](t, c.ok("questions", "list", "--field", "status=open"))
	if len(openQs) != 1 || openQs[0].Fields["prompt"] != "Pick database?" {
		t.Fatalf("open questions after ui.ask = %+v", openQs)
	}
	ans2 := decode[record](t, c.ok("q", "answer", openQs[0].ID, "sqlite"))
	if ans2.Fields["status"] != "answered" || ans2.Fields["answer"] != "sqlite" {
		t.Fatalf("q answer (positional) = %+v", ans2)
	}
	pages := decode[struct {
		Pages []struct {
			Slug      string `json:"slug"`
			Questions []struct {
				Title    string         `json:"title"`
				Answered bool           `json:"answered"`
				Answer   map[string]any `json:"answer"`
			} `json:"questions"`
		} `json:"pages"`
	}](t, c.ok("mcp", "call", "ui.list_pages", `{"pane": "w1"}`))
	if len(pages.Pages) != 1 || len(pages.Pages[0].Questions) != 1 ||
		!pages.Pages[0].Questions[0].Answered || pages.Pages[0].Questions[0].Answer["choice"] != "sqlite" {
		t.Fatalf("ui.page was not synced by mtm question answer: %+v", pages)
	}

	// Validation errors on question answer.
	c.fails("question", "answer", q1.ID)
	c.fails("question", "answer", q1.ID, "--answer", "   ")
	c.fails("question", "answer", q1.ID, "extra", "--answer", "both")

	// 3. command create/list/get/edit/star/delete (and aliases cmd/commands)
	cmd1 := decode[record](t, c.ok("command", "create", "--title", "Run CLI tests", "--field", "command=go test ./cmd/mtm/...", "--body", "Fast targeted check"))
	if cmd1.Kind != "command" || cmd1.Fields["title"] != "Run CLI tests" || cmd1.Fields["command"] != "go test ./cmd/mtm/..." || cmd1.Fields["notes"] != "Fast targeted check" {
		t.Fatalf("command create = %+v", cmd1)
	}
	if cmds := decode[[]record](t, c.ok("cmd", "list")); len(cmds) != 1 || cmds[0].ID != cmd1.ID {
		t.Fatalf("cmd list = %+v", cmds)
	}
	editedCmd := decode[record](t, c.ok("commands", "set", cmd1.ID, "--field", "status=ran"))
	if editedCmd.Fields["status"] != "ran" {
		t.Fatalf("commands set = %+v", editedCmd)
	}

	// 4. context create/list/get/relate (and aliases ctx/contexts)
	ctx1 := decode[record](t, c.ok("context", "create", "--title", "PR #42", "--field", "value=https://github.com/org/repo/pull/42", "--field", "contextKind=pr"))
	if ctx1.Kind != "context" || ctx1.Fields["title"] != "PR #42" || ctx1.Fields["value"] != "https://github.com/org/repo/pull/42" || ctx1.Fields["contextKind"] != "pr" {
		t.Fatalf("context create = %+v", ctx1)
	}
	if ctxs := decode[[]record](t, c.ok("ctx", "list")); len(ctxs) != 1 || ctxs[0].ID != ctx1.ID {
		t.Fatalf("ctx list = %+v", ctxs)
	}
	c.ok("contexts", "relate", ctx1.ID, "about", "command:"+cmd1.ID)
	c.ok("ctx", "star", ctx1.ID)
	if starred := decode[[]record](t, c.ok("context", "list", "--starred")); len(starred) != 1 || starred[0].ID != ctx1.ID {
		t.Fatalf("context list --starred = %+v", starred)
	}
}

func TestAboutCLI(t *testing.T) {
	if oaiProbeTimeout != 6*time.Second {
		t.Fatalf("oaiProbeTimeout = %v, want 6s", oaiProbeTimeout)
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mtm.db")
	cfgDir := filepath.Join(dir, "cfg")
	binDir := filepath.Join(dir, "bin")
	for _, d := range []string{cfgDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MTM_CONFIG_DIR", cfgDir)
	t.Setenv("PATH", binDir)

	// Fake mtm-hook and o-ai on PATH.
	fakeHook := filepath.Join(binDir, "mtm-hook")
	if err := os.WriteFile(fakeHook, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeOAI := filepath.Join(binDir, "o-ai")
	oaiScript := `#!/bin/sh
echo '{"items":[{"kind":"ai.harness","id":"antigravity","fields":{"id":"antigravity","installed":true,"configured":true,"executable":"/usr/local/bin/agy"}}],"total":1}'
`
	if err := os.WriteFile(fakeOAI, []byte(oaiScript), 0o755); err != nil {
		t.Fatal(err)
	}

	// Populate a subset of config files to verify presence/absence reporting.
	if err := os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte(`{"ui.theme":"ochazuke"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "harnesses.json"), []byte(`{"default":"antigravity","harnesses":{"antigravity":{"command":["agy"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "actions.json"), []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Seed records + one managed file.
	c := cli{t: t, db: dbPath}
	c.ok("task", "create", "--title", "Verify about")
	c.ok("question", "create", "--title", "Ready?")
	noteCLI := cli{t: t, db: dbPath, stdin: "# Managed\n"}
	noteCLI.ok("note", "create", "--title", "About Note", "--body", "-", "--managed")

	// Start in-process daemon with one live pane (absolute path since PATH is isolated).
	srv, client := startInProcessDaemon(t, true)
	if err := client.Open(context.Background(), daemon.OpenRequest{
		ID:   "p-about",
		Cmd:  "/bin/cat",
		Cols: 80,
		Rows: 24,
	}); err != nil {
		t.Fatalf("daemon Open: %v", err)
	}

	rep := decode[aboutReport](t, c.ok("about", "--socket", srv.SocketPath(), "--json"))
	if rep.Version != mtmVersion || rep.GoVersion == "" {
		t.Fatalf("version/go = %q / %q", rep.Version, rep.GoVersion)
	}
	if rep.Ochazuke == "" || rep.Oden == "" {
		t.Fatalf("module versions missing: ochazuke=%q oden=%q", rep.Ochazuke, rep.Oden)
	}
	if rep.Executable == "" || rep.MTMHookPath != fakeHook {
		t.Fatalf("paths: exe=%q hook=%q wantHook=%q", rep.Executable, rep.MTMHookPath, fakeHook)
	}
	if !rep.Daemon.Running || rep.Daemon.PID != os.Getpid() || rep.Daemon.Socket != srv.SocketPath() || rep.Daemon.MCPURL == "" || rep.Daemon.PaneCount != 1 {
		t.Fatalf("daemon = %+v", rep.Daemon)
	}
	if len(rep.Daemon.LivePanePIDs) != 1 || rep.Daemon.LivePanePIDs[0] <= 0 {
		t.Fatalf("livePanePIDs = %+v", rep.Daemon.LivePanePIDs)
	}
	if !rep.Storage.DBExists || rep.Storage.DBBytes <= 0 || rep.Storage.TotalRecords != 3 {
		t.Fatalf("storage = %+v", rep.Storage)
	}
	if rep.Storage.RecordCounts["task"] != 1 || rep.Storage.RecordCounts["question"] != 1 || rep.Storage.RecordCounts["note"] != 1 {
		t.Fatalf("recordCounts = %+v", rep.Storage.RecordCounts)
	}
	if rep.Storage.ManagedFilesDir == "" || rep.Storage.ManagedFileCount != 1 {
		t.Fatalf("managed files = dir:%q count:%d", rep.Storage.ManagedFilesDir, rep.Storage.ManagedFileCount)
	}
	if rep.Config.Dir != cfgDir || len(rep.Config.Files) != len(aboutConfigFileNames) {
		t.Fatalf("config = %+v", rep.Config)
	}
	cfgPresent := map[string]bool{}
	for _, f := range rep.Config.Files {
		cfgPresent[f.Name] = f.Exists
	}
	for _, name := range []string{"settings.json", "harnesses.json", "actions.json"} {
		if !cfgPresent[name] {
			t.Errorf("expected config file %s present", name)
		}
	}
	for _, name := range []string{"bindings.json", "themes.json", "handlers.json", "drafts.json"} {
		if cfgPresent[name] {
			t.Errorf("expected config file %s missing", name)
		}
	}
	if !slices.ContainsFunc(rep.Harnesses, func(h aboutHarness) bool { return h.ID == "antigravity" && h.Default }) {
		t.Fatalf("harnesses missing default antigravity: %+v", rep.Harnesses)
	}

	// Text output renders every section cleanly.
	txt := c.ok("about", "--socket", srv.SocketPath())
	for _, want := range []string{
		"Matomeru (mtm)", "Daemon", "running", "live PIDs:", "Storage", "question=1", "Config", "settings.json", "drafts.json", "Harnesses", "antigravity*",
	} {
		if !strings.Contains(txt, want) {
			t.Fatalf("about text missing %q:\n%s", want, txt)
		}
	}

	// Stopped daemon & missing DB file do not fail or create the DB.
	missingSock := shortTestSocket(t)
	missingDB := filepath.Join(dir, "never-created", "mtm.db")
	cMissing := cli{t: t, db: missingDB}
	stopped := decode[aboutReport](t, cMissing.ok("about", "--socket", missingSock, "--json"))
	if stopped.Daemon.Running || stopped.Storage.DBExists || stopped.Storage.TotalRecords != 0 {
		t.Fatalf("stopped/missing about = %+v", stopped)
	}
	if _, err := os.Stat(missingDB); !os.IsNotExist(err) {
		t.Fatalf("about created missing DB %s (err=%v)", missingDB, err)
	}
	c.fails("about", "extra")
}

func TestInspectActionsJSON(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("MTM_CONFIG_DIR", cfgDir)
	c := cli{t: t, db: filepath.Join(t.TempDir(), "unused.db")}

	type actionRow struct {
		ID          string   `json:"id"`
		Description string   `json:"description"`
		Keys        []string `json:"keys"`
		Bound       bool     `json:"bound"`
	}
	actions := decode[[]actionRow](t, c.ok("inspect", "actions", "--json"))
	if len(actions) == 0 {
		t.Fatal("inspect actions --json returned empty list")
	}
	found := map[string]actionRow{}
	for _, a := range actions {
		if a.ID == "" {
			t.Fatalf("action with empty ID: %+v", a)
		}
		found[a.ID] = a
	}
	if a, ok := found["record.star"]; !ok || !a.Bound || len(a.Keys) == 0 {
		t.Fatalf("inspect actions --json missing bound record.star: %+v", a)
	}
}
