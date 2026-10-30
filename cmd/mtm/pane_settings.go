package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/compose"
	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/turns"
)

// The settings line: an agent pane's harness and provider settings, one
// line at the top of its Turns tab, each field a row of its own (the
// pane-setting noun) so it can be acted on: enter opens a chooser of the
// values the harness takes, the current one marked, and the choice is
// typed into the pane as the harness's own in-session command.
//
// Where each value comes from, the live source first:
//
//   - the session's own log, as the Turns tab already follows it
//     (turns.Live): Claude Code's transcript, omp's session log, Codex's
//     rollout (read from its end on each listing while the tab shows it);
//   - tuios's agent metadata (model, context, cost), fed by a status line
//     or a protocol pane;
//   - the launch recipe (the flags mtm started the agent with), drawn
//     "(at launch)": it does not follow a change made inside the session.
//
// How a value changes is configuration, not code: the harness profile's
// settings rows (internal/harness/settings.go, harnesses.json). A typed
// command goes like a prompt when the agent is at rest, and through tuios's
// delivery queue (typed when it rests) while it works; a stepped setting
// (Claude Code's permission mode, shift+tab) is refused while it works,
// since the key would act on the running turn. Nothing is sent while the
// agent waits on the person, and a draft in the prompt box for the pane
// asks first. After the send the log is read again and a notice says what
// the harness now reports.

const (
	settingsListID = "panes/settings"
	settingNoun    = "pane-setting"
	// settingSettle is the wait after typing a setting's command before the
	// log is read again, so the harness has written its reply.
	settingSettle = 1200 * time.Millisecond
	// settingStepPause is the wait between two presses of a cycling key,
	// for the harness to redraw the screen that says where it is.
	settingStepPause = 300 * time.Millisecond
)

// Field names beyond the harness's settings (harness.Setting*).
const (
	fieldMode    = "mode"
	fieldContext = "context"
	fieldCost    = "cost"
)

// The sources a field's value comes from.
const (
	sourceLog    = "log"
	sourceTuios  = "tuios"
	sourceLaunch = "launch"
)

// settingField is one field of the line.
type settingField struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"` // as the source states it ("" unknown)
	Text   string `json:"text"`            // as the line draws it
	Source string `json:"source,omitempty"`
	From   string `json:"from,omitempty"` // which log, for the source "log"
	Tone   string `json:"-"`
	// Keep orders what a narrow line keeps: the lowest is dropped first.
	Keep       int  `json:"-"`
	Changeable bool `json:"changeable,omitempty"`
}

// settingRef is a pane-setting row's data: the pane row and the field.
type settingRef struct {
	Row, Name string
}

// settingVerbs are the pane verbs that change each setting.
var settingVerbs = map[string]string{
	harness.SettingModel:      "set-model",
	harness.SettingEffort:     "set-effort",
	harness.SettingFast:       "set-fast",
	harness.SettingPermission: "set-permission-mode",
}

// settingArgs are the verbs' value parameter names.
var settingArgs = map[string]string{
	harness.SettingModel:      "model",
	harness.SettingEffort:     "level",
	harness.SettingFast:       "value",
	harness.SettingPermission: "mode",
}

// settingDaemon is what a change needs beyond the prompt box's calls: a
// key sent to the pane and its screen read back. tuios provides it.
type settingDaemon interface {
	SendKey(ctx context.Context, p pane, key string) error
	CapturePane(ctx context.Context, p pane) (string, error)
}

// SendKey sends one named key to the pane (tuios send-keys).
func (t tuios) SendKey(ctx context.Context, p pane, key string) error {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	return t.Client.SendKeys(ctx, daemon.SendKeysParams{Session: p.Session, Window: p.ID, Keys: key})
}

// addPaneSettings adds the line's list, the pane-setting noun and the
// pane's setting verbs.
func (a *App) addPaneSettings() error {
	a.r.Define("pane").
		Verb("settings", a.showPaneSettings, "The agent's settings as the line shows them, each with where its value comes from (the session's log, tuios, or the launch flags)").Fetch().
		Verb("set-model", a.setPaneSetting(harness.SettingModel), "Switch the agent's model in its session, with the harness's own command; no model opens a chooser of the harness's models").
		Verb("set-effort", a.setPaneSetting(harness.SettingEffort), "Set the agent's thinking effort in its session; no level opens a chooser").
		Verb("set-fast", a.setPaneSetting(harness.SettingFast), "Turn the agent's fast mode on or off in its session; no value opens a chooser").
		Verb("toggle-fast", a.togglePaneFast, "Turn the agent's fast mode the other way").
		Verb("set-permission-mode", a.setPaneSetting(harness.SettingPermission), "Set the agent's permission mode in its session; no mode opens a chooser").
		Verb("settings-line", a.focusPaneSettings, "Focus the settings line at the top of the Turns tab").
		Verb("read-settings", a.readCodexSettings, "Read a Codex pane's settings from the end of its session rollout").Fetch()
	for name, verb := range settingVerbs {
		arg := settingArgs[name]
		a.parameters("pane."+verb, map[string]ochazuke.Parameter{
			arg:         {Type: "string", Description: "The value; none opens a chooser of the values the harness takes"},
			"confirmed": {Type: "boolean", Description: "Send even though the prompt box holds a draft for the pane"},
		})
	}
	a.parameters("pane.toggle-fast", map[string]ochazuke.Parameter{"confirmed": {Type: "boolean", Description: "Send even though the prompt box holds a draft for the pane"}})
	// A field of the line: enter changes it, h and l walk the line, j and
	// k leave it for the stop drawn below or above, esc goes back to the
	// pane row.
	a.r.Define(settingNoun).
		Verb("change", a.changePaneSetting, "Change this setting: a chooser of the values the harness takes, the current one marked").
		Verb("back", a.settingsBack, "Go back to the pane row").
		Mode("normal").
		Bind("enter", ochazuke.Do(settingNoun+".change").Hinted("change")).
		Bind("h", ochazuke.Do("ochazuke.list.move", ochazuke.Arguments{"delta": -1}).Hide()).
		Bind("left", ochazuke.Do("ochazuke.list.move", ochazuke.Arguments{"delta": -1}).Hide()).
		Bind("l", ochazuke.Do("ochazuke.list.move", ochazuke.Arguments{"delta": 1}).Hide()).
		Bind("right", ochazuke.Do("ochazuke.list.move", ochazuke.Arguments{"delta": 1}).Hide()).
		Bind("j", ochazuke.Do("ochazuke.focus.next").Hide()).
		Bind("down", ochazuke.Do("ochazuke.focus.next").Hide()).
		Bind("k", ochazuke.Do("ochazuke.focus.previous").Hide()).
		Bind("up", ochazuke.Do("ochazuke.focus.previous").Hide()).
		Bind("esc", ochazuke.Do(settingNoun+".back"))
	// j (and down) on the prompt box's last line goes down to the line
	// under it, as k on its first line goes up to the turns.
	a.r.DefineGuard("prompt-bottom-line", func(_ *ochazuke.Runtime, ev ochazuke.KeyEvent) bool {
		in := a.pb.input
		if in == nil || ev.Focus != promptID || a.pv.settings == nil || a.pv.settings.Len() == 0 {
			return false
		}
		p := a.promptPane()
		if p == nil || !a.showsSettings(*p) {
			return false
		}
		at := min(max(0, in.Cursor), len(in.Text))
		return !slices.Contains(in.Text[at:], '\n')
	})
	a.r.Define(promptNoun).Mode("normal").
		Bind("j", ochazuke.Do("ochazuke.focus.next").Guard("prompt-bottom-line").Hide()).
		Bind("down", ochazuke.Do("ochazuke.focus.next").Guard("prompt-bottom-line").Hide())
	list, err := a.r.AddList(settingsListID, nil, ochazuke.Parent(panesPage), ochazuke.NoSpill(), ochazuke.NoMarks())
	if err != nil {
		return err
	}
	a.pv.settings = list
	return nil
}

// harnessOf is the pane's harness profile: by the harness tuios reports,
// else the one mtm launched it with.
func (a *App) harnessOf(p pane) (harness.Harness, bool) {
	if a.harnesses == nil {
		return harness.Harness{}, false
	}
	for _, name := range []string{compose.Harness(p.Harness), p.LaunchHarness, p.Harness} {
		if h, ok := a.harnesses.Get(name); ok && name != "" {
			return h, true
		}
	}
	return harness.Harness{}, false
}

// isCodex reports whether the pane runs Codex.
func isCodex(p pane) bool {
	return strings.HasPrefix(p.Harness, "codex") || p.Harness == "" && strings.HasPrefix(p.LaunchHarness, "codex")
}

// paneLive is what the pane's session log says of its settings, and which
// log that is ("" when none has been read).
func (a *App) paneLive(p pane) (turns.Live, string) {
	switch {
	case isCodex(p):
		return a.codexLive[p.ID], "codex rollout"
	case isClaude(p), isOmp(p):
		pt := a.mturns[p.ID]
		if pt == nil || pt.chain == nil {
			return turns.Live{}, ""
		}
		from := "Claude transcript"
		if isOmp(p) {
			from = "omp session log"
		}
		return pt.chain.Settings(), from
	}
	return turns.Live{}, ""
}

// settingFields are the line's fields for the pane, in drawing order,
// each from its live source first. A setting the harness can change is
// shown even when nothing states it ("fast ?"), so it can be acted on.
func (a *App) settingFields(p pane) []settingField {
	h, _ := a.harnessOf(p)
	live, from := a.paneLive(p)
	changeable := func(name string) bool { _, ok := h.Setting(name); return ok }
	var out []settingField
	add := func(f settingField) {
		f.Changeable = changeable(f.Name)
		if f.Value == "" && !f.Changeable {
			return
		}
		if f.Source == sourceLog {
			f.From = from
		}
		if f.Source == sourceLaunch {
			f.Text += " (at launch)"
			f.Tone = ochazuke.ToneIdle
		}
		out = append(out, f)
	}
	pick := func(name string, liveV, tuiosV, launchV string) settingField {
		switch {
		case liveV != "":
			return settingField{Name: name, Value: liveV, Source: sourceLog}
		case tuiosV != "":
			return settingField{Name: name, Value: tuiosV, Source: sourceTuios}
		case launchV != "":
			return settingField{Name: name, Value: launchV, Source: sourceLaunch}
		}
		return settingField{Name: name}
	}

	f := pick(harness.SettingModel, live.Model, p.Meta["model"], cmp.Or(p.Model, launchFlag(p.LaunchFlags, "--model")))
	f.Text, f.Keep = cmp.Or(modelLabel(f.Value), "model ?"), 7
	add(f)

	f = pick(harness.SettingEffort, live.Effort, "", cmp.Or(launchFlag(p.LaunchFlags, "--effort"), launchFlag(p.LaunchFlags, "--thinking")))
	f.Text, f.Keep = "effort "+cmp.Or(f.Value, "?"), 4
	add(f)

	f = pick(harness.SettingFast, live.Fast, "", "")
	f.Text, f.Keep = "fast "+cmp.Or(f.Value, "?"), 3
	if f.Value == "on" {
		f.Tone = ochazuke.ToneWorking
	}
	add(f)

	launchPerm := launchFlag(p.LaunchFlags, "--permission-mode")
	if launchPerm == "" && slices.Contains(p.LaunchFlags, "--dangerously-skip-permissions") {
		launchPerm = "bypassPermissions"
	}
	f = pick(harness.SettingPermission, live.PermissionMode, "", launchPerm)
	f.Text, f.Keep = permissionLabel(f.Value), 5
	if f.Value == "bypassPermissions" {
		f.Tone = ochazuke.ToneNeedsInput
	}
	add(f)

	if live.Mode != "" {
		add(settingField{Name: fieldMode, Value: live.Mode, Text: live.Mode + " mode", Source: sourceLog, Keep: 1})
	}

	switch {
	case live.ContextTokens > 0:
		window := live.ContextWindow
		if window == 0 {
			window, _ = h.ContextWindow(live.Model)
		}
		f = settingField{Name: fieldContext, Value: strconv.FormatInt(live.ContextTokens, 10), Source: sourceLog, Keep: 6}
		f.Text, f.Tone = contextText(live.ContextTokens, window)
		add(f)
	case p.Meta["context"] != "":
		add(settingField{Name: fieldContext, Value: p.Meta["context"], Text: "ctx " + p.Meta["context"], Source: sourceTuios, Keep: 6})
	}

	switch {
	case live.HasCost:
		v := fmt.Sprintf("$%.2f", live.Cost)
		add(settingField{Name: fieldCost, Value: v, Text: v, Source: sourceLog, Keep: 2})
	case p.costText() != "":
		add(settingField{Name: fieldCost, Value: p.Meta["cost"], Text: p.costText(), Source: sourceTuios, Keep: 2})
	}
	if from == "" {
		for i := range out {
			out[i].From = ""
		}
	}
	return out
}

// contextText is the context used against the window ("ctx 45%"), its
// tone warning from 80% and erring from 95%; tokens alone ("ctx 455k")
// when the window is unknown or smaller than what was used.
func contextText(tokens, window int64) (string, string) {
	if window <= 0 || tokens > window {
		return "ctx " + shortTokens(tokens), ""
	}
	pct := int(tokens * 100 / window)
	tone := ""
	switch {
	case pct >= 95:
		tone = ochazuke.ToneErrored
	case pct >= 80:
		tone = ochazuke.ToneNeedsInput
	}
	return fmt.Sprintf("ctx %d%%", pct), tone
}

// shortTokens is a token count in k or M.
func shortTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + "M"
	case n >= 1000:
		return strconv.FormatInt((n+500)/1000, 10) + "k"
	}
	return strconv.FormatInt(n, 10)
}

// modelLabel is a model id as people say it: claude-opus-5-5 is
// "Opus 5.5", claude-haiku-4-5-20251001 "Haiku 4.5", a [1m] suffix " 1M";
// omp's provider/id drops the provider; anything else is as given.
func modelLabel(id string) string {
	if _, rest, ok := strings.Cut(id, "/"); ok && !strings.HasPrefix(id, "claude-") {
		id = rest
	}
	rest, ok := strings.CutPrefix(id, "claude-")
	if !ok {
		return id
	}
	suffix := ""
	if base, found := strings.CutSuffix(rest, "[1m]"); found {
		rest, suffix = base, " 1M"
	}
	parts := strings.Split(rest, "-")
	if len(parts) < 2 || parts[0] == "" {
		return id
	}
	var version []string
	for _, p := range parts[1:] {
		if _, err := strconv.Atoi(p); err != nil || len(p) > 2 {
			break // a date stamp ends the version
		}
		version = append(version, p)
	}
	if len(version) == 0 {
		return id
	}
	return strings.ToUpper(parts[0][:1]) + parts[0][1:] + " " + strings.Join(version, ".") + suffix
}

// permissionLabel is a permission mode as the line says it.
func permissionLabel(mode string) string {
	switch mode {
	case "":
		return "perms ?"
	case "bypassPermissions":
		return "bypass"
	case "acceptEdits":
		return "accept edits"
	case "plan":
		return "plan"
	case "auto":
		return "auto"
	case "dontAsk":
		return "don't ask"
	}
	if approval, sandbox, ok := strings.Cut(mode, "/"); ok {
		// Codex: the approval policy and the sandbox.
		return "approve " + approval + " · " + strings.TrimPrefix(sandbox, "danger-")
	}
	return "perms " + mode
}

// fitFields are the fields that fit width with " · " between them, the
// lowest Keep dropped first.
func fitFields(fields []settingField, width int) []settingField {
	out := slices.Clone(fields)
	for len(out) > 1 && xansi.StringWidth(joinFields(out)) > width {
		drop := 0
		for i, f := range out {
			if f.Keep < out[drop].Keep {
				drop = i
			}
		}
		out = slices.Delete(out, drop, drop+1)
	}
	return out
}

func joinFields(fields []settingField) string {
	texts := make([]string, len(fields))
	for i, f := range fields {
		texts[i] = f.Text
	}
	return strings.Join(texts, " · ")
}

// showsSettings reports whether the pane's Turns tab draws a settings
// line: a running agent's, once something states one of its settings.
func (a *App) showsSettings(p pane) bool {
	if a.pv.settings == nil || p.Exited || !(p.isAgent() || p.LaunchHarness != "") {
		return false
	}
	// A line of question marks says nothing: it shows once a source has
	// stated one value (the verbs reach the settings without it).
	return slices.ContainsFunc(a.settingFields(p), func(f settingField) bool { return f.Value != "" })
}

// settingsLine is the line drawn at width for the pane, each field in its
// tone, the selected field reversed while the line holds the focus; it
// also sets the list's rows to the fields drawn. "" when the pane shows
// none (a shell, an exited pane).
func (a *App) settingsLine(p pane, row string, width int) string {
	if !a.showsSettings(p) {
		return ""
	}
	fields := fitFields(a.settingFields(p), width)
	if len(fields) == 0 {
		return ""
	}
	a.setSettingRows(row, fields)
	list := a.pv.settings
	focused := list.Focused()
	sel, _ := list.Selected()
	s := a.m.Styles()
	parts := make([]string, len(fields))
	for i, f := range fields {
		st := s.Tone(f.Tone)
		if focused && sel.ID == settingRowID(row, f.Name) {
			st = st.Reverse(true)
		}
		parts[i] = st.Render(f.Text)
	}
	a.r.Placed(settingsListID)
	return xansi.Truncate(strings.Join(parts, s.Muted.Render(" · ")), width, "…")
}

func settingRowID(row, name string) string { return "setting:" + row + "#" + name }

// setSettingRows gives the list the drawn fields, keeping the selection
// on the same field when it is still drawn.
func (a *App) setSettingRows(row string, fields []settingField) {
	items := make([]ochazuke.ListItem, len(fields))
	for i, f := range fields {
		items[i] = ochazuke.ListItem{ID: settingRowID(row, f.Name), Label: f.Text, Noun: settingNoun, Data: settingRef{Row: row, Name: f.Name}}
	}
	old := a.pv.settings.Items()
	if slices.EqualFunc(old, items, func(x, y ochazuke.ListItem) bool { return x.ID == y.ID && x.Label == y.Label }) {
		return
	}
	sel, _ := a.pv.settings.Selected()
	if err := a.pv.settings.SetItems(items); err != nil {
		a.r.UI().Error(err.Error())
		return
	}
	if slices.ContainsFunc(items, func(it ochazuke.ListItem) bool { return it.ID == sel.ID }) {
		_ = a.pv.settings.Select(sel.ID)
	}
}

// settingPane is the pane a request is about: a pane row's own, a field
// of the line's, else the pane the Turns tab shows.
func (a *App) settingPane(ctx ochazuke.Ctx) (pane, string, error) {
	inst := ctx.Instance
	if inst != nil && inst.Noun == settingNoun {
		ref, _ := inst.Data.(settingRef)
		inst = a.r.Instances[ref.Row]
	}
	if inst == nil && a.pv.turnsFor != "" {
		inst = a.r.Instances[a.pv.turnsFor]
	}
	p, err := a.paneAt(inst)
	if err != nil {
		return pane{}, "", err
	}
	return p, p.rowID(), nil
}

// showPaneSettings is pane.settings: the fields with their sources.
func (a *App) showPaneSettings(ctx ochazuke.Ctx) ochazuke.Result {
	p, _, err := a.settingPane(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(a.settingFields(p))
}

// focusPaneSettings is pane.settings-line: show the pane's Turns tab and
// focus its settings line.
func (a *App) focusPaneSettings(ctx ochazuke.Ctx) ochazuke.Result {
	_, row, err := a.settingPane(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	a.pv.tab = tabTurns
	if a.pv.turnsFor != row {
		if err := a.showTurns(row); err != nil {
			return ochazuke.Failed(err)
		}
	}
	if a.pv.settings.Len() == 0 {
		return ochazuke.Failed(errors.New("the pane shows no settings"))
	}
	if err := a.pv.settings.Focus(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(a.r.Focus)
}

// settingsBack is pane-setting.back (esc): the pane row.
func (a *App) settingsBack(ctx ochazuke.Ctx) ochazuke.Result {
	target := paneListID
	if ctx.Instance != nil {
		if ref, ok := ctx.Instance.Data.(settingRef); ok && a.r.Instances[ref.Row] != nil {
			target = ref.Row
		}
	}
	if err := a.r.SetFocus(target); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(target)
}

// changePaneSetting is pane-setting.change (enter on a field): the
// field's chooser, through the pane verb that changes it.
func (a *App) changePaneSetting(ctx ochazuke.Ctx) ochazuke.Result {
	ref, _ := ctx.Instance.Data.(settingRef)
	if _, ok := settingVerbs[ref.Name]; !ok {
		return ochazuke.Failed(fmt.Errorf("%s is read from the session; nothing changes it here", ref.Name))
	}
	return a.setPaneSetting(ref.Name)(ctx)
}

// togglePaneFast is pane.toggle-fast: fast mode the other way (on when
// nothing says it is on).
func (a *App) togglePaneFast(ctx ochazuke.Ctx) ochazuke.Result {
	p, row, err := a.settingPane(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	value := "on"
	for _, f := range a.settingFields(p) {
		if f.Name == harness.SettingFast && f.Value == "on" {
			value = "off"
		}
	}
	confirmed, _ := ctx.Args["confirmed"].(bool)
	return a.applySetting(p, row, harness.SettingFast, value, confirmed)
}

// setPaneSetting is pane.set-model, set-effort, set-fast and
// set-permission-mode: the value given, else a chooser of the values the
// harness takes.
func (a *App) setPaneSetting(name string) ochazuke.AppHandler {
	return func(ctx ochazuke.Ctx) ochazuke.Result {
		p, row, err := a.settingPane(ctx)
		if err != nil {
			return ochazuke.Failed(err)
		}
		h, st, err := a.settingOf(p, name)
		if err != nil {
			return ochazuke.Failed(err)
		}
		value, _ := ctx.Args[settingArgs[name]].(string)
		confirmed, _ := ctx.Args["confirmed"].(bool)
		if strings.TrimSpace(value) == "" {
			return a.openSettingChooser(p, row, h, st, name)
		}
		return a.applySetting(p, row, name, strings.TrimSpace(value), confirmed)
	}
}

// settingOf is the pane's harness and its row for the setting.
func (a *App) settingOf(p pane, name string) (harness.Harness, harness.Setting, error) {
	h, ok := a.harnessOf(p)
	if !ok {
		return h, harness.Setting{}, fmt.Errorf("mtm knows no harness profile for %s", p.label())
	}
	st, ok := h.Setting(name)
	if !ok {
		return h, st, fmt.Errorf("%s has no in-session command for %s: set it at launch, or add a settings row for it in harnesses.json", h.Name, name)
	}
	return h, st, nil
}

// currentSetting is the field's value now ("" unknown).
func (a *App) currentSetting(p pane, name string) settingField {
	for _, f := range a.settingFields(p) {
		if f.Name == name {
			return f
		}
	}
	return settingField{Name: name}
}

// valueMatches reports whether a chooser's value is the current one: the
// same, or (a model alias against the id or name the log states) held in
// it, "opus" in claude-opus-5-5 or in "Opus 5.5".
func valueMatches(name, choice, current string) bool {
	if choice == "" || current == "" {
		return false
	}
	if strings.EqualFold(choice, current) {
		return true
	}
	return name == harness.SettingModel && strings.Contains(strings.ToLower(current), strings.ToLower(choice))
}

// openSettingChooser opens the chooser of the setting's values, the
// current one marked; enter applies the choice. A setting with no listed
// values (omp's models) takes the typed value.
func (a *App) openSettingChooser(p pane, row string, h harness.Harness, st harness.Setting, name string) ochazuke.Result {
	cur := a.currentSetting(p, name)
	var rows []ochazuke.ChooserRow
	for _, v := range h.Choices(name) {
		mark := " "
		if valueMatches(name, v, cur.Value) {
			mark = "●"
		}
		rows = append(rows, ochazuke.ChooserRow{ID: v, Label: v, Data: v, Cells: []string{mark, v}})
	}
	title := name + " · " + p.label()
	if cur.Value != "" {
		title += " · now " + cur.Text
	}
	if st.Note != "" {
		title += " · " + st.Note
	}
	apply := func(v string) ochazuke.Result { return a.applySetting(p, row, name, v, false) }
	spec := ochazuke.ChooserSpec{
		Kind:   "pane-setting-" + name,
		Title:  title,
		Rows:   rows,
		Choose: func(r ochazuke.ChooserRow) ochazuke.Result { return apply(r.ID) },
		Empty:  "type a value",
	}
	if len(rows) == 0 {
		spec.Create, spec.CreateRow = apply, true
		spec.CreateLabel = func(q string) string { return "set " + name + " to " + q }
	}
	_, err := a.r.OpenChooser(spec)
	return resultError(err)
}

// settingWatch is a change sent and not yet confirmed: the notice the
// next read of the pane's log gives.
type settingWatch struct {
	window, name, value, before, sent string
}

// applySetting changes the setting of the pane to value through the
// harness's own command (or key), as its settings row says, when it is
// safe to type into the pane: at rest it is typed now, while the agent
// works a command is queued (tuios types it when the agent rests) and a
// key is refused, while it waits on the person nothing is sent. A draft
// in the prompt box for the pane asks first unless confirmed.
func (a *App) applySetting(p pane, row, name, value string, confirmed bool) ochazuke.Result {
	h, st, err := a.settingOf(p, name)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if choices := h.Choices(name); len(choices) > 0 && !slices.Contains(choices, value) {
		return ochazuke.Failed(fmt.Errorf("%s takes %s: %s", h.Name, name, strings.Join(choices, ", ")))
	}
	switch {
	case p.Exited:
		return ochazuke.Failed(fmt.Errorf("%s has exited", p.label()))
	case p.needsInput():
		return ochazuke.Failed(errors.New("the agent waits on you: answer it first (enter on its row attaches it), then change the setting"))
	case st.Key != "" && p.working():
		return ochazuke.Failed(fmt.Errorf("the agent is working: %s steps with %s, which would act on the running turn; change it once the agent rests", name, st.Key))
	}
	if !confirmed && a.promptDraftFor(p) {
		return a.confirmSetting(row, name, value)
	}
	before := a.currentSetting(p, name).Value
	watch := &settingWatch{window: p.ID, name: name, value: value, before: before}
	if st.Key != "" {
		return a.stepSetting(p, row, st, name, value, before, watch)
	}
	d, ok := a.panesd.(promptDaemon)
	if !ok {
		return ochazuke.Failed(errors.New("changing a setting needs the TUIOS daemon"))
	}
	text := st.Text(value)
	watch.sent = text
	working := p.working()
	sleep := a.sleep
	return ochazuke.Pending(func(c context.Context) (any, error) {
		if working {
			return d.PromptQueue(c, p, text)
		}
		if err := d.PromptSubmit(c, p, text); err != nil {
			return nil, err
		}
		return nil, sleep(c, settingSettle)
	}).Label(name).Then(func(ctx ochazuke.Ctx, v any, err error) {
		if err != nil {
			return
		}
		if working {
			ctx.Notify(fmt.Sprintf("queued %s: tuios types it when the agent rests", text))
			return
		}
		a.pv.watch = watch
		a.rereadSettings(ctx, p, row)
	})
}

// stepSetting presses the setting's key until the pane's screen shows
// value, at most once round the cycle, reading the screen after each
// press: the cycle's order is configuration, and a value the session does
// not offer (bypass without its flag) is skipped by the harness, so the
// screen, not a count, says when to stop.
func (a *App) stepSetting(p pane, row string, st harness.Setting, name, value, before string, watch *settingWatch) ochazuke.Result {
	d, ok := a.panesd.(settingDaemon)
	if !ok {
		return ochazuke.Failed(errors.New("changing a setting needs the TUIOS daemon"))
	}
	if _, err := st.Steps(cmp.Or(before, st.Cycle[0]), value); err != nil {
		return ochazuke.Failed(err)
	}
	sleep := a.sleep
	watch.sent = st.Key
	return ochazuke.Pending(func(c context.Context) (any, error) {
		for presses := 0; presses <= len(st.Cycle); presses++ {
			screen, err := d.CapturePane(c, p)
			if err != nil {
				return nil, err
			}
			if st.OnScreen(xansi.Strip(screen)) == value {
				return presses, nil
			}
			if presses == len(st.Cycle) {
				break
			}
			if err := d.SendKey(c, p, st.Key); err != nil {
				return nil, err
			}
			if err := sleep(c, settingStepPause); err != nil {
				return nil, err
			}
		}
		return nil, fmt.Errorf("%s never showed %s %s after a round of %s: the session does not offer it", p.label(), name, value, st.Key)
	}).Label(name).Then(func(ctx ochazuke.Ctx, v any, err error) {
		if err != nil {
			return
		}
		n, _ := v.(int)
		presses := "presses"
		if n == 1 {
			presses = "press"
		}
		ctx.Notify(fmt.Sprintf("%s now %s (the pane's screen, after %d %s %s)", name, value, n, st.Key, presses))
		a.rereadSettings(ctx, p, row)
	})
}

// rereadSettings reads the pane's log again (the Turns tab's read, or a
// Codex rollout); a watched change gives its notice once the read is in.
func (a *App) rereadSettings(ctx ochazuke.Ctx, p pane, row string) {
	if isCodex(p) {
		if _, err := a.r.Execute(ochazuke.Request{Verb: "read-settings", Noun: "pane", Target: row}); err != nil {
			ctx.Error(err.Error())
		}
		return
	}
	if a.r.Instances[row] == nil {
		return
	}
	if _, err := a.r.Execute(ochazuke.Request{Verb: "read-turns", Noun: "pane", Target: row}); err != nil {
		ctx.Error(err.Error())
	}
}

// settingsRead gives a watched change's notice after the pane's log was
// read again: the value the log now states, or that it has not said one
// yet.
func (a *App) settingsRead(window string) {
	w := a.pv.watch
	if w == nil || w.window != window {
		return
	}
	a.pv.watch = nil
	p, err := a.paneByWindow(window)
	if err != nil {
		return
	}
	now := a.currentSetting(p, w.name)
	if now.Source == sourceLog && (now.Value != w.before || valueMatches(w.name, w.value, now.Value)) {
		a.r.UI().Notify(fmt.Sprintf("%s now %s (%s)", w.name, now.Text, cmp.Or(now.From, "the session's log")))
		return
	}
	a.r.UI().Notify(fmt.Sprintf("sent %s; the session's log does not state the new %s yet (it shows with the next reply)", w.sent, w.name))
}

// promptDraftFor reports whether the prompt box holds an unsent draft
// written for the pane.
func (a *App) promptDraftFor(p pane) bool {
	if a.pb == nil || a.pb.input == nil || strings.TrimSpace(string(a.pb.input.Text)) == "" {
		return false
	}
	box := a.promptPane()
	return box != nil && box.ID == p.ID
}

// confirmSetting asks before typing into a pane whose prompt box holds a
// draft; y sends with confirmed, and the draft stays in the box.
func (a *App) confirmSetting(row, name, value string) ochazuke.Result {
	q := ochazuke.Request{Noun: "pane", Verb: settingVerbs[name], Target: row, Args: ochazuke.Arguments{settingArgs[name]: value, "confirmed": true}}
	prompt := fmt.Sprintf("The prompt box holds a draft for this pane. Set %s to %s anyway? The draft stays in the box.", name, value)
	if _, err := a.r.OpenModal("confirm", ochazuke.Payload{Prompt: prompt, Request: &q}); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(map[string]any{"confirm": prompt})
}

// readCodexSettings is pane.read-settings for a Codex pane: its rollout
// read from the end, off the loop, into the line.
func (a *App) readCodexSettings(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if !isCodex(p) || p.AgentSession == "" {
		return ochazuke.Done(nil)
	}
	root := a.codexSessions
	return ochazuke.Pending(func(context.Context) (any, error) {
		path, err := turns.FindCodexLog(root, p.AgentSession)
		if err != nil {
			return turns.Live{}, nil
		}
		return turns.ReadCodexSettings(path), nil
	}).Replacing("codex-settings").Then(func(_ ochazuke.Ctx, v any, err error) {
		if err != nil {
			return
		}
		if a.codexLive == nil {
			a.codexLive = map[string]turns.Live{}
		}
		a.codexLive[p.ID], _ = v.(turns.Live)
		a.settingsRead(p.ID)
	})
}

// headerStatus is the status part of the detail header: the settings
// line's fields that state a value, once the session's log has stated
// any (pane_settings.go), else the daemon's and the recipe's
// (pane.statusFields).
func (a *App) headerStatus(p pane) []string {
	if live, _ := a.paneLive(p); live.Zero() {
		return p.statusFields()
	}
	var out []string
	for _, f := range a.settingFields(p) {
		if f.Value != "" {
			out = append(out, f.Text)
		}
	}
	return out
}
