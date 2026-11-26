package app

import (
	"fmt"
	"slices"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

// Lane bars: the stack above a pane's site (panes.laneProgressBar) keeps the
// pane's finished lane trees next to the active one
// (panes.keepCompletedLaneBars) until the owner dismisses them. Every bar is
// a focusable views.ScopeLaneBar node; what is dismissed is remembered per
// pane for the session.

const (
	CmdLaneBarDismiss      = "lane.bar.dismiss"
	CmdLaneBarsDismissPick = "lane.bars.dismiss.pick"
	CmdLaneBarsFocus       = "lane.bars.focus"

	laneBarsPickerID       = "lane.bars.dismiss"
	laneBarsPickAllID      = "all-completed"
	laneBarsPickAllTitle   = "Dismiss all completed"
	laneBarArgKey          = "key"
	laneBarArgPane         = "pane"
	laneBarArgAllCompleted = "allCompleted"
)

// columnSkippedRegions are focus regions h and l pass over unless the focus
// is already inside one: reached with j/k in drawing order or their own
// command, they never stand between two columns of a page.
var columnSkippedRegions = []string{views.LaneBarsID}

// laneBarController remembers the dismissed lane bars: pane ID to the
// data.LaneBarEntry keys dismissed there.
type laneBarController struct {
	dismissed map[string]map[string]bool
}

func (a *App) laneBarCtrl() *laneBarController {
	if a.laneBars == nil {
		a.laneBars = &laneBarController{dismissed: map[string]map[string]bool{}}
	}
	return a.laneBars
}

func (lc *laneBarController) dismissedFor(paneID string) map[string]bool {
	return lc.dismissed[paneID]
}

// dismiss records keys as dismissed for the pane and reports how many were new.
func (lc *laneBarController) dismiss(paneID string, keys ...string) int {
	if lc.dismissed[paneID] == nil {
		lc.dismissed[paneID] = map[string]bool{}
	}
	added := 0
	for _, key := range keys {
		if key != "" && !lc.dismissed[paneID][key] {
			lc.dismissed[paneID][key] = true
			added++
		}
	}
	return added
}

func (a *App) keepCompletedLaneBarsEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetPanesKeepCompletedLaneBars, true)
}

func laneBarSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdLaneBarDismiss,
			Description: "Dismiss the focused lane progress bar above the pane's site for this session",
			Params: []core.ParamSpec{
				{Name: laneBarArgKey, Type: core.ParamString, Description: "bar to dismiss instead of the focused one (lane ID and phase, as the laneBarKey fact)"},
				{Name: laneBarArgPane, Type: core.ParamString, Description: "pane whose bar it is (default: the active pane)"},
				{Name: laneBarArgAllCompleted, Type: core.ParamBool, Description: "dismiss every completed lane bar of the pane"},
			},
		},
		spec(CmdLaneBarsDismissPick, "Pick lane progress bars of the active pane to dismiss (or dismiss all completed)"),
		spec(CmdLaneBarsFocus, "Focus the lane progress bars above the active pane's site"),
	}
}

func laneBarBindings() []core.Binding {
	n := ModeNormal
	return []core.Binding{
		when(bind(n, views.ScopeLaneBar, "d", CmdLaneBarDismiss, "dismiss bar"), "facts.dismissable"),
		hidden(when(bind(n, views.ScopeLaneBar, "x", CmdLaneBarDismiss, "dismiss bar"), "facts.dismissable")),
		bind(n, views.ScopeLaneBar, "D", CmdLaneBarsDismissPick, "dismiss bars…"),
		hidden(bind(n, SpacePanes, "g l", CmdLaneBarsFocus, "lane bars")),
	}
}

func (a *App) registerLaneBarHandlers() {
	a.handle(CmdLaneBarDismiss, a.laneBarDismiss)
	a.handle(CmdLaneBarsDismissPick, a.laneBarsDismissPick)
	a.handle(CmdLaneBarsFocus, a.laneBarsFocus)
}

// paneLaneBars are the bars the pane's stack holds now: what the settings
// allow, less what was dismissed.
func (a *App) paneLaneBars(pane data.Record) []data.LaneBarEntry {
	if a.Store == nil || a.cfg == nil || a.cfg.Settings == nil || !a.cfg.Settings.Bool(SetLaneBar, false) {
		return nil
	}
	entries, err := a.Store.PaneLaneBars(pane, a.keepCompletedLaneBarsEnabled())
	if err != nil {
		a.fail("read lanes", err)
		return nil
	}
	gone := a.laneBarCtrl().dismissedFor(pane.ID)
	return slices.DeleteFunc(entries, func(e data.LaneBarEntry) bool { return gone[e.Key()] })
}

// laneBarPane resolves the pane a lane bar command acts on.
func (a *App) laneBarPane(s core.State, c core.Command) (data.Record, bool) {
	if id := c.Arg(laneBarArgPane); id != "" && a.Store != nil {
		if rec, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: id}); err == nil && rec != nil {
			return *rec, true
		}
		return data.Record{}, false
	}
	if pane, ok := a.activePane(s); ok && pane != nil {
		return *pane, true
	}
	return data.Record{}, false
}

// laneBarDismiss takes a lane bar off its pane's stack: the one named by the
// command's arguments, every completed one, or else the focused bar.
func (a *App) laneBarDismiss(s core.State, c core.Command) core.StepResult {
	pane, ok := a.laneBarPane(s, c)
	if !ok {
		a.say("no pane here to dismiss a lane bar from")
		return core.Next(s)
	}
	bars := a.paneLaneBars(pane)
	var keys []string
	label := ""
	switch all, _ := c.Args[laneBarArgAllCompleted].(bool); {
	case all:
		for _, e := range bars {
			if e.Completed {
				keys = append(keys, e.Key())
			}
		}
		label = fmt.Sprintf("%d completed lane bars", len(keys))
	case c.Arg(laneBarArgKey) != "":
		keys = []string{c.Arg(laneBarArgKey)}
	default:
		root := a.View(s)
		if n := root.Find(s.ActiveFrame().FocusID); n != nil && n.Scope == views.ScopeLaneBar && n.Facts["dismissable"] == true {
			keys = []string{data.Text(n.Facts["laneBarKey"])}
		}
	}
	if len(keys) == 1 {
		if i := slices.IndexFunc(bars, func(e data.LaneBarEntry) bool { return e.Key() == keys[0] }); i >= 0 {
			label = "lane bar " + views.Title(a.schema(data.KindLane), bars[i].Roll.Root)
		} else {
			keys = nil
		}
	}
	if len(keys) == 0 {
		a.say("no lane bar to dismiss · focus one with g l")
		return core.Next(s)
	}
	a.laneBarCtrl().dismiss(pane.ID, keys...)
	a.viewMemo.gen++
	a.say("dismissed %s for this session", label)
	return core.Next(s)
}

// laneBarsDismissPick lists the active pane's lane bars to dismiss, with one
// entry that clears every completed bar.
func (a *App) laneBarsDismissPick(s core.State, c core.Command) core.StepResult {
	pane, ok := a.laneBarPane(s, c)
	if !ok {
		a.say("no pane here with lane bars")
		return core.Next(s)
	}
	bars := a.paneLaneBars(pane)
	if len(bars) == 0 {
		a.say("this pane shows no lane bars")
		return core.Next(s)
	}
	completed := 0
	items := make([]picker.Item, 0, len(bars)+1)
	for _, e := range slices.Backward(bars) { // newest first
		badge := ui.Badge{Text: "active", Tone: "accent"}
		if e.Completed {
			completed++
			badge = ui.Badge{Text: "completed", Tone: "success"}
		}
		items = append(items, picker.Item{ID: e.Key(), Title: views.LaneBarDismissLabel(e), Subtitle: data.RefText(e.Roll.Root.Ref()), Badges: []ui.Badge{badge}})
	}
	if completed > 0 {
		items = append([]picker.Item{{ID: laneBarsPickAllID, Title: laneBarsPickAllTitle, Subtitle: fmt.Sprintf("%d completed lane bars", completed)}}, items...)
	}
	pm := picker.New(fmt.Sprintf("Dismiss lane bars (%d)", len(bars)), items)
	pm.MatchMode = a.matchMode()
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       laneBarsPickerID,
		PageSize: 10,
		OnConfirm: func(it picker.Item) modal.Result {
			args := map[string]any{laneBarArgPane: pane.ID, laneBarArgKey: it.ID}
			if it.ID == laneBarsPickAllID {
				args = map[string]any{laneBarArgPane: pane.ID, laneBarArgAllCompleted: true}
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit,
				Emit: []core.Command{{ID: CmdLaneBarDismiss, Args: args}}}
		},
	})
	return core.Next(a.pushModal(s, m))
}

// laneBarsFocus moves the focus onto the lane bars: the active tree's bar
// when there is one, else the newest finished bar.
func (a *App) laneBarsFocus(s core.State, _ core.Command) core.StepResult {
	root := a.View(s)
	stack := root.Find(views.LaneBarsID)
	if stack == nil || len(stack.Children) == 0 {
		a.say("no lane bars here · panes.laneProgressBar shows them")
		return core.Next(s)
	}
	target := stack.Children[len(stack.Children)-1].ID
	a.say("lane bars · j/k move · d dismisses · D picks")
	return core.Next(withFocus(s, target))
}
