package app

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

const (
	CmdChipInspect = "chip.inspect"
	CmdChipAct     = "chip.act"
	chipActionsID  = "chip.actions"
	chipFocusFact  = "pointerChipFocus"
)

var pointerTypes = data.DefaultTypeRegistry()

func chipSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		spec(CmdChipInspect, "Inspect the focused pointer chip, its relationships, and available actions"),
		spec(CmdChipAct, "Choose an action for the focused pointer chip"),
	}
}

func chipBindings() []core.Binding {
	return []core.Binding{
		bind(ModeNormal, views.ScopePointerChip, "enter", CmdChipInspect, "inspect pointer"),
		bind(ModeNormal, views.ScopePointerChip, ".", CmdChipAct, "pointer actions"),
		with(when(bind(ModeNormal, "", "g X", core.CmdFocusTo, "focus pointer chip"),
			"facts."+chipFocusFact), map[string]any{"id": "$facts." + chipFocusFact}),
	}
}

func (a *App) registerChipHandlers() {
	a.handle(CmdChipInspect, a.openChipActions)
	a.handle(CmdChipAct, a.openChipActions)
}

func (a *App) livePointerChipsEnabled() bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetLivePointerChips, true)
}

func newPointerChip(id string, ref core.Ref, kind, targetID, label string, compact bool, meta map[string]string) data.PointerChip {
	return data.PointerChip{ID: id, Ref: ref, TargetKind: kind, TargetID: targetID, Label: label, Compact: compact, Meta: meta}
}

func chipNodeSpecs(chips []data.PointerChip) []views.ChipNodeSpec {
	nodes := make([]views.ChipNodeSpec, 0, len(chips))
	for _, chip := range chips {
		spec, ok := pointerTypes.Lookup(chip.TargetKind)
		if !ok || chip.TargetID == "" || chip.Ref.ID == "" {
			continue
		}
		nodes = append(nodes, views.ChipNodeSpec{
			ID: chip.ID, Ref: chip.Ref, TargetKind: chip.TargetKind, TargetID: chip.TargetID,
			Label: chip.Label, Glyph: firstNonEmpty(chip.Glyph, spec.Glyph), Tone: firstNonEmpty(chip.Tone, spec.Tone),
			Compact: chip.Compact, Lineage: pointerTypes.ResolveLineage(chip.TargetKind),
			VerbCount: len(pointerTypes.ResolveVerbs(chip.TargetKind)), Meta: chip.Meta,
		})
	}
	return nodes
}

// withPointerChips exposes a focus destination on the containing view.
func withPointerChips(surface ui.ViewNode, stripID string, nodes []views.ChipNodeSpec) ui.ViewNode {
	if len(nodes) == 0 {
		return surface
	}
	strip := views.ChipStripView(stripID, nodes)
	surface.Facts = maps.Clone(surface.Facts)
	if surface.Facts == nil {
		surface.Facts = make(map[string]any, 1)
	}
	surface.Facts[chipFocusFact] = strip.Children[0].ID
	surface.Children = append(surface.Children, strip)
	return surface
}

func filesPointerChips(path string, isDir bool) []views.ChipNodeSpec {
	if path == "" {
		return nil
	}
	kind := data.ChipKindFile
	if isDir {
		kind = data.ChipKindDir
	}
	return chipNodeSpecs([]data.PointerChip{
		newPointerChip("target", core.Ref{Kind: data.ChipKindPath, ID: path}, kind, path, filepath.Base(path), true, nil),
	})
}

func worktreePointerChips(wt data.ProjectWorktree) []views.ChipNodeSpec {
	kind := data.ChipKindDir
	if wt.Source == "lane" || wt.Branch != "" {
		kind = data.ChipKindWorktree
	}
	meta := map[string]string{"Directory": wt.Path, "Branch": wt.Branch, "Repository": wt.Repo, "Source": wt.Source}
	chips := []data.PointerChip{newPointerChip("target", wt.Ref, kind, wt.Path, filepath.Base(wt.Path), true, meta)}
	if wt.Branch != "" {
		chips = append(chips, newPointerChip("branch", wt.Ref, data.ChipKindBranch, wt.Branch, wt.Branch, false, meta))
	}
	return chipNodeSpecs(chips)
}

func (a *App) contextPointerChips(item CollectedContextItem, ref core.Ref) []views.ChipNodeSpec {
	if item.Value == "" || item.Kind == contextitems.KindCode {
		return nil
	}
	var pane *data.Record
	if item.Kind == data.ChipKindFile && item.PaneID != "" && !filepath.IsAbs(item.Value) && !strings.HasPrefix(item.Value, "~/") {
		pane, _ = a.Store.Get(core.Ref{Kind: data.KindPane, ID: item.PaneID})
	}
	value := strings.TrimSpace(item.Value)
	if !handlers.IsURL(value) {
		value = contextTargetPath(item.Kind, value, pane)
	}
	kind := data.ChipKindFile
	if handlers.IsURL(value) {
		kind = data.ChipKindURL
	}
	if item.Kind == data.ChipKindPR {
		kind = data.ChipKindPR
	}
	meta := map[string]string{"Project": item.Project, "Source": item.Source, "Pane": item.PaneTitle}
	return chipNodeSpecs([]data.PointerChip{
		newPointerChip("target", ref, kind, value, firstNonEmpty(item.Label, item.Value), true, meta),
	})
}

func focusedPointerChip(chain []core.ContextFrame) (data.PointerChip, bool) {
	for _, frame := range chain {
		if frame.Scope != views.ScopePointerChip || frame.Subject == nil {
			continue
		}
		kind, _ := frame.Facts["chipKind"].(string)
		target, _ := frame.Facts["chipTarget"].(string)
		label, _ := frame.Facts["chipLabel"].(string)
		meta, _ := frame.Facts["chipMeta"].(map[string]string)
		compact, _ := frame.Facts["chipCompact"].(bool)
		return newPointerChip(frame.NodeID, *frame.Subject, kind, target, firstNonEmpty(label, target), compact, meta), true
	}
	return data.PointerChip{}, false
}

func pointerChipAt(root ui.ViewNode, focusID string) (data.PointerChip, bool) {
	chain := ui.ContextChain(root, focusID, nil)
	if chip, ok := focusedPointerChip(chain); ok {
		return chip, true
	}
	for _, frame := range chain {
		if id, _ := frame.Facts[chipFocusFact].(string); id != "" {
			return focusedPointerChip(ui.ContextChain(root, id, nil))
		}
	}
	return data.PointerChip{}, false
}

func (a *App) openChipActions(s core.State, c core.Command) core.StepResult {
	if !a.livePointerChipsEnabled() {
		a.say("pointer chips are disabled")
		return core.Next(s)
	}
	chip, ok := pointerChipAt(a.View(s), s.ActiveFrame().FocusID)
	if !ok {
		a.say("no pointer chip here")
		return core.Next(s)
	}
	verbs := pointerTypes.ResolveVerbOrigins(chip.TargetKind)
	items := make([]picker.Item, 0, len(verbs))
	for _, verb := range verbs {
		items = append(items, picker.Item{
			ID: verb.ID, Title: verb.Label, Subtitle: verb.Kind + " · " + verb.Description,
			Keywords: []string{verb.ID, verb.Command, verb.Kind}, Data: verb.ChipVerb,
		})
	}
	if len(items) == 0 {
		a.say("no actions for %s", chip.Label)
		return core.Next(s)
	}
	preview := a.chipDetails(chip)
	title := "Inspect pointer · "
	if c.ID == CmdChipAct {
		title = "Pointer actions · "
	}
	pm := picker.New(title+chip.Label, items)
	pm.MatchMode = a.matchMode()
	pm.PreviewFn = func(picker.Item) *ui.ViewNode { return &preview }
	target := a.gitActionTarget(s, core.Command{})
	if task := a.chipTask(chip.Ref); task.ID != "" {
		target.Task = task
	}
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID: chipActionsID, PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			verb := picked.Data.(data.ChipVerb)
			a.afterModal(func(st core.State) core.State { return a.runChipVerb(st, chip, verb, target) })
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, m))
}

func (a *App) chipDetails(chip data.PointerChip) ui.ViewNode {
	rows := []string{strings.Join(pointerTypes.ResolveLineage(chip.TargetKind), " → "), chip.TargetID}
	for _, key := range slices.Sorted(maps.Keys(chip.Meta)) {
		if chip.Meta[key] != "" && chip.Meta[key] != chip.TargetID {
			rows = append(rows, key+" · "+chip.Meta[key])
		}
	}
	if rec, err := a.Store.Get(chip.Ref); err == nil && rec != nil {
		rows = append(rows, a.title(chip.Ref)+" · "+data.RefText(chip.Ref))
		for _, field := range slices.Sorted(maps.Keys(rec.MergedFields())) {
			if ref, ok := rec.RefField(field); ok {
				rows = append(rows, field+" → "+a.title(ref))
			}
		}
		relationships, _ := a.Store.Related(chip.Ref, "", data.Both)
		for _, rel := range relationships {
			arrow := " → "
			if rel.To == chip.Ref {
				arrow = " ← "
			}
			rows = append(rows, rel.Kind+arrow+a.title(rel.Other(chip.Ref)))
		}
	} else if info, err := os.Stat(expandHome(chip.TargetID)); err == nil {
		rows = append(rows, info.Mode().String()+" · "+relativeTime(a.now(), info.ModTime()))
	}
	return views.ChipDetailsView("chip.details", rows)
}

func (a *App) chipTask(ref core.Ref) core.Ref {
	if ref.Kind == data.KindTask {
		return ref
	}
	if rec, err := a.Store.Get(ref); err == nil && rec != nil {
		if task, found := rec.RefField("task"); found {
			return task
		}
	}
	return core.Ref{}
}

func (a *App) runChipVerb(s core.State, chip data.PointerChip, verb data.ChipVerb, target gitTarget) core.State {
	value := chip.TargetID
	switch verb.Command {
	case data.ChipCmdCopy:
		a.yank(value, "pointer target")
		return s
	case data.ChipCmdOpen:
		rules := a.cfg.Handlers.Match(value)
		if len(rules) == 0 {
			return a.systemOpenTarget(s, value).State
		}
		return a.runHandler(s, rules[0], openTarget{path: value, title: chip.Label, from: &chip.Ref, rules: rules})
	case data.ChipCmdReveal:
		return a.revealContextValue(s, chip.TargetKind, value, nil).State
	case data.ChipCmdEdit:
		path := expandHome(value)
		return a.newPane(s, &chip.Ref, verb.Label+" · "+chip.Label, editorCommand()+" "+handlers.Quote(path), filepath.Dir(path), data.RoleSideVim)
	case data.ChipCmdCD:
		return a.newPane(s, &chip.Ref, chip.Label, "", expandHome(value), data.RoleNormal)
	case data.ChipCmdList:
		args := append(slices.Clone(verb.Args), expandHome(value))
		return a.startTrackedCommand(s, verb.Label, "", "ls", args, target.Task)
	case data.ChipCmdFind:
		return a.fdSearch(s, core.Command{Target: &chip.Ref, Args: map[string]any{"dir": expandHome(value)}}).State
	case data.ChipCmdMove:
		return a.moveChipPath(s, value, target.Task)
	case data.ChipCmdDelete:
		return a.confirmChipAction(s, verb, chip, func(st core.State) core.State {
			return a.startTrackedCommand(st, verb.Label, "", "rm", []string{"-r", "--", expandHome(value)}, target.Task)
		})
	default:
		return a.runChipGitVerb(s, chip, verb, target)
	}
}

func gitTargetWithChip(target gitTarget, chip data.PointerChip) gitTarget {
	if dir := chip.Meta["Directory"]; dir != "" {
		target.Dir = expandHome(dir)
	}
	target.Repository = firstNonEmpty(chip.Meta["Repository"], target.Repository)
	switch chip.TargetKind {
	case data.ChipKindWorktree:
		target.Worktree = expandHome(chip.TargetID)
		target.Dir = target.Worktree
	case data.ChipKindDir:
		target.Dir = expandHome(chip.TargetID)
		target.Worktree, target.Branch = "", ""
	case data.ChipKindBranch, data.ChipKindGitRef:
		target.Branch = chip.TargetID
		target.Worktree = ""
	case data.ChipKindPR:
		target.PR = chip.TargetID
	}
	return target
}

func (a *App) runChipGitVerb(s core.State, chip data.PointerChip, verb data.ChipVerb, target gitTarget) core.State {
	target = gitTargetWithChip(target, chip)
	run := func(st core.State) core.State { return a.runGitHubAction(st, verb.Command, target) }
	switch verb.Command {
	case "git.worktree.remove", "git.rebase", "git.merge", "git.push":
		return a.confirmChipAction(s, verb, chip, run)
	default:
		return run(s)
	}
}

func (a *App) confirmChipAction(s core.State, verb data.ChipVerb, chip data.PointerChip, run func(core.State) core.State) core.State {
	return a.openConfirm(s, modal.ConfirmOptions{
		Title: verb.Label, Message: fmt.Sprintf("%s — %s", chip.Label, chip.TargetID), ConfirmLabel: verb.Label, Tone: "warning",
		OnConfirm: func() modal.Result {
			a.afterModal(run)
			return modal.Result{}
		},
	})
}

func (a *App) moveChipPath(s core.State, from string, task core.Ref) core.State {
	from = expandHome(from)
	return a.openLaunch(s, "Move directory", "Move", []core.ParamSpec{
		{Name: "destination", Type: core.ParamPath, Required: true, Description: "New path for " + from},
	}, nil, func(st core.State, values map[string]any) (core.State, bool) {
		to := expandHome(strings.TrimSpace(data.Text(values["destination"])))
		if to == "" {
			a.say("a destination path is required")
			return st, false
		}
		return a.startTrackedCommand(st, "Move directory", "", "mv", []string{"-n", "--", from, to}, task), true
	})
}
