package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/compose"
	"ofam.dev/matomeru/internal/harness"
)

// Attachments in the prompt box: ctrl+v in insert mode (turn-prompt.paste)
// reads the OS clipboard (clipboard.go), since a terminal paste carries
// only text:
//
//   - an image is saved as PNG under <data>/attachments/<pane>/<time>.png
//     and a chip [image N] goes in at the cursor;
//   - files copied in Finder: an image file is attached the same way (its
//     own path, not a copy), any other file goes in as @path, as the @
//     completion writes it;
//   - text is inserted (a plain paste); an empty or unreadable clipboard
//     is a status line, never a modal.
//
// The chip is the attachment: deleting its text detaches it (and undo
// brings it back), so the box's list is the attachments whose chip is in
// the text (promptBox.live). turn-prompt.detach removes one by its
// number, or picks among them.
//
// Sending replaces each chip by the harness's way to take a file
// (harness.Harness.Attach, harness data): an @ mention of the absolute
// path for Claude Code and omp, which they read at submit, in every mode;
// a harness that only attaches a pasted path (Codex) gets each path as a
// bracketed paste of its own before the text in send and interrupt mode.
// A queued prompt is typed later by tuios as one paste, so it always
// carries @ mentions, whatever the harness: never a refusal.
//
// The files stay after the send (they are context): each is kept as a
// file context item of the pane (context-item.add), so alt+o finds it.
// Removing old attachment files is a follow-up.

// promptAttachPause is the wait after pasting attachment paths, so the
// harness has attached them before the text arrives.
const promptAttachPause = 300 * time.Millisecond

// promptAttachment is one image represented by a chip in an input.
type promptAttachment struct {
	n    int    // its number, in the chip
	path string // absolute
}

// chip is the attachment's text in the box.
func (at promptAttachment) chip() string { return "[image " + strconv.Itoa(at.n) + "]" }

type imageAttachments []promptAttachment

func (b *promptBox) live() []promptAttachment {
	return b.attachments.live(string(b.input.Text))
}

// live preserves attachment order and keeps deleted chips available to undo.
func (images imageAttachments) live(text string) []promptAttachment {
	var out []promptAttachment
	for _, at := range images {
		if strings.Contains(text, at.chip()) {
			out = append(out, at)
		}
	}
	return out
}

// attach adds an image and returns its chip.
func (images *imageAttachments) attach(path, text string) promptAttachment {
	n := 1
	for _, at := range *images {
		n = max(n, at.n+1)
	}
	for strings.Contains(text, (promptAttachment{n: n}).chip()) {
		n++
	}
	at := promptAttachment{n: n, path: path}
	*images = append(*images, at)
	return at
}

// imageExts are the image files the harnesses attach (Claude Code's and
// omp's pasted-path test: png, jpg, jpeg, gif, webp).
var imageExts = []string{".png", ".jpg", ".jpeg", ".gif", ".webp"}

func isImagePath(path string) bool {
	return slices.Contains(imageExts, strings.ToLower(filepath.Ext(path)))
}

// fileMention quotes paths whose whitespace, quotes or backslashes need
// escaping, so a saved task can recover the exact attached file.
func fileMention(path string) string {
	if strings.ContainsFunc(path, unicode.IsSpace) || strings.ContainsAny(path, "\"\\") {
		return "@" + strconv.Quote(path)
	}
	return "@" + path
}

// promptOutgoing is the text sent for the box's text: each live chip
// replaced by its @ mention (spaced from its neighbours, as a mention
// must start a word), or with mentions false dropped (the paths go as
// pastes of their own).
func promptOutgoing(text string, live []promptAttachment, mentions bool) string {
	for _, at := range live {
		chip := at.chip()
		var b strings.Builder
		rest := text
		for {
			i := strings.Index(rest, chip)
			if i < 0 {
				b.WriteString(rest)
				break
			}
			b.WriteString(rest[:i])
			rest = rest[i+len(chip):]
			if !mentions {
				continue
			}
			if s := b.String(); s != "" && !unicode.IsSpace(rune(s[len(s)-1])) {
				b.WriteByte(' ')
			}
			b.WriteString(fileMention(at.path))
			if rest != "" && !unicode.IsSpace(rune(rest[0])) {
				b.WriteByte(' ')
			}
		}
		text = b.String()
	}
	return text
}

// attachMode is how the pane's harness takes files: its registry entry's
// attach mode (compose's name for the tuios harness, else the launch's),
// else mentions.
func (a *App) attachMode(p pane) string {
	if a.harnesses == nil {
		return harness.AttachMention
	}
	for _, name := range []string{compose.Harness(p.Harness), p.LaunchHarness} {
		if h, ok := a.harnesses.Get(name); ok && name != "" {
			return h.AttachMode()
		}
	}
	return harness.AttachMention
}

// attachmentDir keeps pasted images beside the data, grouped by their owner.
func (a *App) attachmentDir(owner string) string {
	base := filepath.Dir(a.dataPath)
	if a.dataPath == "" {
		base = filepath.Join(os.TempDir(), "matomeru")
	}
	id := strings.Map(func(r rune) rune {
		if r == '/' || r == os.PathSeparator || unicode.IsSpace(r) {
			return '_'
		}
		return r
	}, cmp.Or(owner, "pane"))
	return filepath.Join(base, "attachments", id)
}

// clipboardRead is a clipboard read's outcome, delivered to the loop.
type clipboardRead struct {
	got clipboardContent
	err error
}

func readClipboard(clip clipboardReader, path string) ochazuke.Result {
	return ochazuke.Pending(func(c context.Context) (any, error) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return clipboardRead{err: err}, nil
		}
		got, err := clip.Read(c, path)
		return clipboardRead{got: got, err: err}, nil
	}).Label("clipboard")
}

// pastePrompt is turn-prompt.paste (ctrl+v in insert mode): read the
// clipboard off the loop, then attach or insert what it held.
func (a *App) pastePrompt(ochazuke.Ctx) ochazuke.Result {
	p := a.promptPane()
	if p == nil {
		return ochazuke.Failed(errNoPromptBox)
	}
	clip := a.pb.clip
	if clip == nil {
		return ochazuke.Failed(errors.New("no clipboard reader"))
	}
	dir := a.attachmentDir(p.ID)
	path := filepath.Join(dir, time.Now().UTC().Format("20060102T150405.000000")+".png")
	// Where ctrl+v was pressed: a read that finishes after the person left
	// the box (esc steps the cursor back) still lands there, while the
	// draft is unchanged.
	at, was := a.pb.input.Cursor, string(a.pb.input.Text)
	return readClipboard(clip, path).Then(func(ctx ochazuke.Ctx, v any, err error) {
		read, _ := v.(clipboardRead)
		if err == nil {
			err = read.err
		}
		if err != nil {
			ctx.Error("could not read the clipboard: " + oneLine(err.Error()))
			return
		}
		text, notice := a.pb.attachments.pastedText(read.got, string(a.pb.input.Text))
		if text == "" {
			ctx.Info(notice)
			return
		}
		if in := a.pb.input; a.r.Focus != promptID && string(in.Text) == was {
			in.Cursor = at
		}
		if read.got.Text == "" {
			text = a.spacedAtCursor(text)
		}
		if err := a.pasteIntoPrompt(text); err != nil {
			ctx.Error(err.Error())
			return
		}
		// An attach is a success: a notice, never the error line.
		if notice != "" {
			ctx.Notify(notice)
		}
	})
}

// pasteIntoPrompt writes a finished clipboard read into the box's draft.
// In the box, it is insertIntoPrompt. When the person moved the focus
// elsewhere while the read ran, the text goes in at the draft's cursor and
// the focus and mode stay where they put them (F10): a late result never
// pulls the focus back.
func (a *App) pasteIntoPrompt(text string) error {
	if a.r.Focus == promptID {
		return a.insertIntoPrompt(text)
	}
	in := a.pb.input
	at := min(max(0, in.Cursor), len(in.Text))
	return in.Replace(at, at, text)
}

// pastedText is what a clipboard read writes into the box, attaching its
// images, and the notice that says so; with no text, the notice says why
// nothing was pasted. A saved image is named by its file only: the
// attachments folder is long and always the same.
func (images *imageAttachments) pastedText(got clipboardContent, text string) (string, string) {
	switch {
	case len(got.Files) > 0:
		var parts, chips []string
		for _, f := range got.Files {
			if isImagePath(f) {
				at := images.attach(f, text)
				parts, chips = append(parts, at.chip()), append(chips, at.chip())
			} else {
				parts = append(parts, fileMention(f))
			}
		}
		status := ""
		if len(chips) > 0 {
			status = "attached " + strings.Join(chips, " ")
		}
		return strings.Join(parts, " "), status
	case got.Image != "":
		at := images.attach(got.Image, text)
		return at.chip(), fmt.Sprintf("attached %s (%s)", at.chip(), filepath.Base(got.Image))
	case got.Text != "":
		return got.Text, ""
	}
	return "", "the clipboard is empty"
}

// spacedAtCursor is a chip or mention as written at the box's cursor: a
// space before it unless it starts a word, and one after it unless one
// follows, so the cursor ends past the space and typing goes on.
func (a *App) spacedAtCursor(text string) string {
	in := a.pb.input
	return spacedAtRange(in, text, in.Cursor, in.Cursor)
}

func spacedAtRange(in *ochazuke.Input, text string, start, end int) string {
	start = min(max(0, start), len(in.Text))
	end = min(max(start, end), len(in.Text))
	if start > 0 && !unicode.IsSpace(in.Text[start-1]) {
		text = " " + text
	}
	if end >= len(in.Text) || !unicode.IsSpace(in.Text[end]) {
		text += " "
	}
	return text
}

// detachPrompt is turn-prompt.detach: remove an attachment's chip (and
// so the attachment): the one numbered n, the only one, else a chooser
// of them.
func (a *App) detachPrompt(ctx ochazuke.Ctx) ochazuke.Result {
	live := a.pb.live()
	if len(live) == 0 {
		return ochazuke.Failed(errors.New("the prompt has no attachments"))
	}
	if n := ochazuke.IntArg(ctx.Args, "n", 0); n > 0 {
		for _, at := range live {
			if at.n == n {
				return a.detach(at)
			}
		}
		return ochazuke.Failed(fmt.Errorf("no image %d in the prompt", n))
	}
	if len(live) == 1 {
		return a.detach(live[0])
	}
	rows := make([]ochazuke.ChooserRow, len(live))
	for i, at := range live {
		rows[i] = ochazuke.ChooserRow{ID: at.chip(), Label: at.chip() + " " + at.path, Data: at, Cells: []string{at.chip(), at.path}}
	}
	_, err := a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind:  "prompt-attachments",
		Title: "Detach an attachment",
		Rows:  rows,
		Choose: func(row ochazuke.ChooserRow) ochazuke.Result {
			at, _ := row.Data.(promptAttachment)
			return a.detach(at)
		},
	})
	return resultError(err)
}

// detach removes every copy of the attachment's chip from the box, which
// keeps the focus in insert mode.
func (a *App) detach(at promptAttachment) ochazuke.Result {
	in := a.pb.input
	if err := removeImageChip(in, at); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.insertIntoPrompt(""); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done("detached " + at.chip())
}

func removeImageChip(in *ochazuke.Input, at promptAttachment) error {
	chip := []rune(at.chip())
	for {
		i := runeIndex(in.Text, chip)
		if i < 0 {
			break
		}
		end := i + len(chip)
		if end < len(in.Text) && in.Text[end] == ' ' {
			end++
		}
		if err := in.Replace(i, end, ""); err != nil {
			return err
		}
	}
	return nil
}

func runeIndex(text, sub []rune) int {
	for i := 0; i+len(sub) <= len(text); i++ {
		if slices.Equal(text[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// recordAttachments keeps each sent attachment as a file context item of
// the pane, so alt+o (and the context page) find it later.
func (a *App) recordAttachments(ctx ochazuke.Ctx, p pane, sent []promptAttachment) {
	for _, at := range sent {
		args := ochazuke.Arguments{"text": at.path, "kind": "file", "session": p.Session, "window": p.ID}
		if _, err := a.r.Execute(ochazuke.Request{Verb: "add", Noun: contextNoun, Args: args}); err != nil {
			ctx.Error(err.Error())
		}
	}
}

// attachmentsLabel is the rule's count of the box's attachments: "2
// images", "" for none.
func attachmentsLabel(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "1 image"
	}
	return strconv.Itoa(n) + " images"
}
