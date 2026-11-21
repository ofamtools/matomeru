package migrate

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// noteTarget is the legacyId of what a v1 note is about: its task, else the
// task or project its about names, else its project; "" when none of them
// was imported.
func (im *importer) noteTarget(legacy string, m map[string]any) string {
	var candidates []string
	if id := text(m, "task_id"); id != "" {
		candidates = append(candidates, "task:"+id)
	}
	if kind, id, ok := strings.Cut(text(m, "about"), ":"); ok && (kind == "task" || kind == "project") {
		candidates = append(candidates, kind+":"+id)
	}
	if id := text(m, "project_id"); id != "" {
		candidates = append(candidates, "project:"+id)
	}
	for i, c := range candidates {
		if _, ok := im.refs[c]; ok {
			if i > 0 {
				im.log.warn("%s: %s was not imported; attached to %s", legacy, candidates[0], c)
			}
			return c
		}
	}
	if len(candidates) > 0 {
		im.log.warn("%s: %s was not imported; imported unattached", legacy, candidates[0])
	}
	return ""
}

// notes imports v1 notes in creation order, each with an "about" relation to
// its task or project (both are tasks in v2), which is also its task field.
func (im *importer) notes(rows []row) error {
	slices.SortStableFunc(rows, func(a, b row) int { return cmpInt(integer(a.m, "created"), integer(b.m, "created")) })
	for _, r := range rows {
		m := r.m
		legacy := "note:" + rowID(r)
		body := text(m, "text")
		title := text(m, "title")
		if title == "" {
			title = firstLine(body)
		}
		fields := map[string]any{"title": title, "body": body}
		setCommon(fields, m, "title", "text", "task_id", "project_id")
		target := im.noteTarget(legacy, m)
		it := item{kind: data.KindNote, legacy: legacy, fields: fields, archived: boolean(m, "archived"), starred: boolean(m, "starred")}
		if target != "" {
			it.links = map[string]string{"task": target}
		}
		ref, err := im.put(it, &im.rep.Notes)
		if err != nil {
			return err
		}
		if target != "" {
			if err := im.relate(ref, data.RelAbout, im.refs[target], 0, nil, &im.rep.NoteAbout); err != nil {
				return err
			}
		}
	}
	return nil
}

// reminders imports v1 reminder_defs as schedules of kind "remind". The rules
// are kept as they are; a single live rule also gives a cron expression.
func (im *importer) reminders(rows []row) error {
	for _, r := range rows {
		m := r.m
		legacy := "reminder:" + rowID(r)
		rules, _ := m["rules"].([]any)
		fields := map[string]any{"title": titleOr(text(m, "title"), "(untitled reminder)"), "scheduleKind": "remind"}
		if rules != nil {
			fields["rules"] = rules
		}
		setIf(fields, "cron", reminderCron(rules))
		setCommon(fields, m, "title", "rules")
		archived := len(rules) > 0
		for _, rule := range rules {
			if rm, ok := rule.(map[string]any); !ok || !boolean(rm, "archived") {
				archived = false
			}
		}
		if _, err := im.put(item{kind: data.KindSchedule, legacy: legacy, fields: fields, archived: archived || boolean(m, "archived")}, &im.rep.Schedules); err != nil {
			return err
		}
	}
	return nil
}

// schedules imports v1 schedule_defs as schedules of kind "run". v1 runs on
// an interval, which is kept as intervalSeconds; the run object is kept whole.
func (im *importer) schedules(rows []row) error {
	for _, r := range rows {
		m := r.m
		legacy := "schedule:" + rowID(r)
		fields := map[string]any{"title": titleOr(text(m, "title"), "(untitled schedule)"), "scheduleKind": "run"}
		if run, ok := m["run"].(map[string]any); ok {
			setIf(fields, "command", text(run, "text"))
			fields["run"] = run
		}
		if n := integer(m, "interval"); n > 0 {
			fields["intervalSeconds"] = n
		}
		if n := integer(m, "timeout"); n > 0 {
			fields["timeoutSeconds"] = n
		}
		setIf(fields, "window", text(m, "window"))
		setCommon(fields, m, "title", "run", "interval", "timeout", "window")
		if _, err := im.put(item{kind: data.KindSchedule, legacy: legacy, fields: fields, archived: boolean(m, "archived")}, &im.rep.Schedules); err != nil {
			return err
		}
	}
	return nil
}

func titleOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// directories adds the v1 "directory" relations from projects and tasks.
// v2 models a directory as a reference (kind "directory", ID the path), not
// a record, so the rows of the v1 directories table that no project or task
// uses (the directory picker's scan candidates) are counted as skipped.
func (im *importer) directories(rows []row, links map[string][]dirLink) error {
	used := map[string]bool{}
	froms := make([]string, 0, len(links))
	for from := range links {
		froms = append(froms, from)
	}
	slices.Sort(froms)
	for _, from := range froms {
		ref, ok := im.refs[from]
		for _, l := range links[from] {
			used[l.path] = true
			if !ok {
				im.rep.Directories.Total++
				im.rep.Directories.Skipped++
				im.log.warn("directory %s: %s was not imported; skipped", l.path, from)
				continue
			}
			if err := im.relate(ref, data.RelDirectory, core.Ref{Kind: KindDirectory, ID: l.path}, l.position, l.meta, &im.rep.Directories); err != nil {
				return err
			}
		}
	}
	unused := 0
	for _, r := range rows {
		path := text(r.m, "path")
		if path == "" {
			path = rowID(r)
		}
		if !used[path] {
			unused++
		}
	}
	if unused > 0 {
		im.rep.Directories.Total += unused
		im.rep.Directories.Skipped += unused
		im.log.warn("%d directories of the v1 directories table belong to no project or task (picker candidates); not imported", unused)
	}
	return nil
}

// translate maps a v1 relation end ("project:<id>", "task:<id>",
// "directory:<path>", ...) to its v2 ref.
func (im *importer) translate(end string) (core.Ref, bool) {
	kind, id, ok := strings.Cut(end, ":")
	if !ok || id == "" {
		return core.Ref{}, false
	}
	if kind == KindDirectory {
		return core.Ref{Kind: KindDirectory, ID: id}, true
	}
	ref, ok := im.refs[end]
	return ref, ok
}

// relations keeps the other v1 relations whose two ends were imported, with
// their kind, data, text and position.
func (im *importer) relations(rows []row) error {
	for _, r := range rows {
		m := r.m
		kind, fromText, toText := text(m, "kind"), text(m, "from"), text(m, "to")
		skip := func(why string, args ...any) {
			im.rep.Relations.Total++
			im.rep.Relations.Skipped++
			im.log.warn("relation %s %s -> %s: "+why, append([]any{kind, fromText, toText}, args...)...)
		}
		if kind == "" {
			skip("no kind; skipped")
			continue
		}
		if boolean(m, "archived") {
			skip("archived in v1; skipped")
			continue
		}
		from, okFrom := im.translate(fromText)
		to, okTo := im.translate(toText)
		if !okFrom || !okTo {
			skip("an end was not imported; skipped")
			continue
		}
		if err := im.relate(from, kind, to, int(integer(m, "position")), relationMeta(r), &im.rep.Relations); err != nil {
			return err
		}
	}
	return nil
}
