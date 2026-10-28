package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/compose"
	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/daemon"
)

// The prompt box: an input pinned below an agent pane's turns (the Turns
// tab of a Claude Code or omp pane), a focus stop of its own after the
// tree (} and tab from the tree, i from the tree or the pane row), that
// sends what is typed to the agent through tuios, never through the
// harness itself (no patch to omp, no hook of Claude's):
//
//   - send: send-text with submit, typed as one bracketed paste and then
//     the harness's Enter (tuios's ask-agent typing). Sent while the agent
//     works it is a steer, and the box says so ([steer]): Claude Code
//     delivers it into the running turn (a queued_command), omp marks it
//     steering, and Turns folds it into that turn once the log has it.
//   - queue: queue-prompt (the verb behind `tuios queue`): tuios types it
//     once the agent is at rest, one per rest, never over a waiting
//     prompt. A pane holds daemon.QueueMax messages of at most
//     daemon.QueueMaxText bytes; the box refuses a longer one and names
//     the bound when tuios answers queue_full.
//   - interrupt: send-keys Escape while the agent works (Claude Code and
//     omp both stop the turn on it), a pause, then the send.
//
// enter sends in either mode of the box; ctrl+j (and alt+enter, where the
// terminal reports it) breaks the line in insert mode. ctrl+j is the
// newline key because Bubble Tea reads a line feed as ctrl+j on every
// terminal, while shift+enter needs the kitty keyboard protocol; the
// editor's own enter row (a newline in a multi-line input) is shadowed by
// the box's send row. ctrl+t cycles the modes; neither ochazuke's editor
// tables nor mtm bind it (readline's transpose is not in ochazuke's).
// Each send is a job off the loop; a failure is the status line's. While
// the agent waits on the person (an approval, a question) send and
// interrupt refuse: tuios would type into the prompt it shows.
//
// Completions (ochazuke inline completion): @ files under the pane's
// directory (git ls-files, else a walk; compose.Files) and the context
// items mtm keeps, / the harness's slash commands (and Claude's skills,
// which are commands there), $ the skills the harness sees. Accepting a
// skill writes the harness's own syntax (compose.SkillInvocation): the
// completion keeps its trigger, so a suggestion whose text is a /form
// has its $ dropped right after the accept (promptAfter).
//
// alt+o (turn-prompt.context) opens the context picker scoped to the
// box's pane (ctrl+a c's rows); enter writes the chosen item at the box's
// cursor (promptMention: a file or dir as @path, the rest as its text)
// and the box keeps the focus, in insert mode. alt+o because readline
// leaves it unbound (ctrl+o, ctrl+g and ctrl+] are taken there) and
// neither ochazuke nor mtm binds it.

const (
	promptID   = "panes/prompt"
	promptNoun = "turn-prompt"
	promptRows = 4 // the most lines the box grows to before it scrolls
	// promptInterruptPause is the wait between Escape and the send, so the
	// harness has stopped the turn before the paste arrives.
	promptInterruptPause = 400 * time.Millisecond
)

// The box's modes.
const (
	promptSend = iota
	promptQueue
	promptInterrupt
)

var promptModes = []string{"send", "queue", "interrupt"}

// promptBox is the box's state.
type promptBox struct {
	input   *ochazuke.Input
	mode    int
	sources *compose.Sources
	target  promptTarget
	focused bool // the box held the focus after the last message
}

// promptTarget is what the completions search against, set on the loop
// and read by the searches off it.
type promptTarget struct {
	mu      sync.Mutex
	harness string // compose's name
	cwd     string
	context []contextSuggestion
}

type contextSuggestion struct{ text, kind string }

func (t *promptTarget) set(harness, cwd string) {
	t.mu.Lock()
	t.harness, t.cwd = harness, cwd
	t.mu.Unlock()
}

func (t *promptTarget) setContext(items []contextSuggestion) {
	t.mu.Lock()
	t.context = items
	t.mu.Unlock()
}

func (t *promptTarget) get() (string, string, []contextSuggestion) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.harness, t.cwd, t.context
}

// promptDaemon is how the box reaches a pane: tuios's send-text (with
// submit), send-keys and queue-prompt. tuios provides it; tests replace it.
type promptDaemon interface {
	PromptSubmit(ctx context.Context, p pane, text string) error
	PromptInterrupt(ctx context.Context, p pane) error
	PromptQueue(ctx context.Context, p pane, text string) (daemon.QueuePromptResult, error)
}

// PromptSubmit types text into the pane as one paste and submits it.
func (t tuios) PromptSubmit(ctx context.Context, p pane, text string) error {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	return t.Client.SendText(ctx, daemon.SendTextParams{Session: p.Session, Window: p.ID, Text: text, Submit: true})
}

// PromptInterrupt sends Escape to the pane.
func (t tuios) PromptInterrupt(ctx context.Context, p pane) error {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	return t.Client.SendKeys(ctx, daemon.SendKeysParams{Session: p.Session, Window: p.ID, Keys: "Escape"})
}

// PromptQueue leaves text in the pane's delivery queue, from the person:
// mtm is their UI, so the entry carries from human and the presence nonce
// mtm holds (a refused nonce, after a daemon restart, is renewed once).
// When tuios gives mtm no presence (mtm runs in one of its panes), the
// prompt is queued all the same, without from: tuios then records the
// caller, mtm's pane, as who queued it.
func (t tuios) PromptQueue(ctx context.Context, p pane, text string) (daemon.QueuePromptResult, error) {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	queue := func() (daemon.QueuePromptResult, error) {
		params := daemon.QueuePromptParams{Session: p.Session, Window: p.ID, Text: text}
		if nonce, err := t.HumanNonce(ctx); err == nil && nonce != "" {
			params.From, params.HumanNonce = daemon.FromHuman, nonce
		}
		return t.Client.QueuePrompt(ctx, params)
	}
	got, err := queue()
	if daemonCode(err) == "not_human" {
		t.DropPresence()
		got, err = queue()
	}
	return got, err
}

// addPromptBox adds the box's input and its noun.
func (a *App) addPromptBox() error {
	a.pb = &promptBox{sources: compose.NewSources(compose.DefaultEnv())}
	a.r.Define(promptNoun).
		Verb("send", a.sendPrompt, "Send the prompt to the agent in the box's mode: send (a steer while it works), queue (typed when it rests) or interrupt (Escape, then send)").
		Verb("mode", a.cyclePromptMode, "Switch the prompt box's mode: send, queue, interrupt (or the one named)").
		Verb("back", a.promptBack, "Go back to the turns, else the pane row").
		Verb("context", a.promptContext, "Choose a context item or prompt of the box's pane, its project or no project, and write it at the cursor (a file as @path)").
		Mode("insert").
		Bind("enter", ochazuke.Do(promptNoun+".send")).
		Bind("ctrl+j", ochazuke.Do("input.insert", ochazuke.Arguments{"text": "\n"}).Hinted("newline")).
		Bind("alt+enter", ochazuke.Do("input.insert", ochazuke.Arguments{"text": "\n"}).Hide()).
		Bind("ctrl+t", ochazuke.Do(promptNoun+".mode")).
		Bind("alt+o", ochazuke.Do(promptNoun+".context").Hinted("context")).
		Mode("normal").
		Bind("enter", ochazuke.Do(promptNoun+".send")).
		Bind("ctrl+t", ochazuke.Do(promptNoun+".mode")).
		Bind("alt+o", ochazuke.Do(promptNoun+".context").Hinted("context")).
		Bind("esc", ochazuke.Do(promptNoun+".back"))
	a.parameters(promptNoun+".mode", map[string]ochazuke.Parameter{"mode": {Type: "string", Enum: promptModes, Description: "The mode to switch to; none takes the next"}})
	// i writes a prompt: from the pane row while the Turns tab shows, and
	// from any row of the turns.
	a.r.DefineGuard("pane-prompt", func(_ *ochazuke.Runtime, ev ochazuke.KeyEvent) bool {
		return a.promptPaneAt(ev.Focus) != nil
	})
	for _, noun := range []string{"pane", "turn", "turn-line", "turn-step", "turn-separator"} {
		a.r.Define(noun).
			Verb("prompt", a.focusPrompt, "Write to the agent: focus the prompt box below its turns, in insert mode").
			Mode("normal").
			Bind("i", ochazuke.Do(noun+".prompt").Guard("pane-prompt").Hinted("prompt"))
	}
	a.pb.input = ochazuke.NewInput(promptID, "", true)
	at := ochazuke.CompletionTrigger{Trigger: "@", Search: a.searchMentions}
	slash := ochazuke.CompletionTrigger{Trigger: "/", Search: a.searchCommands}
	skill := ochazuke.CompletionTrigger{Trigger: "$", Search: a.searchSkills}
	return a.r.AddInput(a.pb.input, ochazuke.Parent(panesPage), ochazuke.Kind(promptNoun), ochazuke.Completions(at, slash, skill))
}

// promptPane is the pane the box writes to: the pane whose turns the tab
// shows, when it is an agent's that the box serves. nil is none.
func (a *App) promptPane() *pane {
	if a.pv.tab != tabTurns || a.pv.turnsFor == "" {
		return nil
	}
	p, err := a.paneAt(a.r.Instances[a.pv.turnsFor])
	if err != nil || !promptServes(p) {
		return nil
	}
	return &p
}

// promptPaneAt is the pane i at focus would write to: a pane row's own
// (on the Turns tab), else the shown pane's for a row of the turns.
func (a *App) promptPaneAt(focus string) *pane {
	if a.pv.tab != tabTurns {
		return nil
	}
	if inst := a.r.Instances[focus]; inst != nil && inst.Noun == "pane" {
		p, err := a.paneAt(inst)
		if err != nil || !promptServes(p) {
			return nil
		}
		return &p
	}
	return a.promptPane()
}

// promptServes reports whether the pane's agent gets a prompt box: Claude
// Code and omp, whose turns the tab reads.
func promptServes(p pane) bool { return !p.Exited && (isClaude(p) || isOmp(p)) }

// focusPrompt is pane.prompt and turn*.prompt (i): show the row's turns
// when they are not shown, then focus the box in insert mode.
func (a *App) focusPrompt(ctx ochazuke.Ctx) ochazuke.Result {
	if ctx.Instance != nil && ctx.Instance.Noun == "pane" && a.pv.turnsFor != ctx.Instance.ID {
		if err := a.showTurns(ctx.Instance.ID); err != nil {
			return ochazuke.Failed(err)
		}
	}
	p := a.promptPane()
	if p == nil {
		return ochazuke.Failed(errors.New("the prompt box is for a Claude Code or omp pane, on the Turns tab"))
	}
	a.promptTargetFor(*p)
	if err := a.r.SetFocus(promptID); err != nil {
		return ochazuke.Failed(err)
	}
	a.pb.input.SetMode("insert")
	return ochazuke.Done(promptID)
}

// promptBack is turn-prompt.back (esc in normal mode): the turns, else
// the pane row.
func (a *App) promptBack(ochazuke.Ctx) ochazuke.Result {
	if a.pv.turns.List().Len() > 0 {
		if err := a.pv.turns.Focus(); err == nil {
			return ochazuke.Done(a.r.Focus)
		}
	}
	target := paneListID
	if a.r.Instances[a.pv.turnsFor] != nil {
		target = a.pv.turnsFor
	}
	if err := a.r.SetFocus(target); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(target)
}

// cyclePromptMode is turn-prompt.mode (ctrl+t): the next mode, or the one
// named.
func (a *App) cyclePromptMode(ctx ochazuke.Ctx) ochazuke.Result {
	if name, _ := ctx.Args["mode"].(string); name != "" {
		for i, m := range promptModes {
			if m == name {
				a.pb.mode = i
				return ochazuke.Done(name)
			}
		}
		return ochazuke.Failed(fmt.Errorf("no prompt mode %q (%s)", name, strings.Join(promptModes, ", ")))
	}
	a.pb.mode = (a.pb.mode + 1) % len(promptModes)
	return ochazuke.Done(promptModes[a.pb.mode])
}

// promptContext is turn-prompt.context (alt+o): the context picker with
// into=prompt.
func (a *App) promptContext(ochazuke.Ctx) ochazuke.Result {
	return a.pickIntoPrompt()
}

// pickIntoPrompt opens the context picker over the box's pane; enter
// writes the choice at the box's cursor.
func (a *App) pickIntoPrompt() ochazuke.Result {
	p := a.promptPane()
	if p == nil {
		return ochazuke.Failed(errors.New("the prompt box is for a Claude Code or omp pane, on the Turns tab"))
	}
	return a.openContextPicker(*p, func(row ochazuke.ChooserRow) ochazuke.Result {
		var text string
		switch d := row.Data.(type) {
		case promptRow:
			text = d.turn.Prompt
		case contextitems.Item:
			text = promptMention(d.Kind, d.Text)
		}
		if err := a.insertIntoPrompt(text); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(text)
	})
}

// promptMention is how an item reads in a prompt, as the @ completion
// writes it: a file or dir is @path (Claude Code and omp both attach
// it), anything else is its text.
func promptMention(kind, text string) string {
	if kind == "file" || kind == "dir" {
		return "@" + text
	}
	return text
}

// insertIntoPrompt writes text at the box's cursor and leaves the box
// focused in insert mode, the cursor after the text.
func (a *App) insertIntoPrompt(text string) error {
	if a.r.Focus != promptID {
		if err := a.r.SetFocus(promptID); err != nil {
			return err
		}
	}
	in := a.pb.input
	in.SetMode("insert")
	if text == "" {
		return nil
	}
	return in.Replace(in.Cursor, in.Cursor, text)
}

// promptModeLabel is the box's mode as it reads: [send], [steer] (a send
// while the agent works), [queue], [interrupt].
func promptModeLabel(mode int, p pane) string {
	name := promptModes[mode]
	if mode == promptSend && p.AgentState == "working" {
		name = "steer"
	}
	return "[" + name + "]"
}

// sendPrompt is turn-prompt.send (enter): the box's text to the agent in
// the box's mode, off the loop; the box clears once tuios took it.
func (a *App) sendPrompt(ctx ochazuke.Ctx) ochazuke.Result {
	pp := a.promptPane()
	if pp == nil {
		return ochazuke.Failed(errors.New("the prompt box writes to a Claude Code or omp pane, on the Turns tab"))
	}
	p := *pp
	text := strings.TrimRight(string(a.pb.input.Text), " \t\r\n")
	text = strings.TrimLeft(text, "\r\n")
	if strings.TrimSpace(text) == "" {
		return ochazuke.Failed(errors.New("nothing to send: type a prompt"))
	}
	d, ok := a.panesd.(promptDaemon)
	if !ok {
		return ochazuke.Failed(errors.New("sending needs the TUIOS daemon"))
	}
	mode := a.pb.mode
	if mode != promptQueue && p.needsInput() {
		return ochazuke.Failed(errors.New("the agent waits on you: answer it live (enter on its row), or queue the prompt (ctrl+t)"))
	}
	if mode == promptQueue && len(text) > daemon.QueueMaxText {
		return ochazuke.Failed(fmt.Errorf("a queued prompt is at most %d KiB; this one is %d KiB (send it instead)", daemon.QueueMaxText>>10, (len(text)+1023)>>10))
	}
	working := p.AgentState == "working"
	sleep := a.sleep
	label := promptModes[mode]
	if mode == promptSend && working {
		label = "steer"
	}
	sent := string(a.pb.input.Text)
	return ochazuke.Pending(func(c context.Context) (any, error) {
		switch mode {
		case promptQueue:
			got, err := d.PromptQueue(c, p, text)
			var de *daemon.Error
			if errors.As(err, &de) && de.Code == daemon.CodeQueueFull {
				return nil, fmt.Errorf("the pane's queue is full (tuios keeps %d prompts a pane): wait for the agent to rest, or drop one with tuios queue rm", daemon.QueueMax)
			}
			return got, err
		case promptInterrupt:
			if working {
				if err := d.PromptInterrupt(c, p); err != nil {
					return nil, err
				}
				if err := sleep(c, promptInterruptPause); err != nil {
					return nil, err
				}
			}
		}
		return nil, d.PromptSubmit(c, p, text)
	}).Label(label).Then(func(ctx ochazuke.Ctx, v any, err error) {
		if err != nil {
			return
		}
		// The box clears unless it was edited while the send ran.
		if in := a.pb.input; string(in.Text) == sent {
			if rerr := in.Replace(0, len(in.Text), ""); rerr != nil {
				ctx.Status(rerr.Error())
			}
		}
		if mode == promptInterrupt {
			a.pb.mode = promptSend
		}
		switch got, _ := v.(daemon.QueuePromptResult); {
		case mode == promptQueue && got.Delivering:
			ctx.Status("queued: tuios types it now, the agent is at rest")
		case mode == promptQueue:
			ctx.Status(fmt.Sprintf("queued (%d waiting): tuios types it when the agent rests", max(1, got.Queued)))
		}
		// The new turn (or the steer) shows once the log has it.
		if a.pv.turnsFor != "" && a.r.Instances[a.pv.turnsFor] != nil {
			if _, err := a.r.Execute(ochazuke.Request{Verb: "read-turns", Noun: "pane", Target: a.pv.turnsFor}); err != nil {
				ctx.Status(err.Error())
			}
		}
	})
}

// promptTargetFor points the completions at the pane and loads the
// context items they offer (on the loop: the store is the loop's).
func (a *App) promptTargetFor(p pane) {
	a.pb.target.set(compose.Harness(p.Harness), p.Cwd)
	if a.contexts == nil {
		return
	}
	items, err := a.contexts.All()
	if err != nil {
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Starred != items[j].Starred {
			return items[i].Starred
		}
		return items[i].Seen() > items[j].Seen()
	})
	var out []contextSuggestion
	for _, it := range items {
		if strings.TrimSpace(it.Text) == "" || it.Kind == "command" || it.Kind == "prompt" || strings.ContainsAny(it.Text, "\n") {
			continue
		}
		out = append(out, contextSuggestion{text: it.Text, kind: it.Kind})
		if len(out) == 500 {
			break
		}
	}
	a.pb.target.setContext(out)
}

// searchMentions answers @: the context items first, then the files under
// the pane's directory.
func (a *App) searchMentions(ctx context.Context, query string) ([]ochazuke.Suggestion, error) {
	_, cwd, items := a.pb.target.get()
	var out []ochazuke.Suggestion
	texts := make([]string, len(items))
	for i, it := range items {
		texts[i] = it.text
	}
	for _, m := range compose.Rank(query, texts, 8) {
		out = append(out, ochazuke.Suggestion{Text: m.Text, Detail: "context " + items[m.Index].kind})
	}
	if cwd == "" {
		return out, nil
	}
	for _, m := range compose.Rank(query, a.pb.sources.Files(ctx, cwd), 40) {
		out = append(out, ochazuke.Suggestion{Text: m.Text})
	}
	return out, nil
}

// searchCommands answers /: the harness's commands, then (Claude Code,
// where a skill is a command) its skills.
func (a *App) searchCommands(ctx context.Context, query string) ([]ochazuke.Suggestion, error) {
	harness, cwd, _ := a.pb.target.get()
	cmds := a.pb.sources.Commands(ctx, harness, cwd)
	names := make([]string, len(cmds))
	for i, c := range cmds {
		names[i] = c.Name
	}
	var out []ochazuke.Suggestion
	for _, m := range compose.Rank(query, names, 30) {
		c := cmds[m.Index]
		out = append(out, ochazuke.Suggestion{Text: c.Name, Detail: firstText(c.Description, c.Source)})
	}
	if harness == compose.Claude || harness == compose.Omp {
		skills := a.pb.sources.Skills(ctx, harness, cwd)
		forms := make([]string, len(skills))
		for i, s := range skills {
			forms[i] = strings.TrimPrefix(compose.SkillInvocation(harness, s), "/")
		}
		for _, m := range compose.Rank(query, forms, 20) {
			s := skills[m.Index]
			out = append(out, ochazuke.Suggestion{Text: m.Text, Detail: "skill · " + firstText(s.Description, s.Scope)})
		}
	}
	return out, nil
}

// searchSkills answers $: the skills the harness sees, each inserted in
// the harness's syntax (promptAfter drops the $ of a / form).
func (a *App) searchSkills(ctx context.Context, query string) ([]ochazuke.Suggestion, error) {
	harness, cwd, _ := a.pb.target.get()
	skills := a.pb.sources.Skills(ctx, harness, cwd)
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = s.Name
		if s.Plugin != "" {
			names[i] = s.Plugin + ":" + s.Name
		}
	}
	var out []ochazuke.Suggestion
	for _, m := range compose.Rank(query, names, 40) {
		s := skills[m.Index]
		out = append(out, ochazuke.Suggestion{Text: strings.TrimPrefix(compose.SkillInvocation(harness, s), "$"), Label: m.Text, Detail: firstText(s.Description, s.Scope)})
	}
	return out, nil
}

// firstText is the first non-empty text, on one line and short.
func firstText(texts ...string) string {
	for _, t := range texts {
		if t = strings.TrimSpace(t); t != "" {
			return oneLine(t)
		}
	}
	return ""
}

// promptState is what promptAfter compares with: whether the box had the
// focus, and the $ suggestions open on it before the message.
type promptState struct {
	focused bool
	skill   bool // the $ list was open on the box
	start   int  // the $ token's start
	rows    []ochazuke.Suggestion
	text    string
}

// promptBefore notes the box's state before a message.
func (a *App) promptBefore() promptState {
	if a.pb == nil {
		return promptState{}
	}
	st := promptState{focused: a.pb.focused}
	c := a.r.Completer()
	if c == nil || !c.Open() || c.Input() != promptID {
		return st
	}
	if tok, ok := c.Token(); ok && tok.Trigger == "$" {
		st.skill, st.start, st.rows, st.text = true, tok.Start, c.Suggestions(), string(a.pb.input.Text)
	}
	return st
}

// promptAfter runs after every message: a box that just got the focus
// points its completions at its pane, and a skill just accepted from the
// $ list is written in the harness's syntax: a / form loses the $ the
// completion kept, and a space follows it, so the cursor leaves the token
// and no / list opens on it.
func (a *App) promptAfter(before promptState) {
	if a.pb == nil {
		return
	}
	in := a.pb.input
	a.pb.focused = a.r.Focus == promptID
	if a.pb.focused && !before.focused {
		if p := a.promptPane(); p != nil {
			a.promptTargetFor(*p)
		}
	}
	if !before.skill || string(in.Text) == before.text {
		return
	}
	text := in.Text
	for _, s := range before.rows {
		if !strings.HasPrefix(s.Text, "/") {
			continue
		}
		accepted := []rune("$" + s.Text)
		end := before.start + len(accepted)
		if end > len(text) || in.Cursor != end || string(text[before.start:end]) != string(accepted) {
			continue
		}
		replace := s.Text
		if end == len(text) || text[end] != ' ' {
			replace += " "
		}
		if err := in.Replace(before.start, end, replace); err != nil {
			a.r.UI().Status(err.Error())
		}
		return
	}
}

// promptBoxBlock draws the box at width: a rule naming the mode in the
// mode's colour, then up to promptRows lines of the input, the cursor's
// when the box has the focus.
func (a *App) promptBoxBlock(p pane, width int) ochazuke.Block {
	in := a.pb.input
	s := ochazuke.NewStyles(*a.m.Appearance)
	style := a.promptModeStyle(s)
	label := promptModeLabel(a.pb.mode, p) + " " + p.kind()
	if p.needsInput() {
		label += " · waits on you"
	}
	hint := a.keySentence(promptID, keyAction{promptNoun + ".send", "sends"}, keyAction{promptNoun + ".mode", "mode"})
	rule := "── " + label + " "
	if hint != "" {
		rule += "─ " + hint + " "
	}
	rule = ansi.Truncate(rule, width, "…")
	if n := width - ansi.StringWidth(rule); n > 0 {
		rule += strings.Repeat("─", n)
	}
	rows := min(promptRows, max(1, strings.Count(string(in.Text), "\n")+1))
	fieldW := max(1, width-2)
	var field ochazuke.Block
	if a.r.Focus == promptID {
		field = ochazuke.InputBlock(in, fieldW, rows).Offset(2, 1)
	} else if len(in.Text) == 0 {
		a.r.Placed(promptID) // a stop after the turns even while empty
		field = ochazuke.Text(s.Muted.Render("write to the agent"))
	} else {
		field = ochazuke.Text(ochazuke.InputView(in, fieldW, rows))
	}
	lines := strings.Split(field.Text, "\n")
	for len(lines) < rows {
		lines = append(lines, "")
	}
	lines = lines[:rows]
	for i := range lines {
		prefix := "  "
		if i == 0 {
			prefix = "› "
		}
		lines[i] = prefix + lines[i]
	}
	return field.WithText(style.Render(rule) + "\n" + strings.Join(lines, "\n"))
}

// promptModeStyle is the mode's colour: the accent for a send (a steer
// too: its label says it), amber for queue, the error colour for interrupt; in mono only
// the label tells them apart.
func (a *App) promptModeStyle(s ochazuke.Styles) lipgloss.Style {
	switch a.pb.mode {
	case promptQueue:
		if a.m.Appearance.Theme == "mono" {
			return lipgloss.NewStyle()
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#D7A72F"))
	case promptInterrupt:
		return s.Error
	}
	return s.Accent
}

// promptBoxHeight is the rows the box takes at the bottom of the tab.
func (a *App) promptBoxHeight() int {
	return 1 + min(promptRows, max(1, strings.Count(string(a.pb.input.Text), "\n")+1))
}
