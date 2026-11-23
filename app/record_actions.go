package app

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"ofam.dev/matomeru/actions"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/expr"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

// recordActionPayload carries the command arguments for an item in the
// focused-record actions picker.
type recordActionPayload struct {
	args map[string]any
}

type recordActionCandidate struct {
	title    string
	subtitle string
	keywords []string
	payload  recordActionPayload
	keyHint  string
}

// recordActionsOpen opens a picker containing only the commands that can be
// triggered on the currently focused record (without spaces, navigation,
// list/graph controls, settings or other global commands).
func (a *App) recordActionsOpen(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok {
		a.say("no focused record")
		return core.Next(s)
	}
	items := a.recordActionItems(s, ref)
	if len(items) == 0 {
		a.say("no actions for %s", a.title(ref))
		return core.Next(s)
	}
	schema := a.schema(ref.Kind)
	pm := picker.New("Actions · "+a.title(ref), items)
	pm.Placeholder = "action for this " + strings.ToLower(schema.Title)
	targetRef := ref
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "record.actions",
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			r := targetRef
			var args map[string]any
			if p, ok := picked.Data.(recordActionPayload); ok {
				args = p.args
			}
			a.nav.Registers.SetSpecial(core.RegCommand, picked.Title)
			return modal.Result{
				Handled:     true,
				Close:       true,
				CloseReason: modal.CloseSubmit,
				Emit: []core.Command{{
					ID:     picked.ID,
					Target: &r,
					Args:   args,
					Source: "record.actions",
					Actor:  User,
				}},
			}
		},
	})
	return core.Next(a.pushModal(s, m))
}

func (a *App) recordFacts(s core.State, ref core.Ref) map[string]any {
	facts := map[string]any{"kind": ref.Kind}
	chain := ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, a.Shell.AppFacts(s))
	for i := len(chain) - 1; i >= 0; i-- {
		for k, v := range chain[i].Facts {
			facts[k] = v
		}
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		if cached, found := a.cachedExternal(ref); found {
			rec = &cached
			err = nil
		}
	}
	if err == nil && rec != nil {
		schema := a.schema(ref.Kind)
		maps.Copy(facts, views.RecordFacts(schema, *rec, a.now()))
		facts[factOpenable] = a.openable(*rec)
		if rec.Kind == data.KindPane {
			facts["hasPendingApproval"] = len(a.pendingApprovalsFor(rec.ID)) > 0
		}
	}
	return facts
}

func (a *App) recordBindingKeys(cmdID, kind string, fixedArgs map[string]any) string {
	for _, b := range a.bind.bindings {
		if b.Unbind || b.Command != cmdID || len(b.Keys) == 0 {
			continue
		}
		if b.Mode != "" && b.Mode != ModeNormal {
			continue
		}
		if b.Scope != "" && b.Scope != kind {
			continue
		}
		dynamic := false
		for _, v := range b.Args {
			if str, ok := v.(string); ok && strings.HasPrefix(str, "$") {
				dynamic = true
			}
		}
		if dynamic {
			continue
		}
		if len(fixedArgs) > 0 {
			match := len(b.Args) == len(fixedArgs)
			for k, v := range fixedArgs {
				if fmt.Sprint(b.Args[k]) != fmt.Sprint(v) {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}
		return strings.Join(b.Keys, " ")
	}
	return ""
}

func kindMatchesAny(kind string, kinds []string) bool {
	if len(kinds) == 0 {
		return true
	}
	want := strings.ToLower(strings.TrimSpace(kind))
	for _, k := range kinds {
		nk := strings.ToLower(strings.TrimSpace(k))
		if nk == want || nk == "*" {
			return true
		}
		if strings.TrimSuffix(nk, "s") == want || strings.TrimSuffix(want, "s") == nk {
			return true
		}
	}
	return false
}

func specWithCandidate(sp core.CommandSpec, cand recordActionCandidate, overrideNoun string) core.CommandSpec {
	cp := sp
	meta := make(map[string]any, len(sp.Meta)+2)
	maps.Copy(meta, sp.Meta)
	if overrideNoun != "" {
		meta[actions.MetaNoun] = overrideNoun
	}
	meta["_candidate"] = cand
	cp.Meta = meta
	return cp
}

func (a *App) recordActionItems(s core.State, ref core.Ref) []picker.Item {
	facts := a.recordFacts(s, ref)
	env := map[string]any{
		"mode":  a.Engine.Mode(s),
		"scope": ref.Kind,
		"facts": facts,
		"modal": map[string]any{"open": false, "id": ""},
		"count": nil,
		"layer": nil,
	}
	if rec, ok := a.resolveRecord(ref); ok && rec != nil {
		recEnv := views.RecordExprEnv(a.schema(ref.Kind), *rec, a.now())
		env["fields"] = recEnv["fields"]
		env["archived"] = recEnv["archived"]
		env["starred"] = recEnv["starred"]
		env["snoozed"] = recEnv["snoozed"]
		env["unread"] = recEnv["unread"]
	}

	var candidates []core.CommandSpec

	// If focused on an empty Show-page section, offer adding to that section first.
	chain := ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, a.Shell.AppFacts(s))
	if len(chain) > 0 && chain[0].Facts["empty"] == true {
		rel, _ := chain[0].Facts["relationKind"].(string)
		secKind, _ := chain[0].Facts["sectionKind"].(string)
		_, creatable := a.Store.Registry().Get(secKind)
		if rel == data.RelDirectory || creatable {
			secTitle, _ := chain[0].Facts["sectionTitle"].(string)
			label := "add to " + strings.ToLower(secTitle)
			if creatable {
				label = "new " + strings.ToLower(a.schema(secKind).Title) + " for this " + strings.ToLower(a.schema(ref.Kind).Title)
			} else if rel == data.RelDirectory {
				label = "attach directory to this " + strings.ToLower(a.schema(ref.Kind).Title)
			}
			cand := recordActionCandidate{
				title:    CmdNewHere,
				subtitle: label,
				keywords: []string{secKind, secTitle, "add", "new"},
				payload:  recordActionPayload{},
				keyHint:  a.recordBindingKeys(CmdNewHere, SpaceWork, nil),
			}
			candidates = append(candidates, specWithCandidate(core.CommandSpec{ID: CmdNewHere, Description: label}, cand, ref.Kind))
		}
	}

	curShowing, isShowing := a.Showing()
	onOwnShow := isShowing && curShowing == ref && s.ActivePage == SpaceWork

	for _, sp := range a.bind.specs {
		isRec, _ := sp.Meta["record"].(bool)
		if !isRec {
			continue
		}
		if sp.ID == views.CmdOpen && onOwnShow {
			continue
		}
		kinds, _ := sp.Meta["kinds"].([]string)
		if !kindMatchesAny(ref.Kind, kinds) {
			continue
		}
		if condStr, _ := sp.Meta["when"].(string); condStr != "" {
			if cond, err := expr.Parse(condStr); err != nil || !cond.Eval(env) {
				continue
			}
		}
		cand := recordActionCandidate{
			title:    sp.ID,
			subtitle: sp.Description,
			keywords: append(slices.Clone(sp.Tags), ref.Kind),
			payload:  recordActionPayload{},
			keyHint:  a.recordBindingKeys(sp.ID, ref.Kind, nil),
		}
		candidates = append(candidates, specWithCandidate(sp, cand, ""))
	}

	// Contextual create actions attached to this record.
	noteArgs := map[string]any{"kind": data.KindNote}
	noteSub := "Create a note about this " + strings.ToLower(a.schema(ref.Kind).Title)
	candidates = append(candidates, specWithCandidate(
		core.CommandSpec{ID: views.CmdNew, Description: noteSub},
		recordActionCandidate{
			title:    views.CmdNew + " note",
			subtitle: noteSub,
			keywords: []string{"note", "new", "comment"},
			payload:  recordActionPayload{args: noteArgs},
			keyHint:  a.recordBindingKeys(views.CmdNew, ref.Kind, noteArgs),
		},
		"record",
	))

	if ref.Kind == data.KindTask {
		subArgs := map[string]any{"kind": data.KindTask}
		subDesc := "Create a subtask under this task"
		candidates = append(candidates, specWithCandidate(
			core.CommandSpec{ID: views.CmdNew, Description: subDesc},
			recordActionCandidate{
				title:    views.CmdNew + " subtask",
				subtitle: subDesc,
				keywords: []string{"subtask", "task", "new", "child"},
				payload:  recordActionPayload{args: subArgs},
			},
			"record",
		))
	}

	// Group and sort actions by namespace with exact focused-noun actions at the top.
	grouped := actions.GroupForFocus(ref.Kind, candidates)
	items := make([]picker.Item, 0, len(grouped))
	for _, ga := range grouped {
		cand, _ := ga.Spec.Meta["_candidate"].(recordActionCandidate)
		title := cand.title
		if title == "" {
			title = ga.Spec.ID
		}
		subtitle := cand.subtitle
		if subtitle == "" {
			subtitle = ga.Spec.Description
		}
		groupLabel := ga.Group
		if groupLabel == "" {
			groupLabel = "actions"
		}
		displayTitle := "  " + title
		if ga.FirstInGroup {
			displayTitle = fmt.Sprintf("[%s] %s", groupLabel, title)
		}
		nsTone := "muted"
		if ga.FocusedMatch {
			nsTone = "accent"
		}
		badges := []ui.Badge{{Text: groupLabel, Tone: nsTone}}
		if cand.keyHint != "" {
			badges = append(badges, ui.Badge{Text: cand.keyHint, Tone: "accent"})
		}
		kw := append(slices.Clone(cand.keywords), ga.Group, ga.Namespace, ref.Kind)
		items = append(items, picker.Item{
			ID:       ga.Spec.ID,
			Title:    displayTitle,
			Subtitle: subtitle,
			Badges:   badges,
			Keywords: kw,
			Data:     cand.payload,
		})
	}
	return items
}
