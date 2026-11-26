package app

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/commandline"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/toast"
)

const CmdInputPasteClipboard = "input.paste.clipboard"

type clipboardEnv struct {
	goos     string
	lookPath func(file string) (string, error)
	runCmd   func(name string, args ...string) ([]byte, error)
}

var defaultClipboardEnv = clipboardEnv{
	goos:     runtime.GOOS,
	lookPath: exec.LookPath,
	runCmd: func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).Output()
	},
}

// activeClipboardEnv is used when captureClipboardFunc is not overridden so
// tests can simulate OS clipboard tools end-to-end without touching the OS.
var activeClipboardEnv = defaultClipboardEnv

// captureClipboardFunc is an optional test hook used on ctrl+v so tests can
// inject deterministic clipboard payloads without touching the OS clipboard.
var captureClipboardFunc func(attachDir, idPrefix string) (data.Attachment, string, error)

var pngMagicHeader = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

func clipboardAttachSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdInputPasteClipboard,
			Description: "Paste clipboard text or save clipboard image/file to the attachments directory as @path",
			Meta:        map[string]any{"internal": true},
		},
	}
}

func clipboardAttachBindings() []core.Binding {
	modes := []string{
		ModeFormEdit,
		ModeOutlineEdit,
		ModeFeaturesNote,
		ModeThreadsCompose,
		ModeFilter,
		ModePickerFilter,
		ModeBindingsFilter,
		ModeCommandLine,
	}
	out := make([]core.Binding, 0, len(modes))
	for _, mode := range modes {
		out = append(out, bind(mode, "", "ctrl+v", CmdInputPasteClipboard, "paste or attach clipboard"))
	}
	return out
}

func (a *App) registerClipboardAttachHandlers() {
	a.handle(CmdInputPasteClipboard, a.inputPasteClipboard)
}

func (a *App) clipboardAttachmentsEnabled() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return true
	}
	return a.cfg.Settings.Bool(SetAttachmentsEnabled, true)
}

// resolveAttachmentsDir returns the common directory where pasted clipboard
// attachments are stored, honoring explicit settings and env overrides before
// falling back to the store/XDG data directory.
func (a *App) resolveAttachmentsDir() string {
	if a != nil && a.cfg != nil && a.cfg.Settings != nil {
		if v := strings.TrimSpace(a.cfg.Settings.String(SetAttachmentsDir, "")); v != "" {
			return filepath.Clean(expandHome(v))
		}
	}
	if v := strings.TrimSpace(os.Getenv("MTM_ATTACHMENTS_DIR")); v != "" {
		return filepath.Clean(expandHome(v))
	}
	if a != nil && a.Store != nil {
		if root := strings.TrimSpace(a.Store.FilesRoot()); root != "" {
			clean := filepath.Clean(root)
			if filepath.Base(clean) == "files" {
				return filepath.Join(filepath.Dir(clean), data.CategoryAttachments)
			}
			return filepath.Join(clean, data.CategoryAttachments)
		}
	}
	if a != nil {
		if v := strings.TrimSpace(a.opts.DataDir); v != "" {
			return filepath.Join(filepath.Clean(expandHome(v)), data.CategoryAttachments)
		}
		if a.opts.Live {
			if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
				return filepath.Join(filepath.Clean(expandHome(xdg)), "matomeru", data.CategoryAttachments)
			}
			if home, err := os.UserHomeDir(); err == nil && home != "" {
				return filepath.Join(home, ".local", "share", "matomeru", data.CategoryAttachments)
			}
		}
	}
	return filepath.Join(os.TempDir(), "matomeru-attachments")
}

// formatAttachmentRef formats an absolute attachment path using the @path convention.
func formatAttachmentRef(absPath string) string {
	p := strings.TrimSpace(absPath)
	if p == "" {
		return ""
	}
	return "@" + filepath.ToSlash(p)
}

// cleanLocalTargetPath trims whitespace and surrounding quotes, strips an
// optional leading @ reference prefix, and expands a leading ~.
func cleanLocalTargetPath(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, `"'`)
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "@")
	if s == "" {
		return ""
	}
	return expandHome(s)
}

func newAttachmentPrefix(now time.Time) string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err == nil {
		return fmt.Sprintf("%s-%s", now.UTC().Format("20060102-150405"), hex.EncodeToString(b[:]))
	}
	return fmt.Sprintf("%s-%06x", now.UTC().Format("20060102-150405"), now.Nanosecond()&0xffffff)
}

func safeAttachmentStem(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// writeAttachmentBytes writes content atomically directly into dir using a
// clean sortable filename (YYYYMMDD-HHMMSS-<short>.<ext> when idPrefix is
// empty) with 0o755 directory and 0o644 file permissions.
func writeAttachmentBytes(dir, idPrefix, ext string, content []byte) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "matomeru-attachments")
	}
	absDir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return "", fmt.Errorf("resolve attachments dir %q: %w", dir, err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return "", fmt.Errorf("create attachments dir %q: %w", absDir, err)
	}

	ext = strings.TrimSpace(ext)
	if ext == "" {
		ext = ".bin"
	} else if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}

	stem := safeAttachmentStem(idPrefix)
	if stem == "" {
		stem = newAttachmentPrefix(time.Now())
	}
	target := filepath.Join(absDir, stem+ext)

	tmp, err := os.CreateTemp(absDir, ".mtm-attach-*")
	if err != nil {
		return "", fmt.Errorf("create temp attachment in %q: %w", absDir, err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write attachment %q: %w", tmpPath, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("chmod attachment %q: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close attachment %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return "", fmt.Errorf("rename attachment %q: %w", target, err)
	}
	cleanup = false
	return target, nil
}

// captureClipboardAttachment inspects the system clipboard and writes any
// captured image into <filesRoot>/attachments/ (preserving legacy caller layout).
func captureClipboardAttachment(filesRoot, idPrefix string) (data.Attachment, string, error) {
	return captureClipboardAttachmentWith(filesRoot, idPrefix, activeClipboardEnv)
}

func captureClipboardAttachmentWith(filesRoot, idPrefix string, env clipboardEnv) (data.Attachment, string, error) {
	root := strings.TrimSpace(filesRoot)
	attachDir := filepath.Join(os.TempDir(), "matomeru-attachments")
	if root != "" {
		attachDir = filepath.Join(root, data.CategoryAttachments)
	}
	prefix := strings.TrimSpace(idPrefix)
	if prefix != "" {
		prefix += "-clipboard"
	}
	return captureClipboardToDirWith(attachDir, prefix, true, env)
}

// captureClipboardToDirWith inspects the system clipboard and either:
//  1. When allowAttach is true and an image is on the clipboard, saves PNG bytes
//     atomically into attachDir and returns an image Attachment.
//  2. When allowAttach is true and a local file (macOS Finder file URL or
//     single-line file path) is on the clipboard, returns a file/image Attachment.
//  3. Otherwise returns plain clipboard text so ctrl+v never breaks text paste.
func captureClipboardToDirWith(attachDir, idPrefix string, allowAttach bool, env clipboardEnv) (data.Attachment, string, error) {
	if env.goos == "" {
		env.goos = runtime.GOOS
	}
	if env.lookPath == nil {
		env.lookPath = exec.LookPath
	}
	if env.runCmd == nil {
		env.runCmd = defaultClipboardEnv.runCmd
	}

	if allowAttach {
		if pngBytes, ok := readClipboardPNG(env); ok && len(pngBytes) > 0 {
			savedPath, err := writeAttachmentBytes(attachDir, idPrefix, ".png", pngBytes)
			if err != nil {
				return data.Attachment{}, "", err
			}
			return data.Attachment{
				Kind:   "image",
				Path:   savedPath,
				Label:  filepath.Base(savedPath),
				Mime:   "image/png",
				Source: "clipboard",
			}, "", nil
		}
		if env.goos == "darwin" {
			if att, ok := readDarwinClipboardFile(env); ok {
				return att, "", nil
			}
		}
	}

	raw, err := readClipboardText(env)
	if err != nil {
		return data.Attachment{}, "", err
	}
	if allowAttach {
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" && !strings.ContainsAny(trimmed, "\r\n") {
			candidate := cleanLocalTargetPath(trimmed)
			if candidate != "" {
				if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
					if abs, absErr := filepath.Abs(candidate); absErr == nil {
						kind, mimeType := attachmentKindAndMime(abs)
						return data.Attachment{
							Kind:   kind,
							Path:   abs,
							Label:  filepath.Base(abs),
							Mime:   mimeType,
							Source: "clipboard",
						}, "", nil
					}
				}
			}
		}
	}
	return data.Attachment{}, raw, nil
}

func readClipboardPNG(env clipboardEnv) ([]byte, bool) {
	switch env.goos {
	case "darwin":
		return readDarwinClipboardPNG(env)
	case "linux":
		return readLinuxClipboardPNG(env)
	default:
		return nil, false
	}
}

func readLinuxClipboardPNG(env clipboardEnv) ([]byte, bool) {
	if _, err := env.lookPath("wl-paste"); err == nil {
		if out, err := env.runCmd("wl-paste", "--no-newline", "--type", "image/png"); err == nil && bytes.HasPrefix(out, pngMagicHeader) {
			return out, true
		}
	}
	if _, err := env.lookPath("xclip"); err == nil {
		if out, err := env.runCmd("xclip", "-selection", "clipboard", "-t", "image/png", "-o"); err == nil && bytes.HasPrefix(out, pngMagicHeader) {
			return out, true
		}
	}
	return nil, false
}

func readDarwinClipboardPNG(env clipboardEnv) ([]byte, bool) {
	if _, err := env.lookPath("pngpaste"); err == nil {
		if out, err := env.runCmd("pngpaste", "-"); err == nil && len(out) > 0 {
			return out, true
		}
	}
	if pngBytes, ok := readDarwinClipboardClass(env, "«class PNGf»", ".png"); ok {
		return pngBytes, true
	}
	if tiffBytes, ok := readDarwinClipboardClass(env, "«class TIFF»", ".tiff"); ok {
		if converted, ok := convertDarwinTIFFToPNG(env, tiffBytes); ok {
			return converted, true
		}
	}
	return nil, false
}

func readDarwinClipboardClass(env clipboardEnv, classExpr, ext string) ([]byte, bool) {
	tmp, err := os.CreateTemp("", "mtm-clip-*"+ext)
	if err != nil {
		return nil, false
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	escaped := strings.ReplaceAll(tmpPath, `"`, `\"`)
	out, err := env.runCmd(
		"osascript",
		"-e", "try",
		"-e", "set clipData to the clipboard as "+classExpr,
		"-e", fmt.Sprintf(`set fp to open for access POSIX file "%s" with write permission`, escaped),
		"-e", "set eof fp to 0",
		"-e", "write clipData to fp",
		"-e", "close access fp",
		"-e", "on error",
		"-e", "try",
		"-e", fmt.Sprintf(`close access POSIX file "%s"`, escaped),
		"-e", "end try",
		"-e", "end try",
	)
	if err != nil {
		return nil, false
	}
	if b, rerr := os.ReadFile(tmpPath); rerr == nil && len(b) > 0 {
		return b, true
	}
	if ext == ".png" && bytes.HasPrefix(out, pngMagicHeader) {
		return out, true
	}
	return nil, false
}

func convertDarwinTIFFToPNG(env clipboardEnv, tiffBytes []byte) ([]byte, bool) {
	if len(tiffBytes) == 0 {
		return nil, false
	}
	if bytes.HasPrefix(tiffBytes, pngMagicHeader) {
		return tiffBytes, true
	}
	tmpIn, err := os.CreateTemp("", "mtm-clip-in-*.tiff")
	if err != nil {
		return nil, false
	}
	inPath := tmpIn.Name()
	defer os.Remove(inPath)
	if _, err := tmpIn.Write(tiffBytes); err != nil {
		_ = tmpIn.Close()
		return nil, false
	}
	_ = tmpIn.Close()

	tmpOut, err := os.CreateTemp("", "mtm-clip-out-*.png")
	if err != nil {
		return nil, false
	}
	outPath := tmpOut.Name()
	_ = tmpOut.Close()
	defer os.Remove(outPath)

	out, err := env.runCmd("sips", "-s", "format", "png", inPath, "--out", outPath)
	if err != nil {
		return nil, false
	}
	if b, rerr := os.ReadFile(outPath); rerr == nil && bytes.HasPrefix(b, pngMagicHeader) {
		return b, true
	}
	if bytes.HasPrefix(out, pngMagicHeader) {
		return out, true
	}
	return nil, false
}

func readDarwinClipboardFile(env clipboardEnv) (data.Attachment, bool) {
	out, err := env.runCmd(
		"osascript",
		"-e", "try",
		"-e", "POSIX path of (the clipboard as «class furl»)",
		"-e", "end try",
	)
	if err != nil {
		return data.Attachment{}, false
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || strings.ContainsAny(trimmed, "\r\n") {
		return data.Attachment{}, false
	}
	att, err := attachmentFromPath(trimmed, "clipboard")
	if err != nil {
		return data.Attachment{}, false
	}
	return att, true
}

func readClipboardText(env clipboardEnv) (string, error) {
	if env.goos == "darwin" {
		out, err := env.runCmd("pbpaste")
		if err != nil {
			return "", fmt.Errorf("read clipboard: %w", err)
		}
		return string(out), nil
	}
	for _, cmd := range [][]string{
		{"pbpaste"},
		{"wl-paste", "-n"},
		{"xclip", "-selection", "clipboard", "-o"},
		{"xsel", "--clipboard", "--output"},
	} {
		if _, err := env.lookPath(cmd[0]); err != nil {
			continue
		}
		out, err := env.runCmd(cmd[0], cmd[1:]...)
		if err == nil {
			return string(out), nil
		}
	}
	return "", errors.New("no clipboard command available")
}

// captureClipboard runs the clipboard capture pipeline for a live UI paste,
// honoring input.clipboardAttachments and the captureClipboardFunc test hook.
func (a *App) captureClipboard(idPrefix string) (data.Attachment, string, error) {
	attachDir := a.resolveAttachmentsDir()
	allowAttach := a.clipboardAttachmentsEnabled()
	if captureClipboardFunc != nil {
		att, text, err := captureClipboardFunc(attachDir, idPrefix)
		if err != nil {
			return data.Attachment{}, "", err
		}
		if !allowAttach && (att.Path != "" || att.Ref != "") {
			return data.Attachment{}, text, nil
		}
		return att, text, nil
	}
	return captureClipboardToDirWith(attachDir, idPrefix, allowAttach, activeClipboardEnv)
}

func (a *App) inputPasteClipboard(s core.State, _ core.Command) core.StepResult {
	idPrefix := newAttachmentPrefix(a.now())
	att, text, err := a.captureClipboard(idPrefix)
	if err != nil {
		a.say("clipboard: %s", err.Error())
		return core.Next(s)
	}
	if att.Path != "" || att.Ref != "" {
		ref := formatAttachmentRef(att.Path)
		if ref == "" {
			ref = att.Ref
		}
		insertText := ref
		switch a.Engine.Mode(s) {
		case ModeFormEdit, ModeOutlineEdit, ModeFeaturesNote, ModeThreadsCompose, ModeCommandLine, ModeComposer:
			insertText = ref + " "
		}
		nextState, _, ok := a.insertTextIntoActiveInput(s, insertText)
		if !ok {
			a.say("no active text input for attachment")
			return core.Next(s)
		}
		msg := "attached " + att.Label + " (@path inserted)"
		a.say("%s", msg)
		a.pushToast(msg, toast.Ok)
		return core.Next(nextState)
	}
	if text != "" {
		nextState, _, ok := a.insertTextIntoActiveInput(s, text)
		if !ok {
			a.say("no active text input")
			return core.Next(s)
		}
		a.say("pasted clipboard text")
		return core.Next(nextState)
	}
	a.say("clipboard is empty")
	return core.Next(s)
}

// insertTextIntoActiveInput inserts text into whichever modal or inline text
// input currently holds editing focus.
func (a *App) insertTextIntoActiveInput(s core.State, text string) (core.State, string, bool) {
	if a == nil || text == "" {
		return s, "", false
	}
	mode := a.Engine.Mode(s)

	if mode == ModeOutlineEdit {
		if _, _, ok := a.activeEditTree(); ok {
			res := a.outlineEditInsert(s, core.Command{ID: CmdOutlineEditInsert, Args: map[string]any{"text": text}})
			return res.State, "outline", true
		}
	}
	if mode == ModeFeaturesNote {
		a.featuresNoteInsertText(text)
		return s, "features note", true
	}
	if mode == ModeThreadsCompose {
		a.threadsComposeInsertText(text)
		return s, "thread reply", true
	}
	if mode == ModeFilter {
		if f, _ := a.activeFilterable(s); f != nil {
			res := a.filterEdit(s, core.Command{ID: CmdFilterInsert, Args: map[string]any{"text": text}})
			return res.State, "filter", true
		}
	}

	if a.modals == nil {
		return s, "", false
	}
	top := a.modals.Top()
	if top == nil {
		return s, "", false
	}
	insertVia := func(cmdID, label string) (core.State, string, bool) {
		res := top.Handle(core.Command{ID: cmdID, Args: map[string]any{"text": text}})
		if !res.Handled {
			return s, "", false
		}
		return a.applyModalResult(s, res).State, label, true
	}

	if cm, ok := top.(*richComposerModal); ok {
		return insertVia(CmdComposerInsert, cm.spec.InputTitle)
	}
	if custom, ok := top.(hintInsertable); ok {
		if label, inserted := custom.InsertHintText(text); inserted {
			return s, label, true
		}
	}
	if palModal, ok := top.(*modal.PaletteModal); ok {
		if pal := palModal.Model(); pal != nil {
			if pal.FocusForm && pal.ActiveForm != nil && pal.ActiveForm.Editable() {
				if next, label, handled := insertVia(form.CmdInsert, "form"); handled {
					return next, label, true
				}
				pal.ActiveForm.HandleCommand(core.Command{ID: form.CmdInsert, Args: map[string]any{"text": text}})
				return s, "form", true
			}
			if !pal.FocusForm && pal.Picker != nil && pal.Picker.Filtering {
				return insertVia(CmdPickerInsert, "picker")
			}
		}
		return s, "", false
	}
	if fm := formModelOfModal(top); fm != nil {
		if fm.Editable() {
			return insertVia(form.CmdInsert, "form")
		}
		return s, "", false
	}
	switch top.(type) {
	case *modal.PickerModal, *modal.PathPickerModal:
		return insertVia(CmdPickerInsert, "picker")
	case *modal.CommandLineModal:
		return insertVia(commandline.CmdInsert, "command line")
	case *bindingsModal:
		return insertVia("bindings.filter.append", "bindings filter")
	}
	if mode == ModeFormEdit {
		return insertVia(form.CmdInsert, "form")
	}
	return s, "", false
}

// attachmentFromPath validates that rawPath exists and is a regular file (not a
// directory), resolving its absolute path, kind ("image" or "file"), MIME type,
// and display label.
func attachmentFromPath(rawPath, source string) (data.Attachment, error) {
	cleaned := cleanLocalTargetPath(rawPath)
	if cleaned == "" {
		return data.Attachment{}, errors.New("attachment path is empty")
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return data.Attachment{}, fmt.Errorf("resolve attachment path %q: %w", rawPath, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return data.Attachment{}, fmt.Errorf("stat attachment %q: %w", abs, err)
	}
	if info.IsDir() {
		return data.Attachment{}, fmt.Errorf("cannot attach directory %q", abs)
	}
	kind, mimeType := attachmentKindAndMime(abs)
	return data.Attachment{
		Kind:   kind,
		Path:   abs,
		Label:  filepath.Base(abs),
		Mime:   mimeType,
		Source: strings.TrimSpace(source),
	}, nil
}

func attachmentKindAndMime(path string) (kind, mimeType string) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image", "image/png"
	case ".jpg", ".jpeg":
		return "image", "image/jpeg"
	case ".gif":
		return "image", "image/gif"
	case ".webp":
		return "image", "image/webp"
	case ".svg":
		return "image", "image/svg+xml"
	default:
		mt := mime.TypeByExtension(ext)
		if semi := strings.IndexByte(mt, ';'); semi >= 0 {
			mt = strings.TrimSpace(mt[:semi])
		}
		return "file", mt
	}
}
