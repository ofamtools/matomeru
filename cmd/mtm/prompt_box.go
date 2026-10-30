package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	"github.com/unixsuperhero/matomeru/internal/harness"
)

// The prompt box: an input pinned below a live pane's turns (the Turns
// tab) and below its screen (the Screen tab, while no live terminal is
// drawn there), a focus stop of its own after the tree (} and tab from the
// tree, i from the tree or the pane row), that sends what is typed to the
// pane through tuios, never through the harness itself (no patch to omp,
// no hook of Claude's). Every live pane gets one: an agent of any harness
// and a plain shell or terminal (prompt.on-terminals, prompt.on-screen opt
// out). How it delivers is data, the pane's harness profile's promptBox
// (harness.PromptBox; promptBoxOf): deliver "queue" is an agent's, with
// the three modes below; deliver "send" is a terminal's, the text typed as
// one paste (bracketed when the program turned bracketed paste on, so a
// shell holds a multi-line text whole and runs none of it before the
// Enter) and then the program's Enter, or nothing with submit "none". A
// pane with no profile is an agent's box when tuios reports an agent in
// it, else a terminal's.
//
// An agent's box (deliver queue) has three modes:
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
//
// ctrl+v in insert mode (turn-prompt.paste) pastes from the OS clipboard:
// an image or an image file becomes an attachment, a chip [image N] at
// the cursor, sent as the harness takes files (prompt_attach.go). A read
// that finishes after the focus left the box still goes into the draft,
// where ctrl+v was pressed, and leaves the focus where it went. ctrl+v
// is free in ochazuke's insert-mode editor rows; normal mode's ctrl+v is
// visual block and stays.

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
	// attachments are the images pasted since the last send; the live
	// ones are those whose chip is still in the text (live).
	attachments []promptAttachment
	clip        clipboardReader
	// owner is the pane (its window id) whose draft the input holds;
	// drafts are the other panes' (prompt_drafts.go).
	owner  string
	drafts map[string]*promptDraft
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
// submit, or a paste alone), send-keys and queue-prompt. tuios provides
// it; tests replace it.
type promptDaemon interface {
	PromptSubmit(ctx context.Context, p pane, text string) error
	PromptPaste(ctx context.Context, p pane, text string) error
	PromptInterrupt(ctx context.Context, p pane) error
	PromptQueue(ctx context.Context, p pane, text string) (daemon.QueuePromptResult, error)
}

// PromptSubmit types text into the pane as one paste and submits it.
func (t tuios) PromptSubmit(ctx context.Context, p pane, text string) error {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	return t.Client.SendText(ctx, daemon.SendTextParams{Session: p.Session, Window: p.ID, Text: text, Submit: true})
}

// PromptPaste types text into the pane as a bracketed paste, unsubmitted
// (an attachment's path, for a harness that attaches a pasted path).
func (t tuios) PromptPaste(ctx context.Context, p pane, text string) error {
	return t.PasteText(ctx, p, text)
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
	a.pb = &promptBox{sources: compose.NewSources(compose.DefaultEnv()), clip: macClipboard{}}
	a.r.Define(promptNoun).
		Verb("send", a.sendPrompt, "Send the prompt to the pane: to an agent in the box's mode, send (a steer while it works), queue (typed when it rests) or interrupt (Escape, then send); to a terminal typed, then Enter unless its profile's promptBox.submit is none").
		Verb("mode", a.cyclePromptMode, "Switch the prompt box's mode: send, queue, interrupt (or the one named); an agent's box only").When("prompt-modes").
		Verb("back", a.promptBack, "Go back to the turns, else the pane row").
		Verb("context", a.promptContext, "Choose a context item or prompt of the box's pane, its project or no project, and write it at the cursor (a file as @path)").
		Verb("paste", a.pastePrompt, "Paste from the clipboard into the prompt: an image (or a copied image file) is attached as [image N], another copied file goes in as @path, text as text").
		// pane.attach's handler on the box's pane (no key: enter sends);
		// ctrl+a d comes back to the box.
		Verb("live", a.attachPane, "Attach the box's pane live, to answer what the agent asks; ctrl+a d comes back to the box").
		Verb("detach", a.detachPrompt, "Remove an attachment from the prompt: image n, the only one, else choose among them").
		Mode("insert").
		Bind("ctrl+v", ochazuke.Do(promptNoun+".paste").Hinted("paste/attach")).
		Bind("enter", ochazuke.Do(promptNoun+".send").Hinted("send")).
		Bind("ctrl+j", ochazuke.Do("input.insert", ochazuke.Arguments{"text": "\n"}).Hinted("newline")).
		Bind("alt+enter", ochazuke.Do("input.insert", ochazuke.Arguments{"text": "\n"}).Hide()).
		Bind("ctrl+t", ochazuke.Do(promptNoun+".mode").Hinted("mode")).
		Bind("alt+o", ochazuke.Do(promptNoun+".context").Hinted("context")).
		// up on the box's first line goes back up to the turns, as k
		// does in normal mode; elsewhere it moves the cursor up a line.
		Bind("up", ochazuke.Do(promptNoun+".back").Guard("prompt-top-line").Hide()).
		Mode("normal").
		Bind("enter", ochazuke.Do(promptNoun+".send").Hinted("send")).
		Bind("ctrl+t", ochazuke.Do(promptNoun+".mode").Hinted("mode")).
		Bind("alt+o", ochazuke.Do(promptNoun+".context").Hinted("context")).
		Bind("esc", ochazuke.Do(promptNoun+".back")).
		// The editor's own i (input.begin at the cursor), with a hint, so
		// the footer says how to write from the box's normal mode.
		Bind("i", ochazuke.Do("input.begin", ochazuke.Arguments{"position": "insert"}).Hinted("write")).
		// k (and up) on the box's first line goes back up to the turns, as
		// a motion past a list's edge spills to the stop drawn above;
		// elsewhere they move the cursor up a line. j on the last line
		// stays: the box is the bottom of the page.
		Bind("k", ochazuke.Do(promptNoun+".back").Guard("prompt-top-line").Hide()).
		Bind("up", ochazuke.Do(promptNoun+".back").Guard("prompt-top-line").Hide())
	a.r.DefineGuard("prompt-top-line", func(_ *ochazuke.Runtime, ev ochazuke.KeyEvent) bool {
		in := a.pb.input
		if in == nil || ev.Focus != promptID {
			return false
		}
		at := min(max(0, in.Cursor), len(in.Text))
		return !slices.Contains(in.Text[:at], '\n')
	})
	a.parameters(promptNoun+".mode", map[string]ochazuke.Parameter{"mode": {Type: "string", Enum: promptModes, Description: "The mode to switch to; none takes the next"}})
	a.parameters(promptNoun+".detach", map[string]ochazuke.Parameter{"n": {Type: "integer", Description: "The image's number, as its chip [image n] shows; none: the only one, else a chooser"}})
	// i writes a prompt: from the pane row while the Turns or Screen tab
	// shows, and from any row of the turns. Only a live pane has a box, so
	// on an exited one i stands aside (availability, not a guard row).
	a.r.DefineGuard("pane-prompt", func(_ *ochazuke.Runtime, ev ochazuke.KeyEvent) bool {
		return a.promptPaneAt(ev.Focus) != nil
	})
	// The modes are an agent's box's: on a terminal's ctrl+t stands aside.
	a.r.DefineGuard("prompt-modes", func(*ochazuke.Runtime, ochazuke.KeyEvent) bool {
		p := a.promptPane()
		return p == nil || a.promptBoxOf(*p).Deliver == harness.DeliverQueue
	})
	for _, noun := range []string{"pane", "turn", "turn-line", "turn-step", "turn-separator"} {
		a.r.Define(noun).
			Verb("prompt", a.focusPrompt, "Write to the pane: focus the prompt box below its turns or its screen, in insert mode").When("pane-prompt").
			Mode("normal").
			Bind("i", ochazuke.Do(noun+".prompt").Hinted("prompt"))
	}
	a.pb.input = ochazuke.NewInput(promptID, "", true)
	at := ochazuke.CompletionTrigger{Trigger: "@", Search: a.searchMentions}
	slash := ochazuke.CompletionTrigger{Trigger: "/", Search: a.searchCommands}
	skill := ochazuke.CompletionTrigger{Trigger: "$", Search: a.searchSkills}
	return a.r.AddInput(a.pb.input, ochazuke.Parent(panesPage), ochazuke.Kind(promptNoun), ochazuke.Completions(at, slash, skill))
}

// Settings that opt out of the box where it is new.
const (
	// settingPromptTerminals gives a terminal (a pane with no agent and no
	// harness profile: a shell, a REPL) the box.
	settingPromptTerminals = "prompt.on-terminals"
	// settingPromptScreen draws the box below the Screen tab's screen.
	settingPromptScreen = "prompt.on-screen"
)

var (
	promptTerminalsSetting = ochazuke.Setting{Name: settingPromptTerminals, Type: ochazuke.SettingBoolean, Default: true,
		Description: "Give a terminal (a shell or another program with no agent in it) the prompt box: what is typed goes in as one paste, then Enter"}
	promptScreenSetting = ochazuke.Setting{Name: settingPromptScreen, Type: ochazuke.SettingBoolean, Default: true,
		Description: "Draw the prompt box below the Screen tab's screen too, not only below the Turns tab's turns"}
)

// promptRow is the row of the pane the box writes to: on the Turns tab
// the pane whose turns it shows, on the Screen tab the selected pane while
// its screen (not a live terminal or copy mode) is drawn. "" is none.
func (a *App) promptRow() string {
	switch a.pv.tab {
	case tabTurns:
		return a.pv.turnsFor
	case tabScreen:
		if !a.r.Settings().Bool(settingPromptScreen) || a.liveShown() || a.copyShown(paneTermID) {
			return ""
		}
		row, _ := a.selectedRow()
		return row
	}
	return ""
}

// promptPane is the pane the box writes to (promptRow), when the box
// serves it. nil is none.
func (a *App) promptPane() *pane {
	row := a.promptRow()
	if row == "" {
		return nil
	}
	p, err := a.paneAt(a.r.Instances[row])
	if err != nil || !a.promptServes(p) {
		return nil
	}
	return &p
}

// promptPaneAt is the pane i at focus would write to: a pane row's own
// (on the Turns or Screen tab), else the shown pane's for a row of the
// turns.
func (a *App) promptPaneAt(focus string) *pane {
	if a.pv.tab != tabTurns && a.promptRow() == "" {
		return nil
	}
	if inst := a.r.Instances[focus]; inst != nil && inst.Noun == "pane" {
		p, err := a.paneAt(inst)
		if err != nil || !a.promptServes(p) {
			return nil
		}
		return &p
	}
	return a.promptPane()
}

// promptServes reports whether the pane gets a prompt box: any live pane,
// a terminal unless prompt.on-terminals is off.
func (a *App) promptServes(p pane) bool {
	if p.Exited {
		return false
	}
	_, terminal := a.promptBoxFor(p)
	return !terminal || a.r.Settings().Bool(settingPromptTerminals)
}

// promptBoxOf is how the box writes to the pane (promptBoxFor).
func (a *App) promptBoxOf(p pane) harness.PromptBox {
	b, _ := a.promptBoxFor(p)
	return b
}

// promptBoxFor is the pane's harness profile's promptBox; a pane with no
// profile has harness.AgentPromptBox when tuios reports an agent in it,
// else harness.TerminalPromptBox, and is then a terminal.
func (a *App) promptBoxFor(p pane) (box harness.PromptBox, terminal bool) {
	if h, ok := a.harnessOf(p); ok {
		return h.Box(), false
	}
	if p.isAgent() {
		return harness.AgentPromptBox, false
	}
	return harness.TerminalPromptBox, true
}

// focusPrompt is pane.prompt and turn*.prompt (i): show the row's turns
// when they are not shown, then focus the box in insert mode, the cursor
// after the draft.
func (a *App) focusPrompt(ctx ochazuke.Ctx) ochazuke.Result {
	if ctx.Instance != nil && ctx.Instance.Noun == "pane" && a.pv.tab == tabTurns && a.pv.turnsFor != ctx.Instance.ID {
		if err := a.showTurns(ctx.Instance.ID); err != nil {
			return ochazuke.Failed(err)
		}
	}
	a.syncPromptDraft()
	p := a.promptPane()
	if p == nil {
		return ochazuke.Failed(errNoPromptBox)
	}
	a.promptTargetFor(*p)
	from := a.r.Focus
	if err := a.r.SetFocus(promptID); err != nil {
		return ochazuke.Failed(err)
	}
	if from != promptID {
		// Coming in from the row or a turn, i writes after the draft
		// (like A), never inside it where normal mode's cursor was left.
		a.pb.input.Cursor = len(a.pb.input.Text)
	}
	a.pb.input.SetMode("insert")
	return ochazuke.Done(promptID)
}

// errNoPromptBox is a box verb's answer where no box is drawn.
var errNoPromptBox = errors.New("no prompt box here: it writes to a live pane, below the Turns or Screen tab")

// promptBack is turn-prompt.back (esc in normal mode): the turns, else
// the pane row.
func (a *App) promptBack(ochazuke.Ctx) ochazuke.Result {
	if a.pv.tab == tabTurns && a.turnsList().Len() > 0 {
		if err := a.focusTurnsView(); err == nil {
			return ochazuke.Done(a.r.Focus)
		}
	}
	target := paneListID
	if row := a.promptRow(); a.r.Instances[row] != nil {
		target = row
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
		return ochazuke.Failed(errNoPromptBox)
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
// while the agent works), [queue], [interrupt]; a terminal's box is
// [send], or [type] when it leaves the text unsubmitted.
func promptModeLabel(mode int, p pane, box harness.PromptBox) string {
	if box.Deliver == harness.DeliverSend {
		if box.Submit == harness.SubmitNone {
			return "[type]"
		}
		return "[send]"
	}
	name := promptModes[mode]
	if mode == promptSend && p.working() {
		name = "steer"
	}
	return "[" + name + "]"
}

// promptMode is the mode the box sends to p in: its mode for an agent's
// box, send for a terminal's (it has no other).
func (a *App) promptMode(p pane) int {
	if a.promptBoxOf(p).Deliver == harness.DeliverSend {
		return promptSend
	}
	return a.pb.mode
}

// sendPrompt is turn-prompt.send (enter): the box's text to the agent in
// the box's mode, off the loop; the box clears once tuios took it.
func (a *App) sendPrompt(ctx ochazuke.Ctx) ochazuke.Result {
	a.syncPromptDraft()
	pp := a.promptPane()
	if pp == nil {
		return ochazuke.Failed(errNoPromptBox)
	}
	p := *pp
	box := a.promptBoxOf(p)
	text := strings.TrimRight(string(a.pb.input.Text), " \t\r\n")
	text = strings.TrimLeft(text, "\r\n")
	if strings.TrimSpace(text) == "" {
		return ochazuke.Failed(errors.New("nothing to send: type a prompt"))
	}
	d, ok := a.panesd.(promptDaemon)
	if !ok {
		return ochazuke.Failed(errors.New("sending needs the TUIOS daemon"))
	}
	mode := a.promptMode(p)
	// The attachments go as the harness takes files: @ mentions in the
	// text, or (a harness that attaches a pasted path, outside queue
	// mode) a paste of each path before it.
	attached := a.pb.live()
	var pastes []string
	if mode != promptQueue && a.attachMode(p) == harness.AttachPaste {
		for _, at := range attached {
			pastes = append(pastes, at.path)
		}
		text = strings.TrimSpace(promptOutgoing(text, attached, false))
	} else {
		text = promptOutgoing(text, attached, true)
	}
	if mode != promptQueue && p.needsInput() {
		return ochazuke.Failed(errors.New("the agent waits on you: answer it live (turn-prompt.live from here, enter on its row), or queue the prompt (ctrl+t)"))
	}
	if mode == promptQueue && len(text) > daemon.QueueMaxText {
		return ochazuke.Failed(fmt.Errorf("a queued prompt is at most %d KiB; this one is %d KiB (send it instead)", daemon.QueueMaxText>>10, (len(text)+1023)>>10))
	}
	working := p.working()
	sleep := a.sleep
	label := promptModes[mode]
	if mode == promptSend && working {
		label = "steer"
	}
	sent := string(a.pb.input.Text)
	// The send is a job; prompt.after-submit (the task picker) opens over
	// the page now, while it runs, and never holds it up.
	a.afterSubmit(ctx, p)
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
		for _, path := range pastes {
			if err := d.PromptPaste(c, p, path); err != nil {
				return nil, err
			}
		}
		if len(pastes) > 0 {
			if err := sleep(c, promptAttachPause); err != nil {
				return nil, err
			}
		}
		// One send-text: the text as one paste and, with submit, the
		// Enter after it, so a failure never leaves half of it run.
		if box.Submit == harness.SubmitNone {
			return nil, d.PromptPaste(c, p, text)
		}
		return nil, d.PromptSubmit(c, p, text)
	}).Label(label).Then(func(ctx ochazuke.Ctx, v any, err error) {
		if err != nil {
			return
		}
		// The pane's draft clears unless it was edited while the send ran
		// (the box may show another pane's by now); its attachments go
		// with the text, and stay as the pane's context.
		if _, rerr := a.clearPromptDraft(p.ID, sent); rerr != nil {
			ctx.Error(rerr.Error())
		}
		a.recordAttachments(ctx, p, attached)
		if mode == promptInterrupt {
			a.pb.mode = promptSend
		}
		// A queued prompt is a success: a notice, never the error line.
		switch got, _ := v.(daemon.QueuePromptResult); {
		case mode == promptQueue && got.Delivering:
			ctx.Notify("queued: tuios types it now, the agent is at rest")
		case mode == promptQueue:
			ctx.Notify(fmt.Sprintf("queued (%d waiting): tuios types it when the agent rests", max(1, got.Queued)))
		}
		// The new turn (or the steer) shows once the log has it, and the
		// turns follow it (only a send moves them; focusing the box does
		// not).
		if a.pv.tab != tabTurns {
			return // the Screen tab shows the send with its next refresh
		}
		a.followNewest()
		if a.pv.turnsFor != "" && a.r.Instances[a.pv.turnsFor] != nil {
			if _, err := a.r.Execute(ochazuke.Request{Verb: "read-turns", Noun: "pane", Target: a.pv.turnsFor}); err != nil {
				ctx.Error(err.Error())
			}
		}
	})
}

// followNewest selects the newest turn and keeps it selected as the read
// after a send lands: on cards ochazuke.timeline.goto last, which resumes
// following the end; in the tree the last turn row, now and after the read.
func (a *App) followNewest() {
	if a.turnCards() {
		if a.pv.cards == nil {
			return
		}
		if _, err := a.r.Execute(ochazuke.Request{Noun: "ochazuke.timeline", Verb: "goto", Args: ochazuke.Arguments{"id": a.pv.cards.ID(), "position": "last"}}); err != nil {
			a.r.UI().Error(err.Error())
		}
		return
	}
	a.pv.sent = true
	a.selectNewestTurnRow()
}

// selectNewestTurnRow selects the tree's last turn row.
func (a *App) selectNewestTurnRow() {
	items := a.pv.turns.List().Items()
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Noun == "turn" {
			if err := a.pv.turns.Select(items[i].ID); err != nil {
				a.r.UI().Error(err.Error())
			}
			return
		}
	}
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
			a.r.UI().Error(err.Error())
		}
		return
	}
}

// promptBoxBlock draws the box at width: a rule naming the mode in the
// mode's colour, then up to promptRows lines of the input, the cursor's
// when the box has the focus.
func (a *App) promptBoxBlock(p pane, width int) ochazuke.Block {
	in := a.pb.input
	s := a.m.Styles()
	// The rule shows whether the box has the focus: the mode's colour
	// (bold in mono) while it does, muted (dim in mono) while it does not.
	// Only the style changes, never the text, so nothing moves.
	style := s.Muted
	if a.r.Focus == promptID {
		style = a.promptModeStyle(s)
	}
	box := a.promptBoxOf(p)
	label := promptModeLabel(a.promptMode(p), p, box) + " " + p.kind()
	if p.needsInput() {
		label += " · waits on you"
	}
	if n := attachmentsLabel(len(a.pb.live())); n != "" {
		label += " · " + n
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
		if len(in.Text) == 0 && a.r.BaseMode() == "normal" {
			// The empty box in normal mode says how to write and leave,
			// from its rows, under the cursor.
			var cue []string
			if k := a.keyForRequest(promptID, "input.begin", ochazuke.Arguments{"position": "insert"}); k != "" {
				cue = append(cue, k+" to write")
			}
			if back := a.keySentence(promptID, keyAction{promptNoun + ".back", "back"}); back != "" {
				cue = append(cue, back)
			}
			field = field.WithText(s.Muted.Render(strings.Join(cue, " · ")))
		}
	} else if len(in.Text) == 0 {
		a.r.Placed(promptID) // a stop after the turns even while empty
		field = ochazuke.Text(s.Muted.Render(promptPlaceholder(box)))
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

// promptPlaceholder is the empty, unfocused box's text.
func promptPlaceholder(box harness.PromptBox) string {
	if box.Deliver == harness.DeliverSend {
		return "type into the pane"
	}
	return "write to the agent"
}

// promptModeStyle is the mode's colour: the accent for a send (a steer
// too: its label says it), amber for queue, the error colour for interrupt; in mono only
// the label tells them apart.
func (a *App) promptModeStyle(s ochazuke.Styles) lipgloss.Style {
	mode := a.pb.mode
	if p := a.promptPane(); p != nil {
		mode = a.promptMode(*p)
	}
	switch mode {
	case promptQueue:
		if a.m.Appearance.Theme == "mono" {
			return lipgloss.NewStyle().Bold(true)
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
