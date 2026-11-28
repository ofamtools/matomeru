package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/markdown"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

// Opening things. "o" opens what the focused record points at (its file,
// its URL, its directory, or its own text) with the first handler whose rule
// matches; "O" offers every handler that matches. Which handler opens what
// is data: handlers.DefaultRules with the user's handlers.json laid over it.

const (
	handlersFileName = "handlers.json"
	maxReadBytes     = 4 << 20
	copyPathID       = "copy-path"
)

// openTarget is what a record gives a handler to open.
type openTarget struct {
	path  string // a file, a directory or a URL; "" for inline text
	title string
	body  string    // Markdown kept in the record itself
	from  *core.Ref // the record it came from
	rules []handlers.Rule
	link  markdownLinkLocation
}

// loadHandlers reads the user's handlers.json over the default rules.
func (a *App) loadHandlers(dir string) {
	a.cfg.Handlers = handlers.Default()
	if dir == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(dir, handlersFileName))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err == nil {
		err = a.cfg.Handlers.LoadJSONC(b)
	}
	if err != nil {
		a.warn("%s: %v", handlersFileName, err)
	}
}

// targetFields are the fields a record names something to open with.
var targetFields = []string{"filePath", "path", "url", "cwd"}

// targetOf is what "open" opens for a record: the first of its file, URL
// and directory, or, for a document or a note kept inline, its own text.
func (a *App) targetOf(rec data.Record) (openTarget, bool) {
	ref := rec.Ref()
	t := openTarget{title: views.Title(a.schema(rec.Kind), rec), from: &ref}
	for _, field := range targetFields {
		if v := strings.TrimSpace(rec.Text(field)); v != "" {
			t.path = v
			if !handlers.IsURL(v) {
				t.path = cleanLocalTargetPath(v)
			}
			t.rules = a.cfg.Handlers.Match(t.path)
			return t, true
		}
	}
	if rec.Kind == data.KindContext {
		if raw := rec.Text("value"); strings.TrimSpace(raw) != "" {
			if data.ClassifyContextKind(raw, rec.Text("contextKind")) == "code" {
				t.body = data.FormatCodeMarkdownBlock(raw, rec.Text("language"))
				t.rules = a.inlineMarkdownRules()
				return t, true
			}
			v := strings.TrimSpace(raw)
			var pane *data.Record
			if pRef, hasPane := rec.RefField("pane"); hasPane {
				if p, err := a.Store.Get(pRef); err == nil {
					pane = p
				}
			}
			t.path = contextTargetPath(rec.Text("contextKind"), v, pane)
			t.rules = a.cfg.Handlers.Match(t.path)
			return t, true
		}
	}
	if body := rec.Text("body"); body != "" && (rec.Kind == data.KindDocument || rec.Kind == data.KindNote) {
		t.body = body
		t.rules = a.inlineMarkdownRules()
		return t, true
	}
	return t, false
}

// inlineMarkdownRules returns the built-in MarkdownReader rule for inline text
// targets that have no filesystem path or URL.
func (a *App) inlineMarkdownRules() []handlers.Rule {
	var rules []handlers.Rule
	for _, rule := range a.cfg.Handlers.Match("inline.md") {
		if rule.Kind == handlers.KindBuiltin && rule.Name == handlers.MarkdownReader {
			rules = append(rules, rule)
		}
	}
	if len(rules) == 0 {
		rules = []handlers.Rule{{Name: handlers.MarkdownReader, Match: "*.md", Kind: handlers.KindBuiltin}}
	}
	return rules
}

// openable reports whether "o" has something to open on a record.
func (a *App) openable(rec data.Record) bool {
	_, ok := a.targetOf(rec)
	return ok
}

func (a *App) focusedTarget(s core.State, c core.Command) (openTarget, bool) {
	ref, ok := a.subject(s, c)
	if !ok {
		a.say("nothing here to open")
		return openTarget{}, false
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("open", err)
		return openTarget{}, false
	}
	t, ok := a.targetOf(*rec)
	if !ok {
		a.say("%s has no file, URL or directory to open", a.title(ref))
		return openTarget{}, false
	}
	return t, true
}

// openTargetCmd is "o": the first matching handler opens the target.
func (a *App) openTargetCmd(s core.State, c core.Command) core.StepResult {
	t, ok := a.focusedTarget(s, c)
	if !ok {
		return core.Next(s)
	}
	if len(t.rules) == 0 {
		a.say("no handler opens %s · O copies its path · %s adds one", t.path, handlersFileName)
		return core.Next(s)
	}
	return core.Next(a.runHandler(s, t.rules[0], t))
}

// openWith is "O": a picker over every handler that matches, and Copy Path.
func (a *App) openWith(s core.State, c core.Command) core.StepResult {
	t, ok := a.focusedTarget(s, c)
	if !ok {
		return core.Next(s)
	}
	return core.Next(a.openWithTarget(s, t))
}

func (a *App) openWithTarget(s core.State, t openTarget) core.State {
	var items []picker.Item
	for i, rule := range t.rules {
		item := picker.Item{ID: fmt.Sprint(i), Title: rule.Name, Subtitle: rule.Match, Keywords: []string{rule.Kind}}
		if rule.Kind == handlers.KindExec {
			item.Subtitle = rule.CommandLine(t.path)
		}
		if i == 0 {
			item.Badges = []ui.Badge{{Text: "o", Tone: "accent"}}
		}
		items = append(items, item)
	}
	if t.path != "" {
		items = append(items, picker.Item{ID: copyPathID, Title: "Copy Path", Subtitle: t.path})
	}
	what := t.path
	if what == "" {
		what = t.title
	}
	pm := picker.New("Open "+what+" with", items)
	pm.Placeholder = "a handler"
	target := t
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "openwith",
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			if picked.ID == copyPathID {
				a.yank(target.path, "path")
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
			}
			var i int
			if _, err := fmt.Sscan(picked.ID, &i); err == nil && i >= 0 && i < len(target.rules) {
				rule := target.rules[i]
				a.afterModal(func(st core.State) core.State {
					return a.runHandler(st, rule, target)
				})
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return a.pushModal(s, m)
}

func editorCommand() string {
	if ed := strings.TrimSpace(os.Getenv("EDITOR")); ed != "" {
		return ed
	}
	return "vim"
}

func systemOpener() string {
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}

// runHandler carries out one rule for a target.
func (a *App) runHandler(s core.State, rule handlers.Rule, t openTarget) core.State {
	needPath := func() bool {
		if t.path == "" {
			a.say("%s needs a file: %s is kept inline", rule.Name, t.title)
		}
		return t.path != ""
	}
	base := filepath.Base(t.path)
	if rule.Kind == handlers.KindShell {
		if !needPath() {
			return s
		}
		return a.startShell(s, rule.Name+" "+base, rule.CommandLine(t.path+t.link.suffix), filepath.Dir(t.path))
	}
	if rule.Kind == handlers.KindExec {
		if !needPath() {
			return s
		}
		// A tool with a screen of its own: it gets a pane.
		return a.newPane(s, t.from, rule.Name+": "+base, rule.CommandLine(t.path+t.link.suffix), filepath.Dir(t.path), data.RoleNormal)
	}
	switch rule.Name {
	case handlers.MarkdownReader:
		return a.openReader(s, t)
	case handlers.SideVim:
		if !needPath() {
			return s
		}
		return a.newPane(s, t.from, "vim: "+base, editorCommand()+t.link.editorArgs(t.path), filepath.Dir(t.path), data.RoleSideVim)
	case handlers.PathPicker:
		s, _ = a.modals.Close(s, "openwith", modal.CloseSubmit)
		return a.openPath(s, "", t.from, t.path)
	case handlers.NewPane:
		if !needPath() {
			return s
		}
		return a.newPane(s, t.from, base, "", t.path, data.RoleNormal)
	case handlers.SystemOpen:
		if !needPath() {
			return s
		}
		return a.startShell(s, "open "+t.path, systemOpener()+" "+handlers.Quote(t.path+t.link.suffix), "")
	}
	a.say("no handler is named %s · the built-in ones are %s", rule.Name,
		strings.Join([]string{handlers.MarkdownReader, handlers.SideVim, handlers.PathPicker, handlers.NewPane, handlers.SystemOpen}, ", "))
	return s
}

// newPane records a pane running command ("" is the shell) in dir, selects
// it and goes into its terminal. A side pane keeps the pane it was opened
// beside as its parentPane, and the task of what it was opened from.
func (a *App) newPane(s core.State, from *core.Ref, title, command, dir, role string) core.State {
	fields := map[string]any{"title": title, "runStatus": "running", "role": role}
	harness := command
	if harness == "" {
		harness = "shell"
		if sh := os.Getenv("SHELL"); sh != "" {
			harness = filepath.Base(sh)
		}
	}
	fields["harness"], _, _ = strings.Cut(harness, " ")
	if dir != "" && dir != "." {
		fields["cwd"] = dir
	}
	if command != "" {
		fields["command"] = command
	}
	what := "opened " + title
	if from != nil {
		if rec, err := a.Store.Get(*from); err == nil {
			if from.Kind == data.KindPane {
				fields["parentPane"] = *from
				what += " beside " + a.title(*from)
			}
			if task, ok := rec.RefField("task"); ok {
				fields["task"] = task
			} else if from.Kind == data.KindTask {
				fields["task"] = *from
			}
		}
	}
	rec, cs, err := a.Store.Create(data.CreateInput{Kind: data.KindPane, Fields: fields}, User)
	if err != nil {
		a.fail("open pane", err)
		return s
	}
	s = a.dropModals(s)
	rail := a.rail()
	if !selectRow(rail, data.RefText(rec.Ref())) {
		rail.Query = ""
		selectRow(rail, data.RefText(rec.Ref()))
	}
	a.panes.activePage[rec.ID] = views.ScreenPageID
	s.ActivePage = SpacePanes
	if err := a.attach(*rec); err != nil {
		a.fail("open "+title, err)
		return withPage(s, SpacePanes, siteScreen, ModeNormal)
	}
	a.wrote(what, cs, nil)
	return withPage(s, SpacePanes, siteScreen, ModeTerminal)
}

// --- the Markdown reader ----------------------------------------------------------

func (a *App) readFile(path string) (string, error) {
	read := a.opts.ReadFile
	if read == nil {
		read = os.ReadFile
	}
	b, err := read(path)
	if err != nil {
		return "", err
	}
	if len(b) > maxReadBytes {
		return "", fmt.Errorf("%s is %d MB: too large for the reader", path, len(b)>>20)
	}
	return string(b), nil
}

// openReader shows a target's Markdown in the reader: its inline text, or
// its file.
func (a *App) openReader(s core.State, t openTarget) core.State {
	source, title := t.body, t.title
	if t.path != "" {
		text, err := a.readFile(t.path)
		if err != nil {
			a.fail("read "+t.path, err)
			return s
		}
		source, title = text, filepath.Base(t.path)
	}
	md := markdown.New(title, source)
	md.ShowTOC = len(md.Headings) > 1
	t.link.reveal(md)
	var rm *modal.MarkdownModal
	rm = modal.NewMarkdownModal(md, modal.MarkdownOptions{
		ID:              "reader",
		Scope:           "reader",
		Mode:            ModeReader,
		FocusID:         readerNode,
		Path:            t.path,
		WidthPct:        92,
		HeightPct:       90,
		ShowStatsBadges: true,
		OnOpenLink: func(href string) modal.Result {
			return a.followLink(rm, t.from, href)
		},
		OnYankCode: func(code string) modal.Result {
			a.yank(code, "code block")
			return modal.Result{Handled: true}
		},
		OnCommand: func(m *markdown.Model, cmd core.Command) (modal.Result, bool) {
			switch cmd.ID {
			case CmdReaderBack:
				if !rm.Back() {
					return modal.Result{Handled: true, Status: "this is the first document", SetStatus: true}, true
				}
				return modal.Result{Handled: true, Status: m.Title, SetStatus: true}, true
			case CmdReaderHeadingNext, CmdReaderHeadingPrev:
				step := markdown.CmdTOCNext
				if cmd.ID == CmdReaderHeadingPrev {
					step = markdown.CmdTOCPrev
				}
				m.HandleCommand(core.Command{ID: step, Count: cmd.Count})
				m.HandleCommand(core.Command{ID: markdown.CmdHeadingJump})
				return modal.Result{Handled: true}, true
			}
			return modal.Result{}, false
		},
	})
	a.say("j/k scroll · ]/[ headings · t contents · tab links · enter opens · y yanks code · q closes")
	s, _ = a.modals.Close(s, "openwith", modal.CloseSubmit)
	return a.pushModal(s, rm)
}

func slug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune('-')
		}
	}
	return b.String()
}

// jumpToAnchor scrolls md to the heading a "#fragment" names.
func jumpToAnchor(md *markdown.Model, frag string) bool {
	want := slug(strings.TrimPrefix(frag, "#"))
	for i, h := range md.Headings {
		if slug(h.Text) == want {
			md.HandleCommand(core.Command{ID: markdown.CmdHeadingJump, Args: map[string]any{"index": i}})
			return true
		}
	}
	return false
}
