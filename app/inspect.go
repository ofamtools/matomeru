package app

import (
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/triage"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/bindings"
	"ofam.dev/ochazuke/widgets/picker"
)

// The registries the app runs on are things to look at, like any noun: the
// actions (every command, and the keys it is on) and the modes. They open
// from the palette ("inspect actions", "inspect modes"), from "g n", and
// from the command line (mtm inspect actions|bindings|modes|schemas).

// What can be inspected.
const (
	InspectActions  = "actions"
	InspectBindings = "bindings"
	InspectModes    = "modes"
	InspectSchemas  = "schemas"
)

// InspectKinds lists what Inspect takes.
var InspectKinds = []string{InspectActions, InspectBindings, InspectModes, InspectSchemas}

const unboundBadge = "(unbound)"

// ActionInfo is one command and where it is bound.
type ActionInfo struct {
	ID          string           `json:"id"`
	Description string           `json:"description"`
	Namespace   string           `json:"namespace,omitempty"`
	Tags        []string         `json:"tags,omitempty"`
	Params      []core.ParamSpec `json:"params,omitempty"`
	// Keys are the bindings that run it: "j", or "picker: enter" outside the
	// normal mode, with "(scope)" after a binding of one scope.
	Keys []string `json:"keys"`
	// Mode is the primary mode where this action is bound (when bound).
	Mode string `json:"mode,omitempty"`
	// When is the guard expression that enables this action (when guarded).
	When string `json:"when,omitempty"`
	// CLI is the equivalent mtm CLI command when applicable.
	CLI string `json:"cli,omitempty"`
	// MCP is the equivalent MCP tool name when applicable.
	MCP string `json:"mcp,omitempty"`
	// Bound is false for an action no key reaches.
	Bound bool `json:"bound"`
	// Internal marks an action that only makes sense from its own key
	// (typing into a filter): the palette leaves it out.
	Internal bool `json:"internal,omitempty"`
}

// ModeInfo is one input mode.
type ModeInfo struct {
	Name        string `json:"name"`
	Base        string `json:"base,omitempty"`
	Passthrough bool   `json:"passthrough,omitempty"`
	// Bindings is how many bindings name the mode.
	Bindings int `json:"bindings"`
}

func bindingKeys(b core.Binding) string {
	keys := strings.Join(b.Keys, " ")
	if keys == "" {
		keys = "‹" + b.Class + "›"
	}
	if b.Mode != "" && b.Mode != ModeNormal {
		keys = b.Mode + ": " + keys
	}
	if b.Scope != "" {
		keys += " (" + b.Scope + ")"
	}
	return keys
}

var actionCLIMap = map[string]string{
	"record.new":              "mtm <noun> create",
	"record.edit":             "mtm <noun> edit <id>",
	"record.open":             "mtm <noun> get <id>",
	"record.star":             "mtm <noun> star <id>",
	"record.unstar":           "mtm <noun> unstar <id>",
	"record.archive":          "mtm <noun> archive <id>",
	"record.restore":          "mtm <noun> restore <id>",
	"record.snooze":           "mtm <noun> snooze <id>",
	"record.unsnooze":         "mtm <noun> unsnooze <id>",
	"record.assign":           "mtm <noun> set <id> --field task=<task-id>",
	"store.undo":              "mtm undo",
	"store.redo":              "mtm redo",
	"app.about":               "mtm about",
	"bindings.open":           "mtm binding list",
	"settings.open":           "mtm config export",
	"inspect.open":            "mtm inspect actions",
	"daemon.reset":            "mtm daemon reset",
	"extension.refresh":       "mtm extension discover",
	"extension.run":           "mtm extension run <name> [args...]",
	"extension.setup":         "mtm extension run <name> setup",
	"question.answer":         "mtm question answer <id> --answer \"...\"",
	"command.run":             "mtm command get <id>",
	"pane.associate":          "mtm pane set <id> --field task=<task-id>",
	"lane.show":               "mtm lane get <id>",
	"triage.rule.from-signal": "mtm rule create",
}

var actionMCPMap = map[string]string{
	"record.new":              "data.create",
	"record.edit":             "data.patch",
	"record.open":             "data.get",
	"record.star":             "data.star",
	"record.unstar":           "data.unstar",
	"record.archive":          "data.archive",
	"record.restore":          "data.restore",
	"record.snooze":           "data.snooze",
	"record.unsnooze":         "data.unsnooze",
	"record.assign":           "data.patch",
	"store.undo":              "data.undo",
	"store.redo":              "data.redo",
	"question.answer":         "ui.ask",
	"command.run":             "data.suggest_command",
	"pane.associate":          "data.patch",
	"context.attach":          "data.relate",
	"project.context.attach":  "data.relate",
	"lane.show":               "lane.status",
	"triage.rule.from-signal": "triage.apply",
}

func actionNamespace(sp core.CommandSpec) string {
	if sp.Meta != nil {
		if ns, ok := sp.Meta["noun"].(string); ok && strings.TrimSpace(ns) != "" {
			return strings.TrimSpace(ns)
		}
		if kinds, ok := sp.Meta["kinds"].([]string); ok && len(kinds) == 1 && strings.TrimSpace(kinds[0]) != "" {
			return strings.TrimSpace(kinds[0])
		}
	}
	if ns, _, ok := strings.Cut(sp.ID, "."); ok && ns != "" {
		return ns
	}
	return sp.ID
}

// ActionRows returns the enriched action catalog rows for the given bindings and specs.
func ActionRows(rows []core.Binding, specs []core.CommandSpec) []ActionInfo {
	return ActionsOf(rows, specs)
}

// ActionsOf lists every command of the registry (core's and specs) with the
// bindings that run it, by ID.
func ActionsOf(rows []core.Binding, specs []core.CommandSpec) []ActionInfo {
	reg := core.DefaultRegistry()
	reg.Register(specs...)
	keys := map[string][]string{}
	primaryMode := map[string]string{}
	whenExprs := map[string][]string{}
	for _, b := range rows {
		if b.Command == "" || b.Unbind {
			continue
		}
		if k := bindingKeys(b); !slices.Contains(keys[b.Command], k) {
			keys[b.Command] = append(keys[b.Command], k)
		}
		m := b.Mode
		if m == "" {
			m = ModeNormal
		}
		if prev, ok := primaryMode[b.Command]; !ok || (prev != ModeNormal && m == ModeNormal) {
			primaryMode[b.Command] = m
		}
		if w := strings.TrimSpace(b.When); w != "" && !slices.Contains(whenExprs[b.Command], w) {
			whenExprs[b.Command] = append(whenExprs[b.Command], w)
		}
	}
	var out []ActionInfo
	for _, sp := range reg.List() {
		whenVal := ""
		if sp.Meta != nil {
			if w, ok := sp.Meta["when"].(string); ok && strings.TrimSpace(w) != "" {
				whenVal = strings.TrimSpace(w)
			}
		}
		if whenVal == "" && len(whenExprs[sp.ID]) > 0 {
			whenVal = strings.Join(whenExprs[sp.ID], " || ")
		}
		cliVal := actionCLIMap[sp.ID]
		if sp.Meta != nil {
			if v, ok := sp.Meta["cli"].(string); ok && v != "" {
				cliVal = v
			}
		}
		mcpVal := actionMCPMap[sp.ID]
		if sp.Meta != nil {
			if v, ok := sp.Meta["mcp"].(string); ok && v != "" {
				mcpVal = v
			}
		}
		info := ActionInfo{
			ID:          sp.ID,
			Description: sp.Description,
			Namespace:   actionNamespace(sp),
			Tags:        sp.Tags,
			Params:      sp.Params,
			Keys:        keys[sp.ID],
			Mode:        primaryMode[sp.ID],
			When:        whenVal,
			CLI:         cliVal,
			MCP:         mcpVal,
			Internal:    internal(sp),
		}
		if info.Keys == nil {
			info.Keys = []string{}
		}
		info.Bound = len(info.Keys) > 0
		out = append(out, info)
	}
	slices.SortStableFunc(out, func(x, y ActionInfo) int { return strings.Compare(x.ID, y.ID) })
	return out
}

// ModesOf lists the modes with how many bindings name each.
func ModesOf(modes []core.Mode, rows []core.Binding) []ModeInfo {
	out := make([]ModeInfo, len(modes))
	for i, m := range modes {
		out[i] = ModeInfo{Name: m.Name, Base: m.Base, Passthrough: m.Passthrough}
		for _, b := range rows {
			if b.Mode == m.Name && !b.Unbind {
				out[i].Bindings++
			}
		}
	}
	return out
}

// Inspect returns one of the registries as data: []ActionInfo for
// "actions", []core.Binding for "bindings" (the user's rows first, as the
// keymap reads them), []ModeInfo for "modes" and []data.Schema for
// "schemas". It reads the bindings of configDir ("" reads none).
func Inspect(what, configDir string) (any, error) {
	if !slices.Contains(InspectKinds, what) {
		return nil, fmt.Errorf("inspect %s: not one of %s", what, strings.Join(InspectKinds, ", "))
	}
	store, err := data.Open(":memory:")
	if err != nil {
		return nil, err
	}
	defer store.Close()
	a, err := New(store, Options{ConfigDir: configDir})
	if err != nil {
		return nil, err
	}
	defer a.Close()
	switch what {
	case InspectActions:
		return ActionsOf(a.bindings, a.specs), nil
	case InspectBindings:
		return a.bindings, nil
	case InspectModes:
		return ModesOf(Modes(), a.bindings), nil
	}
	return store.Registry().List(), nil
}

// InspectText renders what Inspect returned as a table.
func InspectText(v any) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 2, 8, 2, ' ', 0)
	switch rows := v.(type) {
	case []ActionInfo:
		fmt.Fprintln(tw, "ACTION\tKEYS\tDESCRIPTION")
		for _, r := range rows {
			keys := strings.Join(r.Keys, ", ")
			if !r.Bound {
				keys = unboundBadge
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", r.ID, keys, r.Description)
		}
	case []core.Binding:
		fmt.Fprintln(tw, "MODE\tSCOPE\tKEYS\tCOMMAND\tWHEN")
		for _, r := range rows {
			keys := strings.Join(r.Keys, " ")
			if keys == "" {
				keys = "‹" + r.Class + "›"
			}
			command := r.Command
			switch {
			case r.Layer != nil:
				command = "layer " + r.Layer.Name
			case r.Unbind:
				command = "(unbind)"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Mode, r.Scope, keys, command, r.When)
		}
	case []ModeInfo:
		fmt.Fprintln(tw, "MODE\tBASE\tPASSTHROUGH\tBINDINGS")
		for _, r := range rows {
			fmt.Fprintf(tw, "%s\t%s\t%v\t%d\n", r.Name, r.Base, r.Passthrough, r.Bindings)
		}
	case []data.Schema:
		fmt.Fprintln(tw, "KIND\tTITLE\tFIELDS\tSTATUS COLUMNS")
		for _, r := range rows {
			var fields, cols []string
			for _, f := range r.Fields {
				fields = append(fields, f.Name)
			}
			for _, c := range r.StatusColumns {
				cols = append(cols, c.Name)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Kind, r.Title, strings.Join(fields, " "), strings.Join(cols, " "))
		}
	}
	tw.Flush()
	return b.String()
}

// --- the inspector overlay ----------------------------------------------------------

func (a *App) inspectItems(what string) []picker.Item {
	var items []picker.Item
	switch what {
	case InspectActions:
		for _, info := range ActionsOf(a.bindings, a.specs) {
			item := picker.Item{ID: info.ID, Title: info.ID, Subtitle: info.Description, Keywords: append(slices.Clone(info.Tags), info.Keys...)}
			if info.Bound {
				item.Badges = []ui.Badge{{Text: strings.Join(info.Keys, ", ")}}
			} else {
				item.Badges = []ui.Badge{{Text: unboundBadge, Tone: "warning"}}
				item.Keywords = append(item.Keywords, "unbound")
			}
			if info.Internal {
				item.Badges = append(item.Badges, ui.Badge{Text: "internal", Tone: "muted"})
			}
			items = append(items, item)
		}
	case InspectModes:
		for _, info := range ModesOf(Modes(), a.bindings) {
			item := picker.Item{ID: info.Name, Title: info.Name, Subtitle: "its own keys only", Badges: []ui.Badge{{Text: fmt.Sprintf("%d bindings", info.Bindings)}}}
			if info.Base != "" {
				item.Subtitle = "base " + info.Base + ": its keys work here too"
				item.Badges = append(item.Badges, ui.Badge{Text: "base " + info.Base, Tone: "info"})
			}
			if info.Passthrough {
				item.Subtitle = "passthrough: unbound keys go to the focused widget"
				item.Badges = append(item.Badges, ui.Badge{Text: "passthrough", Tone: "accent"})
			}
			items = append(items, item)
		}
	}
	return items
}

// inspectOpen opens the inspector on a registry.
func (a *App) inspectOpen(s core.State, c core.Command) core.StepResult {
	what := c.Arg("what")
	if what != InspectActions && what != InspectModes {
		a.say("inspect %q: here it is actions or modes · mtm inspect also lists bindings and schemas", what)
		return core.Next(s)
	}
	pm := picker.New(strings.ToUpper(what[:1])+what[1:], a.inspectItems(what))
	pm.PageSize = 12
	a.pickers[modalInspect] = pm
	a.inspecting = what
	if what == InspectActions {
		a.say("/ filters (try unbound) · a binds the action to a free key · esc closes")
	} else {
		a.say("/ filters · esc closes")
	}
	return core.Next(openModal(a.dropModals(s), modalInspect, ModeInspect, modalInspect+".list"))
}

// inspectCommand runs the inspector's keys: the filter, and binding the
// focused action to a key nothing else is on.
func (a *App) inspectCommand(s core.State, c core.Command) core.StepResult {
	pm := a.pickers[modalInspect]
	if pm == nil || topModal(s) != modalInspect {
		return core.Next(s)
	}
	switch c.ID {
	case CmdInspectFilter:
		a.say("type to filter · enter keeps · esc closes")
		return core.Next(withMode(s, ModePicker))
	case CmdInspectBind:
		item, ok := pm.Focused()
		switch {
		case a.inspecting != InspectActions:
			a.say("only an action can be bound to a key")
		case !ok:
			a.say("no action here to bind")
		default:
			a.bindAction(item.ID)
			selected := pm.SelectedIdx
			pm.SetItems(a.inspectItems(InspectActions))
			pm.SelectedIdx = selected
		}
	}
	return core.Next(s)
}

// bindAction puts an action on the first key sequence nothing else uses in
// the normal mode, and saves it as one more row of bindings.json.
func (a *App) bindAction(id string) {
	keys, ok := bindings.FindFreeBinding(Modes(), a.bindings, "", ModeNormal, false, nil)
	if !ok {
		a.say("no free key was found for %s · B edits the bindings", id)
		return
	}
	row := core.Binding{Mode: ModeNormal, Keys: keys, Command: id, Hint: id}
	if d := config.CheckBindings(Modes(), []core.Binding{row}, a.specs...); slices.ContainsFunc(d, func(x config.BindingDiagnostic) bool { return x.Severity == config.SevError }) {
		a.say("%s cannot be bound: %s", id, d[0].Message)
		return
	}
	a.userBindings = append(a.userBindings, row)
	a.bindings = config.MergeBindings(a.baseBindings(), a.userBindings)
	what := fmt.Sprintf("bound %s to %s", id, strings.Join(keys, " "))
	if err := a.rebuildKeymap(); err != nil {
		what += " · " + err.Error()
	}
	a.bindEd = nil // the binding editor reads the rows again when it opens
	saved, err := a.writeConfig(bindingsFileName, a.userBindings)
	switch {
	case err != nil:
		a.fail("save bindings", err)
	case saved:
		a.say("%s · live · saved to %s", what, bindingsFileName)
	default:
		a.say("%s · live for this session", what)
	}
}

// --- a triage rule from a signal ----------------------------------------------------

// ruleFromSignal opens a short form for a triage rule drafted from the
// focused signal. Sending it creates the rule and runs every rule over the
// signals still unreviewed, so the ones like this one are cleaned up at once.
func (a *App) ruleFromSignal(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindSignal {
		a.say("no signal here to make a rule from")
		return core.Next(s)
	}
	sig, err := a.Store.Get(ref)
	if err != nil {
		a.fail("rule", err)
		return core.Next(s)
	}
	draft := triage.DraftRuleFromSignal(*sig)
	specs := []core.ParamSpec{
		{Name: "when", Type: core.ParamString, Required: true, Description: "which signals: an expression over source, author, eventKind, container, title"},
		{Name: "action", Type: core.ParamString, Enum: data.TriageActions, Description: "what to do with them"},
		{Name: "actionValue", Type: core.ParamString, Description: "the tag, the task ID, or how long to snooze (24h)"},
	}
	title := data.Text(draft["title"])
	next := a.openLaunch(s, "Rule from "+a.title(ref), specs, draft, func(s core.State, v map[string]any) (core.State, bool) {
		fields := map[string]any{"title": title, "when": data.Text(v["when"]), "action": data.Text(v["action"]),
			"actionValue": data.Text(v["actionValue"]), "priority": 0, "enabled": true}
		if fields["action"] == "" {
			fields["action"] = data.ActionMarkNoise
		}
		rule, _, err := a.Store.Create(data.CreateInput{Kind: data.KindTriageRule, Fields: fields}, User)
		if err != nil {
			a.fail("create rule", err)
			return s, false
		}
		res, err := triage.ApplyAll(a.Store, triageActor)
		if err != nil {
			a.fail("apply rules", err)
			return s, true
		}
		msg := fmt.Sprintf("rule %q made · %d of %d unreviewed signals cleaned up", rule.Text("title"), res.Matched, res.Evaluated)
		if len(res.Problems) > 0 {
			msg += " · " + res.Problems[0]
		}
		a.say("%s", msg)
		return s, true
	})
	a.launch.fm.SubmitLabel = "make the rule and apply it"
	a.say("i types · h/l choose the action · ctrl+s makes the rule and applies it")
	return core.Next(next)
}

// triageActor is who a rule's actions are written as: the rule is the
// owner's, what it does to signals is the rule's.
var triageActor = core.Actor{Kind: "system", ID: "mtm.triage", Label: "triage"}
