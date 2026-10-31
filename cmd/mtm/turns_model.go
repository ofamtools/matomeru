package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	xansi "github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
	"ofam.dev/ochazuke/markdown"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/harness"
	"ofam.dev/matomeru/internal/turns"
)

// Turns read from the agent's own log or the shell's command marks
// (internal/turns), for the Panes page's Turns tab:
//
//   - A Claude Code or omp pane's turns come from its session JSONL, found
//     through the agent_session_id tuios reports (list-agents), or the
//     live session the harness's own records name (pane_locate.go). Every
//     session the pane ran is kept (work.PaneSessions), so a /clear
//     chains the new session after the old with a separator row. The
//     logs are read off the loop, from where the last read stopped, on
//     every listing while the tab shows the pane. omp's log is a tree
//     (internal/turns/omp.go): the turns follow its current branch. When
//     the Claude id tuios reports has no transcript (a resume or restart),
//     the current session's transcript is found as claude_session.go says
//     (turns.ResolveClaudeLog), or not at all.
//   - A shell pane whose shell sends OSC 133 marks has a turn per
//     command (tuios command-history): the prompt is the command line, the
//     response its output, read only when the turn is opened or copied.
//     A new command (the window's command_seq) reads the history again.
//   - Any other pane, or a Claude pane whose log is not found, keeps the
//     turns mtm's capture hooks journaled (turns.go): the fallback,
//     which tries the log again on a backoff and on pane.refresh-turns
//     (turns_refresh.go).
//
// Every turn is closed when it appears, the running one too; the running
// turn's row carries activeTurnMarker and is drawn bold while older rows
// are muted. Thinking and tool calls follow the turns.* settings: shown
// or not, open or folded when a turn is opened.
//
// Reading the session log skips tuios's transcript rule (agent-transcript
// answers only the person, with a live human nonce): a deliberate choice,
// since the log is the owner's own file, readable by the owner anyway, and
// mtm is the owner's own UI. A process inside a pane that runs mtm's CLI
// can read the turns this way too.
//
// An agent pane's tab ends in a prompt box (prompt_box.go) that sends to
// the agent through tuios. The prompts picker, a later slice, reads
// paneTurns.list.

// The turn sources.
const (
	turnsJournal = iota // mtm's capture hooks (turns.go)
	turnsClaude
	turnsShell
	turnsOmp
)

// The turns settings.
const (
	settingTurnsThinking     = "turns.thinking"
	settingTurnsThinkingOpen = "turns.thinking-open"
	settingTurnsTools        = "turns.tools"
	settingTurnsToolsOpen    = "turns.tools-open"
)

// activeTurnMarker leads the running turn's row: brightness alone does not
// show in mono.
const activeTurnMarker = "●"

// Limits of what an opened turn draws; a copy takes everything.
const (
	turnOutputShown = 1000 // a shell output's last lines
	turnResultShown = 200  // a tool result's first lines
	turnInputShown  = 40   // a tool input's first lines
)

// turnCopyLimit is the largest text y puts on the clipboard; a larger one
// is offered as a file or a pager instead.
const turnCopyLimit = 100 * 1024

// paneTurns is what mtm has read of one pane's turns.
type paneTurns struct {
	window   string
	chain    *turns.LogChain
	list     []turns.Turn
	loaded   bool // a read finished
	loading  bool
	fallback bool            // no session log of the pane was found: the journal's turns, until a retry or refresh finds one
	cur      turnsResolution // the current session's log, as found (turns_refresh.go)
	err      string          // why the last read failed
	seq      uint64          // the command_seq the shell turns were read at
	running  string          // the command the shell ran then
	outputs  map[string]*turnOutput
	note     string // the pane's SessionNote last put up
}

// turnOutput is a shell turn's output, read when the turn opened.
type turnOutput struct {
	lines   []string
	err     string
	loading bool
}

// turnDaemon is the part of the daemon shell turns read: the command
// history and the buffer a range at a time. tuios provides it.
type turnDaemon interface {
	Commands(ctx context.Context, p pane) ([]daemon.CommandEntry, error)
	CaptureRows(ctx context.Context, p pane, start, end int, unwrap bool) ([]string, error)
}

// pagerOpener opens a file in a pager in a new pane of p's session.
type pagerOpener interface {
	OpenPager(ctx context.Context, p pane, path string) error
}

// CaptureRows is lines start to end (1-based) of the pane's buffer, the
// blank end trimmed; with unwrap the soft-wrapped rows are joined and the
// range counts joined lines.
func (t tuios) CaptureRows(ctx context.Context, p pane, start, end int, unwrap bool) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	got, err := t.Capture(ctx, daemon.CaptureParams{Session: p.Session, Window: p.ID, Source: "recent", Start: &start, End: &end, Unwrap: unwrap, Trim: true})
	if err != nil {
		return nil, err
	}
	content := strings.TrimSuffix(got.Content, "\n")
	if content == "" {
		return nil, nil
	}
	return strings.Split(content, "\n"), nil
}

// OpenPager runs $PAGER (else less -R) on path in a new window of p's
// session, focused.
func (t tuios) OpenPager(ctx context.Context, p pane, path string) error {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	cmd := strings.Fields(os.Getenv("PAGER"))
	if len(cmd) == 0 {
		cmd = []string{"less", "-R"}
	}
	_, err := t.NewWindow(ctx, daemon.NewWindowParams{Session: p.Session, Name: "pager", Cwd: p.Cwd, Focus: true, Command: append(cmd, path)})
	return err
}

// paneCapturer reads one pane's buffer for turns.ShellOutput.
type paneCapturer struct {
	d turnDaemon
	p pane
}

func (c paneCapturer) Capture(ctx context.Context, start, end int, unwrap bool) ([]string, error) {
	return c.d.CaptureRows(ctx, c.p, start, end, unwrap)
}

// isClaude reports whether the pane runs Claude Code.
func isClaude(p pane) bool {
	return strings.HasPrefix(p.Harness, "claude") || p.Harness == "" && strings.HasPrefix(p.LaunchHarness, "claude")
}

// isOmp reports whether the pane runs omp (oh-my-pi).
func isOmp(p pane) bool {
	return p.Harness == "omp" || p.Harness == "" && strings.HasPrefix(p.LaunchHarness, "omp")
}

// paneTurnsOf is the pane's read turns, made on first use.
func (a *App) paneTurnsOf(window string) *paneTurns {
	if a.mturns == nil {
		a.mturns = map[string]*paneTurns{}
	}
	pt := a.mturns[window]
	if pt == nil {
		pt = &paneTurns{window: window, chain: &turns.LogChain{}, outputs: map[string]*turnOutput{}}
		a.mturns[window] = pt
	}
	return pt
}

// turnKind is where the pane's turns come from.
func (a *App) turnKind(p pane) int {
	if pt := a.mturns[p.ID]; pt != nil && pt.fallback {
		return turnsJournal
	}
	return a.logKind(p)
}

// agentSessions are the sessions the pane ran, oldest first, the one it
// runs now last.
func (a *App) agentSessions(p pane) []string {
	var out []string
	if rec, err := a.work.PaneSessions(p.ID); err == nil {
		out = append(out, rec.Sessions...)
	}
	if p.AgentSession != "" && !slices.Contains(out, p.AgentSession) {
		out = append(out, p.AgentSession)
	}
	return out
}

// recordAgentSessions keeps every Claude or omp pane's session as tuios reports
// it, so a /clear later still chains the turns before it.
func (a *App) recordAgentSessions(panes []pane) {
	if a.sessionSeen == nil {
		a.sessionSeen = map[string]string{}
	}
	for _, p := range panes {
		if p.ID == "" || p.AgentSession == "" || !(isClaude(p) || isOmp(p)) || a.sessionSeen[p.ID] == p.AgentSession {
			continue
		}
		if _, err := a.work.AddPaneSession(p.ID, p.Harness, p.AgentSession); err != nil {
			a.r.UI().Error("turns: " + err.Error())
			continue
		}
		a.sessionSeen[p.ID] = p.AgentSession
	}
}

// claudeRead is a session log read's answer (what the CLI prints).
type claudeRead struct {
	Turns   []turns.Turn `json:"turns"`
	NoLog   bool         `json:"no_log,omitempty"`  // no log of any of the pane's sessions exists
	Problem string       `json:"problem,omitempty"` // a log that could not be read; the others' turns stand
	// Session is the session of the log of the pane's current session ("" not
	// found): tuios's id, or the one ResolveClaudeLog fell back to.
	Session string `json:"session,omitempty"`
	Notice  string `json:"notice,omitempty"` // what pane.refresh-turns found (turnsNotice)
}

// readPaneTurns is pane.read-turns: the pane's turns read again off the
// loop (a Claude log from where the last read stopped, a shell's command
// history), then the Turns tab refilled. A pane on the journal fallback
// tries its log again when its retry is due (turns_refresh.go).
func (a *App) readPaneTurns(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	pt := a.paneTurnsOf(p.ID)
	kind := a.turnKind(p)
	if a.retryDue(pt, p) {
		kind = a.logKind(p)
	}
	switch kind {
	case turnsClaude, turnsOmp:
		return a.readLogTurns(p, pt, false)
	case turnsShell:
		d, ok := a.panesd.(turnDaemon)
		if !ok {
			return ochazuke.Failed(errors.New("shell turns need the TUIOS daemon"))
		}
		pt.loading = true
		seq, running := p.CommandSeq, ""
		if !p.AtPrompt {
			running = p.RunningCmdline
		}
		return ochazuke.Pending(func(c context.Context) (any, error) {
			entries, err := d.Commands(c, p)
			if err != nil {
				return nil, err
			}
			cmds := make([]turns.Command, len(entries))
			for i, e := range entries {
				cmds[i] = turns.Command{Seq: e.Seq, Cmdline: e.Cmdline, Exit: e.Exit, At: e.At, DurationMS: e.DurationMS, Cwd: e.Cwd, Row: e.Row}
			}
			return turns.ShellTurns(p.ID, cmds, running), nil
		}).Replacing("turns-read").Then(func(_ ochazuke.Ctx, v any, err error) {
			pt.loading = false
			if err != nil {
				pt.err = err.Error()
				a.turnsRead(p.ID)
				return
			}
			list, _ := v.([]turns.Turn)
			// A command's rows move when the history drops its oldest
			// lines: a read output is read again when its turn opens.
			for id := range pt.outputs {
				if !slices.ContainsFunc(list, func(t turns.Turn) bool { return t.ID == id && t.State != turns.StateRunning }) {
					delete(pt.outputs, id)
				}
			}
			pt.list, pt.loaded, pt.err, pt.seq, pt.running = list, true, "", seq, running
			a.turnsRead(p.ID)
		})
	}
	return ochazuke.Done(nil)
}

// readLogTurns reads the pane's session logs into its chain off the loop:
// the sessions not chained yet, the current one only while the tab holds
// no log for it. Its answer refills the tab, or puts the pane on the
// journal fallback with its next retry; notify puts what was found in the
// status line (pane.refresh-turns).
func (a *App) readLogTurns(p pane, pt *paneTurns, notify bool) ochazuke.Result {
	sessions, chain, find := a.agentSessions(p), pt.chain, a.sessionLogFinder(p)
	if find == nil {
		find = func(string) turns.Log { return nil } // no format to read: the journal fallback
	}
	current := p.AgentSession
	known := a.resolvedSession(p.ID, current)
	pt.loading = true
	return ochazuke.Pending(func(context.Context) (any, error) {
		have := chain.Sessions()
		out := claudeRead{}
		if known != "" && slices.Contains(have, known) {
			out.Session = known
		}
		for _, s := range sessions {
			if slices.Contains(have, s) || s == current && out.Session != "" {
				continue
			}
			if log := find(s); log != nil {
				chain.AddLog(log)
				if s == current {
					out.Session = log.SessionID()
				}
			}
		}
		if current != "" && out.Session == "" && slices.Contains(chain.Sessions(), current) {
			out.Session = current
		}
		if len(chain.Sessions()) == 0 {
			out.NoLog = true
		} else {
			if _, err := chain.Read(); err != nil {
				out.Problem = err.Error()
			}
			out.Turns = chain.Turns()
		}
		if notify {
			out.Notice = turnsNotice(current, out)
		}
		return out, nil
	}).Replacing("turns-read").Then(func(_ ochazuke.Ctx, v any, err error) {
		if pt.chain != chain {
			return // a refresh started again from nothing
		}
		pt.loading = false
		got, _ := v.(claudeRead)
		if err == nil {
			found := got.Session != ""
			if pt.cur.reported != current {
				pt.cur = turnsResolution{reported: current}
			}
			pt.cur.session = got.Session
			if found {
				pt.cur.tries, pt.cur.retryAt = 0, time.Time{}
			}
		}
		switch {
		case err != nil:
			pt.err = err.Error()
		case got.NoLog:
			pt.fallback = true
			pt.cur.tries++
			pt.cur.retryAt = a.now().Add(turnsRetryAfter(pt.cur.tries))
		default:
			pt.fallback = false
			pt.list, pt.loaded, pt.err = got.Turns, true, got.Problem
		}
		if notify && got.Notice != "" {
			a.r.UI().Info(got.Notice)
		}
		if p.SessionNote != "" && (notify || pt.note != p.SessionNote) {
			a.r.UI().Info("turns: " + p.SessionNote) // once, and on each refresh
		}
		pt.note = p.SessionNote
		a.turnsRead(p.ID)
	})
}

// sessionLogFinder finds a session's log for the pane, by its harness's
// log format (harness.Harness.Log, data in harnesses.json) through
// logFinders; nil when the harness names no format mtm reads. The finder
// returns nil for a session whose log it does not find.
func (a *App) sessionLogFinder(p pane) func(session string) turns.Log {
	h, ok := a.harnessOf(p)
	if !ok {
		return nil
	}
	finder := logFinders[h.Log]
	if finder == nil {
		return nil
	}
	return finder(a, p)
}

// logFinders are the session log readers by format: how to find a
// session's log and follow it. A format's log gives its activities when
// it implements turns.ActivityReader.
var logFinders = map[string]func(a *App, p pane) func(session string) turns.Log{
	harness.LogClaude: claudeLogFinder,
	harness.LogOmp:    ompLogFinder,
}

// claudeLogFinder finds Claude Code's logs under its projects directory
// (the pane's cwd first; the current session through
// turns.ResolveClaudeLog, which falls back when tuios's id is stale).
func claudeLogFinder(a *App, p pane) func(session string) turns.Log {
	projects, ws, listing, started := a.claudeProjects, a.work, slices.Clone(a.panes), a.agentStartedFunc()
	return func(s string) turns.Log {
		if s == p.AgentSession {
			// The current session: tuios's id, else the resume fallback
			// (claude_session.go).
			path, session, err := turns.ResolveClaudeLog(claudeLookup(projects, ws, p, listing, started))
			if err != nil {
				return nil
			}
			return turns.NewClaudeLog(path, session)
		}
		path, err := turns.FindClaudeLog(projects, p.Cwd, s)
		if err != nil {
			return nil
		}
		return turns.NewClaudeLog(path, s)
	}
}

// ompLogFinder finds omp's logs under its sessions directory.
func ompLogFinder(a *App, _ pane) func(session string) turns.Log {
	sessions := a.ompSessions
	return func(s string) turns.Log {
		path, err := turns.FindOmpLog(sessions, s)
		if err != nil {
			return nil
		}
		return turns.NewOmpLog(path, s)
	}
}

// turnsRead refills the Turns tab when it shows the pane read.
func (a *App) turnsRead(window string) {
	row := a.pv.turnsFor
	if row == "" || a.r.Instances[row] == nil {
		return
	}
	if p, err := a.paneAt(a.r.Instances[row]); err != nil || p.ID != window {
		return
	}
	if err := a.showTurns(row); err != nil {
		a.r.UI().Error(err.Error())
	}
	a.selectWantedCall()
	if a.pv.sent && !a.turnCards() {
		a.pv.sent = false
		a.selectNewestTurnRow()
	}
	a.settingsRead(window)
}

// pollTurns reads the shown pane's turns again after a listing: a Claude
// log every time (the read starts where the last stopped), a shell's
// history when it finished or started a command.
func (a *App) pollTurns() {
	if !a.turnsHistoryActive() {
		return
	}
	if a.pv.tab != tabTurns || a.pv.turnsFor == "" || a.r.Instances[a.pv.turnsFor] == nil {
		return
	}
	p, err := a.paneAt(a.r.Instances[a.pv.turnsFor])
	if err != nil {
		return
	}
	if isCodex(p) && p.AgentSession != "" {
		// Codex's turns are the journal's; its settings line reads the
		// rollout's end on each listing.
		if _, err := a.r.Execute(ochazuke.Request{Verb: "read-settings", Noun: "pane", Target: a.pv.turnsFor}); err != nil {
			a.r.UI().Error(err.Error())
		}
	}
	pt := a.paneTurnsOf(p.ID)
	a.subs.gen++ // a running subagent's log is read again (wantAgents)
	if pt.loading {
		return
	}
	running := ""
	if !p.AtPrompt {
		running = p.RunningCmdline
	}
	switch a.turnKind(p) {
	case turnsShell:
		if pt.loaded && pt.seq == p.CommandSeq && pt.running == running {
			return
		}
	case turnsClaude, turnsOmp:
	default:
		// The journal fallback tries the pane's log again when due.
		if !a.retryDue(pt, p) {
			return
		}
	}
	if _, err := a.r.Execute(ochazuke.Request{Verb: "read-turns", Noun: "pane", Target: a.pv.turnsFor}); err != nil {
		a.r.UI().Error(err.Error())
	}
}

// liveTurns are the pane's turns as the tab draws them: an agent's turn
// still open in a log whose agent is no longer working is done.
func liveTurns(pt *paneTurns, p pane) []turns.Turn {
	list := pt.list
	if n := len(list); n > 0 && list[n-1].State == turns.StateRunning && (list[n-1].Source == turns.SourceClaude || list[n-1].Source == turns.SourceOmp) && !p.working() && !p.needsInput() {
		list = slices.Clone(list)
		list[n-1].State = turns.StateDone
	}
	return list
}

// showModelTurns fills the Panes page's tree with the turns read for p
// (reading them first when none were); fresh closes every turn.
func (a *App) showModelTurns(row string, p pane, fresh bool) error {
	pt := a.paneTurnsOf(p.ID)
	// A read that failed waits for the next listing (pollTurns) to try again.
	if !pt.loaded && !pt.loading && pt.err == "" {
		if _, err := a.r.Execute(ochazuke.Request{Verb: "read-turns", Noun: "pane", Target: row}); err != nil {
			return err
		}
	}
	tree := a.pv.turns
	st := a.turnFillOf(tree)
	if fresh || !st.model || st.window != p.ID {
		st.known, st.built, st.entered = map[string]bool{}, map[string]bool{}, false
	}
	st.prefix, st.window, st.back, st.model = "turn:", p.ID, turnRef{Back: row}, true
	st.mturns, st.source = liveTurns(pt, p), pt
	return a.refillModelTurns(tree, st)
}

// refillModelTurns pushes a model fill to its tree and closes what is new:
// a turn always, a thinking or tool step unless its setting opens it.
func (a *App) refillModelTurns(tree *ochazuke.Tree, st *turnFill) error {
	if err := a.setTurnNodes(tree, st); err != nil {
		return err
	}
	kids := map[string]bool{}
	for _, n := range st.nodes {
		kids[n.Parent] = true
	}
	var unknown []ochazuke.TreeNode
	open := false
	for _, n := range st.nodes {
		if !kids[n.ID] {
			continue
		}
		if !st.known[n.ID] {
			unknown = append(unknown, n)
		} else if n.Noun == "turn" && !tree.Collapsed(n.ID) {
			open = true
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	if !open {
		// No turn is open, so nothing the person opened can close: one pass.
		for _, n := range unknown {
			st.known[n.ID] = true
		}
		return tree.CollapseAll()
	}
	for _, n := range unknown {
		st.known[n.ID] = true
		ref, _ := n.Data.(turnRef)
		if err := tree.SetCollapsed(n.ID, !(n.Noun == "turn-step" && a.stepOpen(ref.Step))); err != nil {
			return err
		}
	}
	return nil
}

// stepShown and stepOpen read the turns settings for a step kind.
func (a *App) stepShown(kind turns.StepKind) bool {
	switch kind {
	case turns.StepThinking:
		return a.r.Settings().Bool(settingTurnsThinking)
	case turns.StepTool:
		return a.r.Settings().Bool(settingTurnsTools)
	}
	return true
}

func (a *App) stepOpen(kind string) bool {
	switch turns.StepKind(kind) {
	case turns.StepThinking:
		return a.r.Settings().Bool(settingTurnsThinkingOpen)
	case turns.StepTool:
		return a.r.Settings().Bool(settingTurnsToolsOpen)
	}
	return false
}

// turnSettingsChanged redraws the open turns: steps are shown or hidden,
// and every step takes its open or folded state from the settings again.
func (a *App) turnSettingsChanged() {
	a.refoldCards()
	for _, tree := range []*ochazuke.Tree{a.pv.turns} {
		st := a.turnFillOf(tree)
		if !st.model {
			continue
		}
		for id := range st.known {
			if strings.Contains(id, "/step/") {
				delete(st.known, id)
			}
		}
		if err := a.refillModelTurns(tree, st); err != nil {
			a.r.UI().Error(err.Error())
		}
	}
}

// turnsNeedBuild reports an open turn the last fill drew closed (opened by
// the tree's own keys), which the next draw fills.
func turnsNeedBuild(tree *ochazuke.Tree, st *turnFill) bool {
	for _, n := range st.nodes {
		if n.Noun == "turn" && st.known[n.ID] && !st.built[n.ID] && !tree.Collapsed(n.ID) {
			return true
		}
	}
	return false
}

// activeTurn is the index of the running turn, -1 for none.
func activeTurn(list []turns.Turn) int {
	if n := len(list); n > 0 && list[n-1].State == turns.StateRunning {
		return n - 1
	}
	return -1
}

// modelTurnLabel is a turn's row: the prompt's first line, how it ended,
// its steers and tool calls, how long it took and when it started.
func modelTurnLabel(t turns.Turn, active bool, loc *time.Location) string {
	prompt := firstLine(promptText(t.Prompt))
	if prompt == "" {
		prompt = "(no prompt)"
	}
	parts := []string{prompt}
	switch {
	case t.State == turns.StateRunning && t.Source == turns.SourceShell:
		parts = append(parts, "running")
	case t.State == turns.StateRunning:
		parts = append(parts, "working")
	case t.Source == turns.SourceShell && t.Exit != nil:
		parts = append(parts, fmt.Sprintf("exit %d", *t.Exit))
	case t.State == turns.StateError:
		parts = append(parts, "interrupted")
	}
	if n := len(t.Steers); n > 0 {
		parts = append(parts, plural(n, "steer"))
	}
	tools := 0
	for _, s := range t.Steps {
		if s.Kind == turns.StepTool {
			tools++
		}
	}
	if tools > 0 {
		parts = append(parts, plural(tools, "tool call"))
	}
	if t.Duration > 0 && t.State != turns.StateRunning {
		parts = append(parts, shortDuration(t.Duration))
	}
	if !t.StartedAt.IsZero() {
		parts = append(parts, t.StartedAt.In(loc).Format("Jan 2 15:04"))
	}
	label := strings.Join(parts, " · ")
	if active {
		label = activeTurnMarker + " " + label
	}
	return label
}

// promptText is a prompt without the tags a harness wraps a notification
// in (<task-notification>…), so its row reads as words.
func promptText(prompt string) string {
	if !strings.HasPrefix(strings.TrimSpace(prompt), "<") {
		return prompt
	}
	var b strings.Builder
	depth := 0
	for _, r := range prompt {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
			b.WriteByte(' ')
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// shortDuration is a duration in its largest two units: 8.1s, 2m05s, 1h12m.
func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// turnBody builds the rows under a node: each source line wrapped to the
// width, ids <parent>/<n> and <parent>/<n>.<piece> as turn_wrap.go has.
type turnBody struct {
	nodes  *[]ochazuke.TreeNode
	parent string
	ref    turnRef
	width  int // the text width at this depth; 0 does not wrap
	n      int
}

func (b *turnBody) line(text string) {
	text = strings.ReplaceAll(strings.TrimRight(xansi.Strip(text), " \t\r"), "\t", "    ")
	pieces := wrapTurnLine(text, b.width)
	for k, p := range pieces {
		ref := b.ref
		ref.Line, ref.Offset = b.n, p.offset
		n := ochazuke.TreeNode{ID: fmt.Sprintf("%s/%d", b.parent, b.n), Parent: b.parent, Label: p.text, Noun: "turn-line", Data: ref}
		if k > 0 {
			n.ID = fmt.Sprintf("%s/%d.%d", b.parent, b.n, k)
		}
		if len(pieces) > 1 {
			n.Cells = []string{text}
		}
		*b.nodes = append(*b.nodes, n)
	}
	b.n++
}

// text adds a text's lines indented under a heading.
func (b *turnBody) text(text string) {
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		b.line("  " + l)
	}
}

// markdown adds a text rendered as Markdown at the width (Glamour keeps
// a two-cell margin, the indent under a heading). Rows carry no styles, so
// it renders in the dark style, whose emphasis is colour rather than the
// plain style's asterisks, and the colour is stripped.
func (b *turnBody) markdown(text string) {
	w := b.width
	if w <= 0 {
		w = 80
	}
	block := markdown.Block(text, max(12, w), ochazuke.Appearance{Theme: "dark", Dark: true})
	for _, l := range strings.Split(strings.ReplaceAll(xansi.Strip(block.Text), "\u00a0", " "), "\n") {
		b.line(l)
	}
}

// capped adds at most n lines of text, then how many more there are.
func (b *turnBody) capped(text string, n int) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, l := range lines {
		if i == n {
			b.line(fmt.Sprintf("  … %s more (y copies the turn)", plural(len(lines)-n, "line")))
			return
		}
		b.line("  " + l)
	}
}

// modelTurnNodes are a model fill's nodes: per turn a separator above the
// first of a chained session, its row and, open, its prompt, steers,
// steps and answer (closed, one placeholder row keeps it expandable).
func (a *App) modelTurnNodes(tree *ochazuke.Tree, st *turnFill) []ochazuke.TreeNode {
	loc := a.location()
	a.subs.reset()
	calls := agentCalls(st.mturns)
	active := activeTurn(st.mturns)
	var nodes []ochazuke.TreeNode
	for i, t := range st.mturns {
		id := st.prefix + t.ID
		ref := st.back
		ref.Window, ref.ID, ref.Line, ref.Model = st.window, t.ID, -1, true
		if t.NewSession {
			label := "── new session"
			if !t.StartedAt.IsZero() {
				label += " · " + t.StartedAt.In(loc).Format("Jan 2 15:04")
			}
			nodes = append(nodes, ochazuke.TreeNode{ID: id + "#session", Label: label + " ──", Noun: "turn-separator", Data: ref})
		}
		label := modelTurnLabel(t, i == active, loc)
		if n := t.Notice; n != nil {
			if n.RepeatOf != "" {
				continue // its task's first row shows the latest state
			}
			ref.Event = n.ToolUseID
			label = noticeHeader(n, calls[n.ToolUseID]) + " · " + noticeMeta(n, "", loc)
		}
		nodes = append(nodes, ochazuke.TreeNode{ID: id, Label: label, Noun: "turn", Data: ref})
		for k, n := range t.Events {
			eref := ref
			eref.Event = n.ToolUseID
			nodes = append(nodes, ochazuke.TreeNode{ID: fmt.Sprintf("%s#event/%d", id, k), Label: "  " + noticeHeader(&n, calls[n.ToolUseID]) + " · " + noticeMeta(&n, "", loc), Noun: "turn-separator", Data: eref})
		}
		if !st.known[id] || tree.Collapsed(id) {
			nodes = append(nodes, ochazuke.TreeNode{ID: id + "/…", Parent: id, Label: "…", Noun: "turn-line", Data: ref})
			delete(st.built, id)
			continue
		}
		st.built[id] = true
		b := &turnBody{nodes: &nodes, parent: id, ref: ref, width: st.width}
		if n := t.Notice; n != nil {
			b.line("result:")
			if ag := calls[n.ToolUseID]; ag != nil && ag.Result != "" {
				b.markdown(ag.Result)
			} else {
				b.markdown(orNone(n.Result))
			}
			b.line("")
			b.line("response:")
			for _, s := range t.Steps {
				if s.Kind == turns.StepText {
					b.markdown(s.Text)
				}
			}
			continue
		}
		head := "prompt:"
		if t.Source == turns.SourceShell {
			head = "command:"
		}
		b.line(head)
		b.text(t.Prompt)
		for _, s := range t.Steers {
			b.line("")
			b.line("steer · " + s.At.In(loc).Format("15:04:05") + ":")
			b.text(s.Text)
		}
		if t.Source == turns.SourceShell {
			a.shellOutputLines(b, st.source, t)
			continue
		}
		final := -1
		for j := len(t.Steps) - 1; j >= 0; j-- {
			if t.Steps[j].Kind == turns.StepText {
				if t.Steps[j].Text == t.Final {
					final = j
				}
				break
			}
		}
		for j, s := range t.Steps {
			if j == final || !a.stepShown(s.Kind) {
				continue
			}
			b.line("")
			switch s.Kind {
			case turns.StepText:
				b.markdown(s.Text)
			case turns.StepThinking, turns.StepTool:
				if s.Agent != nil {
					nodes = append(nodes, a.agentNodes(id, j, s, ref, st.width, 1, func(n string) bool { return st.known[n] && !tree.Collapsed(n) })...)
					continue
				}
				nodes = append(nodes, a.stepNode(id, j, s, ref, st.width)...)
			}
		}
		b.line("")
		b.line("answer:")
		switch {
		case t.Final != "":
			b.markdown(t.Final)
		case t.State == turns.StateRunning:
			b.line("  (working)")
		default:
			b.line("  (no answer)")
		}
	}
	return nodes
}

// stepNode is a thinking or tool step's row and the lines under it.
func (a *App) stepNode(turnID string, j int, s turns.Step, ref turnRef, width int) []ochazuke.TreeNode {
	id := fmt.Sprintf("%s/step/%d", turnID, j)
	ref.Step = string(s.Kind)
	var label string
	switch s.Kind {
	case turns.StepThinking:
		label = "thinking · " + plural(len(strings.Split(s.Text, "\n")), "line")
	default:
		label = "tool " + s.Tool
		if s.Summary != "" {
			label += ": " + s.Summary
		}
		switch {
		case !s.HasResult:
			label += " · running"
		case s.IsError:
			label += " · error"
		}
	}
	nodes := []ochazuke.TreeNode{{ID: id, Parent: turnID, Label: label, Noun: "turn-step", Data: ref}}
	b := &turnBody{nodes: &nodes, parent: id, ref: ref, width: max(0, width-2)}
	if s.Kind == turns.StepThinking {
		b.text(s.Text)
		return nodes
	}
	// An edit's input reads as a diff, as on a card (turn_cards.go).
	if diff, ok := editDiff(s.Input); ok {
		b.line("diff:")
		b.capped(diff, turnInputShown)
	} else {
		b.line("input:")
		b.capped(prettyJSON(s.Input), turnInputShown)
	}
	if s.HasResult {
		b.line("result:")
		b.capped(s.Result, turnResultShown)
	}
	return nodes
}

// prettyJSON is JSON indented, or the text as it is when it is not JSON.
func prettyJSON(raw string) string {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return raw
	}
	return string(out)
}

// shellOutputLines are a shell turn's output lines: read when it opened,
// the last turnOutputShown of them.
func (a *App) shellOutputLines(b *turnBody, pt *paneTurns, t turns.Turn) {
	b.line("")
	b.line("output:")
	var out *turnOutput
	if pt != nil {
		out = pt.outputs[t.ID]
	}
	switch {
	case t.State == turns.StateRunning:
		b.line("  (running: the output shows once it finishes)")
	case out == nil || out.loading:
		b.line("  loading output…")
	case out.err != "":
		b.line("  " + out.err)
	case len(out.lines) == 0:
		b.line("  (no output)")
	default:
		lines := out.lines
		if n := len(lines) - turnOutputShown; n > 0 {
			b.line(fmt.Sprintf("  … %s earlier (y copies them all)", plural(n, "line")))
			lines = lines[n:]
		}
		for _, l := range lines {
			b.line("  " + l)
		}
	}
}

// readShellOutput is a shell turn's whole output, off the loop.
func readShellOutput(c context.Context, d turnDaemon, p pane, t turns.Turn) ([]string, error) {
	if t.Command == nil {
		return nil, errors.New("the command is still running")
	}
	return turns.ShellOutput(c, paneCapturer{d: d, p: p}, *t.Command, turns.DefaultChunk)
}

// loadShellOutput reads an opened shell turn's output into the tab.
func (a *App) loadShellOutput(p pane, pt *paneTurns, t turns.Turn) ochazuke.Result {
	d, ok := a.panesd.(turnDaemon)
	if !ok || t.Command == nil {
		return ochazuke.Done(t.ID)
	}
	out := &turnOutput{loading: true}
	pt.outputs[t.ID] = out
	return ochazuke.Pending(func(c context.Context) (any, error) {
		return readShellOutput(c, d, p, t)
	}).Then(func(_ ochazuke.Ctx, v any, err error) {
		out.loading = false
		if err != nil {
			out.err = err.Error()
		} else {
			out.lines, _ = v.([]string)
		}
		a.turnsRead(p.ID)
	})
}

// openModelTurn is turn.open on a read turn: open it and fill what it
// holds; a shell turn reads its output.
func (a *App) openModelTurn(ctx ochazuke.Ctx, ref turnRef) ochazuke.Result {
	tree := a.pv.turns
	st := a.turnFillOf(tree)
	if err := tree.SetCollapsed(ctx.Instance.ID, false); err != nil {
		return ochazuke.Failed(err)
	}
	st.known[ctx.Instance.ID] = true
	if err := a.refillModelTurns(tree, st); err != nil {
		return ochazuke.Failed(err)
	}
	return a.loadTurnOutput(ref)
}

// loadTurnOutput reads an opened shell turn's output when it was not read.
func (a *App) loadTurnOutput(ref turnRef) ochazuke.Result {
	pt := a.mturns[ref.Window]
	if pt == nil {
		return ochazuke.Done(ref.ID)
	}
	for _, t := range pt.list {
		if t.ID == ref.ID && t.Source == turns.SourceShell && t.State != turns.StateRunning && pt.outputs[t.ID] == nil {
			p, err := a.paneByWindow(ref.Window)
			if err != nil {
				return ochazuke.Failed(err)
			}
			return a.loadShellOutput(p, pt, t)
		}
	}
	return ochazuke.Done(ref.ID)
}

// paneByWindow is the listed pane of a window id.
func (a *App) paneByWindow(window string) (pane, error) {
	for _, p := range a.panes {
		if p.ID == window {
			return p, nil
		}
	}
	return pane{}, fmt.Errorf("no pane %s", window)
}

// styleTurnRows draws the running turn's row bold and every older turn's
// row muted. Rows already styled (the selected one, a faded list) keep
// their own look; the marker still names the running turn.
func (a *App) styleTurnRows(text string) string {
	styles := a.m.Styles()
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.Contains(l, "\x1b") || !(strings.HasPrefix(l, "▸ ") || strings.HasPrefix(l, "▾ ")) {
			continue
		}
		if strings.HasPrefix(l[len("▸ "):], activeTurnMarker) {
			lines[i] = styles.Title.Bold(true).Render(l)
		} else {
			lines[i] = styles.Muted.Render(l)
		}
	}
	return strings.Join(lines, "\n")
}

// The turn copies: y the response, Y the prompt (and its steers) and the
// response. A shell turn's response is its whole output, read off the
// loop a chunk at a time.

// turnText loads what y (all false) or Y (all true) copies of the turn at
// ref, and names it for the notice.
func (a *App) turnText(ref turnRef, all bool) (func(context.Context) (string, error), string, error) {
	if !ref.Model {
		ts, err := a.hist.Turns(ref.Window)
		if err != nil {
			return nil, "", err
		}
		for _, t := range ts {
			if t.ID != ref.ID {
				continue
			}
			var parts []string
			if all {
				parts = append(parts, t.Prompt)
			}
			for _, r := range t.Responses {
				parts = append(parts, r.Text)
			}
			text := strings.Join(parts, "\n\n")
			return func(context.Context) (string, error) { return text, nil }, "the turn", nil
		}
		return nil, "", fmt.Errorf("no turn %s", ref.ID)
	}
	pt := a.mturns[ref.Window]
	if pt == nil {
		return nil, "", fmt.Errorf("no turns read for %s", ref.Window)
	}
	i := slices.IndexFunc(pt.list, func(t turns.Turn) bool { return t.ID == ref.ID })
	if i < 0 {
		return nil, "", fmt.Errorf("no turn %s", ref.ID)
	}
	t := pt.list[i]
	head := ""
	if all {
		var parts []string
		parts = append(parts, t.Prompt)
		for _, s := range t.Steers {
			parts = append(parts, s.Text)
		}
		head = strings.Join(parts, "\n\n") + "\n\n"
	}
	if t.Source != turns.SourceShell {
		text := head + t.Response()
		return func(context.Context) (string, error) { return text, nil }, "the turn", nil
	}
	if all {
		head = "$ " + t.Prompt + "\n"
	}
	d, ok := a.panesd.(turnDaemon)
	if !ok {
		return nil, "", errors.New("shell output needs the TUIOS daemon")
	}
	p, err := a.paneByWindow(ref.Window)
	if err != nil {
		return nil, "", err
	}
	return func(c context.Context) (string, error) {
		lines, err := readShellOutput(c, d, p, t)
		if err != nil {
			return "", err
		}
		return head + strings.Join(lines, "\n") + "\n", nil
	}, "the output of " + oneLine(t.Prompt), nil
}

// copyOutcome is a copy job's answer.
type copyOutcome struct {
	text string
	big  bool // over turnCopyLimit: offered as a file or a pager
	osc  bool // no clipboard program serves the session
}

// copyTurn is turn.copy (y) and turn.copy-all (Y).
func (a *App) copyTurn(all bool) ochazuke.AppHandler {
	return func(ctx ochazuke.Ctx) ochazuke.Result {
		if ctx.Instance == nil {
			return ochazuke.Failed(errors.New("select a turn"))
		}
		ref, _ := ctx.Instance.Data.(turnRef)
		load, what, err := a.turnText(ref, all)
		if err != nil {
			return ochazuke.Failed(err)
		}
		return a.copyLoaded(ref, load, what)
	}
}

// copyLoaded copies what load reads to the clipboard off the loop, or
// offers it as a file or a pager when the clipboard does not take it.
func (a *App) copyLoaded(ref turnRef, load func(context.Context) (string, error), what string) ochazuke.Result {
	{
		sys := a.cx.sys
		return ochazuke.Pending(func(c context.Context) (any, error) {
			text, err := load(c)
			if err != nil {
				return nil, err
			}
			if len(text) > turnCopyLimit {
				return copyOutcome{text: text, big: true}, nil
			}
			osc, err := sys.Copy(c, text)
			if err != nil {
				return nil, err
			}
			return copyOutcome{text: text, osc: osc}, nil
		}).Then(func(ctx ochazuke.Ctx, v any, err error) {
			out, ok := v.(copyOutcome)
			if !ok || err != nil {
				return
			}
			if out.big || out.osc {
				if err := a.offerTurnSave(ref, out); err != nil {
					ctx.Error(err.Error())
				}
				return
			}
			ctx.Notify("copied " + what)
		})
	}
}

// turnSaveChooser is the chooser a copy too large for the clipboard (or
// with no clipboard program) opens.
const turnSaveChooser = "turn-save"

// offerTurnSave offers a text the clipboard does not take as a file, in a
// pager, or (when small) through the terminal's clipboard.
func (a *App) offerTurnSave(ref turnRef, out copyOutcome) error {
	path := a.turnSavePath(ref)
	why := "No clipboard program here"
	if out.big {
		why = fmt.Sprintf("%d KB is too much for the clipboard", (len(out.text)+1023)/1024)
	}
	rows := []ochazuke.ChooserRow{
		{ID: "save", Label: "Save to " + tildePath(path, homeDir())},
		{ID: "pager", Label: "Open in a pager (a new pane)"},
	}
	if !out.big {
		rows = append(rows, ochazuke.ChooserRow{ID: "osc", Label: "Copy through the terminal (OSC 52)"})
	}
	text := out.text
	_, err := a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind:  turnSaveChooser,
		Title: why,
		Rows:  rows,
		Choose: func(c ochazuke.ChooserRow) ochazuke.Result {
			switch c.ID {
			case "osc":
				if err := a.copyOSC52(text); err != nil {
					return ochazuke.Failed(err)
				}
				a.r.UI().Notify("copied through the terminal")
				return ochazuke.Done(nil)
			case "pager":
				return a.saveTurnText(ref, path, text, true)
			}
			return a.saveTurnText(ref, path, text, false)
		},
	})
	return err
}

// saveTurnText writes the text to path off the loop and, with pager, opens
// it in a pager in a new pane of the turn's session (else the system's
// opener).
func (a *App) saveTurnText(ref turnRef, path, text string, pager bool) ochazuke.Result {
	sys := a.cx.sys
	opener, _ := a.panesd.(pagerOpener)
	p, perr := a.paneByWindow(ref.Window)
	return ochazuke.Pending(func(c context.Context) (any, error) {
		if err := writeTurnFile(path, text); err != nil {
			return nil, err
		}
		if !pager {
			return path, nil
		}
		if opener != nil && perr == nil {
			return path, opener.OpenPager(c, p, path)
		}
		return path, sys.Open(c, path)
	}).Then(func(ctx ochazuke.Ctx, _ any, err error) {
		if err != nil {
			return
		}
		if pager {
			ctx.Notify("opened " + tildePath(path, homeDir()) + " in a pager")
			return
		}
		ctx.Notify("saved " + tildePath(path, homeDir()))
	})
}

// homeDir is the home directory, "" when unknown (paths then show whole).
func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// turnSavePath is where a turn's text is saved: mtm's state directory
// (else the temporary directory) under turns/, named by the pane, the turn
// and the time.
func (a *App) turnSavePath(ref turnRef) string {
	dir := a.r.Config().StateDirectory()
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "mtm")
	}
	window := ref.Window
	if len(window) > 8 {
		window = window[:8]
	}
	id := strings.TrimPrefix(ref.ID, "shell:")
	if len(id) > 40 {
		id = id[:40]
	}
	name := unsafeName.ReplaceAllString(window+"-"+id, "_") + "-" + time.Now().Format("20060102-150405") + ".txt"
	return filepath.Join(dir, "turns", name)
}

// writeTurnFile writes text to path atomically, making its directory.
func writeTurnFile(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// noTurnsText is what the Turns tab says with no turn to show.
func (a *App) noTurnsText(st *turnFill) string {
	if !st.model {
		return "no turns recorded yet"
	}
	switch pt := st.source; {
	case pt != nil && pt.err != "":
		return "turns unavailable: " + pt.err
	case pt == nil || !pt.loaded:
		return "loading turns…"
	}
	return "no turns yet"
}
