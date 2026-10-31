package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
	osetup "ofam.dev/ochazuke/setup"
)

// Extension setup: a registered extension that declares setup items
// (ochazuke's shared setup, cli.DeclareSetup) answers the ochazuke.setup
// noun in its catalog. mtm reads from the catalog only whether it has
// setup; the items' states are runtime facts (files, harness config,
// check commands) only the tool can probe, so mtm asks `<bin>
// ochazuke.setup plan --json {}` off the loop (one exec answers both the
// item states and the writes and commands an install would make) and
// keeps the answer per extension, like the catalogs: checked at startup
// and when the Extensions page opens, and again after every setup verb.
//
// Each extension's setup is one of:
//
//	complete     every item that applies is installed
//	needs setup  N of the M items that apply are missing, outdated or in conflict
//	no setup     the catalog declares no setup items
//	unknown      the tool's plan failed (the error is in the detail)
//
// The verbs: extension.setup (install, confirmed with the plan's
// summary; agents never), extension.setup-plan (the whole plan as text),
// extension.setup-status (data for the CLI and MCP; every registered
// extension when NAME is omitted) and extension.setup-undo (uninstall,
// confirmed; agents never). Nothing here names an extension: any tool
// whose catalog has the setup noun works.

// extSetupNoun is ochazuke's shared setup noun in a catalog.
const extSetupNoun = "ochazuke.setup"

// settingExtSetupStatus turns the setup column, the detail's setup
// section, the page's summary line, the key's hint and the automatic
// checks off; the verbs stay.
const settingExtSetupStatus = "extensions.setup-status"

var extSetupStatusSetting = ochazuke.Setting{Name: settingExtSetupStatus, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection,
	Type: ochazuke.SettingBoolean, Default: true,
	Description: "Check registered extensions' setup on startup and page open, and show it on the Extensions page (a setup column, the detail's items, a summary line and the setup key's hint); off hides them, the extension.setup verbs stay"}

// Setup states.
const (
	setupComplete = "complete"
	setupNeeded   = "needs setup"
	setupNone     = "no setup"
	setupUnknown  = "unknown"
	setupChecking = "checking"
)

// extSetupInfo is one extension's setup, as extension.setup-status
// answers it.
type extSetupInfo struct {
	Name      string              `json:"name"`
	State     string              `json:"state"`
	Missing   int                 `json:"missing"`
	Total     int                 `json:"total"`
	Error     string              `json:"error,omitempty"`
	CheckedAt time.Time           `json:"checkedAt,omitzero"`
	Items     []osetup.Status     `json:"items,omitempty"`
	Writes    []osetup.Write      `json:"writes,omitempty"`
	Commands  []osetup.Invocation `json:"commands,omitempty"`
}

// extHasSetup reports whether a catalog declares setup items: it has the
// shared setup noun with the verbs mtm runs.
func extHasSetup(doc ocli.CatalogDoc) bool {
	for _, n := range doc.Nouns {
		if n.Name != extSetupNoun {
			continue
		}
		have := map[string]bool{}
		for _, v := range n.Verbs {
			have[v.Name] = true
		}
		return have["plan"] && have["install"] && have["uninstall"]
	}
	return false
}

// setupNeeds reports whether an item's state is one install changes.
func setupNeeds(s osetup.State) bool {
	return s == osetup.Missing || s == osetup.Outdated || s == osetup.Conflict
}

// setupApplies reports whether an item counts toward M: it is neither
// not applicable here nor skipped by the owner.
func setupApplies(s osetup.State) bool {
	return s != osetup.NotApplicable && s != osetup.Skipped
}

// summarizeSetup is the state of a plan's items.
func summarizeSetup(name string, plan osetup.Plan) extSetupInfo {
	info := extSetupInfo{Name: name, State: setupComplete, Items: plan.Items, Writes: plan.Writes, Commands: plan.Commands}
	for _, it := range plan.Items {
		if setupApplies(it.State) {
			info.Total++
		}
		if setupNeeds(it.State) {
			info.Missing++
		}
	}
	if info.Missing > 0 {
		info.State = setupNeeded
	}
	return info
}

// parseSetupPlan reads a tool's `ochazuke.setup plan --json` answer.
func parseSetupPlan(out []byte) (osetup.Plan, error) {
	var p osetup.Plan
	if err := json.Unmarshal(out, &p); err != nil {
		return p, fmt.Errorf("setup plan: %w", err)
	}
	return p, nil
}

// setupCell is the setup column's cell.
func setupCell(info extSetupInfo, ok bool) string {
	switch {
	case !ok:
		return ""
	case info.State == setupNeeded:
		return fmt.Sprintf("needs %d of %d", info.Missing, info.Total)
	case info.State == setupChecking:
		return "checking…"
	}
	return info.State
}

// setupLine is the detail's and the plan's long form of a state.
func setupLine(info extSetupInfo) string {
	if info.State == setupNeeded {
		return fmt.Sprintf("needs setup (%d of %d items missing)", info.Missing, info.Total)
	}
	return setupCell(info, true)
}

// extSetupTarget is what a setup verb needs from the registry, read on the
// loop: the bin, its environment, and whether its catalog has setup.
type extSetupTarget struct {
	name, bin string
	env       []string
	has       bool
}

// extSetupTargets reads names from the registry (every registered
// extension when names is empty).
func (a *App) extSetupTargets(names ...string) ([]extSetupTarget, error) {
	reg := a.extRegistry()
	if len(names) == 0 {
		var err error
		if names, err = reg.Names(); err != nil {
			return nil, err
		}
	}
	out := make([]extSetupTarget, 0, len(names))
	for _, name := range names {
		e, err := reg.Read(name)
		if err != nil {
			return nil, err
		}
		out = append(out, extSetupTarget{name: name, bin: e.Bin, env: a.extEnv(name), has: extHasSetup(e.Catalog)})
	}
	return out, nil
}

// setupArgs is the JSON arguments of a setup verb: the group, when given.
func setupArgs(group string) string {
	if group == "" {
		return "{}"
	}
	js, _ := json.Marshal(map[string]string{"group": group})
	return string(js)
}

// probeSetup asks one tool its plan (off the loop).
func probeSetup(ctx context.Context, t extSetupTarget) extSetupInfo {
	if !t.has {
		return extSetupInfo{Name: t.name, State: setupNone, CheckedAt: time.Now()}
	}
	out, err := execExt(ctx, t.bin, t.env, extSetupNoun, "plan", "--json", "{}")
	var plan osetup.Plan
	if err == nil {
		plan, err = parseSetupPlan(out)
	}
	if err != nil {
		return extSetupInfo{Name: t.name, State: setupUnknown, Error: err.Error(), CheckedAt: time.Now()}
	}
	info := summarizeSetup(t.name, plan)
	info.CheckedAt = time.Now()
	return info
}

// extSetupOn reports whether the setup status is shown and checked.
func (a *App) extSetupOn() bool { return a.r.Settings().Bool(settingExtSetupStatus) }

// noteSetup stores checked states and redraws the page's rows.
func (a *App) noteSetup(infos []extSetupInfo) {
	if a.extPage.setup == nil {
		a.extPage.setup = map[string]extSetupInfo{}
	}
	for _, info := range infos {
		a.extPage.setup[info.Name] = info
	}
	if a.extList != nil && a.extPage.discovered {
		if err := a.setExtRows(a.extPage.rows); err != nil {
			a.r.UI().Error(err.Error())
		}
	}
}

// setupCheckAtStart is startup's check of every registered extension's
// setup, as a command for Init.
func (a *App) setupCheckAtStart() tea.Cmd {
	if !a.checkExtSetup() {
		return nil
	}
	return a.r.PendingCommands()
}

// checkExtSetup checks the setup of names (every registered extension
// when empty) off the loop, when the setting is on; each row reads
// "checking…" until its first answer lands, then keeps the last one. The
// job's command is queued (the model drains it after the message); it
// reports whether one started.
func (a *App) checkExtSetup(names ...string) bool {
	if a.cli || !a.extSetupOn() {
		return false
	}
	targets, err := a.extSetupTargets(names...)
	if err != nil || len(targets) == 0 {
		return false
	}
	var checking []extSetupInfo
	for _, t := range targets {
		if !t.has {
			continue
		}
		// A row keeps its last answer (and its key) while it is checked
		// again; only a first check reads "checking…".
		if _, ok := a.extPage.setup[t.name]; !ok {
			checking = append(checking, extSetupInfo{Name: t.name, State: setupChecking})
		}
	}
	a.noteSetup(checking)
	res := ochazuke.Pending(func(ctx context.Context) (any, error) {
		out := make([]extSetupInfo, 0, len(targets))
		for _, t := range targets {
			out = append(out, probeSetup(ctx, t))
		}
		return out, ctx.Err()
	}).Replacing("extension-setup-check:" + strings.Join(names, ",")).
		Then(func(_ ochazuke.Ctx, v any, err error) {
			infos, _ := v.([]extSetupInfo)
			a.noteSetup(infos)
		})
	ctx := ochazuke.Ctx{Runtime: a.r, Context: a.r.Context(), Instance: a.r.Instances["app"]}
	if _, err := a.r.Launch("app", "extension.setup-status", res, ctx); err != nil {
		a.r.UI().Notify("extension setup check: " + err.Error())
		return false
	}
	return true
}

// defineExtensionSetup adds the setup verbs, their key and guard.
func (a *App) defineExtensionSetup() {
	a.r.Define("extension").
		Verb("setup", a.extensionSetup, "Install a registered extension's setup items (its `setup install`); group limits it to one group").Positional("name", "group").
		Confirm("Set up extension {{name}}?").
		Verb("setup-plan", a.extensionSetupPlan, "Show what setting up a registered extension would change: each item's state, the files it writes and the commands it runs").Positional("name", "group").Fetch().Quiet().TextResult().
		Verb("setup-status", a.extensionSetupStatus, "Each registered extension's setup: complete, needs setup (N of M items), no setup or unknown, with its items (every one when NAME is omitted)").Positional("name").Fetch().Quiet().
		Verb("setup-undo", a.extensionSetupUndo, "Remove a registered extension's setup items (its `setup uninstall`): only what it owns").Positional("name").
		Confirm("Remove extension {{name}}'s setup?").
		Mode("normal").
		Bind("s", ochazuke.Do("extension.setup").Guard("ext-needs-setup"))
	name := ochazuke.Parameter{Type: "string", Description: "The extension; on an Extensions row, the row's"}
	group := ochazuke.Parameter{Type: "string", Description: "Only the setup items in this group"}
	a.parameters("extension.setup", map[string]ochazuke.Parameter{"name": name, "group": group})
	a.parameters("extension.setup-plan", map[string]ochazuke.Parameter{"name": name, "group": group})
	a.parameters("extension.setup-status", map[string]ochazuke.Parameter{"name": {Type: "string", Description: "The extension; omitted (and off a row), every registered one"}})
	a.parameters("extension.setup-undo", map[string]ochazuke.Parameter{"name": name})
	for _, verb := range []string{"setup", "setup-undo"} {
		key := a.r.CanonicalKey("extension." + verb)
		op := a.r.Operations[key]
		op.ConfirmFor = a.setupConfirm(verb == "setup-undo")
		a.r.Operations[key] = op
	}
	a.r.DefineGuard("ext-needs-setup", func(r *ochazuke.Runtime, ev ochazuke.KeyEvent) bool {
		c, ok := extRow(r.Instances[ev.Focus])
		if !ok || !a.extSetupOn() || (c.Status != statusRegistered && c.Status != statusStale) {
			return false
		}
		return a.extPage.setup[c.Name].State == setupNeeded
	})
}

// setupConfirm is the setup verbs' ConfirmFor: the prompt, then the last
// checked plan's summary (what changes, where), so the owner reads the
// change before y. A group narrows the items shown.
func (a *App) setupConfirm(undo bool) func(*ochazuke.Runtime, ochazuke.Context, ochazuke.Request, string) string {
	return func(r *ochazuke.Runtime, c ochazuke.Context, q ochazuke.Request, prompt string) string {
		name := ochazuke.StringArg(q.Args, "name", "")
		if name == "" {
			if row, ok := extRow(r.Instances[q.Target]); ok {
				name = row.Name
			} else if row, ok := extRow(r.Instances[r.Focus]); ok {
				name = row.Name
			}
		}
		prompt = strings.ReplaceAll(prompt, "{{name}}", name)
		info, ok := a.extPage.setup[name]
		if !ok || info.State == setupNone {
			return prompt + "\n\nThe plan was not checked yet: extension.setup-plan " + name + " shows it."
		}
		home, _ := a.home()
		return prompt + "\n\n" + setupSummary(info, ochazuke.StringArg(q.Args, "group", ""), undo, home)
	}
}

// setupSummary is a plan's short form for a confirm: the items that would
// change (installed ones for undo), each with what it changes, then the
// files written and the commands run (whole-plan; omitted for a group).
func setupSummary(info extSetupInfo, group string, undo bool, home string) string {
	var b strings.Builder
	if !info.CheckedAt.IsZero() {
		fmt.Fprintf(&b, "plan as checked at %s\n", info.CheckedAt.Local().Format(time.TimeOnly))
	}
	n := 0
	for _, it := range info.Items {
		if group != "" && it.Group != group {
			continue
		}
		if undo && it.State != osetup.Installed && it.State != osetup.Outdated {
			continue
		}
		if !undo && !setupNeeds(it.State) {
			continue
		}
		n++
		fmt.Fprintf(&b, "  %s  %s", it.Name, it.State)
		if it.Path != "" {
			fmt.Fprintf(&b, "  %s", tildePath(it.Path, home))
		}
		b.WriteString("\n")
	}
	if n == 0 {
		b.WriteString("  nothing to change\n")
	}
	if !undo && group == "" {
		for _, w := range info.Writes {
			verb := "writes"
			if w.Remove {
				verb = "removes"
			}
			fmt.Fprintf(&b, "  %s %s\n", verb, tildePath(w.Path, home))
		}
		for _, cmd := range info.Commands {
			fmt.Fprintf(&b, "  runs %s\n", strings.Join(cmd.Argv, " "))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// setupTargetOf is the registered extension a setup verb names, or why it
// cannot run.
func (a *App) setupTargetOf(ctx ochazuke.Ctx, verb string) (extSetupTarget, error) {
	name := rowExtName(ctx)
	if name == "" {
		return extSetupTarget{}, fmt.Errorf("extension %s: give NAME", verb)
	}
	ts, err := a.extSetupTargets(name)
	if err != nil {
		return extSetupTarget{}, err
	}
	if !ts[0].has {
		return extSetupTarget{}, fmt.Errorf("extension %s declares no setup", name)
	}
	return ts[0], nil
}

// extensionSetup is extension.setup (confirmed; agents never): `<bin>
// ochazuke.setup install --json ARGS --yes` off the loop, then the
// extension's setup is checked again.
func (a *App) extensionSetup(ctx ochazuke.Ctx) ochazuke.Result {
	return a.runSetup(ctx, "install", ochazuke.StringArg(ctx.Args, "group", ""))
}

// extensionSetupUndo is extension.setup-undo (confirmed; agents never):
// `<bin> ochazuke.setup uninstall --json {} --yes`.
func (a *App) extensionSetupUndo(ctx ochazuke.Ctx) ochazuke.Result {
	return a.runSetup(ctx, "uninstall", "")
}

func (a *App) runSetup(ctx ochazuke.Ctx, verb, group string) ochazuke.Result {
	t, err := a.setupTargetOf(ctx, "setup")
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Pending(func(c context.Context) (any, error) {
		out, err := execExt(c, t.bin, t.env, extSetupNoun, verb, "--json", setupArgs(group), "--yes")
		if err != nil {
			return nil, err
		}
		var rows []osetup.Status
		if err := json.Unmarshal(out, &rows); err != nil {
			return extDecoded(out), nil
		}
		return rows, nil
	}).Label(t.name).Then(func(c ochazuke.Ctx, v any, err error) {
		if err == nil {
			if rows, ok := v.([]osetup.Status); ok {
				c.Notify(fmt.Sprintf("extension %s: setup %s: %s", t.name, verb, setupRowsLine(rows)))
			}
		}
		a.checkExtSetup(t.name)
	})
}

// setupRowsLine is install's or uninstall's answer in one line.
func setupRowsLine(rows []osetup.Status) string {
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, r.ID+" "+string(r.State))
	}
	if len(parts) == 0 {
		return "nothing selected"
	}
	return strings.Join(parts, ", ")
}

// extensionSetupPlan is extension.setup-plan: the plan as text (the
// palette shows it in the text view).
func (a *App) extensionSetupPlan(ctx ochazuke.Ctx) ochazuke.Result {
	t, err := a.setupTargetOf(ctx, "setup-plan")
	if err != nil {
		return ochazuke.Failed(err)
	}
	group := ochazuke.StringArg(ctx.Args, "group", "")
	return ochazuke.Pending(func(c context.Context) (any, error) {
		out, err := execExt(c, t.bin, t.env, extSetupNoun, "plan", "--json", "{}")
		if err != nil {
			return nil, err
		}
		plan, err := parseSetupPlan(out)
		if err != nil {
			return nil, err
		}
		return setupPlanText(t.name, plan, group), nil
	}).Label(t.name)
}

// setupPlanText is a plan in full: each item (name, state, target, what
// it is for, the next step), then every write with its whole content and
// every command. A group keeps its items; the writes and commands are
// the whole install's, said so.
func setupPlanText(name string, plan osetup.Plan, group string) string {
	var b strings.Builder
	info := summarizeSetup(name, plan)
	fmt.Fprintf(&b, "%s: %s", name, setupLine(info))
	if group != "" {
		fmt.Fprintf(&b, " (group %s shown)", group)
	}
	b.WriteString("\n\nitems\n")
	for _, it := range plan.Items {
		if group != "" && it.Group != group {
			continue
		}
		fmt.Fprintf(&b, "  %s (%s)  %s\n", it.Name, it.ID, it.State)
		if it.Path != "" {
			fmt.Fprintf(&b, "    target: %s\n", it.Path)
		}
		if it.Description != "" {
			fmt.Fprintf(&b, "    changes: %s\n", it.Description)
		}
		if it.Reason != "" {
			fmt.Fprintf(&b, "    why: %s\n", it.Reason)
		}
		if it.Next != "" {
			fmt.Fprintf(&b, "    next: %s\n", it.Next)
		}
	}
	if len(plan.Writes) > 0 || len(plan.Commands) > 0 {
		b.WriteString("\ninstall writes and runs")
		if group != "" {
			b.WriteString(" (every default item, not only the group)")
		}
		b.WriteString("\n")
	}
	for _, w := range plan.Writes {
		if w.Remove {
			fmt.Fprintf(&b, "\nremove %s\n", w.Path)
			continue
		}
		fmt.Fprintf(&b, "\nwrite %s\n%s\n", w.Path, strings.TrimRight(w.Content, "\n"))
	}
	for _, c := range plan.Commands {
		fmt.Fprintf(&b, "\nrun %s\n", strings.Join(c.Argv, " "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// extensionSetupStatus is extension.setup-status: fresh checks of NAME,
// else every registered extension; in the TUI the page's cache takes
// them too.
func (a *App) extensionSetupStatus(ctx ochazuke.Ctx) ochazuke.Result {
	var names []string
	if name := rowExtName(ctx); name != "" {
		names = []string{name}
	}
	targets, err := a.extSetupTargets(names...)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Pending(func(c context.Context) (any, error) {
		out := make([]extSetupInfo, 0, len(targets))
		for _, t := range targets {
			out = append(out, probeSetup(c, t))
		}
		return out, c.Err()
	}).Then(func(_ ochazuke.Ctx, v any, err error) {
		if infos, ok := v.([]extSetupInfo); ok && !a.cli {
			a.noteSetup(infos)
		}
	})
}

// setupNeededCount is how many registered extensions need setup, by the
// last checks.
func (a *App) setupNeededCount() int {
	n := 0
	for _, c := range a.extPage.rows {
		if c.Status != statusRegistered && c.Status != statusStale {
			continue
		}
		if a.extPage.setup[c.Name].State == setupNeeded {
			n++
		}
	}
	return n
}

// extSetupSummary is the page's summary line, "" when nothing needs setup
// or the setting is off.
func (a *App) extSetupSummary() string {
	if !a.extSetupOn() {
		return ""
	}
	n := a.setupNeededCount()
	if n == 0 {
		return ""
	}
	line := plural(n, "extension") + " need setup"
	if n == 1 {
		line = "1 extension needs setup"
	}
	for _, c := range a.extPage.rows {
		if a.extPage.setup[c.Name].State != setupNeeded {
			continue
		}
		if key := a.keyFor(extListID+"/"+c.Name, "extension.setup"); key != "" {
			line += " (" + key + " on a row sets it up)"
		}
		break
	}
	return line
}

// extSetupFacts is the detail's setup section for a row ("" when the
// setting is off or the row is not registered).
func (a *App) extSetupFacts(c extCandidate) string {
	if !a.extSetupOn() || (c.Status != statusRegistered && c.Status != statusStale) {
		return ""
	}
	info, ok := a.extPage.setup[c.Name]
	if !ok {
		if !extHasSetup(c.catalog) {
			return "setup  " + setupNone
		}
		return "setup  not checked yet"
	}
	home, _ := a.home()
	var b strings.Builder
	head := setupCell(info, true)
	if info.State == setupNeeded {
		head = fmt.Sprintf("%d of %d items missing", info.Missing, info.Total)
	}
	b.WriteString("setup  " + head)
	if info.State == setupNeeded {
		if key := a.keyFor(extListID+"/"+c.Name, "extension.setup"); key != "" {
			b.WriteString(" (" + key + " sets it up)")
		}
	}
	if info.Error != "" {
		b.WriteString("\n  " + info.Error)
	}
	w := 0
	for _, it := range info.Items {
		w = max(w, len(it.Name))
	}
	for _, it := range info.Items {
		fmt.Fprintf(&b, "\n  %-*s  %s", w, it.Name, it.State)
		if it.Path != "" {
			b.WriteString("\n    " + tildePath(it.Path, home))
		}
		if it.Description != "" {
			b.WriteString("\n    " + it.Description)
		}
		if setupNeeds(it.State) && it.Next != "" {
			b.WriteString("\n    next: " + it.Next)
		} else if it.Reason != "" && it.State != osetup.Installed {
			b.WriteString("\n    " + it.Reason)
		}
	}
	return b.String()
}
