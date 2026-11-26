package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/form"
)

const draftsFileName = "drafts.json"

func (a *App) draftsPath() string {
	if a.opts.ConfigDir == "" {
		return ""
	}
	return filepath.Join(a.opts.ConfigDir, draftsFileName)
}

func (a *App) loadDrafts(dir string) {
	if dir == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(dir, draftsFileName))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		a.warn("%s: %v", draftsFileName, err)
		return
	}
	var ds form.DraftStore
	if err := json.Unmarshal(b, &ds); err != nil {
		a.warn("%s: %v", draftsFileName, err)
		return
	}
	if ds.Drafts == nil {
		ds.Drafts = map[string][]form.Draft{}
	}
	if ds.MaxPerKey <= 0 {
		ds.MaxPerKey = form.DefaultMaxDraftsPerKey
	}
	a.cfg.drafts = &ds
}

func (a *App) saveDrafts() {
	path := a.draftsPath()
	if path == "" || a.cfg.drafts == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		a.warn("%s: %v", draftsFileName, err)
		return
	}
	b, err := json.MarshalIndent(a.cfg.drafts, "", "  ")
	if err != nil {
		a.warn("%s: %v", draftsFileName, err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		a.warn("%s: %v", draftsFileName, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		a.warn("%s: %v", draftsFileName, err)
	}
}

func recordDraftKey(kind string, ref *core.Ref) string {
	if ref != nil && ref.ID != "" {
		return "edit:" + ref.Kind + ":" + ref.ID
	}
	return "new:" + kind
}

func answerDraftKey(pageID, cardID string) string {
	return "answer:" + pageID + ":" + cardID
}

func launchDraftKey(title string) string {
	return "launch:" + title
}

func (a *App) attachFormDrafts(fm *form.Model, key string) {
	if fm == nil || key == "" {
		return
	}
	if a.cfg.drafts == nil {
		a.cfg.drafts = form.NewDraftStore()
	}
	fm.SetDrafts(a.cfg.drafts.List(key))
}

func (a *App) clearSubmittedDraft(fm *form.Model, draftKey string) {
	if fm == nil || draftKey == "" || a.cfg.drafts == nil {
		return
	}
	if idx := fm.ActiveDraftIndex(); idx >= 0 {
		if a.cfg.drafts.Remove(draftKey, idx) {
			a.saveDrafts()
		}
	}
}

func draftLabel(fm *form.Model) string {
	if fm == nil {
		return ""
	}
	for _, field := range []string{"title", "name", "command", "answer", "when"} {
		if s, ok := fm.Values[field].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return fm.Title
}

// saveFormDraft persists dirty form values into the draft ring on cancel/close.
// It skips saving when the form is clean or when the user is viewing an
// unmodified existing draft.
func (a *App) saveFormDraft(fm *form.Model, draftKey string) bool {
	if fm == nil || draftKey == "" || a.cfg.drafts == nil {
		return false
	}
	if fm.Editing {
		_, _ = fm.HandleCommand(core.Command{ID: form.CmdStop})
	}
	if !fm.Dirty {
		return false
	}
	if idx := fm.ActiveDraftIndex(); idx >= 0 && idx < len(fm.Drafts) {
		if reflect.DeepEqual(fm.Values, fm.Drafts[idx].Values) {
			return false
		}
	}
	if !a.cfg.drafts.Save(draftKey, fm.Values, draftLabel(fm), a.now()) {
		return false
	}
	a.saveDrafts()
	return true
}

func parseSnoozeTime(text string) (time.Time, bool) {
	for _, layout := range snoozeLayouts {
		if t, err := time.ParseInLocation(layout, text, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func (a *App) outgoingRelationRefs(ref core.Ref, kind string) []core.Ref {
	rows, err := a.Store.Related(ref, kind, data.Outgoing)
	if err != nil || len(rows) == 0 {
		return nil
	}
	refs := make([]core.Ref, 0, len(rows))
	for _, r := range rows {
		if r.To.Kind != "" && r.To.ID != "" {
			refs = append(refs, r.To)
		}
	}
	return refs
}

func (a *App) loadFormRelations(schema data.Schema, ref core.Ref) map[string][]core.Ref {
	if a == nil || a.Store == nil || ref.Kind == "" || ref.ID == "" {
		return nil
	}
	rels := schema.FormRelations()
	if len(rels) == 0 {
		return nil
	}
	out := map[string][]core.Ref{}
	for _, spec := range rels {
		if refs := a.outgoingRelationRefs(ref, spec.Kind); len(refs) > 0 {
			out[spec.Kind] = refs
		}
	}
	return out
}

func (a *App) editOptionsFor(schema data.Schema, rec *data.Record) views.EditOptions {
	opts := a.editOptions()
	if rec != nil {
		opts.InitialRelations = a.loadFormRelations(schema, rec.Ref())
	}
	return opts
}

func (a *App) findRecordByTitle(schema data.Schema, raw string) (core.Ref, bool) {
	res, err := a.Store.Query(data.Query{Kind: schema.Kind})
	if err != nil {
		return core.Ref{}, false
	}
	for _, rec := range res.Items {
		if strings.EqualFold(views.Title(schema, rec), raw) {
			return rec.Ref(), true
		}
	}
	return core.Ref{}, false
}

func (a *App) resolveFormRef(kind, raw string) (core.Ref, bool) {
	if a == nil || a.Store == nil {
		return core.Ref{}, false
	}
	raw = strings.TrimSpace(raw)
	if kind == "" || raw == "" {
		return core.Ref{}, false
	}
	if rec, err := a.Store.Get(core.Ref{Kind: kind, ID: raw}); err == nil && rec != nil {
		return rec.Ref(), true
	}
	schema := a.schema(kind)
	if sf := schema.SummaryField; sf != "" {
		if rec, err := a.Store.GetByField(kind, sf, raw); err == nil && rec != nil {
			return rec.Ref(), true
		}
	}
	return a.findRecordByTitle(schema, raw)
}

func (a *App) applyRelationDiff(from core.Ref, changes []data.RelationChange) (*data.ChangeSet, []error) {
	var lastCS *data.ChangeSet
	var errs []error
	for _, ch := range changes {
		for _, to := range ch.Add {
			if _, cs, err := a.Store.Relate(data.RelateInput{From: from, Kind: ch.Kind, To: to}, User); err != nil {
				errs = append(errs, fmt.Errorf("relate %s %s: %w", ch.Kind, data.RefText(to), err))
			} else if cs != nil {
				lastCS = cs
			}
		}
		for _, to := range ch.Remove {
			if cs, err := a.Store.Unrelate(from, ch.Kind, to, User); err != nil {
				errs = append(errs, fmt.Errorf("unrelate %s %s: %w", ch.Kind, data.RefText(to), err))
			} else if cs != nil {
				lastCS = cs
			}
		}
	}
	return lastCS, errs
}

// submitForm commits the record form rf as one data.FormSubmission. An edit
// is one Store.Patch, so its fields and its star, archive and snooze changes
// are one change set and one undo. It reports whether the modal should close.
func (a *App) submitForm(rf *recordFormModal, values map[string]any) bool {
	if rf == nil || rf.Model() == nil {
		return false
	}
	fm := rf.Model()
	sub := data.FormSubmission{
		Schema:        a.schema(rf.kind),
		Submitted:     values,
		Defaults:      rf.hiddenDefaults,
		VisibleFields: fm.Specs,
		ResolveRef:    a.resolveFormRef,
	}
	snoozeVisible := slices.ContainsFunc(fm.Specs, func(sp core.ParamSpec) bool { return sp.Name == data.ColSnoozedUntil })
	snoozeText := strings.TrimSpace(data.Text(values[data.ColSnoozedUntil]))
	if snoozeText == "" && rf.ref == nil {
		snoozeText = strings.TrimSpace(data.Text(rf.hiddenDefaults[data.ColSnoozedUntil]))
	}
	if snoozeText != "" {
		t, ok := parseSnoozeTime(snoozeText)
		switch {
		case ok:
			sub.SnoozeUntil = &t
		case snoozeVisible:
			if fm.Errors == nil {
				fm.Errors = map[string]string{}
			}
			fm.Errors[data.ColSnoozedUntil] = "must be 2006-01-02 15:04 or RFC 3339"
			a.say("%s %q is not a time (2006-01-02 15:04 or RFC 3339); not written", data.ColSnoozedUntil, snoozeText)
			return false
		}
	}

	if rf.ref == nil {
		rec, ok := a.createFromSubmission(sub, rf.about)
		if !ok {
			return false
		}
		a.clearSubmittedDraft(fm, rf.draftKey)
		if rf.kind == data.KindPane {
			a.afterPaneCreated(*rec)
		}
		return true
	}

	cur, err := a.Store.Get(*rf.ref)
	if err != nil {
		a.fail("save "+rf.kind, err)
		return false
	}
	sub.Before = cur
	sub.BeforeRelations = a.loadFormRelations(sub.Schema, cur.Ref())
	if !sub.HasChanges() {
		a.say("no changes")
		return true
	}
	in := sub.PatchInput()
	rec, cs, relErrs, err := a.commitFormEdit(cur, in, sub)
	if err != nil {
		a.fail("save "+rf.kind, err)
		return false
	}
	a.clearSubmittedDraft(fm, rf.draftKey)
	a.reportFormSave(sub.Schema, *rec, cs, relErrs)
	_, hasBody := in.Fields["body"]
	_, hasTitle := in.Fields["title"]
	if hasBody || hasTitle {
		a.syncManagedFile(rec) // after the commit: the file follows the record
	}
	return true
}

func (a *App) commitFormEdit(cur *data.Record, in data.PatchInput, sub data.FormSubmission) (*data.Record, *data.ChangeSet, []error, error) {
	rec := cur
	var cs *data.ChangeSet
	if len(in.Fields) > 0 {
		var err error
		rec, cs, err = a.Store.Patch(in, User)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	relCS, relErrs := a.applyRelationDiff(rec.Ref(), sub.RelationDiff())
	if cs == nil {
		cs = relCS
	}
	return rec, cs, relErrs, nil
}

func (a *App) reportFormSave(schema data.Schema, rec data.Record, cs *data.ChangeSet, relErrs []error) {
	what := fmt.Sprintf("saved %s %q", strings.ToLower(schema.Title), views.Title(schema, rec))
	if err := errors.Join(relErrs...); err != nil {
		a.fail(what, err)
		return
	}
	a.wrote(what, cs, nil)
}

// createFromSubmission creates the new record of sub, then attaches it to
// about, writes its non-default statuses, and links any InForm relations. Every
// failure after the create is reported; the record stays, so the form still
// closes. The managed file is synced after the record is committed.
func (a *App) createFromSubmission(sub data.FormSubmission, about *core.Ref) (*data.Record, bool) {
	in := sub.CreateInput()
	rec, cs, err := a.Store.Create(in, User)
	if err != nil {
		a.fail("create "+in.Kind, err)
		return nil, false
	}
	ref := rec.Ref()
	a.markOwnerCreated(ref)
	var errs []error
	if about != nil {
		if _, _, err := a.Store.Relate(data.RelateInput{From: ref, Kind: data.RelAbout, To: *about}, User); err != nil {
			errs = append(errs, fmt.Errorf("attach note: %w", err))
		}
	}
	if _, relErrs := a.applyRelationDiff(ref, sub.RelationDiff()); len(relErrs) > 0 {
		errs = append(errs, relErrs...)
	}
	if overlay := sub.CreateOverlay(ref); len(overlay.Fields) > 0 {
		if patched, _, err := a.Store.Patch(overlay, User); err != nil {
			errs = append(errs, fmt.Errorf("set status: %w", err))
		} else {
			rec = patched
		}
	}
	a.syncManagedFile(rec)
	what := fmt.Sprintf("created %s %q", strings.ToLower(sub.Schema.Title), views.Title(sub.Schema, *rec))
	if err := errors.Join(errs...); err != nil {
		a.fail(what, err)
		return rec, true
	}
	a.wrote(what, cs, nil)
	return rec, true
}

// afterPaneCreated opens the pane just created from the form, once the modal
// has closed.
func (a *App) afterPaneCreated(created data.Record) {
	a.maybeHarvestPaneRepo(created.Text("cwd"))
	a.afterModal(func(st core.State) core.State {
		a.viewMemo.gen++
		if isProjectTab(st.ActivePage) && a.projectChildPaneViewEnabled() {
			return a.openProjectChildPane(st, projectIDFromTab(st.ActivePage), created.ID, true)
		}
		if st.ActivePage == SpacePanes {
			rail := a.rail()
			if !selectRow(rail, data.RefText(created.Ref())) {
				rail.Query = ""
				selectRow(rail, data.RefText(created.Ref()))
			}
			a.panes.activePage[created.ID] = views.ScreenPageID
			return a.enterScreen(withFocus(withPage(st, SpacePanes, siteScreen, ModeNormal), siteScreen), created)
		}
		return st
	})
}
