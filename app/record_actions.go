package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/expr"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/picker"
)

// recordActionPayload carries the command arguments for an item in the
// focused-record actions picker.
type recordActionPayload struct {
	args map[string]any
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
	r := ref
	a.actionRef = &r
	return core.Next(a.openPicker(s, modalRecordActions, pm))
}

func (a *App) recordFacts(s core.State, ref core.Ref) map[string]any {
	facts := map[string]any{"kind": ref.Kind}
	chain := ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, a.Shell.AppFacts(s))
	for i := len(chain) - 1; i >= 0; i-- {
		for k, v := range chain[i].Facts {
			facts[k] = v
		}
	}
	if rec, err := a.Store.Get(ref); err == nil {
		schema := a.schema(ref.Kind)
		facts[data.ColArchiveStatus] = rec.ArchiveStatus
		facts[data.ColStarStatus] = rec.StarStatus
		for _, col := range schema.StatusColumns {
			facts[col.Name] = rec.Text(col.Name)
		}
		facts["snoozed"] = rec.IsSnoozed(a.now())
		facts[factOpenable] = a.openable(*rec)
		if rec.Kind == data.KindSchedule {
			facts["paused"] = rec.Bool("paused")
		}
		if rec.Kind == data.KindPane {
			facts["hasPendingApproval"] = len(a.pendingApprovalsFor(rec.ID)) > 0
		}
	}
	return facts
}

func (a *App) recordBindingKeys(cmdID, kind string, fixedArgs map[string]any) string {
	for _, b := range a.bindings {
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

	var specific, universal []picker.Item

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
			it := picker.Item{
				ID:       CmdNewHere,
				Title:    CmdNewHere,
				Subtitle: label,
				Keywords: []string{secKind, secTitle, "add", "new"},
				Data:     recordActionPayload{},
			}
			if k := a.recordBindingKeys(CmdNewHere, SpaceWork, nil); k != "" {
				it.Badges = []ui.Badge{{Text: k, Tone: "accent"}}
			}
			specific = append(specific, it)
		}
	}

	curShowing, isShowing := a.Showing()
	onOwnShow := isShowing && curShowing == ref && s.ActivePage == SpaceWork

	for _, sp := range a.specs {
		isRec, _ := sp.Meta["record"].(bool)
		if !isRec {
			continue
		}
		if sp.ID == views.CmdOpen && onOwnShow {
			continue
		}
		kinds, _ := sp.Meta["kinds"].([]string)
		if len(kinds) > 0 && !slices.Contains(kinds, ref.Kind) {
			continue
		}
		if condStr, _ := sp.Meta["when"].(string); condStr != "" {
			if cond, err := expr.Parse(condStr); err != nil || !cond.Eval(env) {
				continue
			}
		}
		it := picker.Item{
			ID:       sp.ID,
			Title:    sp.ID,
			Subtitle: sp.Description,
			Keywords: append(slices.Clone(sp.Tags), ref.Kind),
			Data:     recordActionPayload{},
		}
		if k := a.recordBindingKeys(sp.ID, ref.Kind, nil); k != "" {
			it.Badges = []ui.Badge{{Text: k}}
		}
		if len(kinds) > 0 {
			specific = append(specific, it)
		} else {
			universal = append(universal, it)
		}
	}

	// Contextual create actions attached to this record.
	noteArgs := map[string]any{"kind": data.KindNote}
	noteItem := picker.Item{
		ID:       views.CmdNew,
		Title:    views.CmdNew + " note",
		Subtitle: "Create a note about this " + strings.ToLower(a.schema(ref.Kind).Title),
		Keywords: []string{"note", "new", "comment"},
		Data:     recordActionPayload{args: noteArgs},
	}
	if k := a.recordBindingKeys(views.CmdNew, ref.Kind, noteArgs); k != "" {
		noteItem.Badges = []ui.Badge{{Text: k}}
	}
	universal = append(universal, noteItem)

	if ref.Kind == data.KindTask {
		subArgs := map[string]any{"kind": data.KindTask}
		universal = append(universal, picker.Item{
			ID:       views.CmdNew,
			Title:    views.CmdNew + " subtask",
			Subtitle: "Create a subtask under this task",
			Keywords: []string{"subtask", "task", "new", "child"},
			Data:     recordActionPayload{args: subArgs},
		})
	}

	return append(specific, universal...)
}
