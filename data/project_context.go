package data

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

var (
	validContextKinds = []string{"url", "pr", "file", "doc", "gdoc", "sheet"}
	ghPRURLRe         = regexp.MustCompile(`(?i)^https?://github\.com/([A-Za-z0-9][A-Za-z0-9_.-]{0,38}/[A-Za-z0-9_.-]{1,100})/pull/([1-9][0-9]*)(?:[/?#].*)?$`)
	genericPRURLRe    = regexp.MustCompile(`(?i)^https?://[^/\s]+/.+/(?:pull|merge_requests)/[1-9][0-9]*(?:[/?#].*)?$`)
	ghPRRefRe         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,38}/[A-Za-z0-9_.-]{1,100}#[1-9][0-9]*$`)
)

// OfficialContextInput describes an official curated project context item to
// create or promote on a project.
type OfficialContextInput struct {
	ProjectID   string
	Title       string
	Value       string
	ContextKind string
	Notes       string
	Pinned      bool
}

// ProjectWorktree is one deduplicated worktree or directory associated with a
// project (via a lane worktree, a task defaultDir, or a RelDirectory relation).
type ProjectWorktree struct {
	Path   string   `json:"path"`
	Branch string   `json:"branch,omitempty"`
	Repo   string   `json:"repo,omitempty"`
	Source string   `json:"source"`
	Ref    core.Ref `json:"ref"`
}

// ProjectContextOverrides controls how ResolveProjectContextBundle resolves
// project context for agent runs and task queues.
type ProjectContextOverrides struct {
	IncludeProjectContext *bool
	ContextIDs            []string
	ExcludeIDs            []string
}

// ClassifyContextKind returns the canonical contextKind for value. A valid
// explicit kind (url, pr, file, doc, gdoc, sheet) is always honored.
func ClassifyContextKind(value, explicit string) string {
	if exp := strings.ToLower(strings.TrimSpace(explicit)); exp != "" && slices.Contains(validContextKinds, exp) {
		return exp
	}
	val := strings.TrimSpace(value)
	low := strings.ToLower(val)
	switch {
	case strings.Contains(low, "docs.google.com/document"):
		return "gdoc"
	case strings.Contains(low, "docs.google.com/spreadsheets"):
		return "sheet"
	case ghPRURLRe.MatchString(val) || genericPRURLRe.MatchString(val) || ghPRRefRe.MatchString(val):
		return "pr"
	case strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://"):
		return "url"
	}
	ext := strings.ToLower(filepath.Ext(val))
	if ext == ".md" || ext == ".markdown" {
		return "doc"
	}
	if looksLikeLocalPath(val, ext) {
		return "file"
	}
	return "url"
}

func looksLikeLocalPath(val, ext string) bool {
	if val == "" {
		return false
	}
	if strings.HasPrefix(val, "/") || strings.HasPrefix(val, "~/") || strings.HasPrefix(val, "./") || strings.HasPrefix(val, "../") {
		return true
	}
	if strings.Contains(val, "/") || strings.Contains(val, `\`) {
		return !strings.Contains(val, "://")
	}
	return ext != "" && !strings.ContainsAny(val, " \t\n")
}

func deriveContextTitleAndCanon(val, kind, explicitTitle string) (title, canonKey string) {
	title = strings.TrimSpace(explicitTitle)
	if title == "" {
		if m := ghPRURLRe.FindStringSubmatch(val); len(m) == 3 {
			title = m[1] + "#" + m[2]
		} else if kind == "file" || kind == "doc" {
			title = filepath.Base(val)
		} else {
			title = val
		}
	}
	switch kind {
	case "pr":
		if m := ghPRURLRe.FindStringSubmatch(val); len(m) == 3 {
			canonKey = "pr:" + strings.ToLower(m[1]+"#"+m[2])
		} else {
			canonKey = "pr:" + strings.ToLower(strings.TrimRight(val, "/"))
		}
	case "url", "gdoc", "sheet":
		canonKey = kind + ":" + strings.ToLower(strings.TrimRight(val, "/"))
	default:
		canonKey = kind + ":" + filepath.Clean(val)
	}
	return title, canonKey
}

// RootProjectID walks task.parent references up to the root task (project) ID
// with a cycle guard.
func (s *Store) RootProjectID(taskID string) string {
	cur := strings.TrimSpace(taskID)
	if cur == "" {
		return ""
	}
	visited := map[string]bool{cur: true}
	for {
		rec, err := s.Get(core.Ref{Kind: KindTask, ID: cur})
		if err != nil {
			return cur
		}
		parent, ok := rec.RefField("parent")
		if !ok || parent.Kind != KindTask || strings.TrimSpace(parent.ID) == "" {
			return cur
		}
		next := strings.TrimSpace(parent.ID)
		if visited[next] {
			return cur
		}
		visited[next] = true
		cur = next
	}
}

// ProjectTaskIDs returns a set containing projectID and all descendant task IDs.
func (s *Store) ProjectTaskIDs(projectID string, includeArchived bool) (map[string]bool, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return map[string]bool{}, nil
	}
	res, err := s.Query(Query{Kind: KindTask, IncludeArchived: includeArchived})
	if err != nil {
		return nil, err
	}
	children := map[string][]string{}
	for _, rec := range res.Items {
		if parent, ok := rec.RefField("parent"); ok && parent.Kind == KindTask && parent.ID != "" {
			children[parent.ID] = append(children[parent.ID], rec.ID)
		}
	}
	ids := map[string]bool{projectID: true}
	queue := []string{projectID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range children[cur] {
			if !ids[child] {
				ids[child] = true
				queue = append(queue, child)
			}
		}
	}
	return ids, nil
}

const sqlInBatchSize = 400

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

func (s *Store) projectScopedContext(projectID string) ([]Record, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, nil
	}
	taskIDs, err := s.ProjectTaskIDs(projectID, false)
	if err != nil {
		return nil, err
	}
	if len(taskIDs) == 0 {
		return nil, nil
	}
	taskIDList := make([]string, 0, len(taskIDs))
	for id := range taskIDs {
		taskIDList = append(taskIDList, id)
	}
	slices.Sort(taskIDList)

	relLinked := map[string]bool{}
	for start := 0; start < len(taskIDList); start += sqlInBatchSize {
		end := min(start+sqlInBatchSize, len(taskIDList))
		batch := taskIDList[start:end]
		args := make([]any, 0, len(batch)+3)
		args = append(args, KindTask)
		for _, id := range batch {
			args = append(args, id)
		}
		args = append(args, RelContext, KindContext)
		rels, err := queryRelations(s.db, "from_kind = ? AND from_id IN ("+placeholders(len(batch))+") AND kind = ? AND to_kind = ?", args...)
		if err != nil {
			return nil, err
		}
		for _, rel := range rels {
			if taskIDs[rel.From.ID] && rel.To.ID != "" {
				relLinked[rel.To.ID] = true
			}
		}
	}

	local, err := loadLocal(s.db, KindContext, "")
	if err != nil {
		return nil, err
	}
	fetchIDSet := make(map[string]bool, len(relLinked))
	for id := range relLinked {
		fetchIDSet[id] = true
	}
	for ref, fields := range local {
		if ref.Kind == KindContext {
			if _, hasTask := fields["task"]; hasTask {
				fetchIDSet[ref.ID] = true
			}
		}
	}

	seen := map[string]bool{}
	var out []Record
	appendRows := func(query string, args ...any) error {
		rows, err := s.db.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			rec, err := scanRecord(rows)
			if err != nil {
				return err
			}
			rec.Local = local[rec.Ref()]
			s.coerce(&rec)
			if rec.IsArchived() || seen[rec.ID] {
				continue
			}
			inScope := relLinked[rec.ID]
			if !inScope {
				if tRef, ok := rec.RefField("task"); ok && tRef.Kind == KindTask && taskIDs[tRef.ID] {
					inScope = true
				}
			}
			if !inScope {
				continue
			}
			seen[rec.ID] = true
			out = append(out, rec)
		}
		return rows.Err()
	}

	for start := 0; start < len(taskIDList); start += sqlInBatchSize {
		end := min(start+sqlInBatchSize, len(taskIDList))
		batch := taskIDList[start:end]
		args := make([]any, 0, len(batch)+3)
		args = append(args, KindContext, ArchiveActive, KindTask)
		for _, id := range batch {
			args = append(args, id)
		}
		q := "SELECT " + recordCols + " FROM records WHERE kind = ? AND +archive_status = ? AND json_extract(fields, '$.task.kind') = ? AND json_extract(fields, '$.task.id') IN (" + placeholders(len(batch)) + ") ORDER BY created_at, seq"
		if err := appendRows(q, args...); err != nil {
			return nil, err
		}
	}

	if len(fetchIDSet) > 0 {
		fetchIDs := make([]string, 0, len(fetchIDSet))
		for id := range fetchIDSet {
			if !seen[id] {
				fetchIDs = append(fetchIDs, id)
			}
		}
		slices.Sort(fetchIDs)
		for start := 0; start < len(fetchIDs); start += sqlInBatchSize {
			end := min(start+sqlInBatchSize, len(fetchIDs))
			batch := fetchIDs[start:end]
			args := make([]any, 0, len(batch)+2)
			args = append(args, KindContext, ArchiveActive)
			for _, id := range batch {
				args = append(args, id)
			}
			q := "SELECT " + recordCols + " FROM records WHERE kind = ? AND +archive_status = ? AND id IN (" + placeholders(len(batch)) + ") ORDER BY created_at, seq"
			if err := appendRows(q, args...); err != nil {
				return nil, err
			}
		}
	}

	return out, nil
}

// OfficialProjectContext returns active KindContext records explicitly marked
// official == true scoped to projectID (via task in ProjectTaskIDs or outgoing
// RelContext from the project/descendant tasks), deduplicated by ID and sorted
// deterministically (pinned/starred desc, title asc, id asc).
func (s *Store) OfficialProjectContext(projectID string) ([]Record, error) {
	scoped, err := s.projectScopedContext(projectID)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, rec := range scoped {
		if rec.Bool("official") {
			out = append(out, rec)
		}
	}
	slices.SortStableFunc(out, compareOfficialContext)
	return out, nil
}

// ProjectContextAll returns all active KindContext records scoped to projectID,
// sorted with official first, then pinned/starred, then remaining by UpdatedAt
// descending.
func (s *Store) ProjectContextAll(projectID string) ([]Record, error) {
	out, err := s.projectScopedContext(projectID)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(out, compareProjectContextAll)
	return out, nil
}

func boolDesc(a, b bool) int {
	if a == b {
		return 0
	}
	if a {
		return -1
	}
	return 1
}

func compareContextTitleID(a, b Record) int {
	ta := strings.TrimSpace(a.Text("title"))
	tb := strings.TrimSpace(b.Text("title"))
	if c := strings.Compare(strings.ToLower(ta), strings.ToLower(tb)); c != 0 {
		return c
	}
	if c := strings.Compare(ta, tb); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

func compareOfficialContext(a, b Record) int {
	if c := boolDesc(a.Bool("pinned"), b.Bool("pinned")); c != 0 {
		return c
	}
	if c := boolDesc(a.IsStarred(), b.IsStarred()); c != 0 {
		return c
	}
	return compareContextTitleID(a, b)
}

func compareProjectContextAll(a, b Record) int {
	if c := boolDesc(a.Bool("official"), b.Bool("official")); c != 0 {
		return c
	}
	if c := boolDesc(a.Bool("pinned"), b.Bool("pinned")); c != 0 {
		return c
	}
	if c := boolDesc(a.IsStarred(), b.IsStarred()); c != 0 {
		return c
	}
	if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
		return c
	}
	return compareContextTitleID(a, b)
}

// SetContextOfficial updates the official curation flag (and optional pinned
// and notes fields) on a KindContext record.
func (s *Store) SetContextOfficial(ref core.Ref, official bool, pinned *bool, notes *string, actor core.Actor) (Record, error) {
	if ref.Kind == "" && ref.ID != "" {
		ref.Kind = KindContext
	}
	if ref.Kind != KindContext || strings.TrimSpace(ref.ID) == "" {
		return Record{}, errors.New("data: context ref must be a context record with non-empty ID")
	}
	existing, err := s.Get(ref)
	if err != nil {
		return Record{}, err
	}
	fields := map[string]any{"official": official}
	if official && strings.TrimSpace(existing.Text("source")) == "" {
		fields["source"] = "curated"
	}
	if pinned != nil {
		fields["pinned"] = *pinned
	}
	if notes != nil {
		fields["notes"] = *notes
	}
	updated, _, err := s.Patch(PatchInput{
		Ref:     ref,
		Fields:  fields,
		Summary: fmt.Sprintf("set context official=%v on %s", official, RefText(ref)),
	}, actor)
	if err != nil {
		return Record{}, err
	}
	return *updated, nil
}

// AddOfficialProjectContext creates or promotes an official curated KindContext
// record on the root project of in.ProjectID, returning the record, whether an
// existing project context item was deduplicated/promoted, and any error.
func (s *Store) AddOfficialProjectContext(in OfficialContextInput, actor core.Actor) (Record, bool, error) {
	val := strings.TrimSpace(in.Value)
	if val == "" {
		return Record{}, false, errors.New("data: context value is required")
	}
	projID := strings.TrimSpace(in.ProjectID)
	if projID == "" {
		return Record{}, false, errors.New("data: project ID is required")
	}
	if exp := strings.ToLower(strings.TrimSpace(in.ContextKind)); exp != "" && !slices.Contains(validContextKinds, exp) {
		return Record{}, false, fmt.Errorf("data: invalid contextKind %q (want %s)", in.ContextKind, strings.Join(validContextKinds, ", "))
	}

	rootID := s.RootProjectID(projID)
	rootRef := core.Ref{Kind: KindTask, ID: rootID}
	if _, err := s.Get(rootRef); err != nil {
		return Record{}, false, err
	}

	kind := ClassifyContextKind(val, in.ContextKind)
	title, canonKey := deriveContextTitleAndCanon(val, kind, in.Title)

	existingList, err := s.ProjectContextAll(rootID)
	if err != nil {
		return Record{}, false, err
	}
	var existing *Record
	for i := range existingList {
		cand := existingList[i]
		candCanon := strings.TrimSpace(cand.Text("canonKey"))
		if candCanon == "" {
			candVal := strings.TrimSpace(cand.Text("value"))
			_, candCanon = deriveContextTitleAndCanon(candVal, ClassifyContextKind(candVal, cand.Text("contextKind")), "")
		}
		if candCanon == canonKey || strings.EqualFold(strings.TrimRight(strings.TrimSpace(cand.Text("value")), "/"), strings.TrimRight(val, "/")) {
			cp := cand
			existing = &cp
			break
		}
	}

	if existing != nil {
		patchFields := map[string]any{
			"official": true,
			"pinned":   existing.Bool("pinned") || in.Pinned,
		}
		if t := strings.TrimSpace(in.Title); t != "" {
			patchFields["title"] = t
		}
		if n := strings.TrimSpace(in.Notes); n != "" {
			patchFields["notes"] = n
		}
		if strings.TrimSpace(existing.Text("source")) == "" {
			patchFields["source"] = "curated"
		}
		if strings.TrimSpace(existing.Text("canonKey")) == "" {
			patchFields["canonKey"] = canonKey
		}
		if strings.TrimSpace(in.ContextKind) != "" {
			patchFields["contextKind"] = kind
		}
		if strings.HasPrefix(strings.ToLower(val), "http") && !strings.HasPrefix(strings.ToLower(existing.Text("value")), "http") {
			patchFields["value"] = val
		}
		updated, _, err := s.Patch(PatchInput{
			Ref:     existing.Ref(),
			Fields:  patchFields,
			Summary: fmt.Sprintf("promote official context %s", RefText(existing.Ref())),
		}, actor)
		if err != nil {
			return Record{}, true, err
		}
		if _, _, err := s.Relate(RelateInput{From: rootRef, Kind: RelContext, To: updated.Ref()}, actor); err != nil {
			return Record{}, true, err
		}
		return *updated, true, nil
	}

	fields := map[string]any{
		"title":       title,
		"value":       val,
		"contextKind": kind,
		"source":      "curated",
		"canonKey":    canonKey,
		"official":    true,
		"pinned":      in.Pinned,
		"task":        rootRef,
	}
	if n := strings.TrimSpace(in.Notes); n != "" {
		fields["notes"] = n
	}
	created, _, err := s.Create(CreateInput{Kind: KindContext, Fields: fields}, actor)
	if err != nil {
		return Record{}, false, err
	}
	if _, _, err := s.Relate(RelateInput{From: rootRef, Kind: RelContext, To: created.Ref()}, actor); err != nil {
		return Record{}, false, err
	}
	return *created, false, nil
}

// ProjectWorktrees aggregates deduplicated worktree and directory paths for
// projectID and its descendant tasks from active lane.worktree, task.defaultDir,
// and outgoing RelDirectory relations, sorted by path.
func (s *Store) ProjectWorktrees(projectID string) ([]ProjectWorktree, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, nil
	}
	taskIDs, err := s.ProjectTaskIDs(projectID, false)
	if err != nil {
		return nil, err
	}

	byPath := map[string]ProjectWorktree{}
	add := func(wt ProjectWorktree) {
		p := strings.TrimSpace(wt.Path)
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		wt.Path = p
		if cur, exists := byPath[p]; exists {
			if cur.Branch == "" && wt.Branch != "" {
				cur.Branch = wt.Branch
			}
			if cur.Repo == "" && wt.Repo != "" {
				cur.Repo = wt.Repo
			}
			byPath[p] = cur
			return
		}
		byPath[p] = wt
	}

	// 1. Active lanes attached to the project or its descendant tasks.
	if lanes, err := s.Query(Query{Kind: KindLane}); err != nil {
		return nil, err
	} else {
		for _, lane := range lanes.Items {
			if lane.IsArchived() {
				continue
			}
			tRef, ok := lane.RefField("task")
			if !ok || tRef.Kind != KindTask || !taskIDs[tRef.ID] {
				continue
			}
			if p := strings.TrimSpace(lane.Text("worktree")); p != "" {
				add(ProjectWorktree{
					Path:   p,
					Branch: strings.TrimSpace(lane.Text("branch")),
					Repo:   strings.TrimSpace(lane.Text("repo")),
					Source: "lane",
					Ref:    lane.Ref(),
				})
			}
		}
	}

	// 2. Project and descendant task.defaultDir values.
	if tasks, err := s.Query(Query{Kind: KindTask}); err != nil {
		return nil, err
	} else {
		for _, task := range tasks.Items {
			if task.IsArchived() || !taskIDs[task.ID] {
				continue
			}
			if p := strings.TrimSpace(task.Text("defaultDir")); p != "" {
				add(ProjectWorktree{
					Path:   p,
					Repo:   strings.TrimSpace(task.Text("repo")),
					Source: "task",
					Ref:    task.Ref(),
				})
			}
		}
	}

	// 3. Outgoing RelDirectory relations from project or descendant tasks.
	rels, err := queryRelations(s.db, "from_kind = ? AND kind = ?", KindTask, RelDirectory)
	if err != nil {
		return nil, err
	}
	for _, rel := range rels {
		if !taskIDs[rel.From.ID] {
			continue
		}
		p := strings.TrimSpace(rel.To.ID)
		if rel.To.Kind != "directory" {
			if rec, gerr := s.Get(rel.To); gerr == nil && rec != nil {
				for _, field := range []string{"path", "cwd", "worktree", "defaultDir"} {
					if v := strings.TrimSpace(rec.Text(field)); v != "" {
						p = v
						break
					}
				}
			}
		}
		if p != "" {
			add(ProjectWorktree{
				Path:   p,
				Source: "directory",
				Ref:    rel.To,
			})
		}
	}

	out := make([]ProjectWorktree, 0, len(byPath))
	for _, wt := range byPath {
		out = append(out, wt)
	}
	slices.SortStableFunc(out, func(a, b ProjectWorktree) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		if c := strings.Compare(a.Source, b.Source); c != 0 {
			return c
		}
		return strings.Compare(RefText(a.Ref), RefText(b.Ref))
	})
	return out, nil
}

// ResolveProjectContextBundle resolves the context records to inject for a
// project according to opt-out and explicit override settings.
func (s *Store) ResolveProjectContextBundle(projectID string, overrides ProjectContextOverrides) ([]Record, error) {
	var cleanIDs []string
	seenIDs := map[string]bool{}
	for _, raw := range overrides.ContextIDs {
		id := strings.TrimSpace(raw)
		if id == "" || seenIDs[id] {
			continue
		}
		seenIDs[id] = true
		cleanIDs = append(cleanIDs, id)
	}

	excluded := map[string]bool{}
	for _, raw := range overrides.ExcludeIDs {
		if id := strings.TrimSpace(raw); id != "" {
			excluded[id] = true
		}
	}

	if overrides.IncludeProjectContext != nil && !*overrides.IncludeProjectContext && len(cleanIDs) == 0 {
		return nil, nil
	}

	if len(cleanIDs) > 0 {
		out := make([]Record, 0, len(cleanIDs))
		for _, id := range cleanIDs {
			if excluded[id] {
				continue
			}
			rec, err := s.Get(core.Ref{Kind: KindContext, ID: id})
			if err != nil {
				return nil, err
			}
			if rec.IsArchived() {
				continue
			}
			out = append(out, *rec)
		}
		return out, nil
	}

	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, nil
	}
	official, err := s.OfficialProjectContext(projectID)
	if err != nil {
		return nil, err
	}
	if len(excluded) == 0 {
		return official, nil
	}
	out := make([]Record, 0, len(official))
	for _, rec := range official {
		if !excluded[rec.ID] {
			out = append(out, rec)
		}
	}
	return out, nil
}

// FormatProjectContextMarkdown renders a deterministic Markdown block for
// official/curated project context records. Returns "" when records is empty.
func FormatProjectContextMarkdown(records []Record) string {
	if len(records) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Official Project Context\n")
	for _, rec := range records {
		val := strings.TrimSpace(rec.Text("value"))
		title := strings.TrimSpace(rec.Text("title"))
		kind := strings.TrimSpace(rec.Text("contextKind"))
		if kind == "" {
			kind = ClassifyContextKind(val, "")
		}
		switch {
		case title != "" && val != "" && title != val:
			fmt.Fprintf(&b, "- [%s] **%s**: %s\n", kind, title, val)
		case val != "":
			fmt.Fprintf(&b, "- [%s] %s\n", kind, val)
		case title != "":
			fmt.Fprintf(&b, "- [%s] **%s**\n", kind, title)
		default:
			fmt.Fprintf(&b, "- [%s] %s\n", kind, rec.ID)
		}
		if notes := strings.TrimSpace(rec.Text("notes")); notes != "" {
			normalized := strings.ReplaceAll(notes, "\r\n", "\n")
			lines := strings.Split(normalized, "\n")
			fmt.Fprintf(&b, "  Notes: %s\n", strings.TrimSpace(lines[0]))
			for _, cont := range lines[1:] {
				if c := strings.TrimSpace(cont); c != "" {
					fmt.Fprintf(&b, "  %s\n", c)
				}
			}
		}
	}
	return b.String()
}
