// Package migrate imports a Matomeru v1 database (mtm.db) into a v2 store.
//
// The v1 file is opened strictly read-only (mode=ro and query_only on every
// connection): the import never writes to it, never checkpoints it and never
// creates a file beside it. A dry run, the default, reads and maps every row
// and reports what an apply would create, update or skip without writing to
// the v2 store; it needs no store at all.
//
// Every imported record keeps its v1 identity in the legacyId field,
// namespaced by v1 table ("project:<id>", "task:<id>", "note:<id>",
// "reminder:<id>", "schedule:<id>"), and a legacyHash of what the mapping
// wrote. A second import finds records by legacyId, leaves a record whose v1
// row maps to the same values as before (and so keeps the owner's v2 edits),
// and patches one whose v1 row changed.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// DefaultActor is the actor an import's change sets name when Options.Actor
// is empty.
var DefaultActor = core.Actor{Kind: "system", ID: "mtm.migrate", Label: "v1 import"}

// DefaultMaxWarnings is how many warnings a Report lists; the rest are counted.
const DefaultMaxWarnings = 200

// KindDirectory is the kind of a directory reference: the target of a
// "directory" relation, whose ID is the path. v2 keeps no directory records.
const KindDirectory = "directory"

// plannedPrefix marks the ID of a record a dry run would create.
const plannedPrefix = "new:"

// Options configures Import.
type Options struct {
	From  string     // path of the v1 mtm.db
	Apply bool       // false (the default) is a dry run: nothing is written
	Actor core.Actor // actor of the change sets; empty uses DefaultActor
	// MaxWarnings caps Report.Warnings; 0 uses DefaultMaxWarnings.
	MaxWarnings int
}

// Counts is what happened to the v1 items of one kind. Total counts every
// item seen; each lands in exactly one of the other four.
type Counts struct {
	Total     int `json:"total"`
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Skipped   int `json:"skipped"`
}

// Report describes an import. In a dry run the counts are what an apply
// would do: with a store they tell created from unchanged; without one every
// item counts as created.
type Report struct {
	Source  string `json:"source"`
	DryRun  bool   `json:"dryRun"`
	Applied bool   `json:"applied"`
	// Snapshot reports that the source was read from a temporary copy: it is
	// in WAL mode and its -wal and -shm files were not beside it, so opening
	// it in place would have created them.
	Snapshot      bool     `json:"snapshot,omitempty"`
	MissingTables []string `json:"missingTables,omitempty"`

	Projects    Counts `json:"projects"`    // v1 projects: root tasks
	Tasks       Counts `json:"tasks"`       // v1 tasks: tasks under a parent
	Notes       Counts `json:"notes"`       // v1 notes
	NoteAbout   Counts `json:"noteAbout"`   // "about" relations from notes to their task or project
	Directories Counts `json:"directories"` // "directory" relations, and directories attached to nothing
	Schedules   Counts `json:"schedules"`   // v1 reminder_defs and schedule_defs
	Relations   Counts `json:"relations"`   // the other v1 relations

	Skipped         int      `json:"skipped"` // sum of the kinds' Skipped
	Warnings        []string `json:"warnings"`
	WarningsDropped int      `json:"warningsDropped,omitempty"`
	Writes          int      `json:"writes"` // change sets written to the v2 store
	ElapsedMS       int64    `json:"elapsedMs"`
}

type reporter struct {
	rep *Report
	max int
}

func (r *reporter) warn(format string, args ...any) {
	if len(r.rep.Warnings) >= r.max {
		r.rep.WarningsDropped++
		return
	}
	r.rep.Warnings = append(r.rep.Warnings, fmt.Sprintf(format, args...))
}

// Import reads the v1 database at opts.From and maps it into store. With
// Apply false it writes nothing, and store may be nil. An unreadable or
// malformed row, a missing table or an unknown key never stops the import:
// it is skipped or kept, and reported.
func Import(ctx context.Context, store *data.Store, opts Options) (*Report, error) {
	start := time.Now()
	if opts.From == "" {
		return nil, errors.New("migrate: no v1 database named")
	}
	if opts.Apply && store == nil {
		return nil, errors.New("migrate: an apply needs a v2 store")
	}
	src, err := openSource(opts.From)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	rep := &Report{Source: src.path, DryRun: !opts.Apply, Snapshot: src.snapshot != "", Warnings: []string{}}
	maxWarn := opts.MaxWarnings
	if maxWarn <= 0 {
		maxWarn = DefaultMaxWarnings
	}
	actor := opts.Actor
	if actor.Kind == "" {
		actor = DefaultActor
	}
	im := &importer{ctx: ctx, store: store, apply: opts.Apply, actor: actor, rep: rep, log: &reporter{rep: rep, max: maxWarn},
		index: map[string]data.Record{}, refs: map[string]core.Ref{}, rels: map[relKey]map[core.Ref]bool{}}

	present, err := src.tables(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]*Counts{"projects": &rep.Projects, "tasks": &rep.Tasks, "notes": &rep.Notes, "directories": &rep.Directories,
		"relations": &rep.Relations, "schedule_defs": &rep.Schedules, "reminder_defs": &rep.Schedules}
	tables := map[string][]row{}
	for _, t := range v1Tables {
		if !present[t] {
			rep.MissingTables = append(rep.MissingTables, t)
			continue
		}
		rows, err := src.read(ctx, t, im.log, counts[t])
		if err != nil {
			return nil, err
		}
		tables[t] = rows
	}
	if err := im.loadIndex(); err != nil {
		return nil, err
	}

	links, others := im.directoryLinks(tables["relations"])
	for _, step := range []func() error{
		func() error { return im.projects(tables["projects"], links) },
		func() error { return im.tasks(tables["tasks"], links) },
		func() error { return im.notes(tables["notes"]) },
		func() error { return im.reminders(tables["reminder_defs"]) },
		func() error { return im.schedules(tables["schedule_defs"]) },
		func() error { return im.directories(tables["directories"], links) },
		func() error { return im.relations(others) },
	} {
		if err := step(); err != nil {
			return nil, err
		}
	}
	rep.Applied = opts.Apply
	for _, c := range []Counts{rep.Projects, rep.Tasks, rep.Notes, rep.NoteAbout, rep.Directories, rep.Schedules, rep.Relations} {
		rep.Skipped += c.Skipped
	}
	rep.ElapsedMS = time.Since(start).Milliseconds()
	return rep, nil
}

// --- the write path -----------------------------------------------------------

type relKey struct {
	from core.Ref
	kind string
}

type importer struct {
	ctx   context.Context
	store *data.Store // nil in a dry run without a v2 database
	apply bool
	actor core.Actor
	rep   *Report
	log   *reporter

	index map[string]data.Record       // legacyId -> the record an earlier import made
	refs  map[string]core.Ref          // legacyId -> v2 ref (existing, created or planned)
	rels  map[relKey]map[core.Ref]bool // outgoing relations known per end and kind

	projectOrder []string // legacyIds of the projects, in import order
}

// item is one v2 record to import.
type item struct {
	kind   string
	legacy string
	fields map[string]any
	// links are reference fields by the legacyId of their target, resolved
	// when the item is written (a task's parent, a note's task).
	links             map[string]string
	archived, starred bool
}

// loadIndex reads every imported record in one query per kind, so legacy
// lookups never scan the store again.
func (im *importer) loadIndex() error {
	if im.store == nil {
		return nil
	}
	for _, kind := range []string{data.KindTask, data.KindNote, data.KindSchedule} {
		res, err := im.store.Query(data.Query{Kind: kind, IncludeArchived: true})
		if err != nil {
			return fmt.Errorf("migrate: read v2 %s records: %w", kind, err)
		}
		for _, rec := range res.Items {
			if id := rec.Text("legacyId"); id != "" {
				if _, dup := im.index[id]; !dup {
					im.index[id] = rec // the oldest wins
				}
			}
		}
	}
	return nil
}

func hashOf(it item) string {
	b, _ := json.Marshal(struct {
		Fields   map[string]any    `json:"f"`
		Links    map[string]string `json:"l,omitempty"`
		Archived bool              `json:"a,omitempty"`
		Starred  bool              `json:"s,omitempty"`
	}{it.fields, it.links, it.archived, it.starred})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// sameValue compares a value to write with a stored one in stored form.
func sameValue(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	var na, nb any
	_ = json.Unmarshal(ja, &na)
	_ = json.Unmarshal(jb, &nb)
	ja, _ = json.Marshal(na)
	jb, _ = json.Marshal(nb)
	return string(ja) == string(jb)
}

func statuses(it item) (archive, star string) {
	archive, star = data.ArchiveActive, data.StarUnstarred
	if it.archived {
		archive = data.ArchiveArchived
	}
	if it.starred {
		star = data.StarStarred
	}
	return archive, star
}

// put creates the item, patches the record an earlier import made of it, or
// leaves that record alone when the v1 row maps to what it mapped to then.
func (im *importer) put(it item, c *Counts) (core.Ref, error) {
	if err := im.ctx.Err(); err != nil {
		return core.Ref{}, err
	}
	c.Total++
	hash := hashOf(it)
	fields := maps.Clone(it.fields)
	for field, legacy := range it.links {
		fields[field] = im.refs[legacy]
	}
	fields["legacyId"], fields["legacyHash"] = it.legacy, hash
	archive, star := statuses(it)

	if rec, ok := im.index[it.legacy]; ok {
		ref := rec.Ref()
		im.refs[it.legacy] = ref
		if rec.Text("legacyHash") == hash {
			c.Unchanged++
			return ref, nil
		}
		patch := map[string]any{}
		for k, v := range fields {
			if old, had := rec.Fields[k]; !had || !sameValue(old, v) {
				patch[k] = v
			}
		}
		if rec.ArchiveStatus != archive {
			patch[data.ColArchiveStatus] = archive
		}
		if rec.StarStatus != star {
			patch[data.ColStarStatus] = star
		}
		c.Updated++
		if im.apply {
			_, cs, err := im.store.Patch(data.PatchInput{Ref: ref, Fields: patch, Summary: "migrate: update " + it.legacy}, im.actor)
			if err != nil {
				return ref, fmt.Errorf("migrate: update %s: %w", it.legacy, err)
			}
			im.wrote(cs)
		}
		return ref, nil
	}

	c.Created++
	if !im.apply {
		ref := core.Ref{Kind: it.kind, ID: plannedPrefix + it.legacy}
		im.refs[it.legacy] = ref
		return ref, nil
	}
	rec, cs, err := im.store.Create(data.CreateInput{Kind: it.kind, Fields: fields}, im.actor)
	if err != nil {
		return core.Ref{}, fmt.Errorf("migrate: create %s: %w", it.legacy, err)
	}
	im.wrote(cs)
	ref := rec.Ref()
	im.refs[it.legacy] = ref
	if archive != data.ArchiveActive || star != data.StarUnstarred {
		_, cs, err := im.store.Patch(data.PatchInput{Ref: ref, Fields: map[string]any{data.ColArchiveStatus: archive, data.ColStarStatus: star},
			Summary: "migrate: status of " + it.legacy}, im.actor)
		if err != nil {
			return ref, fmt.Errorf("migrate: status of %s: %w", it.legacy, err)
		}
		im.wrote(cs)
	}
	return ref, nil
}

func (im *importer) wrote(cs *data.ChangeSet) {
	if cs != nil {
		im.rep.Writes++
	}
}

// relate adds a relation unless it is already there. A relation that exists
// is left as it is (its meta is not rewritten).
func (im *importer) relate(from core.Ref, kind string, to core.Ref, position int, meta map[string]any, c *Counts) error {
	if err := im.ctx.Err(); err != nil {
		return err
	}
	c.Total++
	key := relKey{from, kind}
	set, ok := im.rels[key]
	if !ok {
		set = map[core.Ref]bool{}
		if im.store != nil && !strings.HasPrefix(from.ID, plannedPrefix) {
			rels, err := im.store.Related(from, kind, data.Outgoing)
			if err != nil {
				return fmt.Errorf("migrate: read relations of %s: %w", data.RefText(from), err)
			}
			for _, rel := range rels {
				set[rel.To] = true
			}
		}
		im.rels[key] = set
	}
	if set[to] {
		c.Unchanged++
		return nil
	}
	set[to] = true
	c.Created++
	if !im.apply {
		return nil
	}
	_, cs, err := im.store.Relate(data.RelateInput{From: from, Kind: kind, To: to, Position: position, Meta: meta}, im.actor)
	if err != nil {
		return fmt.Errorf("migrate: relate %s -%s-> %s: %w", data.RefText(from), kind, data.RefText(to), err)
	}
	im.wrote(cs)
	return nil
}

// --- the mapping --------------------------------------------------------------

func rowID(r row) string {
	if r.id != "" {
		return r.id
	}
	return text(r.m, "id")
}

// setPriority maps a v1 priority; an unknown one is kept as legacyPriority.
func (im *importer) setPriority(fields, m map[string]any, what string) {
	raw := text(m, "priority")
	p, ok := priority(raw)
	if !ok {
		im.log.warn("%s: priority %q unknown; kept as legacyPriority", what, raw)
		fields["legacyPriority"] = raw
		return
	}
	setIf(fields, "priority", p)
}

// setCommon sets what every imported task, note and schedule may carry.
func setCommon(fields, m map[string]any, known ...string) {
	if p := integer(m, "position"); p != 0 {
		fields["position"] = p
	}
	setIf(fields, "legacyCreatedAt", msTime(m, "created"))
	setIf(fields, "legacyUpdatedAt", msTime(m, "updated"))
	if x := extra(m, append(known, "id", "position", "created", "updated", "archived", "starred")...); x != nil {
		fields["legacyExtra"] = x
	}
}

// dirLink is one v1 "directory" relation from a project or task.
type dirLink struct {
	path     string
	position int
	meta     map[string]any
	isDef    bool
}

// directoryLinks splits the v1 relations: "directory" relations by the
// legacyId of their project or task, and the others.
func (im *importer) directoryLinks(rows []row) (map[string][]dirLink, []row) {
	links := map[string][]dirLink{}
	var others []row
	for _, r := range rows {
		if text(r.m, "kind") != data.RelDirectory {
			others = append(others, r)
			continue
		}
		from, to := text(r.m, "from"), text(r.m, "to")
		path, isDir := strings.CutPrefix(to, KindDirectory+":")
		switch {
		case from == "":
			im.rep.Directories.Total++
			im.rep.Directories.Skipped++
			im.log.warn("directory relation %s: detached in v1 (was from %q); skipped", text(r.m, "key"), text(r.m, "former_from"))
			continue
		case !isDir || path == "":
			im.rep.Directories.Total++
			im.rep.Directories.Skipped++
			im.log.warn("directory relation %s: target %q is not a directory; skipped", text(r.m, "key"), to)
			continue
		case boolean(r.m, "archived"):
			im.rep.Directories.Total++
			im.rep.Directories.Skipped++
			im.log.warn("directory relation %s: archived in v1; skipped", text(r.m, "key"))
			continue
		}
		meta := relationMeta(r)
		isDef := false
		if d, ok := r.m["data"].(map[string]any); ok {
			isDef = boolean(d, "default")
		}
		links[from] = append(links[from], dirLink{path: path, position: int(integer(r.m, "position")), meta: meta, isDef: isDef})
	}
	for _, l := range links {
		slices.SortStableFunc(l, func(a, b dirLink) int { return a.position - b.position })
	}
	return links, others
}

// relationMeta keeps a v1 relation's data, text and key on the v2 relation.
func relationMeta(r row) map[string]any {
	meta := map[string]any{}
	if d, ok := r.m["data"].(map[string]any); ok {
		maps.Copy(meta, d)
	}
	setIf(meta, "text", text(r.m, "text"))
	key := text(r.m, "key")
	if key == "" {
		key = rowID(r)
	}
	meta["legacyKey"] = key
	return meta
}

// defaultDir is the v1 default directory of a project or task: the
// directory relation marked default, else the first one.
func defaultDir(links []dirLink) string {
	for _, l := range links {
		if l.isDef {
			return l.path
		}
	}
	if len(links) > 0 {
		return links[0].path
	}
	return ""
}

// projects imports v1 projects as root tasks, ordered as v1 showed them:
// positioned projects first, then by creation.
func (im *importer) projects(rows []row, links map[string][]dirLink) error {
	slices.SortStableFunc(rows, func(a, b row) int {
		pa, pb := integer(a.m, "position"), integer(b.m, "position")
		switch {
		case pa != pb && (pa == 0 || pb == 0):
			if pa == 0 {
				return 1
			}
			return -1
		case pa != pb:
			return cmpInt(pa, pb)
		}
		return cmpInt(integer(a.m, "created"), integer(b.m, "created"))
	})
	for _, r := range rows {
		id, m := rowID(r), r.m
		legacy := "project:" + id
		status := text(m, "status")
		st, ok := projectStatuses[strings.ToLower(status)]
		if !ok {
			im.log.warn("%s: project status %q unknown; imported as open", legacy, status)
			st = projectStatuses["open"]
		}
		title := text(m, "name")
		if title == "" {
			title = "(untitled project)"
		}
		fields := map[string]any{"title": title, "workStatus": st.work}
		im.setPriority(fields, m, legacy)
		repo := text(m, "repo")
		setIf(fields, "repo", repo)
		dir := defaultDir(links[legacy])
		if dir == "" && (strings.HasPrefix(repo, "/") || strings.HasPrefix(repo, "~")) {
			dir = repo
		}
		setIf(fields, "defaultDir", dir)
		if settings, ok := m["settings"].(map[string]any); ok {
			setIf(fields, "defaultHarness", text(settings, "harness"))
		}
		if status != "" && st.archived {
			fields["legacyStatus"] = status
		}
		setCommon(fields, m, "name", "repo", "status", "priority")
		if _, err := im.put(item{kind: data.KindTask, legacy: legacy, fields: fields,
			archived: st.archived || boolean(m, "archived"), starred: boolean(m, "starred")}, &im.rep.Projects); err != nil {
			return err
		}
		im.projectOrder = append(im.projectOrder, legacy)
	}
	return nil
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// tasks imports v1 tasks parents first: each project's subtree in v1 order
// (position, then creation), then tasks with no project. A task whose parent
// task is missing goes under its project; one with no project either becomes
// a root task. A parent loop is broken at its first task, which goes under its
// project.
func (im *importer) tasks(rows []row, links map[string][]dirLink) error {
	slices.SortStableFunc(rows, func(a, b row) int {
		if c := cmpInt(integer(a.m, "position"), integer(b.m, "position")); c != 0 {
			return c
		}
		return cmpInt(integer(a.m, "created"), integer(b.m, "created"))
	})
	byID := map[string]bool{}
	for _, r := range rows {
		byID[rowID(r)] = true
	}
	parentOf := map[string]string{} // task id -> legacyId of its parent ("" for a root)
	projectOf := func(r row) string {
		if pid := text(r.m, "project_id"); pid != "" {
			if _, ok := im.refs["project:"+pid]; ok {
				return "project:" + pid
			}
		}
		return ""
	}
	children := map[string][]row{}
	for _, r := range rows {
		id := rowID(r)
		legacy := "task:" + id
		parent := ""
		if pid := text(r.m, "parent_id"); pid != "" {
			if byID[pid] {
				parent = "task:" + pid
			} else {
				im.log.warn("%s: parent task %s missing; placed under its project", legacy, pid)
			}
		}
		if parent == "" {
			parent = projectOf(r)
			if parent == "" {
				if pid := text(r.m, "project_id"); pid != "" {
					im.log.warn("%s: project %s missing; imported as a root task", legacy, pid)
				} else {
					im.log.warn("%s: no project; imported as a root task", legacy)
				}
			}
		}
		parentOf[id] = parent
		children[parent] = append(children[parent], r)
	}

	done := map[string]bool{}
	var visit func(rs []row) error
	visit = func(rs []row) error {
		for _, r := range rs {
			id := rowID(r)
			if done[id] {
				continue
			}
			done[id] = true
			if err := im.task(r, parentOf[id], links); err != nil {
				return err
			}
			if err := visit(children["task:"+id]); err != nil {
				return err
			}
		}
		return nil
	}
	// Projects in the order they were imported, then root tasks.
	for _, legacy := range append(slices.Clone(im.projectOrder), "") {
		if err := visit(children[legacy]); err != nil {
			return err
		}
	}
	for _, r := range rows {
		id := rowID(r)
		if done[id] {
			continue
		}
		parentOf[id] = projectOf(r)
		im.log.warn("task:%s: parent loop broken here; placed under its project", id)
		if err := visit([]row{r}); err != nil {
			return err
		}
	}
	return nil
}

func (im *importer) task(r row, parent string, links map[string][]dirLink) error {
	id, m := rowID(r), r.m
	legacy := "task:" + id
	status := text(m, "status")
	work, ok := taskStatuses[strings.ToLower(strings.TrimSpace(status))]
	if !ok {
		im.log.warn("%s: status %q unknown; imported as todo", legacy, status)
		work = "todo"
	}
	title := text(m, "title")
	if title == "" {
		title = "(untitled task)"
	}
	fields := map[string]any{"title": title, "workStatus": work}
	if !ok {
		fields["legacyStatus"] = status
	}
	im.setPriority(fields, m, legacy)
	setIf(fields, "defaultDir", defaultDir(links[legacy]))
	setCommon(fields, m, "title", "status", "priority", "parent_id", "project_id")
	it := item{kind: data.KindTask, legacy: legacy, fields: fields, archived: boolean(m, "archived"), starred: boolean(m, "starred")}
	if parent != "" {
		it.links = map[string]string{"parent": parent}
	}
	_, err := im.put(it, &im.rep.Tasks)
	return err
}
