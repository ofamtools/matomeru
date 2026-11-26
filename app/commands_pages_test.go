package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/harness"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/matomeru/runs"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/tree"
)

func TestWaveD2CheckBindingsZeroErrors(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatalf("CheckBindings error: %v", err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings(\"\") reported %d errors: %+v", len(rep.Errors), rep.Errors)
	}
}

func TestWaveD2UnifiedPagesPickerNeverEmptyAndNavigatesAllGroups(t *testing.T) {
	// 1. Empty store (zero panes, zero MCP pages): `'` Pages picker is STILL populated with [app] and [noun]!
	emptyStore, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { emptyStore.Close() })
	ea, err := New(emptyStore, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ea.Close() })
	ed := NewDriver(ea, 120, 36)

	ed.Keys("'")
	if ea.Modal() != "pages" || ea.Mode() != ModePickerFilter {
		t.Fatalf("' did not open modalPages in filter mode on empty store: modal=%q mode=%q", ea.Modal(), ea.Mode())
	}
	mod := ea.activePicker("pages")
	if mod == nil || len(mod.Model().Items) == 0 {
		t.Fatal("unified pages picker must never be empty")
	}
	pm := mod.Model()
	var ids []string
	for _, it := range pm.Items {
		ids = append(ids, it.ID)
	}
	for _, want := range []string{
		pagePickAppPrefix + "space:" + SpacePanes,
		pagePickAppPrefix + "about",
		pagePickAppPrefix + "settings",
		pagePickNounPrefix + data.KindTask,
		pagePickNounPrefix + data.KindQuestion,
		pagePickNounPrefix + data.KindCommand,
		pagePickNounPrefix + data.KindContext,
	} {
		if !slices.Contains(ids, want) {
			t.Errorf("unified pages picker missing %q in %v", want, ids)
		}
	}
	// Selecting [app] About opens modalAbout.
	ed.Type("about")
	ed.Keys("enter")
	if ea.Modal() != "about" || ea.Mode() != ModeAbout {
		t.Fatalf("picking About from ' did not open About: modal=%q mode=%q", ea.Modal(), ea.Mode())
	}
	ed.Keys("esc")

	// 2. Demo store with mounted extension noun: includes [app], [pane] (Screen + MCP pages), and [noun] (including extension).
	a, d := demo(t)
	if err := a.MountCatalog("/bin/true", ext.Manifest{
		Name: "o-df",
		Schemas: []data.Schema{{
			Kind:   "df.culprit",
			Title:  "Culprit",
			Plural: "Culprits",
		}},
	}); err != nil {
		t.Fatal(err)
	}

	d.Keys("'")
	pm = a.activePicker("pages").Model()
	var hasApp, hasPane, hasScreen, hasNoun, hasExt bool
	for _, it := range pm.Items {
		if strings.HasPrefix(it.ID, pagePickAppPrefix) {
			hasApp = true
		}
		if strings.HasPrefix(it.ID, pagePickPagePrefix) {
			hasPane = true
		}
		if strings.HasPrefix(it.ID, pagePickScreenPrefix) {
			hasScreen = true
		}
		if it.ID == pagePickNounPrefix+data.KindQuestion {
			hasNoun = true
		}
		if it.ID == pagePickNounPrefix+"df.culprit" {
			hasExt = true
		}
	}
	if !hasApp || !hasPane || !hasScreen || !hasNoun || !hasExt {
		t.Fatalf("missing unified picker groups: app=%v pane=%v screen=%v noun=%v ext=%v", hasApp, hasPane, hasScreen, hasNoun, hasExt)
	}

	// Pick mounted extension noun -> jumps directly to 2 Work browsing df.culprit!
	d.Type("culprits")
	d.Keys("enter")
	if a.Modal() != "" || a.Space() != SpaceWork || a.workKind() != "df.culprit" {
		t.Fatalf("picking Culprits from ': modal=%q space=%q workKind=%q", a.Modal(), a.Space(), a.workKind())
	}

	// Pick [pane] Screen -> jumps to 1 Panes on Screen.
	d.Keys("'")
	d.Type("screen claude")
	d.Keys("enter")
	if a.Space() != SpacePanes || a.Modal() != "" {
		t.Fatalf("picking Screen from ': space=%q modal=%q", a.Space(), a.Modal())
	}

	// Opt-out: ui.pickerDefaultMode="all-normal" opens Pages picker in normal mode.
	a.cfg.userSettings[SetPickerDefaultMode] = PickerDefaultModeAllNormal
	a.reloadSettings()
	d.Keys("'")
	if a.Modal() != "pages" || a.Mode() != ModePicker {
		t.Fatalf("all-normal opt-out did not open pages picker in ModePicker: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("esc")
}

func TestWaveD2AboutInfoAndOverlay(t *testing.T) {
	cfgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte(`{"ui.toast": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "mtm.db")
	store, err := data.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := SeedDemo(store); err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{ConfigDir: cfgDir, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	info := a.BuildAboutInfo()
	if info.Build.GoVersion == "" || info.Processes.PID <= 0 {
		t.Fatalf("invalid Build/Process info: %+v", info)
	}
	if info.Storage.DBPath != dbPath || info.Storage.DBSizeBytes <= 0 || info.Storage.TotalRecords == 0 {
		t.Fatalf("unexpected storage info: %+v", info.Storage)
	}
	if len(info.Storage.ConfigFiles) != 7 {
		t.Fatalf("expected 7 config files tracked, got %d", len(info.Storage.ConfigFiles))
	}
	foundSettingsLoaded := false
	for _, cf := range info.Storage.ConfigFiles {
		if cf.Name == "settings.json" && cf.Loaded {
			foundSettingsLoaded = true
		}
	}
	if !foundSettingsLoaded {
		t.Fatalf("settings.json should be reported as loaded: %+v", info.Storage.ConfigFiles)
	}
	if len(info.Processes.Panes) < 2 || len(info.Extensions) < 3 || len(info.Harnesses) == 0 {
		t.Fatalf("missing panes/extensions/harnesses in AboutInfo: panes=%d ext=%d harnesses=%d",
			len(info.Processes.Panes), len(info.Extensions), len(info.Harnesses))
	}

	// Open About via g A binding.
	d.Keys("g", "A")
	if a.Modal() != "about" || a.Mode() != ModeAbout {
		t.Fatalf("g A did not open modalAbout: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	frame := d.Frame()
	for _, want := range []string{"About Matomeru", "Build & Versions", "Processes & Daemon"} {
		if !strings.Contains(frame, want) {
			t.Errorf("About overlay frame missing %q:\n%s", want, frame)
		}
	}
	d.Keys("j", "esc", "esc")
	if a.Modal() != "" {
		t.Fatalf("esc did not close modalAbout: %q", a.Modal())
	}
}

func TestWaveD2QuestionAnswerFromWorkSyncsPanePage(t *testing.T) {
	a, d := demo(t)
	pane := titled(t, a, data.KindPane, "claude: wave 5B")

	// Ask a decision question via MCP Router -> creates both ui.page card AND KindQuestion record.
	asked, err := a.Router.Ask(seedAgent, mcpui.AskArgs{
		Pane:  pane.ID,
		ID:    "q-sync-check",
		Title: "Ship Wave D2 now?",
		Choices: []mcpui.Choice{
			{ID: "yes", Label: "Yes, ship it"},
			{ID: "wait", Label: "Wait for review"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Find the synchronized KindQuestion record.
	qRes, err := a.Store.Query(data.Query{Kind: data.KindQuestion})
	if err != nil {
		t.Fatal(err)
	}
	var qRec *data.Record
	for i := range qRes.Items {
		if qRes.Items[i].Text("cardId") == "q-sync-check" {
			qRec = &qRes.Items[i]
			break
		}
	}
	if qRec == nil {
		t.Fatalf("KindQuestion record not created for ui.ask card: %+v", qRes.Items)
	}

	// Browse Questions in 2 Work and press `a` to answer it.
	browseTo(t, a, d, *qRec)
	d.Keys("a")
	if a.Modal() != "answer" || a.Mode() != ModeForm {
		t.Fatalf("a on KindQuestion in 2 Work did not open modalAnswer: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("ctrl+s did not submit answer: modal=%q status=%q", a.Modal(), a.Status())
	}

	// Verify BOTH the KindQuestion record AND the pane's ui.page card are answered!
	updatedQ, err := a.Store.Get(qRec.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if updatedQ.Text("status") != "answered" || updatedQ.Text("answer") != "yes" {
		t.Fatalf("KindQuestion not updated: status=%q answer=%q", updatedQ.Text("status"), updatedQ.Text("answer"))
	}
	pages, err := a.Router.ListPages(mcpui.ListPagesArgs{Pane: pane.ID})
	if err != nil {
		t.Fatal(err)
	}
	synced := false
	for _, p := range pages.Pages {
		if p.ID != asked.ID {
			continue
		}
		for _, q := range p.Questions {
			if q.CardID == "q-sync-check" && q.Answered {
				synced = true
			}
		}
	}
	if !synced {
		t.Fatal("answering KindQuestion from 2 Work did not sync back to pane ui.page card")
	}
}

func TestWaveD2CommandRunEditAndYank(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	runner := runs.New(store, runs.Options{})
	t.Cleanup(func() { runner.Close() })

	a, err := New(store, Options{Runner: runner, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	cmdRec, _, err := store.Create(data.CreateInput{
		Kind: data.KindCommand,
		Fields: map[string]any{
			"title":      "Check version",
			"command":    "echo hello-waved2",
			"scriptPath": "/tmp/check.sh",
			"status":     "pending",
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	browseTo(t, a, d, *cmdRec)

	// 1. Yank: y y copies the literal command string, and scriptPath is in the yank picker.
	d.Keys("y")
	if a.Modal() != "yank" {
		t.Fatalf("y did not open yank picker: %q", a.Modal())
	}
	yankIDs := pickerIDs(a, "yank")
	if len(yankIDs) == 0 || yankIDs[0] != "command" || !slices.Contains(yankIDs, "scriptPath") {
		t.Fatalf("command yank picker expected command first + scriptPath: %v", yankIDs)
	}
	d.Keys("y")
	if got := a.Register(core.RegUnnamed); got != "echo hello-waved2" {
		t.Fatalf("y y on KindCommand yanked %q, want %q", got, "echo hello-waved2")
	}

	// 2. Run via Enter -> executes via a.Runner in 3 Runs and updates status="ran" & lastRun.
	d.Keys("enter")
	if a.Space() != SpaceRuns {
		t.Fatalf("enter on KindCommand did not open 3 Runs: space=%q status=%q", a.Space(), a.Status())
	}
	afterRun, err := store.Get(cmdRec.Ref())
	if err != nil {
		t.Fatal(err)
	}
	lastRunRef, hasLastRun := afterRun.RefField("lastRun")
	if afterRun.Text("status") != "ran" || !hasLastRun || lastRunRef.Kind != data.KindRun {
		t.Fatalf("command record after run: status=%q lastRun=%v (%v)", afterRun.Text("status"), lastRunRef, hasLastRun)
	}
	if done := runner.Done(lastRunRef.ID); done != nil {
		<-done
	}
	runRec, err := store.Get(lastRunRef)
	if err != nil || !strings.Contains(runRec.Text("output"), "hello-waved2") {
		t.Fatalf("run record output = %q (err=%v)", runRec.Text("output"), err)
	}

	// 3. Edit & Run via `e` -> opens launch form with command/args/cwd, runs tweaked command!
	browseTo(t, a, d, *cmdRec)
	d.Keys("e")
	if a.Modal() != "launch" || a.Mode() != ModeForm {
		t.Fatalf("e on KindCommand did not open edit-run launch form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	// Move to args field (index 1) and add extra arg.
	d.Keys("j", "i")
	d.Type("extra-arg")
	d.Keys("ctrl+s")
	if a.Modal() != "" || a.Space() != SpaceRuns {
		t.Fatalf("edit-run submit failed: modal=%q space=%q status=%q", a.Modal(), a.Space(), a.Status())
	}
	afterEdit, _ := store.Get(cmdRec.Ref())
	editRunRef, _ := afterEdit.RefField("lastRun")
	if editRunRef == lastRunRef {
		t.Fatal("edit-run did not create a new run")
	}
	if done := runner.Done(editRunRef.ID); done != nil {
		<-done
	}
	editRunRec, _ := store.Get(editRunRef)
	if !strings.Contains(editRunRec.Text("output"), "hello-waved2 extra-arg") {
		t.Fatalf("edit-run output = %q, want hello-waved2 extra-arg", editRunRec.Text("output"))
	}
}

func TestWaveD2UnlinkedPaneWarningContextPersistAndAssociateBackfill(t *testing.T) {
	a, d := demo(t)
	task := titled(t, a, data.KindTask, "matomeru-v2")

	// Create an unlinked pane (no task ref).
	unlinked, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindPane,
		Fields: map[string]any{
			"title":      "loose-worker",
			"harness":    "agy",
			"runStatus":  "running",
			"promptedAt": time.Now().UTC().Format(data.TimeFormat),
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	// Final-response context persists as KindContext in SQLite linked to pane.
	seedResponseContext(a, *unlinked, "Check https://example.com/pr/42 and owner/repo#99")
	ctxRes, err := a.Store.Query(data.Query{Kind: data.KindContext})
	if err != nil {
		t.Fatal(err)
	}
	if ctxRes.Total < 2 {
		t.Fatalf("expected harvested context persisted as KindContext records, got %d", ctxRes.Total)
	}
	// Harvesting the same response again deduplicates cleanly.
	seedResponseContext(a, *unlinked, "Check https://example.com/pr/42 and owner/repo#99")
	ctxRes2, _ := a.Store.Query(data.Query{Kind: data.KindContext})
	if ctxRes2.Total != ctxRes.Total {
		t.Fatalf("context persistence did not dedupe: before=%d after=%d", ctxRes.Total, ctxRes2.Total)
	}

	// Also create an unlinked document & command referencing the pane.
	docRec, _, _ := a.Store.Create(data.CreateInput{Kind: data.KindDocument, Fields: map[string]any{
		"title": "Scratch plan",
		"pane":  unlinked.Ref(),
	}}, User)
	cmdRec, _, _ := a.Store.Create(data.CreateInput{Kind: data.KindCommand, Fields: map[string]any{
		"title":   "Run tests",
		"command": "go test ./...",
		"pane":    unlinked.Ref(),
	}}, User)

	// 1 Panes rail shows [unlinked] warning badge and warning tone on the unlinked pane!
	d.Keys("1")
	rail := a.rail()
	if !selectRow(rail, data.RefText(unlinked.Ref())) {
		t.Fatal("unlinked pane not found in rail")
	}
	row, _ := rail.FocusedRow()
	if row.Facts["unlinked"] != true || row.Facts["statusTone"] != "warning" {
		t.Fatalf("unlinked pane missing warning facts: %+v", row.Facts)
	}
	if !strings.Contains(data.Text(row.Cells["rail"]), "[unlinked]") {
		t.Fatalf("unlinked pane rail cell missing [unlinked]: %q", data.Text(row.Cells["rail"]))
	}

	// Press L (`pane.associate`) -> pick matomeru-v2 -> sets pane.task AND backfills context/doc/cmd!
	d.Keys("L")
	if a.Modal() != "pane.associate" || a.Mode() != ModePicker {
		t.Fatalf("L on 1 Panes did not open pane.associate picker: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("/")
	d.Type("matomeru-v2")
	d.Keys("enter")
	if a.Modal() != "" {
		t.Fatalf("picking task in pane.associate did not close modal: %q", a.Modal())
	}
	updatedPane, _ := a.Store.Get(unlinked.Ref())
	if tRef, ok := updatedPane.RefField("task"); !ok || tRef != task.Ref() {
		t.Fatalf("pane.task after associate = %v (%v), want %v", tRef, ok, task.Ref())
	}
	updatedDoc, _ := a.Store.Get(docRec.Ref())
	if tRef, ok := updatedDoc.RefField("task"); !ok || tRef != task.Ref() {
		t.Fatalf("backfill did not link document to task: %v (%v)", tRef, ok)
	}
	updatedCmd, _ := a.Store.Get(cmdRec.Ref())
	if tRef, ok := updatedCmd.RefField("task"); !ok || tRef != task.Ref() {
		t.Fatalf("backfill did not link command to task: %v (%v)", tRef, ok)
	}
	if !strings.Contains(a.Status(), "record(s) linked") {
		t.Fatalf("expected backfill count in status, got %q", a.Status())
	}

	// Verify active Lanes show in the Project header overview on 1 Panes!
	d.Keys("1", "g", "g")
	projFrame := d.Frame()
	if !strings.Contains(projFrame, "ACTIVE LANES (") || !strings.Contains(projFrame, "Matomeru v2 Step 2") {
		t.Fatalf("Project overview missing active Lanes section:\n%s", projFrame)
	}
}

func TestWaveD2DFCulpritCleanConfirmAndNonRebuildableRefusal(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	runner := runs.New(store, runs.Options{})
	t.Cleanup(func() { runner.Close() })

	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "clean.log")
	dfBin := filepath.Join(binDir, "o-df")
	script := `#!/bin/sh
case "$1" in
  query)
    cat <<'EOF'
{"total":2,"items":[
  {"kind":"df.culprit","id":"go-build-cache","fields":{"name":"go-build-cache","sizeHuman":"1.2 GB","rebuildable":true}},
  {"kind":"df.culprit","id":"postgres-data","fields":{"name":"postgres-data","sizeHuman":"8.0 GB","rebuildable":false}}
]}
EOF
    ;;
  clean)
    echo "$@" >> "` + logFile + `"
    echo "freed $2"
    ;;
esac
`
	if err := os.WriteFile(dfBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{Runner: runner, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	manifestJSON := `{
  "name": "o-df",
  "version": "0.2.0",
  "schemas": [{
    "kind": "df.culprit",
    "title": "Culprit",
    "plural": "Culprits",
    "summaryField": "name",
    "defaultColumns": ["name", "sizeHuman", "rebuildable"],
    "fields": [
      {"name": "name", "type": "string"},
      {"name": "sizeHuman", "type": "string"},
      {"name": "rebuildable", "type": "bool"}
    ],
    "styleRules": [
      {"when": "fields.rebuildable == false", "target": "rebuildable", "tone": "muted"}
    ]
  }],
  "commands": [{
    "id": "df.culprit.clean",
    "description": "Free disk space for a rebuildable culprit via o-df clean --yes",
    "kinds": ["df.culprit"],
    "tags": ["o-df", "editing", "destructive"],
    "key": "X",
    "keyHint": "clean culprit",
    "exec": {
      "argv": ["{{binary}}", "clean", "{{name|title|id}}", "--yes"],
      "title": "o-df clean {{name|title|id}}",
      "refreshKinds": ["df.culprit"]
    },
    "guard": {
      "when": "fields.rebuildable == true",
      "refuseMessage": "Cannot clean {{name|title|id}}: culprit is not rebuildable"
    },
    "confirm": {
      "title": "Clean {{name|title|id}}",
      "message": "Free {{sizeHuman|size|'space'}} from {{name|title|id}} via o-df clean --yes?",
      "label": "Clean",
      "tone": "warning"
    },
    "feedback": {
      "running": "Cleaning {{name|title|id}}...",
      "success": "Freed {{sizeHuman|size|'space'}} from {{name|title|id}}",
      "failure": "o-df clean {{name|title|id}} failed"
    }
  }]
}`
	manifest, err := ext.ParseManifest([]byte(manifestJSON))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if err := a.MountCatalog(dfBin, manifest); err != nil {
		t.Fatal(err)
	}

	// Open Culprits in 2 Work and wait for cache load.
	d.Keys("g", "n", "/")
	d.Type("culprits")
	d.Keys("enter")
	a.WaitBackground()
	a.Shell.Update(wakeMsg())
	_ = d.Frame()

	// Verify `;` Actions puts df.culprit.clean at the top!
	d.Keys(";")
	if a.Modal() != "record.actions" {
		t.Fatalf("; did not open record actions: %q", a.Modal())
	}
	actionIDs := pickerIDs(a, "record.actions")
	if len(actionIDs) == 0 || actionIDs[0] != "df.culprit.clean" {
		t.Fatalf("expected %q at top of ; Actions on df.culprit, got %v", "df.culprit.clean", actionIDs)
	}
	d.Keys("esc", "esc")

	// Row 0 is rebuildable ("go-build-cache"): X opens Wave 4 confirmation dialog.
	d.Keys("X")
	if a.Modal() != "confirm" || a.Mode() != ModeConfirm {
		t.Fatalf("X on rebuildable df.culprit did not open confirm modal: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	if !strings.Contains(d.Frame(), "Free 1.2 GB from go-build-cache via o-df clean --yes?") {
		t.Fatalf("confirm dialog missing expected message:\n%s", d.Frame())
	}
	d.Keys("y")
	a.WaitBackground()
	a.Shell.Update(wakeMsg())

	logged, err := os.ReadFile(logFile)
	if err != nil || !strings.Contains(string(logged), "clean go-build-cache --yes") {
		t.Fatalf("o-df clean was not executed: log=%q err=%v", string(logged), err)
	}

	// Row 1 is NOT rebuildable ("postgres-data"): X refuses gracefully with toast and no confirm modal.
	d.Keys("j", "X")
	if a.Modal() == "confirm" {
		t.Fatal("X on non-rebuildable culprit should not open confirm modal")
	}
	if !strings.Contains(a.Status(), "not rebuildable") {
		t.Fatalf("expected non-rebuildable refusal status, got %q", a.Status())
	}

	// User bindings.json MUST override the extension's default key "X" on df.culprit.
	cfgDir := t.TempDir()
	overrideJSON := `[{"mode":"normal","scope":"df.culprit","keys":["X"],"command":"record.star","hint":"star override"}]`
	if err := os.WriteFile(filepath.Join(cfgDir, bindingsFileName), []byte(overrideJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	a2, err := New(store, Options{ConfigDir: cfgDir, Runner: runner, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a2.Close() })
	d2 := NewDriver(a2, 120, 36)
	if err := a2.MountCatalog(dfBin, manifest); err != nil {
		t.Fatal(err)
	}
	d2.Keys("g", "n", "/")
	d2.Type("culprits")
	d2.Keys("enter")
	a2.WaitBackground()
	a2.Shell.Update(wakeMsg())
	_ = d2.Frame()
	d2.Keys("X")
	if a2.Modal() == "confirm" {
		t.Fatal("user bindings.json override on X was shadowed by extension binding")
	}
	shadow, err := store.Get(core.Ref{Kind: "df.culprit", ID: "go-build-cache"})
	if err != nil || !shadow.IsStarred() {
		t.Fatalf("expected user bindings.json override X -> record.star to star culprit, got rec=%+v err=%v", shadow, err)
	}
}

func TestWaveD2WorkTasksTreeLayoutAndOutlineAddFold(t *testing.T) {
	a, d := demo(t)
	if a.workTasksLayout() != WorkTasksLayoutTree {
		t.Fatalf("default work.tasksLayout = %q, want tree", a.workTasksLayout())
	}

	d.Keys("2")
	collapsedFrame := d.Frame()
	if !strings.Contains(collapsedFrame, "▸") || !strings.Contains(collapsedFrame, "tree") {
		t.Fatalf("2 Work tree layout should start collapsed by default:\n%s", collapsedFrame)
	}
	// Unfold all to verify box-drawing tree guides.
	d.Keys("z", "R")
	frame := d.Frame()
	if !strings.Contains(frame, "├─") || !strings.Contains(frame, "└─") || !strings.Contains(frame, "tree") {
		t.Fatalf("2 Work tree layout missing box-drawing tree guides:\n%s", frame)
	}

	// Fold ochazuke-v2 subtree with `z a`.
	d.Keys("z", "a")
	foldedFrame := d.Frame()
	if !strings.Contains(foldedFrame, "▸") || strings.Contains(foldedFrame, "Tier 0: trie") {
		t.Fatalf("z a did not fold ochazuke-v2 children:\n%s", foldedFrame)
	}
	// Unfold with `z a`.
	d.Keys("z", "a")
	if !strings.Contains(d.Frame(), "Tier 0: trie") {
		t.Fatal("second z a did not unfold ochazuke-v2 children")
	}

	// `o` opens an inline row below at the SAME indent level (root sibling); pressing Tab indents it under ochazuke-v2.
	ochazuke := titled(t, a, data.KindTask, "ochazuke-v2")
	d.Keys("o")
	if ed := a.trees.activeEdit(); a.Mode() != ModeOutlineEdit || ed == nil || a.Modal() != "" {
		t.Fatalf("o did not enter inline outline edit: modal=%q mode=%q", a.Modal(), a.Mode())
	} else if ed.ParentID != "" {
		t.Fatalf("o on root task should start at same top-level indent, got ParentID=%q", ed.ParentID)
	}
	d.Keys("tab")
	if ed := a.trees.activeEdit(); ed == nil || ed.ParentID != ochazuke.ID {
		t.Fatalf("tab during inline o should indent under %q, got %+v", ochazuke.ID, ed)
	}
	d.Type("Tree Child Task")
	d.Keys("enter")
	childRec := titled(t, a, data.KindTask, "Tree Child Task")
	if p, ok := childRec.RefField("parent"); !ok || p != ochazuke.Ref() {
		t.Fatalf("created child task parent = %v, want %v", p, ochazuke.Ref())
	}

	// `o` on a subtask creates a sibling under the SAME parent (NOT a grandchild).
	d.Keys("o")
	if ed := a.trees.activeEdit(); a.Mode() != ModeOutlineEdit || ed == nil {
		t.Fatalf("o on subtask did not enter inline edit: mode=%q", a.Mode())
	} else if ed.ParentID != ochazuke.ID {
		t.Fatalf("o sibling task ParentID = %q, want %q", ed.ParentID, ochazuke.ID)
	}
	d.Keys("esc")

	// `s` cycles status on focused task in Work tree.
	curID := a.trees.workModel(data.KindTask).SelectedID
	beforeRec, _ := a.Store.Get(core.Ref{Kind: data.KindTask, ID: curID})
	d.Keys("s")
	afterRec, _ := a.Store.Get(core.Ref{Kind: data.KindTask, ID: curID})
	if afterRec.Text("workStatus") == beforeRec.Text("workStatus") {
		t.Fatalf("s did not cycle workStatus in tree (still %q)", afterRec.Text("workStatus"))
	}

	// Wire o, O, and z a in T Outline too!
	d.Keys("T", "g", "g", "z", "a")
	if !a.trees.folded[data.KindTask][ochazuke.ID] {
		t.Fatal("z a in T Outline did not fold ochazuke-v2")
	}
	d.Keys("z", "a", "O")
	if ed := a.trees.activeEdit(); a.Mode() != ModeOutlineEdit || ed == nil || ed.Kind != tree.EditAddAbove {
		t.Fatalf("O in T Outline did not start inline above row: mode=%q edit=%+v", a.Mode(), ed)
	}
	d.Keys("esc", "esc", "esc")

	// Opt-out setting work.tasksLayout = "table" switches cleanly to flat table view.
	a.cfg.userSettings[SetWorkTasksLayout] = WorkTasksLayoutTable
	a.reloadSettings()
	a.viewMemo.gen++
	d.Keys("2")
	tableFrame := d.Frame()
	if !strings.Contains(tableFrame, "Status") || !strings.Contains(tableFrame, "Priority") {
		t.Fatalf("work.tasksLayout=table did not render table columns:\n%s", tableFrame)
	}
}

func TestWaveD2ExtensionSetupBinding(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	runner := runs.New(store, runs.Options{})
	t.Cleanup(func() { runner.Close() })

	binDir := t.TempDir()
	setupBin := filepath.Join(binDir, "o-sample")
	if err := os.WriteFile(setupBin, []byte("#!/bin/sh\necho setup-complete-$1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, _ = store.Create(data.CreateInput{Kind: data.KindExtension, Fields: map[string]any{
		"name":      "o-sample",
		"binary":    setupBin,
		"runStatus": "healthy",
		"commands":  []string{"setup", "doctor"},
	}}, User)
	_, _, _ = store.Create(data.CreateInput{Kind: data.KindExtension, Fields: map[string]any{
		"name":      "o-nosetup",
		"binary":    setupBin,
		"runStatus": "healthy",
		"commands":  []string{"doctor"},
	}}, User)

	a, err := New(store, Options{Runner: runner, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	d.Keys("4", "U")
	runsRes, _ := store.Query(data.Query{Kind: data.KindRun})
	if runsRes.Total != 1 {
		t.Fatalf("U on extension with setup command expected 1 run, got %d (status=%q)", runsRes.Total, a.Status())
	}
	if done := runner.Done(runsRes.Items[0].ID); done != nil {
		<-done
	}
	finished, _ := store.Get(runsRes.Items[0].Ref())
	if !strings.Contains(finished.Text("output"), "setup-complete-setup") {
		t.Fatalf("setup run output = %q", finished.Text("output"))
	}

	// Move to extension without setup command -> shows helpful toast/status, no new run.
	d.Keys("j", "U")
	runsRes2, _ := store.Query(data.Query{Kind: data.KindRun})
	if runsRes2.Total != 1 || !strings.Contains(a.Status(), "needs no setup") {
		t.Fatalf("U on extension without setup: runs=%d status=%q", runsRes2.Total, a.Status())
	}
}

func TestWaveD2EnrichedActionCatalog(t *testing.T) {
	rows := ActionRows(DefaultBindings(), CommandSpecs())
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, r := range decoded {
		if id, _ := r["id"].(string); id != "" {
			byID[id] = r
		}
	}

	star, ok := byID["record.star"]
	if !ok {
		t.Fatal("record.star missing from ActionRows")
	}
	for _, field := range []string{"id", "description", "namespace", "keys", "mode", "when", "cli", "mcp"} {
		if v, exists := star[field]; !exists || v == "" {
			t.Errorf("record.star missing enriched field %q: %+v", field, star)
		}
	}

	newRec, ok := byID["record.new"]
	if !ok {
		t.Fatal("record.new missing from ActionRows")
	}
	params, ok := newRec["params"].([]any)
	if !ok || len(params) == 0 {
		t.Fatalf("record.new missing params: %+v", newRec)
	}
	p0, _ := params[0].(map[string]any)
	if p0["name"] != "kind" || p0["type"] != "string" || p0["default"] != "task" {
		t.Fatalf("record.new param[0] incomplete: %+v", p0)
	}

	aboutAct, ok := byID[CmdAppAbout]
	if !ok || aboutAct["cli"] != "mtm about" || aboutAct["namespace"] != "app" {
		t.Fatalf("app.about enriched row unexpected: %+v", aboutAct)
	}
}

func TestWaveD2NewPaneRefPickerCreateNewTaskAndDefaultCwd(t *testing.T) {
	a, d := demo(t)

	// Configure agy as an installed harness to verify agy shows cleanly too.
	a.SetInstalledHarnesses([]harness.InstalledHarness{
		{ID: "agy", Installed: true, Configured: true, Executable: "/usr/local/bin/agy"},
		{ID: "claude", Installed: true, Configured: true, Executable: "/usr/local/bin/claude"},
	})

	// Open New Pane form on 1 Panes.
	d.Keys("1", "n")
	rf := a.activeRecordForm()
	if a.Modal() != "form" || rf == nil || rf.kind != data.KindPane {
		t.Fatalf("1 n did not open New pane form: modal=%q", a.Modal())
	}

	// Verify cwd is pre-filled with a non-empty default working directory (project defaultDir or os.Getwd()).
	if cwd := strings.TrimSpace(data.Text(rf.Model().Values["cwd"])); cwd == "" {
		t.Fatal("New pane form did not pre-fill cwd")
	}

	// Navigate to the `title` field and give the pane a title.
	d.Keys("i")
	d.Type("Auth Flow Pane")
	d.Keys("enter")

	// Navigate to the `task` ref field and open the record-reference picker.
	for range len(rf.Model().Specs) {
		if rf.Model().Specs[rf.Model().ActiveIdx].Name == "task" {
			break
		}
		d.Keys("j")
	}
	if rf.Model().Specs[rf.Model().ActiveIdx].Name != "task" {
		t.Fatal("could not locate task field on New pane form")
	}
	d.Keys("enter")
	if a.Modal() != "ref" || a.Mode() != ModePicker {
		t.Fatalf("enter on task field did not open modalRef: modal=%q mode=%q", a.Modal(), a.Mode())
	}

	// Type a non-existent task title -> dynamic `+ Create new Task: "Fix auth flow"` row appears!
	d.Keys("/")
	d.Type("Fix auth flow")
	frame := d.Frame()
	if !strings.Contains(frame, `+ Create new Task: "Fix auth flow"`) {
		t.Fatalf("dynamic create option not shown in task ref picker:\n%s", frame)
	}

	// Confirm the dynamic row -> creates the Task record in Store and selects it into the form!
	d.Keys("enter")
	if a.Modal() != "form" {
		t.Fatalf("confirming + Create new Task did not return to New pane form: modal=%q", a.Modal())
	}
	createdTask, err := a.Store.GetByField(data.KindTask, "title", "Fix auth flow")
	if err != nil || createdTask == nil {
		t.Fatalf("task \"Fix auth flow\" was not created in store: err=%v", err)
	}
	if gotRef, ok := data.AsRef(a.activeRecordForm().Model().TypedValues()["task"]); !ok || gotRef != createdTask.Ref() {
		t.Fatalf("form task field = %v, want %v", a.activeRecordForm().Model().TypedValues()["task"], createdTask.Ref())
	}

	// Submit New Pane form -> new pane is persisted with task pointing to "Fix auth flow"!
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("ctrl+s did not create new pane: modal=%q status=%q", a.Modal(), a.Status())
	}
	newPane, err := a.Store.GetByField(data.KindPane, "title", "Auth Flow Pane")
	if err != nil || newPane == nil {
		t.Fatalf("new pane not persisted: err=%v", err)
	}
	if pTask, ok := newPane.RefField("task"); !ok || pTask != createdTask.Ref() {
		t.Fatalf("new pane task ref = %v (%v), want %v", pTask, ok, createdTask.Ref())
	}
}

func TestAppReloadAndDaemonUpgradeActions(t *testing.T) {
	a, d := demo(t)

	// Bindings must stay clean with g U (app.reload) and g D (daemon.upgrade).
	rep, err := CheckBindings("")
	if err != nil || len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings errors: %+v (err=%v)", rep.Errors, err)
	}

	// daemon.upgrade without a live daemon reports cleanly without crashing.
	d.Keys("g", "D")
	if !strings.Contains(a.Status(), "no local daemon") {
		t.Fatalf("g D status = %q, want no local daemon", a.Status())
	}

	// app.reload (g U) sets WantReload() so cmd/mtm re-execs the updated binary.
	if a.WantReload() {
		t.Fatal("WantReload should start false")
	}
	d.Keys("g", "U")
	if !a.WantReload() {
		t.Fatal("g U did not set WantReload() = true")
	}
}

func TestDeadPaneAutoDetachAndArchive(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	task, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Auth Task"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	pane, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title":     "Short-lived Pane",
		"runStatus": "running",
		"task":      task.Ref(),
	}}, User)
	if err != nil {
		t.Fatal(err)
	}

	host := ptyhost.NewLocalHost()
	t.Cleanup(host.CloseAll)
	a, err := New(store, Options{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)

	// Open a short-lived PTY that exits immediately with code 7.
	if err := host.Open(pane.ID, "exit 7", "", 80, 20); err != nil {
		t.Fatalf("host.Open: %v", err)
	}
	a.rt.live.shown = pane.ID
	a.rt.live.frames[pane.ID] = ptyhost.Frame{ID: pane.ID, Cols: 80, Rows: 20, Exited: false, Revision: 0}

	// Attach into ModeTerminal.
	s := withMode(a.State(), ModeTerminal)

	// Wait until the PTY exits in LocalHost.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f, ok := host.Snapshot(pane.ID); ok && f.Exited {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	// Trigger wakeUp -> must detach from ModeTerminal, mark runStatus="dead", and archive pane!
	res := a.wakeUp(s, core.Command{ID: CmdWake})
	if res.State.ActiveFrame().Mode != ModeNormal {
		t.Fatalf("mode after dead pane exit = %q, want %q", res.State.ActiveFrame().Mode, ModeNormal)
	}
	got, err := store.Get(pane.Ref())
	if err != nil {
		t.Fatalf("store.Get pane: %v", err)
	}
	if got.Text("runStatus") != "dead" {
		t.Fatalf("dead pane runStatus = %q, want dead", got.Text("runStatus"))
	}
	if got.ArchiveStatus != data.ArchiveArchived {
		t.Fatalf("dead pane archiveStatus = %q, want archived", got.ArchiveStatus)
	}
	if tr, ok := got.RefField("task"); !ok || tr != task.Ref() {
		t.Fatalf("dead pane lost task association: %v (%v)", tr, ok)
	}
}
