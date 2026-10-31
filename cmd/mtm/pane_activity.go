package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/turns"
)

// A pane's activities are what its agent has running beside its own turn
// (turns.Activity: today the subagents a Claude pane's spawn calls
// started), drawn as child rows under the pane's row wherever the row is
// (the Panes tree under its session, the Needs you or Working list):
// the subagent's description and type, its status word in its class's
// tone (statusWords, turn_status.go) and its age. A row is there while
// the subagent runs; one that ends done drops off at once, one that ends
// bad (failed, killed, stopped) stays panes.subagents-linger and then
// drops, so the end is seen. The list code draws whatever the pane's
// harness's log gives (its format, harnesses.json "log", read as the Turns
// tab reads it; turns.ActivitySourceOf): a harness with no log format, or
// one whose log names no subagents, has no rows. A background subagent
// shows whatever its pane's state; a foreground one only while its pane's
// agent works.
//
// The sources follow each agent pane's current session log off the loop
// on every listing (panes.read-activities), reading only the spawn calls,
// their results and the task notifications, from where the last read
// stopped. A row is the pane-child noun (a pane's child row: any harness's activity): j/k pass through it, its
// selection shows its pane's detail, enter (pane-child.open) puts the
// detail on the pane's Turns tab with the card of the call that started
// it selected, without moving the focus.
//
// Settings: panes.subagents (active, the default, or off: no rows and no
// reads) and panes.subagents-linger (default 30s; 0 drops a bad end at
// once as a done one).

const (
	settingPanesSubagents       = "panes.subagents"
	settingPanesSubagentsLinger = "panes.subagents-linger"
	subagentsActive             = "active"
	subagentsOff                = "off"
	activityNoun                = "pane-child"
	activityRowStart            = "child:"
)

func paneActivitySettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingPanesSubagents, Type: ochazuke.SettingString, Default: subagentsActive, Enum: []string{subagentsActive, subagentsOff},
			Description: "Show an agent pane's running subagents as rows under its row (active), or not (off)"},
		{Name: settingPanesSubagentsLinger, Type: ochazuke.SettingDuration, Default: "30s",
			Description: "How long a subagent that ended badly (failed, killed, stopped) stays under its pane; a finished one drops at once"},
	}
}

// paneActivities is what was read of one pane's activities.
type paneActivities struct {
	session string               // the agent session the source follows
	src     turns.ActivitySource // nil until its log is found
	list    []turns.Activity
	tries   int       // finds that found no log
	retryAt time.Time // the next find after one that found none
}

// activityRow is an activity row's data: the pane it is under and the
// activity.
type activityRow struct {
	Window string
	Row    string // the pane's row id
	turns.Activity
}

// activityRead is one pane's part of a read.
type activityRead struct {
	window string
	src    turns.ActivitySource
	list   []turns.Activity
	found  bool
}

func (a *App) definePaneActivities() {
	a.r.Define(activityNoun).
		Verb("open", a.openActivity, "Show the subagent's pane on its Turns tab, at the card of the call that started it").
		Verb("copy", a.copyActivity, "Copy the subagent's description, type and id").
		Mode("normal").
		Bind("enter", ochazuke.Do(activityNoun+".open")).
		Bind("y", ochazuke.Do(activityNoun+".copy"))
}

// subagentsOn reports whether panes.subagents shows the rows.
func (a *App) subagentsOn() bool {
	return a.r.Settings().String(settingPanesSubagents) != subagentsOff
}

// pollActivities reads every agent pane's activities again after a
// listing, off the loop; the rows follow when a read changed them.
func (a *App) pollActivities(panes []pane) {
	if a.r.Settings().Bool(settingHistoryVisible) && a.r.Page != panesPage {
		return
	}
	if !a.subagentsOn() {
		if len(a.acts) > 0 {
			a.acts = nil
		}
		return
	}
	if !a.actsSince.IsZero() && a.now().Sub(a.actsSince) < activityReadStale {
		return // one read at a time: a read finds and keeps the sources
	}
	if a.acts == nil {
		a.acts = map[string]*paneActivities{}
	}
	listed := map[string]bool{}
	var jobs []activityJob
	now := a.now()
	for _, p := range panes {
		if p.ID == "" || p.Exited || p.AgentSession == "" {
			continue
		}
		find := a.sessionLogFinder(p) // the harness's log format (harnesses.json "log"), as the Turns tab reads it
		if find == nil {
			continue
		}
		listed[p.ID] = true
		pa := a.acts[p.ID]
		if pa == nil || pa.session != p.AgentSession {
			pa = &paneActivities{session: p.AgentSession}
			a.acts[p.ID] = pa
		}
		switch {
		case pa.src != nil:
			jobs = append(jobs, activityJob{window: p.ID, src: pa.src})
		case now.After(pa.retryAt):
			jobs = append(jobs, activityJob{window: p.ID, find: find, session: p.AgentSession})
		}
	}
	for w := range a.acts {
		if !listed[w] {
			delete(a.acts, w)
		}
	}
	if len(jobs) == 0 {
		return
	}
	a.actsJobs = jobs
	if _, err := a.r.Execute(ochazuke.Request{Noun: "panes", Verb: "read-activities", Target: paneListID}); err != nil {
		a.paneErr = err.Error()
	}
}

// activityReadStale is how long a read may be out before the next listing
// starts another (one that never answered: its job was dropped).
const activityReadStale = 2 * time.Minute

// activityJob is one pane's read: its source, or how to find its log.
type activityJob struct {
	window  string
	src     turns.ActivitySource
	find    func(string) turns.Log
	session string
}

// readActivities is panes.read-activities: the activities of the panes
// the last listing named (pollActivities), read off the loop; the rows
// are rebuilt when a read changed them.
func (a *App) readActivities(ochazuke.Ctx) ochazuke.Result {
	jobs := a.actsJobs
	a.actsJobs = nil
	if len(jobs) == 0 {
		return ochazuke.Done(nil)
	}
	a.actsSince = a.now()
	return ochazuke.Pending(func(c context.Context) (any, error) {
		out := make([]activityRead, 0, len(jobs))
		for _, j := range jobs {
			if c.Err() != nil {
				return out, c.Err()
			}
			src := j.src
			if src == nil {
				if l := j.find(j.session); l != nil {
					src = turns.ActivitySourceOf(l)
				}
				if src == nil {
					out = append(out, activityRead{window: j.window})
					continue
				}
			}
			if _, err := src.Read(); err != nil {
				out = append(out, activityRead{window: j.window})
				continue
			}
			out = append(out, activityRead{window: j.window, src: src, list: src.Activities(), found: true})
		}
		return out, nil
	}).Replacing("activities-read").Then(func(_ ochazuke.Ctx, v any, _ error) {
		a.actsSince = time.Time{}
		got, _ := v.([]activityRead)
		changed := false
		for _, r := range got {
			pa := a.acts[r.window]
			if pa == nil {
				continue
			}
			if !r.found {
				pa.tries++
				pa.retryAt = a.now().Add(turnsRetryAfter(pa.tries))
				continue
			}
			pa.src, pa.tries, pa.retryAt = r.src, 0, time.Time{}
			if !slices.Equal(pa.list, r.list) {
				pa.list, changed = r.list, true
			}
		}
		if changed {
			if err := a.rebuildPanes(); err != nil {
				a.paneErr = err.Error()
			}
		}
	})
}

// shownActivities are the pane's activities its rows show now: the
// running ones (a foreground one only while its pane's agent works: one
// whose turn stopped without its result was cut off) and the bad ends
// for panes.subagents-linger after they ended.
func (a *App) shownActivities(p pane, now time.Time) []turns.Activity {
	pa := a.acts[p.ID]
	if pa == nil || !a.subagentsOn() || p.Exited {
		return nil
	}
	linger := a.r.Settings().Duration(settingPanesSubagentsLinger)
	var out []turns.Activity
	for _, act := range pa.list {
		switch {
		case act.Running():
			if !act.Background && !p.working() && !p.needsInput() {
				continue
			}
		case turnStatuses[activityKind(act)].Tone == ochazuke.ToneErrored:
			if act.EndedAt.IsZero() || now.Sub(act.EndedAt) >= linger {
				continue
			}
		default:
			continue // done: it drops off
		}
		out = append(out, act)
	}
	return out
}

// activityKind is the activity's status kind (statusWords): its word's,
// else its state's.
func activityKind(act turns.Activity) string {
	if k := statusKind(act.Status); k != "" {
		return k
	}
	return statusKind(string(act.State))
}

// activityNodes are the rows of the pane's shown activities, under the
// row parent (the pane's).
func (a *App) activityNodes(p pane, parent string, now time.Time) []ochazuke.TreeNode {
	acts := a.shownActivities(p, now)
	out := make([]ochazuke.TreeNode, 0, len(acts))
	for _, act := range acts {
		kind := activityKind(act)
		st := turnStatuses[kind]
		glyph := cmp.Or(st.Glyph, "·")
		title := cmp.Or(oneLine(act.Title), "subagent")
		name := glyph + " " + joinNonEmpty(title, act.Kind)
		since := act.StartedAt
		if !act.Running() {
			since = act.EndedAt
		}
		state := act.Status
		if !since.IsZero() {
			state += " " + age(now.Sub(since))
		}
		var tones []ochazuke.ToneSpan
		if st.Tone != "" {
			tones = []ochazuke.ToneSpan{ochazuke.SpanTone(0, 0, utf8.RuneCountInString(glyph), st.Tone), ochazuke.CellTone(1, st.Tone)}
		}
		out = append(out, ochazuke.TreeNode{
			ID:     activityRowStart + p.ID + "/" + act.ID,
			Parent: parent,
			Label:  "subagent " + title,
			Noun:   activityNoun,
			Data:   activityRow{Window: p.ID, Row: p.rowID(), Activity: act},
			Cells:  []string{name, state, ""},
			Tones:  tones,
		})
	}
	return out
}

// activityAt is the activity row's data.
func activityAt(inst *ochazuke.Instance) (activityRow, error) {
	if inst != nil {
		if r, ok := inst.Data.(activityRow); ok {
			return r, nil
		}
	}
	return activityRow{}, errors.New("no subagent selected")
}

// openActivity is pane-child.open: the detail on the pane's Turns
// tab, the card of the turn whose call started the subagent selected
// when the pane's turns are read. The focus stays on the list.
func (a *App) openActivity(ctx ochazuke.Ctx) ochazuke.Result {
	r, err := activityAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	a.pv.wantCall = r.Window + "\x00" + r.ID
	if a.pv.tab != tabTurns {
		a.pv.tab = tabTurns
		row, _ := a.selectedRow()
		a.pv.followed = fmt.Sprintf("%s|%d", row, a.pv.tab)
		a.loadDetail()
	}
	a.selectWantedCall()
	return ochazuke.Done(nil)
}

// selectWantedCall selects, in its pane's timeline, the card of the turn
// that made the call pane-child.open asked for, once the pane's turns
// hold it.
func (a *App) selectWantedCall() {
	window, call, ok := strings.Cut(a.pv.wantCall, "\x00")
	if !ok {
		return
	}
	pt, tl := a.mturns[window], a.pv.timelines[window]
	if pt == nil || tl == nil {
		return
	}
	for _, t := range pt.list {
		if !slices.ContainsFunc(t.Steps, func(s turns.Step) bool { return s.ToolID == call }) {
			continue
		}
		id := turnCardID(window, t.ID)
		if !slices.ContainsFunc(tl.Cards(), func(c ochazuke.Card) bool { return c.ID == id }) {
			return // not drawn yet: the next fill tries again
		}
		a.pv.wantCall = ""
		if err := tl.Select(id); err != nil {
			a.r.UI().Error(err.Error())
		}
		return
	}
}

// copyActivity is pane-child.copy.
func (a *App) copyActivity(ctx ochazuke.Ctx) ochazuke.Result {
	r, err := activityAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.copyText(joinNonEmpty(r.Title, r.Kind, r.AgentID))
}
