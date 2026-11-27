package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

// Modal IDs and the picker row of the reusable dispatch-target picker.
const (
	dispatchTargetPickerID = "dispatch.target"
	dispatchTargetFormID   = "dispatch.target.new"
	dispatchCwdPickerID    = "dispatch.target.cwd"
	dispatchNewAgentItemID = "dispatch.target.new-agent"
)

// dispatchTargetRequest asks the owner where work should go. It is the one
// target picker of the app: the Queue Page uses it, and any page that hands
// work to an agent can (the first choice is always a new agent).
type dispatchTargetRequest struct {
	// Title names what is being dispatched ("Dispatch 2 queued tasks").
	Title string
	// ProjectID scopes the defaults (directory, harness) and the new pane.
	ProjectID string
	// TaskID is the task the pane works on; empty leaves the pane unbound.
	TaskID string
	// Defaults pre-fill the New Agent form.
	Defaults data.DispatchTargetSpec
	// BuildPrompt optionally computes the initial prompt before a new agent
	// pane is started so harnesses with native CLI prompt flags receive it at
	// process launch.
	BuildPrompt func(spec data.DispatchTargetSpec, pane data.Record) (string, bool)
	// OnTarget receives the resolved target, its pane (spawned or reused), and
	// whether the initial prompt was already delivered during pane startup.
	OnTarget func(spec data.DispatchTargetSpec, pane data.Record, promptDelivered bool)
}

// dispatchTargetItems builds the picker rows: New Agent first, always, then
// the live agent panes.
func dispatchTargetItems(defaults data.DispatchTargetSpec, panes []picker.Item) []picker.Item {
	var sub []string
	if defaults.Harness != "" {
		sub = append(sub, defaults.Harness)
	}
	if defaults.CWD != "" {
		sub = append(sub, defaults.CWD)
	}
	sub = append(sub, "choose directory, harness, orchestrator, context")
	items := make([]picker.Item, 0, len(panes)+1)
	items = append(items, picker.Item{
		ID:       dispatchNewAgentItemID,
		Title:    "＋ New Agent",
		Subtitle: strings.Join(sub, " · "),
		Badges:   []ui.Badge{{Text: "new", Tone: "accent"}},
		Keywords: []string{"new agent", "spawn", "start", defaults.Harness, defaults.CWD},
	})
	return append(items, panes...)
}

// dispatchCwdItems lists the directories a new agent can start in, most
// useful first: the default, then directories by frecency, base directories
// and discovered repositories. Each is searchable by its path and its name,
// so typing "hub" finds ~/hub.
func dispatchCwdItems(first string, paths []data.PathEntry, bases []data.BaseDir, repos []data.DiscoveredRepo, home string) []picker.Item {
	var items []picker.Item
	seen := map[string]bool{}
	add := func(path, source string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		full := filepath.Clean(expandHomePath(path, home))
		if seen[full] {
			return
		}
		seen[full] = true
		shown := full
		if home != "" {
			if rest, ok := strings.CutPrefix(full, home); ok && (rest == "" || strings.HasPrefix(rest, string(filepath.Separator))) {
				shown = "~" + rest
			}
		}
		items = append(items, picker.Item{
			ID: full, Title: shown, Subtitle: source,
			Keywords: []string{full, shown, filepath.Base(full)},
			Data:     full,
		})
	}
	add(first, "default")
	for _, p := range paths {
		if p.IsDir {
			add(p.Path, "recent")
		}
	}
	for _, b := range bases {
		add(b.Path, "base directory")
	}
	for _, r := range repos {
		add(r.Path, "repository")
	}
	return items
}

func (a *App) dispatchCwdChoices(first string) []picker.Item {
	home, _ := os.UserHomeDir()
	if a.files == nil {
		return dispatchCwdItems(first, nil, nil, nil, home)
	}
	return dispatchCwdItems(first, a.files.frecency.List(data.PathSortFrecency, a.now()), a.files.baseDirs, a.files.repos, home)
}

// dispatchTargetDefaults fills the unset defaults of a request from the
// project: its directory and harness.
func (a *App) dispatchTargetDefaults(req dispatchTargetRequest) data.DispatchTargetSpec {
	d := req.Defaults
	d.NewAgent = true
	var proj *data.Record
	if req.ProjectID != "" && a.Store != nil {
		proj, _ = a.Store.Get(core.Ref{Kind: data.KindTask, ID: req.ProjectID})
	}
	if d.CWD == "" && req.ProjectID != "" {
		d.CWD = a.defaultDirForTask(core.Ref{Kind: data.KindTask, ID: req.ProjectID})
	}
	if d.CWD == "" {
		if choices := a.dispatchCwdChoices(""); len(choices) > 0 {
			d.CWD, _ = choices[0].Data.(string)
		}
	}
	if d.Harness == "" && proj != nil {
		d.Harness = strings.TrimSpace(proj.Text("defaultHarness"))
	}
	if d.Harness == "" {
		for _, e := range a.Harnesses() {
			if e.ID != "" && !e.Shell {
				d.Harness = e.ID
				break
			}
		}
	}
	if d.Harness == "" {
		d.Harness = "claude"
	}
	if d.WatchMode == "" {
		d.WatchMode = data.WatchModeManual
	}
	if d.WatchQueues == "" {
		d.WatchQueues = data.QueueNameAny
	}
	return d
}

// openDispatchTargetPicker shows the target picker: New Agent first, then
// every live agent pane.
func (a *App) openDispatchTargetPicker(s core.State, req dispatchTargetRequest) core.State {
	defaults := a.dispatchTargetDefaults(req)
	paneSchema := a.schema(data.KindPane)
	var panes []picker.Item
	for _, p := range a.liveAgentPanes() {
		ref := p.Ref()
		title := views.Title(paneSchema, p)
		var sub []string
		if h := strings.TrimSpace(p.Text("harness")); h != "" {
			sub = append(sub, h)
		}
		if tRef, ok := p.RefField("task"); ok {
			sub = append(sub, a.title(tRef))
		}
		if mode := data.NormalizeWatchMode(p.Text(data.FieldWatchMode)); mode != data.WatchModeManual {
			sub = append(sub, "watches "+strings.Join(data.ParseWatchQueues(p.Text(data.FieldWatchQueues)), ",")+" "+mode)
		}
		panes = append(panes, picker.Item{
			ID: p.ID, Title: title, Subtitle: strings.Join(sub, " · "),
			Ref: &ref, Scope: data.KindPane,
			Badges:   views.StatusBadges(paneSchema, p),
			Keywords: []string{title, p.ID, p.Text("harness")},
			Data:     p,
		})
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Dispatch to"
	}
	pm := picker.New(title, dispatchTargetItems(defaults, panes))
	pm.MatchMode = a.matchMode()
	pm.Placeholder = "New Agent, or pick a live agent pane"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       dispatchTargetPickerID,
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			if picked.ID == dispatchNewAgentItemID {
				a.afterModal(func(st core.State) core.State {
					return a.openNewAgentForm(st, req, defaults)
				})
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
			}
			pane, ok := picked.Data.(data.Record)
			if !ok {
				return modal.Result{Handled: true}
			}
			spec := defaults
			spec.NewAgent, spec.ExistingPaneID = false, pane.ID
			spec.Harness, spec.CWD = pane.Text("harness"), pane.Text("cwd")
			// An existing pane keeps the watch cadence it already has.
			spec.WatchMode, spec.WatchQueues = pane.Text(data.FieldWatchMode), pane.Text(data.FieldWatchQueues)
			a.afterModal(func(st core.State) core.State {
				a.finishDispatchTarget(req, spec)
				return st
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("enter on New Agent starts one · or pick a live agent pane · / filters")
	return a.pushModal(s, m)
}

// newAgentFormSpecs are the fields of the New Agent form, in the order the
// owner decides them.
func (a *App) newAgentFormSpecs(defaults data.DispatchTargetSpec) []core.ParamSpec {
	return []core.ParamSpec{
		{Name: "cwd", Type: core.ParamPath, Default: defaults.CWD, Description: "directory the agent starts in (p searches recent directories, base directories and repositories)"},
		{Name: "harness", Type: core.ParamString, Enum: a.harnessChoices(defaults.Harness), Default: defaults.Harness, Description: "agent harness to run"},
		{Name: "orchestrator", Type: core.ParamBool, Default: defaults.Orchestrator, Description: "the agent plans and delegates instead of doing the work itself"},
		{Name: "includeProjectContext", Type: core.ParamBool, Default: defaults.IncludeProjectContext, Description: "embed the project's default and official context in the prompt"},
		{Name: "watchMode", Type: core.ParamString, Enum: data.WatchModes, Default: data.NormalizeWatchMode(defaults.WatchMode), Description: "when the agent takes the next queued item on its own: on_done, schedule, or manual"},
		{Name: "watchQueues", Type: core.ParamString, Default: defaults.WatchQueues, Description: `queues the agent watches: "any" or comma-separated names`},
	}
}

func dispatchSpecFromForm(vals map[string]any) data.DispatchTargetSpec {
	return data.DispatchTargetSpec{
		NewAgent:              true,
		CWD:                   data.Text(vals["cwd"]),
		Harness:               data.Text(vals["harness"]),
		Orchestrator:          vals["orchestrator"] == true,
		IncludeProjectContext: vals["includeProjectContext"] == true,
		WatchMode:             data.Text(vals["watchMode"]),
		WatchQueues:           data.Text(vals["watchQueues"]),
	}.Normalized()
}

func (a *App) openNewAgentForm(s core.State, req dispatchTargetRequest, defaults data.DispatchTargetSpec) core.State {
	fm := form.New("New Agent", a.newAgentFormSpecs(defaults), nil)
	fm.SubmitRow = true
	fm.SubmitLabel = "Start agent"
	fm.Wrap = a.wrapInputs()
	var mod *modal.FormModal
	mod = modal.NewFormModal(fm, modal.FormOptions{
		ID:            dispatchTargetFormID,
		Scope:         dispatchTargetFormID,
		Title:         "New Agent",
		WidthPct:      70,
		HeightPct:     70,
		DisableDrafts: true,
		OnPickPath: func(req form.PathRequest, current any) modal.Result {
			return modal.Result{Handled: true, Push: a.newDispatchCwdPicker(data.Text(current), func(path string) {
				mod.SetValue(req.Field, path)
			})}
		},
		OnSubmit: func(vals map[string]any) modal.Result {
			spec := dispatchSpecFromForm(vals)
			a.afterModal(func(st core.State) core.State {
				a.finishDispatchTarget(req, spec)
				return st
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("j/k move · p search directories · l/h cycle harness · space toggles · ctrl+s starts the agent")
	return a.pushModal(s, mod)
}

// newDispatchCwdPicker is the searchable directory picker of the New Agent
// form. A typed path that is not listed is used as it is.
func (a *App) newDispatchCwdPicker(current string, onPick func(path string)) modal.Modal {
	pm := picker.New("Agent directory", a.dispatchCwdChoices(current))
	pm.MatchMode = a.matchMode()
	pm.Placeholder = "type to search directories (hub finds ~/hub)"
	pick := func(path string) modal.Result {
		path = expandHome(strings.TrimSpace(path))
		if path != "" {
			onPick(path)
			if a.files != nil {
				_ = a.files.frecency.Record(path, true, a.now())
				a.refreshFilesPaths()
			}
		}
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
	}
	return a.newPickerModal(pm, modal.PickerOptions{
		ID:            dispatchCwdPickerID,
		PageSize:      10,
		StartInFilter: true,
		Create:        &modal.PickerCreateHook{Label: "Directory", Subtitle: "start the agent in this path"},
		OnConfirm: func(picked picker.Item) modal.Result {
			path, _ := picked.Data.(string)
			return pick(path)
		},
		OnCreate: pick,
	})
}

// finishDispatchTarget resolves the pane (spawning or reusing it) and hands
// both to the request.
func (a *App) finishDispatchTarget(req dispatchTargetRequest, spec data.DispatchTargetSpec) {
	pane, delivered, err := a.resolveDispatchPane(req, spec)
	if err != nil {
		a.fail("dispatch target", err)
		return
	}
	if req.OnTarget != nil {
		req.OnTarget(spec, pane, delivered)
	}
}

// resolveDispatchPane returns the target's pane: the existing one, or a new
// agent pane started in the target's directory with its harness and watch
// cadence, bound to the request's task. When req.BuildPrompt is supplied for a
// new pane, the prompt is passed directly to attachWithPrompt so CLI harnesses
// receive it on startup.
func (a *App) resolveDispatchPane(req dispatchTargetRequest, spec data.DispatchTargetSpec) (data.Record, bool, error) {
	if a.Store == nil {
		return data.Record{}, false, fmt.Errorf("no store")
	}
	if id := strings.TrimSpace(spec.ExistingPaneID); id != "" {
		pane, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: id})
		if err != nil {
			return data.Record{}, false, err
		}
		return *pane, false, nil
	}

	harnessName := strings.TrimSpace(spec.Harness)
	if harnessName == "" {
		harnessName = "claude"
	}
	fields := a.newDispatchPaneFields(req, spec, harnessName)
	pane, cs, err := a.Store.Create(data.CreateInput{Kind: data.KindPane, Fields: fields}, User)
	if err != nil {
		return data.Record{}, false, err
	}
	initialPrompt := ""
	if req.BuildPrompt != nil {
		if built, ok := req.BuildPrompt(spec, *pane); ok {
			initialPrompt = built
		}
	}
	delivered, err := a.attachWithPrompt(*pane, initialPrompt)
	if err != nil {
		return data.Record{}, false, fmt.Errorf("start %s: %w", harnessName, err)
	}
	a.wrote("started agent "+views.Title(a.schema(data.KindPane), *pane), cs, nil)
	a.viewMemo.gen++
	return *pane, delivered, nil
}

func (a *App) newDispatchPaneFields(req dispatchTargetRequest, spec data.DispatchTargetSpec, harnessName string) map[string]any {
	title := harnessName
	if spec.Orchestrator {
		title = "orchestrator · " + harnessName
	}
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		taskID = strings.TrimSpace(req.ProjectID)
	}
	fields := map[string]any{
		"title":     title,
		"harness":   harnessName,
		"runStatus": "running",
		"role":      data.RoleNormal,
	}
	for k, v := range spec.PaneWatchFields() {
		fields[k] = v
	}
	if cwd := strings.TrimSpace(spec.CWD); cwd != "" {
		fields["cwd"] = cwd
	}
	if taskID != "" {
		ref := core.Ref{Kind: data.KindTask, ID: taskID}
		fields["task"] = ref
		fields["title"] = title + ": " + a.title(ref)
	}
	return fields
}
