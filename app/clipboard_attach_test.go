package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/modal"
)

func TestResolveAttachmentsDirPrecedence(t *testing.T) {
	// Ensure MTM_ATTACHMENTS_DIR does not leak from outer shell.
	t.Setenv("MTM_ATTACHMENTS_DIR", "")
	t.Setenv("MTM_FILES_DIR", "")

	dbDir := t.TempDir()
	store, err := data.Open(filepath.Join(dbDir, "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	// 1. Default file-backed store resolves <dbDir>/files -> sibling <dbDir>/attachments.
	wantSibling := filepath.Join(dbDir, "attachments")
	if got := a.resolveAttachmentsDir(); got != wantSibling {
		t.Fatalf("resolveAttachmentsDir (store sibling) = %q, want %q", got, wantSibling)
	}

	// 2. MTM_ATTACHMENTS_DIR overrides store path.
	envDir := filepath.Join(t.TempDir(), "env-attach")
	t.Setenv("MTM_ATTACHMENTS_DIR", envDir)
	if got := a.resolveAttachmentsDir(); got != envDir {
		t.Fatalf("resolveAttachmentsDir (MTM_ATTACHMENTS_DIR) = %q, want %q", got, envDir)
	}

	// 3. attachments.dir setting overrides MTM_ATTACHMENTS_DIR.
	cfgDir := filepath.Join(t.TempDir(), "cfg-attach")
	a.cfg.userSettings[SetAttachmentsDir] = cfgDir
	a.reloadSettings()
	if got := a.resolveAttachmentsDir(); got != cfgDir {
		t.Fatalf("resolveAttachmentsDir (attachments.dir) = %q, want %q", got, cfgDir)
	}

	// 4. In-memory store with Live=true falls back to $XDG_DATA_HOME/matomeru/attachments.
	t.Setenv("MTM_ATTACHMENTS_DIR", "")
	xdgDir := filepath.Join(t.TempDir(), "xdg-data")
	t.Setenv("XDG_DATA_HOME", xdgDir)
	memStore, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { memStore.Close() })
	liveApp, err := New(memStore, Options{Live: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { liveApp.Close() })
	wantXDG := filepath.Join(xdgDir, "matomeru", "attachments")
	if got := liveApp.resolveAttachmentsDir(); got != wantXDG {
		t.Fatalf("resolveAttachmentsDir (XDG_DATA_HOME) = %q, want %q", got, wantXDG)
	}

	// 5. opts.DataDir takes precedence over XDG_DATA_HOME when store is in-memory.
	dataDir := filepath.Join(t.TempDir(), "custom-data")
	dataApp, err := New(memStore, Options{DataDir: dataDir, Live: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dataApp.Close() })
	wantData := filepath.Join(dataDir, "attachments")
	if got := dataApp.resolveAttachmentsDir(); got != wantData {
		t.Fatalf("resolveAttachmentsDir (DataDir) = %q, want %q", got, wantData)
	}
}

func TestCaptureClipboardLinuxAndDarwinFinder(t *testing.T) {
	attachDir := filepath.Join(t.TempDir(), "attachments")
	fakePNG := append(append([]byte{}, pngMagicHeader...), []byte("linux-png-bytes")...)

	// 1. Linux wl-paste image/png capture writes directly into attachDir (no double attachments/attachments).
	att, text, err := captureClipboardToDirWith(attachDir, "20261126-073000-aa11bb", true, clipboardEnv{
		goos:     "linux",
		lookPath: func(file string) (string, error) { return "/usr/bin/" + file, nil },
		runCmd: func(name string, args ...string) ([]byte, error) {
			if name == "wl-paste" && len(args) >= 3 && args[2] == "image/png" {
				return fakePNG, nil
			}
			return nil, errors.New("unexpected: " + name)
		},
	})
	if err != nil || text != "" {
		t.Fatalf("wl-paste capture failed: att=%+v text=%q err=%v", att, text, err)
	}
	if att.Kind != "image" || att.Mime != "image/png" || filepath.Dir(att.Path) != attachDir {
		t.Fatalf("unexpected wl-paste attachment: %+v (attachDir=%q)", att, attachDir)
	}
	if strings.Contains(filepath.ToSlash(att.Path), "/attachments/attachments/") {
		t.Fatalf("unexpected double attachments dir: %q", att.Path)
	}
	gotBytes, err := os.ReadFile(att.Path)
	if err != nil || !bytes.Equal(gotBytes, fakePNG) {
		t.Fatalf("wl-paste PNG mismatch: err=%v", err)
	}

	// 2. Linux xclip fallback when wl-paste is unavailable (and rejects non-PNG magic).
	att2, text2, err := captureClipboardToDirWith(attachDir, "20261126-073001-cc22dd", true, clipboardEnv{
		goos: "linux",
		lookPath: func(file string) (string, error) {
			if file == "xclip" {
				return "/usr/bin/xclip", nil
			}
			return "", errors.New("not found")
		},
		runCmd: func(name string, args ...string) ([]byte, error) {
			if name == "xclip" {
				return fakePNG, nil
			}
			return nil, errors.New("unexpected")
		},
	})
	if err != nil || text2 != "" || att2.Kind != "image" || filepath.Base(att2.Path) != "20261126-073001-cc22dd.png" {
		t.Fatalf("xclip capture failed: att=%+v text=%q err=%v", att2, text2, err)
	}

	// 3. macOS Finder Cmd+C («class furl») returns file attachment referencing existing local file.
	finderFile := filepath.Join(t.TempDir(), "design-spec.png")
	if err := os.WriteFile(finderFile, fakePNG, 0o600); err != nil {
		t.Fatal(err)
	}
	att3, text3, err := captureClipboardToDirWith(attachDir, "", true, clipboardEnv{
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "", errors.New("no pngpaste") },
		runCmd: func(name string, args ...string) ([]byte, error) {
			if name == "osascript" {
				joined := strings.Join(args, " ")
				if strings.Contains(joined, "«class furl»") {
					return []byte(finderFile + "\n"), nil
				}
				return nil, errors.New("no image")
			}
			return nil, errors.New("unexpected: " + name)
		},
	})
	if err != nil || text3 != "" {
		t.Fatalf("darwin furl capture failed: att=%+v text=%q err=%v", att3, text3, err)
	}
	if att3.Kind != "image" || att3.Path != finderFile || att3.Label != "design-spec.png" {
		t.Fatalf("unexpected darwin furl attachment: %+v", att3)
	}

	// 4. macOS «class TIFF» fallback converted with sips -s format png.
	att4, text4, err := captureClipboardToDirWith(attachDir, "20261126-073002-ee33ff", true, clipboardEnv{
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "", errors.New("no pngpaste") },
		runCmd: func(name string, args ...string) ([]byte, error) {
			if name == "osascript" {
				joined := strings.Join(args, " ")
				if strings.Contains(joined, "«class TIFF»") {
					// Find POSIX file path and write fake TIFF bytes to it.
					for _, arg := range args {
						if idx := strings.Index(arg, `POSIX file "`); idx >= 0 {
							rest := arg[idx+len(`POSIX file "`):]
							if end := strings.IndexByte(rest, '"'); end > 0 {
								_ = os.WriteFile(rest[:end], []byte("MM\x00*fake-tiff"), 0o600)
							}
						}
					}
					return nil, nil
				}
				return nil, errors.New("no png")
			}
			if name == "sips" {
				// Write converted PNG to --out path.
				for i := range args {
					if args[i] == "--out" && i+1 < len(args) {
						_ = os.WriteFile(args[i+1], fakePNG, 0o600)
					}
				}
				return nil, nil
			}
			return nil, errors.New("unexpected: " + name)
		},
	})
	if err != nil || text4 != "" || att4.Kind != "image" || filepath.Base(att4.Path) != "20261126-073002-ee33ff.png" {
		t.Fatalf("darwin TIFF->sips fallback failed: att=%+v text=%q err=%v", att4, text4, err)
	}
}

func TestInputPasteClipboardUniversalAllModes(t *testing.T) {
	t.Setenv("MTM_ATTACHMENTS_DIR", "")
	t.Setenv("MTM_FILES_DIR", "")

	dbDir := t.TempDir()
	store, err := data.Open(filepath.Join(dbDir, "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	seed := core.Actor{Kind: "system", ID: "seed"}
	taskRec, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "task-paste",
		Fields: map[string]any{"title": "Paste target task"},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Create(data.CreateInput{
		Kind: data.KindPane,
		ID:   "pane-paste",
		Fields: map[string]any{
			"title":     "claude: paste",
			"harness":   "claude",
			"runStatus": "running",
			"task":      taskRec.Ref(),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	fixedNow := time.Date(2026, 11, 26, 7, 30, 0, 0, time.UTC)
	a, err := New(store, Options{
		FS:  smokeFS(),
		Now: func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	enableLegacyPagesForTest(a, SpacePanes)
	d := NewDriver(a, 120, 36)

	fakePNG := append(append([]byte{}, pngMagicHeader...), []byte("universal-png")...)
	origEnv := activeClipboardEnv
	origHook := captureClipboardFunc
	t.Cleanup(func() {
		activeClipboardEnv = origEnv
		captureClipboardFunc = origHook
	})
	captureClipboardFunc = nil

	modeImage := true
	plainPayload := "plain-paste-text"
	activeClipboardEnv = clipboardEnv{
		goos:     "darwin",
		lookPath: func(file string) (string, error) { return "/opt/homebrew/bin/" + file, nil },
		runCmd: func(name string, args ...string) ([]byte, error) {
			if modeImage {
				if name == "pngpaste" {
					return fakePNG, nil
				}
				return nil, errors.New("unexpected in image mode: " + name)
			}
			if name == "pbpaste" {
				return []byte(plainPayload), nil
			}
			return nil, errors.New("no image")
		},
	}

	wantDirPrefix := "@" + filepath.ToSlash(filepath.Join(dbDir, "attachments")) + "/20261126-073000-"

	// 1. ModeFormEdit: new task form -> ctrl+v image inserts @path, ctrl+v text pastes plain text.
	d.Keys("2", "n")
	if a.Mode() != ModeFormEdit {
		// Enter edit mode on title field if form started in normal mode.
		d.Keys("i")
	}
	if a.Mode() != ModeFormEdit {
		t.Fatalf("expected ModeFormEdit, got %q", a.Mode())
	}
	modeImage = true
	d.Keys("ctrl+v")
	rf := a.activeRecordForm()
	if rf == nil {
		t.Fatal("expected active record form")
	}
	titleVal := data.Text(rf.Model().TypedValues()["title"])
	if !strings.Contains(titleVal, wantDirPrefix) || !strings.Contains(titleVal, ".png") {
		t.Fatalf("ModeFormEdit image ctrl+v did not insert @path: %q", titleVal)
	}
	modeImage = false
	d.Keys("ctrl+v")
	titleVal = data.Text(rf.Model().TypedValues()["title"])
	if !strings.Contains(titleVal, plainPayload) {
		t.Fatalf("ModeFormEdit text ctrl+v did not paste plain text: %q", titleVal)
	}
	d.Keys("esc", "esc", "esc")

	// 2a. ModeOutlineEdit inline in 2 Work Tasks tree.
	d.Keys("2", "a")
	if a.Mode() != ModeOutlineEdit {
		t.Fatalf("expected ModeOutlineEdit in 2 Work, got %q (status=%q)", a.Mode(), a.Status())
	}
	modeImage = true
	d.Keys("ctrl+v")
	ed := a.trees.activeEdit()
	if ed == nil || !strings.Contains(ed.Value(), wantDirPrefix) {
		t.Fatalf("ModeOutlineEdit (2 Work) image ctrl+v failed: ed=%v val=%q", ed != nil, ed.Value())
	}
	modeImage = false
	d.Keys("ctrl+v")
	ed = a.trees.activeEdit()
	if ed == nil || !strings.Contains(ed.Value(), plainPayload) {
		t.Fatalf("ModeOutlineEdit (2 Work) text ctrl+v failed: val=%q mode=%q modal=%q status=%q", ed.Value(), a.Mode(), a.Modal(), a.Status())
	}
	d.Keys("ctrl+c")

	// 2b. ModeOutlineEdit inside T Outline modal.
	d.Keys("T", "a")
	if a.Modal() != "outline" || a.Mode() != ModeOutlineEdit {
		t.Fatalf("expected Outline modal ModeOutlineEdit, got modal=%q mode=%q", a.Modal(), a.Mode())
	}
	modeImage = true
	d.Keys("ctrl+v")
	ed = a.trees.activeEdit()
	if ed == nil || !strings.Contains(ed.Value(), wantDirPrefix) {
		t.Fatalf("ModeOutlineEdit (Outline modal) image ctrl+v failed: ed=%v val=%q", ed != nil, ed.Value())
	}
	modeImage = false
	d.Keys("ctrl+v")
	ed = a.trees.activeEdit()
	if ed == nil || !strings.Contains(ed.Value(), plainPayload) {
		t.Fatalf("ModeOutlineEdit (Outline modal) text ctrl+v failed: %q", ed.Value())
	}
	d.Keys("ctrl+c", "q")

	// 3. ModeComposer: ctrl+v image attaches chip AND inserts @path inline; text pastes plain text.
	d.Keys("1", "c")
	cm := a.activeComposer()
	if cm == nil || a.Mode() != ModeComposer {
		t.Fatalf("expected ModeComposer, got modal=%q mode=%q", a.Modal(), a.Mode())
	}
	modeImage = true
	d.Keys("ctrl+v")
	if len(cm.attachments) != 1 || !strings.Contains(cm.in.Value(), wantDirPrefix) {
		t.Fatalf("ModeComposer image ctrl+v failed: atts=%+v text=%q", cm.attachments, cm.in.Value())
	}
	modeImage = false
	d.Keys("ctrl+v")
	if !strings.Contains(cm.in.Value(), plainPayload) {
		t.Fatalf("ModeComposer text ctrl+v failed: %q", cm.in.Value())
	}
	d.Keys("esc", "esc")

	// 4. ModeFilter: `/` on 2 Work tree/list.
	d.Keys("2", "/")
	if a.Mode() != ModeFilter {
		t.Fatalf("expected ModeFilter, got %q", a.Mode())
	}
	modeImage = true
	d.Keys("ctrl+v")
	f, _ := a.activeFilterable(a.State())
	if f == nil || !strings.Contains(f.FilterQuery(), wantDirPrefix) {
		t.Fatalf("ModeFilter image ctrl+v failed: query=%q", f.FilterQuery())
	}
	modeImage = false
	d.Keys("ctrl+v")
	if !strings.Contains(f.FilterQuery(), plainPayload) {
		t.Fatalf("ModeFilter text ctrl+v failed: query=%q", f.FilterQuery())
	}
	d.Keys("esc")

	// 5. ModePickerFilter: `:` command palette.
	d.Keys(":")
	if a.Mode() != ModePickerFilter {
		d.Keys("/")
	}
	if a.Mode() != ModePickerFilter {
		t.Fatalf("expected ModePickerFilter, got modal=%q mode=%q", a.Modal(), a.Mode())
	}
	modeImage = true
	d.Keys("ctrl+v")
	pal, ok := a.modals.Top().(*modal.PaletteModal)
	if !ok || !strings.Contains(pal.Model().Picker.Query, wantDirPrefix) {
		t.Fatalf("ModePickerFilter image ctrl+v failed: top=%T query=%q", a.modals.Top(), pal.Model().Picker.Query)
	}
	modeImage = false
	d.Keys("ctrl+v")
	if !strings.Contains(pal.Model().Picker.Query, plainPayload) {
		t.Fatalf("ModePickerFilter text ctrl+v failed: query=%q", pal.Model().Picker.Query)
	}
	d.Keys("esc", "esc", "esc")

	// 6. ModeBindingsFilter: `B` then `/`.
	d.Keys("B", "/")
	if a.Mode() != ModeBindingsFilter {
		t.Fatalf("expected ModeBindingsFilter, got modal=%q mode=%q", a.Modal(), a.Mode())
	}
	modeImage = true
	d.Keys("ctrl+v")
	bm := a.activeBindings()
	if bm == nil || !strings.Contains(bm.line.Value(), wantDirPrefix) {
		t.Fatalf("ModeBindingsFilter image ctrl+v failed: query=%q", bm.line.Value())
	}
	modeImage = false
	d.Keys("ctrl+v")
	if !strings.Contains(bm.line.Value(), plainPayload) {
		t.Fatalf("ModeBindingsFilter text ctrl+v failed: query=%q", bm.line.Value())
	}
	d.Keys("esc", "esc", "esc")

	// 7. ModeCommandLine: `!`.
	d.Keys("!")
	if a.Mode() != ModeCommandLine {
		t.Fatalf("expected ModeCommandLine, got modal=%q mode=%q", a.Modal(), a.Mode())
	}
	modeImage = true
	d.Keys("ctrl+v")
	clm, ok := a.modals.Top().(*modal.CommandLineModal)
	if !ok || !strings.Contains(clm.Model().Value(), wantDirPrefix) {
		t.Fatalf("ModeCommandLine image ctrl+v failed: top=%T val=%q", a.modals.Top(), clm.Model().Value())
	}
	modeImage = false
	d.Keys("ctrl+v")
	if !strings.Contains(clm.Model().Value(), plainPayload) {
		t.Fatalf("ModeCommandLine text ctrl+v failed: val=%q", clm.Model().Value())
	}
	d.Keys("esc")
}

func TestClipboardAttachmentsOptOutSetting(t *testing.T) {
	t.Setenv("MTM_ATTACHMENTS_DIR", "")
	t.Setenv("MTM_FILES_DIR", "")

	dbDir := t.TempDir()
	store, err := data.Open(filepath.Join(dbDir, "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	seed := core.Actor{Kind: "system", ID: "seed"}
	_, _, err = store.Create(data.CreateInput{
		Kind:   data.KindPane,
		ID:     "pane-optout",
		Fields: map[string]any{"title": "claude: optout", "harness": "claude", "runStatus": "running"},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	a.cfg.userSettings[SetAttachmentsEnabled] = false
	a.reloadSettings()
	enableLegacyPagesForTest(a, SpacePanes)

	d := NewDriver(a, 120, 36)

	origEnv := activeClipboardEnv
	origHook := captureClipboardFunc
	t.Cleanup(func() {
		activeClipboardEnv = origEnv
		captureClipboardFunc = origHook
	})
	captureClipboardFunc = nil

	fakePNG := append(append([]byte{}, pngMagicHeader...), []byte("should-not-save")...)
	pngPasteCalled := false
	activeClipboardEnv = clipboardEnv{
		goos:     "darwin",
		lookPath: func(file string) (string, error) { return "/opt/homebrew/bin/" + file, nil },
		runCmd: func(name string, args ...string) ([]byte, error) {
			if name == "pngpaste" {
				pngPasteCalled = true
				return fakePNG, nil
			}
			if name == "pbpaste" {
				return []byte("plain-only-fallback"), nil
			}
			return nil, errors.New("unexpected: " + name)
		},
	}

	// Opt-out in ModeFormEdit: falls back to plain text, writes zero files.
	d.Keys("2", "n")
	if a.Mode() != ModeFormEdit {
		d.Keys("i")
	}
	d.Keys("ctrl+v")
	rf := a.activeRecordForm()
	if rf == nil || data.Text(rf.Model().TypedValues()["title"]) != "plain-only-fallback" {
		t.Fatalf("opt-out ModeFormEdit title = %q, want plain-only-fallback", data.Text(rf.Model().TypedValues()["title"]))
	}
	d.Keys("esc", "esc", "esc")

	// Opt-out in ModeComposer: pastes plain text, attaches zero chips.
	d.Keys("1", "c")
	cm := a.activeComposer()
	if cm == nil {
		t.Fatal("expected composer")
	}
	d.Keys("ctrl+v")
	if len(cm.attachments) != 0 || cm.in.Value() != "plain-only-fallback" {
		t.Fatalf("opt-out ModeComposer atts=%+v text=%q", cm.attachments, cm.in.Value())
	}
	if pngPasteCalled {
		t.Fatal("pngpaste should not be invoked when input.clipboardAttachments is false")
	}
	attachDir := filepath.Join(dbDir, "attachments")
	if entries, err := os.ReadDir(attachDir); err == nil && len(entries) > 0 {
		t.Fatalf("opt-out should not create attachment files, found %d", len(entries))
	}
}

func TestAtPrefixedAttachmentPathOpenTarget(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	attPath := "/Users/alice/.local/share/matomeru/attachments/20261126-073000-a1b2c3.png"
	rec := data.Record{
		Kind:   data.KindDocument,
		ID:     "doc-img",
		Fields: map[string]any{"title": "Screenshot", "filePath": "@" + attPath},
	}
	target, ok := a.targetOf(rec)
	if !ok {
		t.Fatal("targetOf should accept @-prefixed attachment path")
	}
	if target.path != attPath {
		t.Fatalf("target.path = %q, want %q", target.path, attPath)
	}
	if len(target.rules) == 0 || target.rules[0].Name != handlers.SystemOpen {
		t.Fatalf("target.rules = %+v, want SystemOpen", target.rules)
	}

	if got := contextTargetPath(contextitems.KindFile, "@"+attPath, nil); got != attPath {
		t.Fatalf("contextTargetPath(@path) = %q, want %q", got, attPath)
	}
}
