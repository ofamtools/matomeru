package app

import (
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/actions"
	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/toast"
)

const (
	CmdActionsPick    = "actions.pick"
	CmdWebOpen        = "web.open"
	CmdUpgradePreview = "upgrade.preview"

	customActionsModalID    = "custom.actions"
	customActionFormModalID = "custom.action.form"
	customActionSingleScope = "custom.action.single"
	customActionMultiScope  = "custom.action.multi"
	upgradePreviewModalID   = "upgrade.preview"
)

func customActionsSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdActionsPick,
			Description: "Open the Custom Actions picker (built-in recipes and parameterized actions.json actions)",
			Params: []core.ParamSpec{
				{Name: "id", Type: core.ParamString, Description: "Optional custom action ID to invoke directly"},
			},
		},
		{
			ID:          CmdWebOpen,
			Description: "Open a local Markdown or Mermaid file in the running MTM web viewer (mdweb)",
			Params: []core.ParamSpec{
				{Name: "file", Type: core.ParamPath, Required: true, Description: "Local .md or .mmd file path"},
			},
		},
		spec(CmdUpgradePreview, "Preview pending g U upgrade (compare installed vs running binary version and changelog delta)"),
	}
}

func customActionsBindings() []core.Binding {
	return []core.Binding{
		bind(ModeGlobal, "", "g a", CmdActionsPick, "custom actions"),
		bind(ModeForm, customActionSingleScope, "enter", form.CmdSubmit, "run"),
	}
}

func (a *App) registerCustomActions() {
	a.handle(CmdActionsPick, a.customActionsPick)
	a.handle(CmdWebOpen, a.webOpen)
	a.handle(CmdUpgradePreview, a.upgradePreview)
}

func (a *App) activeCustomActions() []actions.CustomAction {
	return actions.MergeCustomActions(a.builtinCustomActionsEnabled(), a.ext.customActions)
}

func (a *App) customActionsPick(s core.State, c core.Command) core.StepResult {
	list := a.activeCustomActions()
	focusedPath := a.focusedLocalFilePath(s, c)
	baseVars := a.customActionBaseVars(s, c, focusedPath)

	if directID := strings.TrimSpace(c.Arg("id")); directID != "" {
		for _, ca := range list {
			if ca.ID == directID {
				return a.invokeCustomActionDirectly(s, ca, focusedPath, baseVars)
			}
		}
		a.say("unknown custom action %q", directID)
		return core.Next(s)
	}

	if len(list) == 0 {
		a.say("no custom actions available (enable %s or add entries in %s)", SetBuiltinCustomActions, actions.ActionsFile)
		return core.Next(s)
	}

	items, byID := buildCustomActionPickerItems(list)
	pm := picker.New("Custom Actions", items)
	pm.Placeholder = "search custom actions (google, markdown, upgrade...)"
	pm.MatchMode = a.matchMode()

	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       customActionsModalID,
		PageSize: 12,
		OnConfirm: func(it picker.Item) modal.Result {
			ca, ok := byID[it.ID]
			if !ok {
				return modal.Result{Handled: true, Close: true}
			}
			return a.confirmCustomActionChoice(ca, focusedPath, baseVars)
		},
	})
	return core.Next(a.pushModal(s, m))
}

func buildCustomActionPickerItems(list []actions.CustomAction) ([]picker.Item, map[string]actions.CustomAction) {
	items := make([]picker.Item, 0, len(list))
	byID := make(map[string]actions.CustomAction, len(list))
	for _, ca := range list {
		byID[ca.ID] = ca
		cat := ca.DisplayCategory()
		badges := []ui.Badge{
			{Text: "[" + cat + "]", Tone: customCategoryTone(cat)},
		}
		if len(ca.Params) == 0 {
			badges = append(badges, ui.Badge{Text: "instant", Tone: "ok"})
		} else {
			badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d arg", len(ca.Params)), Tone: "info"})
		}
		if ca.Source != "" {
			badges = append(badges, ui.Badge{Text: ca.Source, Tone: "muted"})
		}
		kw := append([]string{ca.ID, cat, ca.Source, "custom", "action"}, ca.Keywords...)
		for _, p := range ca.Params {
			kw = append(kw, p.Name, p.Type)
		}
		items = append(items, picker.Item{
			ID:       ca.ID,
			Title:    ca.DisplayTitle(),
			Subtitle: ca.Description,
			Badges:   badges,
			Keywords: kw,
		})
	}
	return items, byID
}

func customCategoryTone(cat string) string {
	switch cat {
	case "web":
		return "info"
	case "files":
		return "accent"
	case "system":
		return "warn"
	default:
		return "muted"
	}
}

func (a *App) confirmCustomActionChoice(ca actions.CustomAction, focusedPath string, baseVars map[string]string) modal.Result {
	if ca.HasRequiredParams() {
		a.afterModal(func(st core.State) core.State {
			return a.openCustomActionForm(st, ca, focusedPath, baseVars)
		})
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
	}
	if ca.Exec.Verb != "" {
		return modal.Result{
			Handled:     true,
			Close:       true,
			CloseReason: modal.CloseSubmit,
			Emit: []core.Command{{
				ID:     ca.Exec.Verb,
				Args:   actions.ExpandArgs(ca.Exec.Args, baseVars),
				Source: "custom.actions",
				Actor:  User,
			}},
		}
	}
	a.afterModal(func(st core.State) core.State {
		return a.executeCustomAction(st, ca, nil, baseVars)
	})
	return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
}

func (a *App) invokeCustomActionDirectly(s core.State, ca actions.CustomAction, focusedPath string, baseVars map[string]string) core.StepResult {
	if ca.HasRequiredParams() {
		return core.Next(a.openCustomActionForm(s, ca, focusedPath, baseVars))
	}
	return core.Next(a.executeCustomAction(s, ca, nil, baseVars))
}

func (a *App) openCustomActionForm(s core.State, ca actions.CustomAction, focusedPath string, baseVars map[string]string) core.State {
	specs := ca.ToParamSpecs()
	initial := customActionInitialValues(ca.Params, focusedPath)
	fm := form.New(ca.DisplayTitle(), specs, initial)
	fm.SubmitRow = true
	fm.SubmitLabel = "Run " + ca.DisplayTitle()
	fm.Wrap = a.wrapInputs()

	if len(specs) > 0 && len(specs[0].Enum) == 0 && (specs[0].Type == core.ParamString || specs[0].Type == core.ParamPath) {
		fm.HandleCommand(core.Command{ID: form.CmdEdit})
	}

	scope := customActionMultiScope
	if len(specs) == 1 {
		scope = customActionSingleScope
	}
	var mod *modal.FormModal
	mod = modal.NewFormModal(fm, modal.FormOptions{
		ID:            customActionFormModalID,
		Scope:         scope,
		Title:         ca.DisplayTitle(),
		WidthPct:      68,
		DisableDrafts: true,
		OnPickPath:    a.formPickPath(func() *modal.FormModal { return mod }),
		OnPickRef:     a.formPickRef(func() *modal.FormModal { return mod }),
		OnSubmit: func(vals map[string]any) modal.Result {
			return a.submitCustomActionForm(ca, vals, baseVars)
		},
	})
	a.say("%s", customActionFormHint(ca, specs))
	return a.pushModal(s, mod)
}

func customActionInitialValues(params []actions.CustomParamSpec, focusedPath string) map[string]any {
	initial := map[string]any{}
	for _, p := range params {
		if p.Default != nil {
			initial[p.Name] = p.Default
		}
		if p.Type == "path" && strings.TrimSpace(data.Text(initial[p.Name])) == "" && focusedPath != "" {
			initial[p.Name] = focusedPath
		}
	}
	return initial
}

func customActionFormHint(ca actions.CustomAction, specs []core.ParamSpec) string {
	hasPath := false
	for _, sp := range specs {
		if sp.Type == core.ParamPath {
			hasPath = true
			break
		}
	}
	if hasPath {
		return fmt.Sprintf("%s · p picks file · ctrl+s runs · esc cancels", ca.DisplayTitle())
	}
	return fmt.Sprintf("%s · ctrl+s runs · esc cancels", ca.DisplayTitle())
}

func (a *App) submitCustomActionForm(ca actions.CustomAction, vals map[string]any, baseVars map[string]string) modal.Result {
	vars := mergeCustomActionVars(baseVars, vals)
	if ca.Exec.Verb != "" {
		verbArgs := buildCustomVerbArgs(ca.Exec.Args, vals, vars)
		return modal.Result{
			Handled:     true,
			Close:       true,
			CloseReason: modal.CloseSubmit,
			Emit: []core.Command{{
				ID:     ca.Exec.Verb,
				Args:   verbArgs,
				Source: "custom.actions",
				Actor:  User,
			}},
		}
	}
	a.afterModal(func(st core.State) core.State {
		return a.executeCustomAction(st, ca, vals, vars)
	})
	return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
}

func mergeCustomActionVars(base map[string]string, vals map[string]any) map[string]string {
	vars := maps.Clone(base)
	if vars == nil {
		vars = map[string]string{}
	}
	for k, v := range vals {
		if v == nil {
			continue
		}
		vars[k] = data.Text(v)
	}
	return vars
}

func buildCustomVerbArgs(rawArgs map[string]any, vals map[string]any, vars map[string]string) map[string]any {
	if len(rawArgs) > 0 {
		return actions.ExpandArgs(rawArgs, vars)
	}
	return maps.Clone(vals)
}

func (a *App) executeCustomAction(s core.State, ca actions.CustomAction, vals map[string]any, vars map[string]string) core.State {
	vars = mergeCustomActionVars(vars, vals)
	if ca.Exec.Verb != "" {
		verbArgs := buildCustomVerbArgs(ca.Exec.Args, vals, vars)
		return a.dispatchCustomVerb(s, ca.Exec.Verb, verbArgs)
	}
	if ca.Exec.URL != "" {
		targetURL := strings.TrimSpace(actions.ExpandTemplate(ca.Exec.URL, vars))
		if targetURL == "" {
			a.say("%s: empty URL", ca.DisplayTitle())
			return s
		}
		return a.openExternalURL(s, ca.ID, ca.DisplayTitle(), targetURL)
	}
	if len(ca.Exec.Argv) > 0 {
		spec := actions.RecordActionSpec{
			ID:          ca.ID,
			Description: ca.DisplayTitle(),
			Exec: actions.ExecSpec{
				Argv:       ca.Exec.Argv,
				Dir:        ca.Exec.Dir,
				Env:        ca.Exec.Env,
				Foreground: ca.Exec.Foreground,
				Title:      ca.DisplayTitle(),
			},
		}
		return a.execRecordAction(s, spec, nil, vars)
	}
	if ca.Exec.Shell != "" {
		expanded := strings.TrimSpace(actions.ExpandTemplate(ca.Exec.Shell, vars))
		if expanded == "" {
			a.say("%s: empty shell command", ca.DisplayTitle())
			return s
		}
		shellVars := maps.Clone(vars)
		shellVars["__custom_shell"] = expanded
		spec := actions.RecordActionSpec{
			ID:          ca.ID,
			Description: ca.DisplayTitle(),
			Exec: actions.ExecSpec{
				Argv:       []string{"sh", "-c", "{{__custom_shell}}"},
				Dir:        ca.Exec.Dir,
				Env:        ca.Exec.Env,
				Foreground: ca.Exec.Foreground,
				Title:      ca.DisplayTitle(),
			},
		}
		return a.execRecordAction(s, spec, nil, shellVars)
	}
	a.say("%s: no execution target configured", ca.DisplayTitle())
	return s
}

func (a *App) dispatchCustomVerb(s core.State, verb string, args map[string]any) core.State {
	var facts map[string]any
	if a.Shell != nil && a.Shell.AppFacts != nil {
		facts = a.Shell.AppFacts(s)
	}
	cmd := core.Command{ID: verb, Args: args, Source: "custom.actions", Actor: User}
	step := a.Engine.Step(s, ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, facts), core.CommandEvent{Command: cmd})
	for _, eff := range step.Effects {
		a.effect(eff)
	}
	return step.State
}

func (a *App) openExternalURL(s core.State, id, title, targetURL string) core.State {
	spec := actions.RecordActionSpec{
		ID:          id,
		Description: title,
		Exec: actions.ExecSpec{
			Argv:  []string{systemOpener(), "{{__target_url}}"},
			Title: title,
		},
		Feedback: &actions.FeedbackSpec{
			Success: "Opened " + title,
			Queued:  "Opened " + title,
		},
	}
	return a.execRecordAction(s, spec, nil, map[string]string{"__target_url": targetURL})
}

func (a *App) focusedLocalFilePath(s core.State, c core.Command) string {
	ref, ok := a.subject(s, c)
	if !ok {
		return ""
	}
	rec, err := a.Store.Get(ref)
	if err != nil || rec == nil {
		return ""
	}
	t, ok := a.targetOf(*rec)
	if !ok || t.path == "" || handlers.IsURL(t.path) {
		return ""
	}
	return t.path
}

func (a *App) customActionBaseVars(s core.State, c core.Command, focusedPath string) map[string]string {
	vars := map[string]string{}
	if focusedPath != "" {
		vars["focusedFile"] = focusedPath
	}
	if ref, ok := a.subject(s, c); ok {
		vars["focusedKind"] = ref.Kind
		vars["focusedID"] = ref.ID
		vars["focusedTitle"] = a.title(ref)
	}
	return vars
}

func (a *App) mdwebBaseURL() string {
	if base := strings.TrimRight(a.daemonMCPBase(), "/"); base != "" {
		return base
	}
	if envURL := strings.TrimSpace(os.Getenv(daemon.EnvMCPURL)); envURL != "" {
		if u, err := url.Parse(envURL); err == nil && u.Scheme != "" && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	return ""
}

// webOpen opens a local Markdown or Mermaid file in the running MTM daemon's
// mdweb viewer (`http://127.0.0.1:<port>/md/view?file=<absPath>`).
func (a *App) webOpen(s core.State, c core.Command) core.StepResult {
	rawPath := strings.TrimSpace(c.Arg("file"))
	if rawPath == "" {
		a.say("web.open: missing file path")
		return core.Next(s)
	}
	expanded := expandHome(rawPath)
	absPath, err := filepath.Abs(expanded)
	if err != nil {
		a.fail("web.open", err)
		a.pushToast("web.open: "+err.Error(), toast.Err)
		return core.Next(s)
	}
	st, err := os.Stat(absPath)
	if err != nil {
		a.fail("web.open", err)
		a.pushToast("web.open: "+err.Error(), toast.Err)
		return core.Next(s)
	}
	if st.IsDir() {
		msg := fmt.Sprintf("web.open: %s is a directory; pick a .md or .mmd file", filepath.Base(absPath))
		a.say("%s", msg)
		a.pushToast(msg, toast.Warn)
		return core.Next(s)
	}
	base := a.mdwebBaseURL()
	if base == "" {
		msg := fmt.Sprintf("mdweb daemon not running · start mtm daemon or run: mtm web %s", absPath)
		a.say("%s", msg)
		a.pushToast(msg, toast.Warn)
		return core.Next(s)
	}
	viewURL := base + "/md/view?file=" + url.QueryEscape(absPath)
	title := "MTM Web Viewer (" + filepath.Base(absPath) + ")"
	return core.Next(a.openExternalURL(s, CmdWebOpen, title, viewURL))
}
