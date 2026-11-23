package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
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
	a.drafts = &ds
}

func (a *App) saveDrafts() {
	path := a.draftsPath()
	if path == "" || a.drafts == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		a.warn("%s: %v", draftsFileName, err)
		return
	}
	b, err := json.MarshalIndent(a.drafts, "", "  ")
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
	if a.drafts == nil {
		a.drafts = form.NewDraftStore()
	}
	fm.SetDrafts(a.drafts.List(key))
}

func (a *App) clearSubmittedDraft(fm *form.Model, draftKey string) {
	if fm == nil || draftKey == "" || a.drafts == nil {
		return
	}
	if idx := fm.ActiveDraftIndex(); idx >= 0 {
		if a.drafts.Remove(draftKey, idx) {
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
	if fm == nil || draftKey == "" || a.drafts == nil {
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
	if !a.drafts.Save(draftKey, fm.Values, draftLabel(fm), a.now()) {
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

// fieldValuesEqual compares a record's stored field value with a form's typed value.
func fieldValuesEqual(oldVal, newVal any) bool {
	if oldVal == nil && newVal == nil {
		return true
	}
	if oldVal == nil || newVal == nil {
		return false
	}
	if nr, ok := newVal.(core.Ref); ok {
		or, ok2 := data.AsRef(oldVal)
		return ok2 && or == nr
	}
	if ns, ok := newVal.([]string); ok {
		switch ov := oldVal.(type) {
		case []string:
			return reflect.DeepEqual(ov, ns)
		case []any:
			if len(ov) != len(ns) {
				return false
			}
			for i := range ns {
				if data.Text(ov[i]) != ns[i] {
					return false
				}
			}
			return true
		}
		return false
	}
	switch nv := newVal.(type) {
	case int:
		switch ov := oldVal.(type) {
		case int:
			return ov == nv
		case int64:
			return ov == int64(nv)
		case float64:
			return ov == float64(nv)
		}
	case float64:
		switch ov := oldVal.(type) {
		case float64:
			return ov == nv
		case int:
			return float64(ov) == nv
		case int64:
			return float64(ov) == nv
		}
	case time.Duration:
		if ov, ok := oldVal.(string); ok {
			return ov == nv.String()
		}
	}
	return reflect.DeepEqual(oldVal, newVal)
}

// submitForm atomically creates or patches the record represented by rf.
// It reports whether the modal should close.
func (a *App) submitForm(rf *recordFormModal, values map[string]any) bool {
	if rf == nil || rf.Model() == nil {
		return false
	}
	fm := rf.Model()
	schema := a.schema(rf.kind)

	// Validate snoozedUntil before performing any write.
	var (
		hasSnoozeSpec bool
		snoozeText    string
		snoozeUntil   time.Time
	)
	for _, sp := range fm.Specs {
		if sp.Name == data.ColSnoozedUntil {
			hasSnoozeSpec = true
			break
		}
	}
	if hasSnoozeSpec {
		snoozeText = strings.TrimSpace(data.Text(values[data.ColSnoozedUntil]))
		if snoozeText != "" {
			t, ok := parseSnoozeTime(snoozeText)
			if !ok {
				if fm.Errors == nil {
					fm.Errors = map[string]string{}
				}
				fm.Errors[data.ColSnoozedUntil] = "must be 2006-01-02 15:04 or RFC 3339"
				a.say("%s %q is not a time (2006-01-02 15:04 or RFC 3339); not written", data.ColSnoozedUntil, snoozeText)
				return false
			}
			snoozeUntil = t
		}
	}

	fields := maps.Clone(values)
	if fields == nil {
		fields = map[string]any{}
	}
	maps.DeleteFunc(fields, func(k string, _ any) bool { return views.IsUniversalField(k) })

	if rf.ref == nil {
		rec, cs, err := a.Store.Create(data.CreateInput{Kind: rf.kind, Fields: fields}, User)
		if err != nil {
			a.fail("create "+rf.kind, err)
			return false
		}
		ref := rec.Ref()
		a.syncManagedFile(rec)
		if rf.about != nil {
			if _, _, err := a.Store.Relate(data.RelateInput{From: ref, Kind: data.RelAbout, To: *rf.about}, User); err != nil {
				a.fail("attach note", err)
			}
		}
		if data.Text(values[data.ColStarStatus]) == data.StarStarred {
			_, _, _ = a.Store.Star(ref, User)
		}
		if data.Text(values[data.ColArchiveStatus]) == data.ArchiveArchived {
			_, _, _ = a.Store.Archive(ref, User)
		}
		if snoozeText != "" {
			_, _, _ = a.Store.Snooze(ref, snoozeUntil, User)
		}
		a.clearSubmittedDraft(fm, rf.draftKey)
		if rf.kind == data.KindPane {
			createdRef := ref
			a.afterModal(func(st core.State) core.State {
				if st.ActivePage == SpacePanes {
					a.gen++
					selectRow(a.rail(), data.RefText(createdRef))
				}
				return st
			})
		}
		a.wrote(fmt.Sprintf("created %s %q", strings.ToLower(schema.Title), views.Title(schema, *rec)), cs, nil)
		return true
	}

	cur, err := a.Store.Get(*rf.ref)
	if err != nil {
		a.fail("save "+rf.kind, err)
		return false
	}

	diff := map[string]any{}
	var hasStarSpec, hasArchiveSpec bool
	for _, sp := range fm.Specs {
		switch sp.Name {
		case data.ColStarStatus:
			hasStarSpec = true
			continue
		case data.ColArchiveStatus:
			hasArchiveSpec = true
			continue
		case data.ColSnoozedUntil:
			continue
		}
		newVal, hasNew := fields[sp.Name]
		oldVal, hasOld := cur.Value(sp.Name)
		if !hasNew {
			if hasOld && oldVal != nil && oldVal != "" {
				diff[sp.Name] = nil
			}
			continue
		}
		if !hasOld || oldVal == nil {
			if sp.Type == core.ParamBool {
				if b, ok := newVal.(bool); ok && !b && sp.Default == nil {
					continue
				}
			}
			diff[sp.Name] = newVal
			continue
		}
		if !fieldValuesEqual(oldVal, newVal) {
			diff[sp.Name] = newVal
		}
	}

	starVal := data.Text(values[data.ColStarStatus])
	starChanged := hasStarSpec && starVal != "" && starVal != cur.StarStatus

	archiveVal := data.Text(values[data.ColArchiveStatus])
	archiveChanged := hasArchiveSpec && archiveVal != "" && archiveVal != cur.ArchiveStatus

	var snoozeChanged bool
	if hasSnoozeSpec {
		if snoozeText == "" {
			snoozeChanged = cur.SnoozedUntil != nil
		} else {
			snoozeChanged = cur.SnoozedUntil == nil || !cur.SnoozedUntil.Equal(snoozeUntil)
		}
	}

	if len(diff) == 0 && !starChanged && !archiveChanged && !snoozeChanged {
		a.say("no changes")
		return true
	}

	rec := cur
	var lastCS *data.ChangeSet
	if len(diff) > 0 {
		patched, pcs, err := a.Store.Patch(data.PatchInput{Ref: *rf.ref, Fields: diff}, User)
		if err != nil {
			a.fail("save "+rf.kind, err)
			return false
		}
		rec, lastCS = patched, pcs
		if _, hasBody := diff["body"]; hasBody {
			a.syncManagedFile(rec)
		} else if _, hasTitle := diff["title"]; hasTitle {
			a.syncManagedFile(rec)
		}
	}
	if starChanged {
		var ucs *data.ChangeSet
		var uerr error
		if starVal == data.StarStarred {
			_, ucs, uerr = a.Store.Star(*rf.ref, User)
		} else {
			_, ucs, uerr = a.Store.Unstar(*rf.ref, User)
		}
		if uerr != nil {
			a.fail("star "+rf.kind, uerr)
			return false
		}
		if ucs != nil {
			lastCS = ucs
		}
	}
	if archiveChanged {
		var ucs *data.ChangeSet
		var uerr error
		if archiveVal == data.ArchiveArchived {
			_, ucs, uerr = a.Store.Archive(*rf.ref, User)
		} else {
			_, ucs, uerr = a.Store.Restore(*rf.ref, User)
		}
		if uerr != nil {
			a.fail("archive "+rf.kind, uerr)
			return false
		}
		if ucs != nil {
			lastCS = ucs
		}
	}
	if snoozeChanged {
		var ucs *data.ChangeSet
		var uerr error
		if snoozeText == "" {
			_, ucs, uerr = a.Store.Unsnooze(*rf.ref, User)
		} else {
			_, ucs, uerr = a.Store.Snooze(*rf.ref, snoozeUntil, User)
		}
		if uerr != nil {
			a.fail("snooze "+rf.kind, uerr)
			return false
		}
		if ucs != nil {
			lastCS = ucs
		}
	}

	a.clearSubmittedDraft(fm, rf.draftKey)
	a.wrote(fmt.Sprintf("saved %s %q", strings.ToLower(schema.Title), views.Title(schema, *rec)), lastCS, nil)
	return true
}
