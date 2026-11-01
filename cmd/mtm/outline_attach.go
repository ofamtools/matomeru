package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"ofam.dev/ochazuke"
)

const settingTaskImages = "tasks.image-attachments"

// Saved titles use the prompt's @file representation; only absolute image
// mentions become chips. The per-outline map also retains deleted chips for undo.
var imageMention = regexp.MustCompile(`@"(?:\\.|[^"\\])*"|@[^\s]+`)

func (o *outlineState) taskImages(id string) *imageAttachments {
	images := o.attachments[id]
	if images == nil {
		images = new(imageAttachments)
		o.attachments[id] = images
	}
	return images
}

func (o *outlineState) imageLabel(id, title string) string {
	if !o.images {
		return title
	}
	var out strings.Builder
	from := 0
	for _, span := range imageMention.FindAllStringIndex(title, -1) {
		start, end := span[0], span[1]
		if start > 0 {
			before, _ := utf8.DecodeLastRuneInString(title[:start])
			if !unicode.IsSpace(before) {
				continue
			}
		}
		if end < len(title) {
			after, _ := utf8.DecodeRuneInString(title[end:])
			if !unicode.IsSpace(after) {
				continue
			}
		}
		path := strings.TrimPrefix(title[start:end], "@")
		if strings.HasPrefix(path, `"`) && strings.HasSuffix(path, `"`) {
			var err error
			path, err = strconv.Unquote(path)
			if err != nil {
				continue
			}
		}
		if !filepath.IsAbs(path) || !isImagePath(path) {
			continue
		}
		images := o.taskImages(id)
		var at promptAttachment
		for _, known := range *images {
			if known.path == path {
				at = known
				break
			}
		}
		if at.n == 0 {
			at = images.attach(path, title)
		}
		out.WriteString(title[from:start])
		out.WriteString(at.chip())
		from = end
	}
	if from == 0 {
		return title
	}
	out.WriteString(title[from:])
	return out.String()
}

func (a *App) defineOutlineAttachments() {
	a.r.DefineGuard("task-image-input", func(*ochazuke.Runtime, ochazuke.KeyEvent) bool {
		_, in := a.outlineImageInput()
		return in != nil
	})
	a.r.Define(outlineModal).
		Verb("paste", a.pasteTaskImage, "Paste into the task being edited: images as [image N], other files as @path, text as text").When("task-image-input").
		Verb("detach", a.detachTaskImage, "Remove an image from the task being edited: image n, the only one, else choose").When("task-image-input").
		Mode("insert").
		Bind("ctrl+v", ochazuke.Do(outlineModal+".paste").Hinted("paste/attach"))
	a.parameters(outlineModal+".detach", map[string]ochazuke.Parameter{"n": {Type: "integer", Description: "Image number; omit to choose"}})
}

func (a *App) outlineImageInput() (string, *ochazuke.Input) {
	o := a.outline
	if o == nil || !o.images || a.r.Tree(outlineTree) != o.tree {
		return "", nil
	}
	id := o.tree.Editing()
	if id == "" || a.isProjectRow(id) {
		return "", nil
	}
	return taskOf(id), a.r.Input(outlineTree + "/line")
}

func (a *App) pasteTaskImage(ochazuke.Ctx) ochazuke.Result {
	id, in := a.outlineImageInput()
	if in == nil {
		return ochazuke.Failed(errors.New("edit a task to attach an image"))
	}
	if a.pb.clip == nil {
		return ochazuke.Failed(errors.New("no clipboard reader"))
	}
	o := a.outline
	path := filepath.Join(a.attachmentDir("task-"+id), time.Now().UTC().Format("20060102T150405.000000")+".png")
	return readClipboard(a.pb.clip, path).Then(func(ctx ochazuke.Ctx, v any, err error) {
		currentID, currentInput := a.outlineImageInput()
		if a.outline != o || currentID != id || currentInput != in {
			_ = os.Remove(path) // only this cancelled read's managed file, never a copied file
			return
		}
		read, _ := v.(clipboardRead)
		if err == nil {
			err = read.err
		}
		if err != nil {
			ctx.Error("could not read the clipboard: " + oneLine(err.Error()))
			return
		}
		images := o.taskImages(id)
		text, notice := images.pastedText(read.got, string(in.Text))
		if text == "" {
			ctx.Info(notice)
			return
		}
		start, end, _ := in.SelectedText()
		if read.got.Text == "" {
			text = spacedAtRange(in, text, start, end)
		}
		if err := in.Replace(start, end, text); err != nil {
			ctx.Error(err.Error())
			return
		}
		if notice != "" {
			ctx.Notify(notice)
		}
	})
}

func (a *App) detachTaskImage(ctx ochazuke.Ctx) ochazuke.Result {
	id, in := a.outlineImageInput()
	if in == nil {
		return ochazuke.Failed(errors.New("edit a task to remove an image"))
	}
	o := a.outline
	live := o.taskImages(id).live(string(in.Text))
	if len(live) == 0 {
		return ochazuke.Failed(errors.New("the task has no attachments"))
	}
	detach := func(at promptAttachment) ochazuke.Result {
		currentID, currentInput := a.outlineImageInput()
		if a.outline != o || currentID != id || currentInput != in {
			return ochazuke.Failed(errors.New("the task editor has closed"))
		}
		if err := removeImageChip(in, at); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done("detached " + at.chip())
	}
	if n := ochazuke.IntArg(ctx.Args, "n", 0); n > 0 {
		for _, at := range live {
			if at.n == n {
				return detach(at)
			}
		}
		return ochazuke.Failed(fmt.Errorf("no image %d in the task", n))
	}
	if len(live) == 1 {
		return detach(live[0])
	}
	rows := make([]ochazuke.ChooserRow, len(live))
	for i, at := range live {
		rows[i] = ochazuke.ChooserRow{ID: at.chip(), Label: at.chip() + " " + at.path, Data: at, Cells: []string{at.chip(), at.path}}
	}
	_, err := a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind: "task-attachments", Title: "Detach an attachment", Rows: rows,
		Choose: func(row ochazuke.ChooserRow) ochazuke.Result {
			return detach(row.Data.(promptAttachment))
		},
	})
	return resultError(err)
}
