package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestClipboardAttachImageFileAndText(t *testing.T) {
	filesRoot := filepath.Join(t.TempDir(), "files")
	fakePNG := append(append([]byte{}, pngMagicHeader...), []byte("fake-png-body")...)

	// 1. macOS pngpaste image capture writes managed PNG under attachments/.
	att, text, err := captureClipboardAttachmentWith(filesRoot, "shot1", clipboardEnv{
		goos:     "darwin",
		lookPath: func(file string) (string, error) { return "/opt/homebrew/bin/" + file, nil },
		runCmd: func(name string, args ...string) ([]byte, error) {
			if name == "pngpaste" {
				return fakePNG, nil
			}
			return nil, errors.New("unexpected command: " + name)
		},
	})
	if err != nil {
		t.Fatalf("captureClipboardAttachmentWith pngpaste: %v", err)
	}
	if text != "" {
		t.Fatalf("expected empty plain text for PNG image, got %q", text)
	}
	if att.Kind != "image" || att.Mime != "image/png" || att.Source != "clipboard" {
		t.Fatalf("unexpected image attachment: %+v", att)
	}
	if !strings.Contains(filepath.ToSlash(att.Path), "/attachments/shot1-clipboard.png") {
		t.Fatalf("unexpected managed path: %q", att.Path)
	}
	gotBytes, err := os.ReadFile(att.Path)
	if err != nil || !bytes.Equal(gotBytes, fakePNG) {
		t.Fatalf("managed PNG bytes mismatch: err=%v len=%d", err, len(gotBytes))
	}

	// 2. macOS osascript fallback when pngpaste is unavailable.
	att2, text2, err := captureClipboardAttachmentWith(filesRoot, "shot2", clipboardEnv{
		goos: "darwin",
		lookPath: func(file string) (string, error) {
			return "", errors.New("not found")
		},
		runCmd: func(name string, args ...string) ([]byte, error) {
			if name == "osascript" {
				return fakePNG, nil
			}
			return nil, errors.New("unexpected command: " + name)
		},
	})
	if err != nil || text2 != "" || att2.Kind != "image" || !strings.HasSuffix(att2.Path, "shot2-clipboard.png") {
		t.Fatalf("osascript fallback failed: att=%+v text=%q err=%v", att2, text2, err)
	}

	// 3. pbpaste pointing at an existing local file path returns file Attachment.
	localFile := filepath.Join(t.TempDir(), "schema.go")
	if err := os.WriteFile(localFile, []byte("package data\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	att3, text3, err := captureClipboardAttachmentWith(filesRoot, "f1", clipboardEnv{
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "", errors.New("no pngpaste") },
		runCmd: func(name string, args ...string) ([]byte, error) {
			if name == "osascript" {
				return nil, errors.New("no image")
			}
			if name == "pbpaste" {
				return []byte("  " + localFile + "\n"), nil
			}
			return nil, errors.New("unexpected")
		},
	})
	if err != nil || text3 != "" {
		t.Fatalf("pbpaste file path failed: text=%q err=%v", text3, err)
	}
	if att3.Kind != "file" || att3.Path != localFile || att3.Label != "schema.go" || att3.Source != "clipboard" {
		t.Fatalf("unexpected file attachment from pbpaste: %+v", att3)
	}

	// 4. pbpaste plain text returns empty attachment + pasted text.
	att4, text4, err := captureClipboardAttachmentWith(filesRoot, "txt", clipboardEnv{
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "", errors.New("no pngpaste") },
		runCmd: func(name string, args ...string) ([]byte, error) {
			if name == "osascript" {
				return nil, errors.New("no image")
			}
			if name == "pbpaste" {
				return []byte("hello plain clipboard"), nil
			}
			return nil, errors.New("unexpected")
		},
	})
	if err != nil || att4.Path != "" || text4 != "hello plain clipboard" {
		t.Fatalf("pbpaste plain text failed: att=%+v text=%q err=%v", att4, text4, err)
	}

	// 5. attachmentFromPath handles images, regular files, missing files, and directories.
	imgFile := filepath.Join(t.TempDir(), "diagram.webp")
	if err := os.WriteFile(imgFile, []byte("RIFF"), 0o600); err != nil {
		t.Fatal(err)
	}
	imgAtt, err := attachmentFromPath(imgFile, "picker")
	if err != nil || imgAtt.Kind != "image" || imgAtt.Mime != "image/webp" || imgAtt.Label != "diagram.webp" || imgAtt.Source != "picker" {
		t.Fatalf("attachmentFromPath image = %+v err=%v", imgAtt, err)
	}
	if _, err := attachmentFromPath(filepath.Dir(imgFile), "picker"); err == nil {
		t.Fatal("attachmentFromPath should reject directories")
	}
	if _, err := attachmentFromPath(filepath.Join(t.TempDir(), "nope.txt"), "picker"); err == nil {
		t.Fatal("attachmentFromPath should reject missing files")
	}
}

func TestRichComposerTriggersAttachmentsAndNoteSubmit(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "alpha.go"), []byte("package alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	attachFile := filepath.Join(workDir, "mock.png")
	if err := os.WriteFile(attachFile, pngMagicHeader, 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}

	taskRec, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "task-comp",
		Fields: map[string]any{"title": "Wire rich composer", "defaultDir": workDir},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Create(data.CreateInput{
		Kind: data.KindContext,
		ID:   "ctx-pr",
		Fields: map[string]any{
			"title":       "PR 42 Auth Fix",
			"value":       "https://github.com/acme/app/pull/42",
			"contextKind": "pr",
			"task":        taskRec.Ref(),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	paneRec, _, err := store.Create(data.CreateInput{
		Kind: data.KindPane,
		ID:   "pane-comp",
		Fields: map[string]any{
			"title":     "claude: main",
			"harness":   "claude",
			"cwd":       workDir,
			"runStatus": "running",
			"task":      taskRec.Ref(),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	enableLegacyPagesForTest(a, SpacePanes)
	a.SetComposerSkills([]composerCompletion{
		{Trigger: "$", Label: "$tdd-loop", Insert: "tdd-loop", Detail: "Test-driven loop", Kind: "skill"},
	})
	d := NewDriver(a, 120, 36)

	// Inject deterministic clipboard hook for ctrl+v tests.
	origClip := captureClipboardFunc
	t.Cleanup(func() { captureClipboardFunc = origClip })
	clipStep := 0
	captureClipboardFunc = func(filesRoot, idPrefix string) (data.Attachment, string, error) {
		clipStep++
		if clipStep == 1 {
			return data.Attachment{
				Kind:   "image",
				Path:   attachFile,
				Label:  "mock.png",
				Mime:   "image/png",
				Source: "clipboard",
			}, "", nil
		}
		return data.Attachment{}, "pasted-snippet ", nil
	}

	// 1. Open pane composer, test @file, @ctx, @agent, $skill, /cmd triggers & attachment chips.
	d.Keys("c")
	a.WaitBackground()
	wake(d)
	cm := a.activeComposer()
	if cm == nil {
		t.Fatal("expected pane composer modal to be open")
	}

	// @file completion
	d.Type("@alp")
	if !cm.compOpen || !strings.Contains(d.Frame(), "@alpha.go") {
		t.Fatalf("expected @alpha.go completion, got items=%+v", cm.compItems)
	}
	d.Keys("tab")

	// @ctx completion
	d.Type("@ctx")
	if !cm.compOpen || !strings.Contains(d.Frame(), "@ctx:PR 42 Auth Fix") {
		t.Fatalf("expected @ctx:PR 42 Auth Fix completion, got items=%+v", cm.compItems)
	}
	d.Keys("tab")
	if !strings.Contains(cm.in.Value(), "@context:ctx-pr") {
		t.Fatalf("accepting @ctx completion did not insert @context:ctx-pr: %q", cm.in.Value())
	}

	// @agent completion (from configured harnesses)
	d.Type("@agent:cl")
	if !cm.compOpen || !strings.Contains(d.Frame(), "@agent:claude") {
		t.Fatalf("expected @agent:claude completion, got items=%+v", cm.compItems)
	}
	d.Keys("tab")

	// $skill completion
	d.Type("$tdd")
	if !cm.compOpen || !strings.Contains(d.Frame(), "$tdd-loop") {
		t.Fatalf("expected $tdd-loop completion, got items=%+v", cm.compItems)
	}
	d.Keys("tab")

	// /command completion
	d.Type("/rev")
	if !cm.compOpen || !strings.Contains(d.Frame(), "/review") {
		t.Fatalf("expected /review completion, got items=%+v", cm.compItems)
	}
	d.Keys("tab")

	// ctrl+v attaches image chip, second ctrl+v pastes plain text, ctrl+x removes chip, ctrl+v re-attaches.
	d.Keys("ctrl+v")
	if len(cm.attachments) != 1 || !strings.Contains(d.Frame(), "Attachments: [📎 mock.png]") {
		t.Fatalf("ctrl+v did not render attachment chip: atts=%+v\n%s", cm.attachments, d.Frame())
	}
	d.Keys("ctrl+v")
	if !strings.Contains(cm.in.Value(), "pasted-snippet") {
		t.Fatalf("second ctrl+v did not paste plain text: %q", cm.in.Value())
	}
	d.Keys("ctrl+x")
	if len(cm.attachments) != 0 {
		t.Fatalf("ctrl+x did not remove attachment: %+v", cm.attachments)
	}
	clipStep = 0
	d.Keys("ctrl+v")

	// Submit pane prompt -> delivered prompt appends compact [Attachments] section!
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("ctrl+s did not close pane composer: modal=%q", a.Modal())
	}
	lines := strings.Join(a.termOf(paneRec.ID).lines, "\n")
	if !strings.Contains(lines, "[Attachments]") || !strings.Contains(lines, attachFile) {
		t.Fatalf("delivered pane prompt missing [Attachments] section:\n%s", lines)
	}

	// 2. Open task note reply composer via `r` in 2 Work -> hides delivery bar, attaches file, saves KindNote.
	d.Keys("2", "r")
	if a.Modal() != taskNoteComposerModalID || a.Mode() != ModeComposer {
		t.Fatalf("r on task did not open note reply composer: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	noteFrame := d.Frame()
	if strings.Contains(noteFrame, "Delivery:") {
		t.Fatalf("task note reply composer should hide Delivery mode bar:\n%s", noteFrame)
	}
	clipStep = 0
	d.Keys("ctrl+v")
	d.Type("Please check the screenshot @alpha.go")
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("ctrl+s did not submit task note reply: modal=%q status=%q", a.Modal(), a.Status())
	}

	notes, err := store.TaskNotes(taskRec.ID)
	if err != nil || len(notes) != 1 {
		t.Fatalf("expected 1 persisted note, got %d (err=%v)", len(notes), err)
	}
	if notes[0].Text("authorKind") != "owner" || !strings.Contains(notes[0].Text("body"), "Please check the screenshot") {
		t.Fatalf("unexpected note fields: %+v", notes[0].Fields)
	}
	attsVal, _ := notes[0].Value("attachments")
	gotAtts := data.DecodeAttachments(attsVal)
	if len(gotAtts) != 1 || gotAtts[0].Path != attachFile {
		t.Fatalf("note attachments = %+v, want [%s]", gotAtts, attachFile)
	}
}
