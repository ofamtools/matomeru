package app

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	otea "ofam.dev/ochazuke/tea"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

// Registers, macros and the yank picker.
//
// The app has one register bank. A yank writes the unnamed register and "0,
// mirrors the text into "+ and puts it on the system clipboard; "% follows
// the focused record (its file, or its reference), "/ the last filter and ":
// the last command run from the palette. Macros record the keys as they are
// pressed and replay them through the engine, so a macro does exactly what
// the keys would.

// maxReplayDepth bounds a macro that replays a macro.
const maxReplayDepth = 8

// program is the app as a Bubble Tea model: the shell, with every key that
// is pressed while a macro is being recorded written into it first.
type program struct{ a *App }

func (p program) Init() tea.Cmd { return p.a.Shell.Init() }

func (p program) View() tea.View { return p.a.Shell.View() }

func (p program) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, recording := p.a.nav.Macros.Recording(); recording {
		if ev, ok := otea.Translate(msg); ok {
			if key, isKey := ev.(core.KeyEvent); isKey {
				p.a.nav.Macros.Record(key)
			}
		}
	}
	_, cmd := p.a.Shell.Update(msg)
	return p, cmd
}

// Program returns the model to run: tea.NewProgram(a.Program()).
func (a *App) Program() tea.Model { return program{a} }

// effect queues an effect for the command being handled.
func (a *App) effect(e core.Effect) { a.effects = append(a.effects, e) }

// yank puts text into the registers and onto the system clipboard. what
// names it in the status line.
func (a *App) yank(text, what string) {
	a.nav.Registers.Yank(0, text, strings.Contains(text, "\n"))
	a.nav.Registers.SetSpecial(core.RegClipboard, text)
	a.rt.live.clipboard = text
	a.effect(core.CustomEffect{Kind: effectClipboard, Data: text})
	shown := clip(strings.ReplaceAll(text, "\n", "⏎"), 48)
	a.say("yanked %s: %s", what, shown)
}

// Register returns the text of a register: '"' the last yank, '0' the same,
// '+' the clipboard, '%' the focused record, '/' the last filter, ':' the
// last command.
func (a *App) Register(reg rune) string {
	v, _ := a.nav.Registers.Get(reg)
	return v.Text
}

// syncPath keeps "% on the focused record: its file when it has one,
// otherwise its reference.
func (a *App) syncPath(s core.State) {
	if len(s.Modals) > 0 {
		return // an overlay's focus is not what the owner is on
	}
	ref, ok := a.subject(s, core.Command{})
	if !ok {
		return
	}
	path := data.RefText(ref)
	if rec, err := a.Store.Get(ref); err == nil {
		if file := rec.Text("filePath"); file != "" {
			path = file
		}
	}
	if a.Register(core.RegPath) != path {
		a.nav.Registers.SetSpecial(core.RegPath, path)
	}
}

// --- yank -----------------------------------------------------------------------

// YankValue is one thing of a record that can be yanked.
type YankValue struct {
	Name string // the field, or ref, id, json, markdown
	Text string // exactly what is copied
}

// primaryFields are tried in order for a record's primary value.
var primaryFields = []string{"title", "url", "filePath"}

// recordMarkdown is a record as a short Markdown summary.
func recordMarkdown(schema data.Schema, rec data.Record, fields []YankValue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", views.Title(schema, rec))
	fmt.Fprintf(&b, "- kind: %s\n- ref: %s\n", rec.Kind, data.RefText(rec.Ref()))
	for _, f := range fields {
		if f.Name == schema.SummaryField || strings.Contains(f.Text, "\n") {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", f.Name, f.Text)
	}
	for _, f := range fields {
		if f.Name != schema.SummaryField && strings.Contains(f.Text, "\n") {
			fmt.Fprintf(&b, "\n## %s\n\n%s\n", views.Label(f.Name), strings.TrimSpace(f.Text))
		}
	}
	return b.String()
}

// YankValues lists everything of a record that can be yanked: every field
// that has a value (the schema's fields in their order, then the fields no
// schema declares, local overrides laid over both), then its reference, its
// ID, the whole record as JSON and a Markdown summary. Nothing is special
// to a noun: a new schema's fields are offered the day it is registered.
func YankValues(schema data.Schema, rec data.Record) []YankValue {
	merged := rec.MergedFields()
	var out []YankValue
	seen := map[string]bool{}
	add := func(name string) {
		v, has := merged[name]
		if seen[name] || !has || v == nil {
			return
		}
		seen[name] = true
		if text := data.Text(v); text != "" {
			out = append(out, YankValue{Name: name, Text: text})
		}
	}
	for _, f := range schema.Fields {
		add(f.Name)
	}
	for _, name := range slices.Sorted(maps.Keys(merged)) {
		add(name)
	}
	fields := slices.Clone(out)
	out = append(out, YankValue{"ref", data.RefText(rec.Ref())}, YankValue{"id", rec.ID})
	if b, err := json.MarshalIndent(rec, "", "  "); err == nil {
		out = append(out, YankValue{"json", string(b)})
	}
	return append(out, YankValue{"markdown", recordMarkdown(schema, rec, fields)})
}

// PrimaryYank is the one value "y y" copies: for a suggested command record
// its literal command string, otherwise the record's title, URL, file, or ref.
func PrimaryYank(schema data.Schema, rec data.Record) YankValue {
	if rec.Kind == data.KindCommand {
		if cmd := rec.Text("command"); cmd != "" {
			return YankValue{Name: "command", Text: cmd}
		}
		if sp := rec.Text("scriptPath"); sp != "" {
			return YankValue{Name: "scriptPath", Text: sp}
		}
	}
	for _, name := range append([]string{schema.SummaryField}, primaryFields...) {
		if text := rec.Text(name); name != "" && text != "" {
			return YankValue{Name: name, Text: text}
		}
	}
	return YankValue{Name: "ref", Text: data.RefText(rec.Ref())}
}

// yankPick opens the yank picker over the focused record: one row per
// value, each showing exactly what it copies.
func (a *App) yankPick(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok {
		a.say("nothing here to yank")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("yank", err)
		return core.Next(s)
	}
	schema := a.schema(ref.Kind)
	primary := PrimaryYank(schema, *rec)
	var items []picker.Item
	for _, v := range YankValues(schema, *rec) {
		item := picker.Item{ID: v.Name, Title: v.Name, Subtitle: clip(strings.ReplaceAll(v.Text, "\n", "⏎"), 60), Data: v.Text}
		if v.Name == primary.Name {
			item.Badges = []ui.Badge{{Text: "y", Tone: "accent"}}
		}
		items = append(items, item)
	}
	// The primary value first: "y enter" and "y y" copy the same thing.
	slices.SortStableFunc(items, func(x, y picker.Item) int {
		return map[bool]int{true: 0, false: 1}[x.ID == primary.Name] - map[bool]int{true: 0, false: 1}[y.ID == primary.Name]
	})
	pm := picker.New("Yank from "+a.title(ref), items)
	pm.Placeholder = "a value · y copies the " + primary.Name
	pm.PreviewFn = func(it picker.Item) *ui.ViewNode {
		text, _ := it.Data.(string)
		return &ui.ViewNode{ID: "yank.preview.text", Kind: ui.KindText, Props: map[string]any{"text": text}}
	}
	yankTarget := ref
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "yank",
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			text, _ := picked.Data.(string)
			a.yank(text, picked.ID)
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
		OnCommand: func(_ *picker.Model, cmd core.Command) (modal.Result, bool) {
			if cmd.ID != CmdYankPrimary {
				return modal.Result{}, false
			}
			rec, err := a.Store.Get(yankTarget)
			if err != nil {
				a.fail("yank", err)
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}, true
			}
			v := PrimaryYank(a.schema(yankTarget.Kind), *rec)
			a.yank(v.Text, v.Name)
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}, true
		},
	})
	return core.Next(a.pushModal(s, m))
}

// yankPrimary copies the primary value of the record the yank picker is
// open on, or of the focused record, without a picker.
func (a *App) yankPrimary(s core.State, c core.Command) core.StepResult {
	if a.Modal() == "yank" {
		return a.handleModal(s, c)
	}
	ref, ok := a.subject(s, c)
	if !ok {
		a.say("nothing here to yank")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("yank", err)
		return core.Next(s)
	}
	v := PrimaryYank(a.schema(ref.Kind), *rec)
	a.yank(v.Text, v.Name)
	return core.Next(s)
}

// --- macros ---------------------------------------------------------------------

func regOf(c core.Command) rune {
	r := []rune(c.Arg("reg"))
	if len(r) != 1 {
		return 0
	}
	return r[0]
}

// macroRecord starts recording the keys pressed into a register.
func (a *App) macroRecord(s core.State, c core.Command) core.StepResult {
	reg := regOf(c)
	if !a.nav.Macros.Start(reg) {
		a.say("q then a letter records a macro")
		return core.Next(s)
	}
	a.say("recording @%c · q stops", reg)
	return core.Next(s)
}

// macroStop ends the recording. The keys that ended it are not part of it.
func (a *App) macroStop(s core.State, c core.Command) core.StepResult {
	reg, recording := a.nav.Macros.Recording()
	if !recording {
		return core.Next(s)
	}
	keys := a.nav.Macros.Stop()
	if seq, fromKey := strings.CutPrefix(c.Source, "key:"); fromKey {
		keys = keys[:max(len(keys)-len(strings.Fields(seq)), 0)]
		a.nav.Macros.Set(reg, keys)
	}
	a.say("recorded @%c: %d keys · @ %c replays", reg, len(keys), reg)
	return core.Next(s)
}

// macroReplay feeds a recorded macro back through the engine, as if its
// keys were pressed again.
func (a *App) macroReplay(s core.State, c core.Command) core.StepResult {
	reg := regOf(c)
	if a.nav.replaying >= maxReplayDepth {
		a.say("the macro replays itself: stopped")
		return core.Next(s)
	}
	keys, ok := a.nav.Macros.Replay(reg, c.Times())
	if !ok {
		if reg == '@' {
			a.say("no macro was replayed yet")
		} else {
			a.say("no macro in @%c · q %c records one", reg, reg)
		}
		return core.Next(s)
	}
	a.nav.replaying++
	res := a.Engine.ReplayKeys(s, func(st core.State) []core.ContextFrame {
		return ui.ContextChain(a.View(st), st.ActiveFrame().FocusID, a.Shell.AppFacts(st))
	}, keys)
	a.nav.replaying--
	if a.status == "" {
		a.say("replayed @%c", a.nav.Macros.LastPlayed())
	}
	return res
}

// macroBindings are the keys of the macro recorder: q and a letter records,
// q stops, @ and a letter (or @) replays. Recording starts only from the
// normal mode itself: a mode based on it (the outline) keeps q for closing,
// with no prefix to wait on.
func macroBindings() []core.Binding {
	out := []core.Binding{
		when(bind(ModeNormal, "", "q", CmdMacroStop, "stop recording"), "facts.recording"),
		hidden(with(bind(ModeNormal, "", "@ @", CmdMacroReplay, "replay the last macro"), map[string]any{"reg": "@"})),
	}
	for r := 'a'; r <= 'z'; r++ {
		rec := when(with(bind(ModeNormal, "", "q "+string(r), CmdMacroRecord, "record a macro"), map[string]any{"reg": string(r)}), "!facts.recording && mode == \"normal\"")
		play := with(bind(ModeNormal, "", "@ "+string(r), CmdMacroReplay, "replay a macro"), map[string]any{"reg": string(r)})
		// One row of each shows in the footer and the help; the rest are the same.
		rec.Hidden, play.Hidden = r != 'a', r != 'a'
		out = append(out, rec, play)
	}
	return out
}
