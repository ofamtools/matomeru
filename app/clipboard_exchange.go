package app

import (
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

// The shared clipboard: clips the owner and agents hand to each other.
//
// clipboard.open lists the newest clips (agents share with the clipboard.put
// MCP tool or `mtm clip put`); enter copies one to the system clipboard and
// the unnamed register. clipboard.push shares the owner's last yank (or the
// system clipboard) so agents read it with clipboard.list.
const (
	CmdClipboardOpen = "clipboard.open"
	CmdClipboardPush = "clipboard.push"

	clipboardModalID     = "clipboard"
	clipboardPickerLimit = 200
)

// clipKindOfRegister maps the kind of a yanked register value to a clipKind.
// A kind that is not listed is inferred from the text.
var clipKindOfRegister = map[string]string{
	"code": data.ClipKindCode,
	"path": data.ClipKindPath,
	"refs": data.ClipKindRef,
}

func clipboardExchangeSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		spec(CmdClipboardOpen, "Open the shared clipboard: copy what agents and the owner shared"),
		{
			ID:          CmdClipboardPush,
			Description: "Push the last yank (or the system clipboard) to the shared clipboard for agents",
			Tags:        []string{"editing"},
			Params: []core.ParamSpec{
				{Name: "text", Type: core.ParamString, Description: "text to share (default: the unnamed register, then the system clipboard)"},
			},
		},
	}
}

func clipboardExchangeBindings() []core.Binding {
	return []core.Binding{
		bind(ModeNormal, "", "g y", CmdClipboardOpen, "shared clipboard"),
		bind(ModeNormal, "", "g Y", CmdClipboardPush, "push yank to shared clipboard"),
	}
}

func (a *App) registerClipboardExchangeHandlers() {
	a.handle(CmdClipboardOpen, a.clipboardOpen)
	a.handle(CmdClipboardPush, a.clipboardPush)
}

func oneLine(text string, n int) string {
	return clip(strings.ReplaceAll(strings.TrimSpace(text), "\n", "⏎"), n)
}

// clipPickerItem is one row of the shared clipboard picker: the clip's title,
// its content on one line with where it came from, and the schema's badges.
func (a *App) clipPickerItem(schema data.Schema, rec data.Record) picker.Item {
	ref := rec.Ref()
	content := rec.Text("content")
	parts := []string{oneLine(content, 60)}
	keywords := []string{rec.Text("authorKind"), rec.Text("clipKind"), oneLine(content, 200)}
	for _, field := range []string{"fromPane", "project"} {
		if from, ok := rec.RefField(field); ok && from.ID != "" {
			title := a.title(from)
			parts = append(parts, title)
			keywords = append(keywords, title)
		}
	}
	return picker.Item{
		ID:       rec.ID,
		Title:    rec.Text("title"),
		Subtitle: strings.Join(parts, "  ·  "),
		Badges:   views.StatusBadges(schema, rec),
		Ref:      &ref,
		Scope:    rec.Kind,
		Data:     content,
		Keywords: keywords,
	}
}

// clipboardOpen opens the shared clipboard picker, newest clip first.
func (a *App) clipboardOpen(s core.State, _ core.Command) core.StepResult {
	clips, err := a.Store.ListClips(data.ClipFilter{Limit: clipboardPickerLimit})
	if err != nil {
		a.fail("shared clipboard", err)
		return core.Next(s)
	}
	if len(clips) == 0 {
		a.say("the shared clipboard is empty: %s pushes your last yank, agents share with clipboard.put", CmdClipboardPush)
		return core.Next(s)
	}
	schema := a.schema(data.KindClip)
	items := make([]picker.Item, 0, len(clips))
	for _, rec := range clips {
		items = append(items, a.clipPickerItem(schema, rec))
	}
	pm := picker.New("Shared Clipboard", items)
	pm.Placeholder = "clips from you and agents · enter copies"
	pm.PreviewFn = func(it picker.Item) *ui.ViewNode {
		text, _ := it.Data.(string)
		return &ui.ViewNode{ID: "clipboard.preview.text", Kind: ui.KindText, Props: map[string]any{"text": text}}
	}
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:        clipboardModalID,
		WidthPct:  76,
		HeightPct: 80,
		PageSize:  10,
		OnConfirm: func(picked picker.Item) modal.Result {
			a.copyClip(picked.ID)
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, m))
}

// copyClip yanks clip id (the unnamed register and the system clipboard) and
// stamps when it was copied.
func (a *App) copyClip(id string) {
	rec, err := a.Store.MarkClipCopied(id, a.now(), User)
	if err != nil {
		a.fail("copy clip", err)
		return
	}
	a.viewMemo.gen++
	a.yank(rec.Text("content"), rec.Text("clipKind"))
}

// clipboardPushText is what clipboard.push shares: the text argument, else
// the unnamed register (with its kind), else the system clipboard.
func (a *App) clipboardPushText(c core.Command) (text, kind string) {
	if arg, _ := c.Args["text"].(string); strings.TrimSpace(arg) != "" {
		return arg, ""
	}
	if v, _ := a.nav.Registers.Get('"'); strings.TrimSpace(v.Text) != "" {
		return v.Text, clipKindOfRegister[v.Kind]
	}
	text, _ = readClipboardText(activeClipboardEnv)
	return text, ""
}

// clipboardPush shares text on the shared clipboard as the owner, tied to
// the pane in view and its task's project when there is one.
func (a *App) clipboardPush(s core.State, c core.Command) core.StepResult {
	text, kind := a.clipboardPushText(c)
	if strings.TrimSpace(text) == "" {
		a.say("nothing to push: yank something first")
		return core.Next(s)
	}
	if newest, err := a.Store.ListClips(data.ClipFilter{Limit: 1}); err == nil && len(newest) == 1 && newest[0].Text("content") == text {
		a.say("already on the shared clipboard: %s", oneLine(text, 48))
		return core.Next(s)
	}
	in := data.ClipInput{Content: text, Kind: kind, AuthorKind: data.ClipAuthorOwner}
	if pane, ok := a.activePane(s); ok && pane != nil {
		in.FromPane = pane.ID
		task, _ := pane.RefField("task")
		in.Project = task.ID
	}
	if _, err := a.Store.PutClip(in, User); err != nil {
		a.fail("push to shared clipboard", err)
		return core.Next(s)
	}
	a.viewMemo.gen++
	a.say("pushed to the shared clipboard: %s", oneLine(text, 48))
	return core.Next(s)
}
