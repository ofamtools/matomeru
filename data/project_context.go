package data

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

// ValidContextKinds lists the canonical contextKind values for KindContext records.
var ValidContextKinds = []string{"url", "pr", "file", "doc", "gdoc", "sheet", "code"}

var (
	ghPRURLRe      = regexp.MustCompile(`(?i)^https?://github\.com/([A-Za-z0-9][A-Za-z0-9_.-]{0,38}/[A-Za-z0-9_.-]{1,100})/pull/([1-9][0-9]*)(?:[/?#].*)?$`)
	genericPRURLRe = regexp.MustCompile(`(?i)^https?://[^/\s]+/.+/(?:pull|merge_requests)/[1-9][0-9]*(?:[/?#].*)?$`)
	ghPRRefRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,38}/[A-Za-z0-9_.-]{1,100}#[1-9][0-9]*$`)
)

// OfficialContextInput describes an official curated project context item to
// create or promote on a project.
type OfficialContextInput struct {
	ProjectID   string
	Title       string
	Value       string
	ContextKind string
	Language    string
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

// NormalizeContextKind returns the canonical lowercase contextKind for an
// explicit kind string, normalizing "snippet" to "code". Returns "" when
// explicit is empty or not a recognized contextKind.
func NormalizeContextKind(explicit string) string {
	exp := strings.ToLower(strings.TrimSpace(explicit))
	if exp == "snippet" {
		return "code"
	}
	if slices.Contains(ValidContextKinds, exp) {
		return exp
	}
	return ""
}

// ClassifyContextKind returns the canonical contextKind for value. A valid
// explicit kind (url, pr, file, doc, gdoc, sheet, code, or alias snippet) is
// always honored.
func ClassifyContextKind(value, explicit string) string {
	if exp := NormalizeContextKind(explicit); exp != "" {
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

// CodeContextCanonKey returns a deterministic short hash canonKey for a code
// context item ("code:" + 16 hex chars of SHA-256 over normalized content).
func CodeContextCanonKey(code string) string {
	norm := strings.TrimSpace(strings.ReplaceAll(code, "\r\n", "\n"))
	if norm == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(norm))
	return "code:" + hex.EncodeToString(sum[:8])
}

// CodeContextTitle derives a clean single-line display title for a code snippet
// or fenced code block, optionally prefixed with its language tag and clipped
// to ~60 runes.
func CodeContextTitle(code, language string) string {
	norm := strings.ReplaceAll(code, "\r\n", "\n")
	first := ""
	for _, ln := range strings.Split(norm, "\n") {
		if collapsed := strings.Join(strings.Fields(ln), " "); collapsed != "" {
			first = collapsed
			break
		}
	}
	lang := sanitizeCodeLanguage(language)
	if first == "" {
		if lang != "" {
			return lang + " code"
		}
		return "code snippet"
	}
	if lang != "" {
		return clip(lang+": "+first, 60)
	}
	return clip(first, 60)
}

func sanitizeCodeLanguage(language string) string {
	fields := strings.Fields(strings.ReplaceAll(language, "`", ""))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// FormatCodeMarkdownBlock wraps code in a CommonMark fenced code block whose
// backtick fence is guaranteed longer than any backtick run inside code.
func FormatCodeMarkdownBlock(code, language string) string {
	body := strings.TrimRight(strings.ReplaceAll(code, "\r\n", "\n"), "\n")
	maxRun := maxBacktickRun(body)
	fenceLen := max(3, maxRun+1)
	fence := strings.Repeat("`", fenceLen)
	lang := sanitizeCodeLanguage(language)
	return fence + lang + "\n" + body + "\n" + fence
}

// FormatInlineCodeMarkdown wraps a single-line code snippet in a CommonMark
// inline code span whose backtick delimiter cannot collide with code's contents.
func FormatInlineCodeMarkdown(code string) string {
	maxRun := maxBacktickRun(code)
	delim := strings.Repeat("`", maxRun+1)
	inner := code
	if strings.HasPrefix(inner, "`") || strings.HasSuffix(inner, "`") ||
		(len(inner) > 0 && inner[0] == ' ' && inner[len(inner)-1] == ' ' && strings.Trim(inner, " ") != "") {
		inner = " " + inner + " "
	}
	return delim + inner + delim
}

func maxBacktickRun(s string) int {
	maxRun, cur := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			cur++
			if cur > maxRun {
				maxRun = cur
			}
		} else {
			cur = 0
		}
	}
	return maxRun
}

// DeriveContextTitleAndCanon derives a clean display title and deterministic
// canonKey for a context item of kind. Code items never run through
// filepath.Clean and use a short SHA-256 hash canonKey.
func DeriveContextTitleAndCanon(val, kind, explicitTitle, language string) (title, canonKey string) {
	title = strings.TrimSpace(explicitTitle)
	if title == "" {
		switch {
		case kind == "code":
			title = CodeContextTitle(val, language)
		case ghPRURLRe.MatchString(val):
			if m := ghPRURLRe.FindStringSubmatch(val); len(m) == 3 {
				title = m[1] + "#" + m[2]
			}
		case kind == "file" || kind == "doc":
			title = filepath.Base(val)
		default:
			title = val
		}
	}
	switch kind {
	case "code":
		canonKey = CodeContextCanonKey(val)
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

func deriveContextTitleAndCanon(val, kind, explicitTitle string) (title, canonKey string) {
	return DeriveContextTitleAndCanon(val, kind, explicitTitle, "")
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

const sqlInBatchSize = 400

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

func (s *Store) projectScopedContext(projectID string) ([]Record, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, nil
	}
	scope, err := s.ProjectScope(projectID, nil, false)
	if err != nil {
		return nil, err
	}
	return s.ProjectRecords(scope, KindContext, false)
}

// OfficialProjectContext returns active KindContext records explicitly marked
// official == true scoped to projectID, deduplicated by ID and sorted
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

// ProjectContextAll returns all active context items in the project.
func (s *Store) ProjectContextAll(projectID string) ([]Record, error) {
	out, err := s.projectScopedContext(projectID)
	slices.SortStableFunc(out, compareProjectContextAll)
	return out, err
}

// ProjectContextRecords returns the scope's context items with official and
// pinned context first, then by recency.
func (s *Store) ProjectContextRecords(scope ProjectScope, includeArchived bool) ([]Record, error) {
	out, err := s.ProjectRecords(scope, KindContext, includeArchived)
	slices.SortStableFunc(out, compareProjectContextAll)
	return out, err
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
	if strings.TrimSpace(in.Value) == "" {
		return Record{}, false, errors.New("data: context value is required")
	}
	projID := strings.TrimSpace(in.ProjectID)
	if projID == "" {
		return Record{}, false, errors.New("data: project ID is required")
	}
	if strings.TrimSpace(in.ContextKind) != "" && NormalizeContextKind(in.ContextKind) == "" {
		return Record{}, false, fmt.Errorf("data: invalid contextKind %q (want %s)", in.ContextKind, strings.Join(ValidContextKinds, ", "))
	}

	rootID := s.RootProjectID(projID)
	rootRef := core.Ref{Kind: KindTask, ID: rootID}
	if _, err := s.Get(rootRef); err != nil {
		return Record{}, false, err
	}

	kind := ClassifyContextKind(in.Value, in.ContextKind)
	val := strings.TrimSpace(in.Value)
	if kind == "code" {
		val = strings.Trim(strings.ReplaceAll(in.Value, "\r\n", "\n"), "\n")
	}
	lang := sanitizeCodeLanguage(in.Language)
	title, canonKey := DeriveContextTitleAndCanon(val, kind, in.Title, lang)

	existingList, err := s.ProjectContextAll(rootID)
	if err != nil {
		return Record{}, false, err
	}
	var existing *Record
	for i := range existingList {
		cand := existingList[i]
		candKind := ClassifyContextKind(cand.Text("value"), cand.Text("contextKind"))
		candCanon := strings.TrimSpace(cand.Text("canonKey"))
		if candCanon == "" {
			candVal := strings.TrimSpace(cand.Text("value"))
			if candKind == "code" {
				candVal = strings.Trim(strings.ReplaceAll(cand.Text("value"), "\r\n", "\n"), "\n")
			}
			_, candCanon = DeriveContextTitleAndCanon(candVal, candKind, "", cand.Text("language"))
		}
		sameVal := kind != "code" && strings.EqualFold(strings.TrimRight(strings.TrimSpace(cand.Text("value")), "/"), strings.TrimRight(val, "/"))
		if candCanon == canonKey || sameVal {
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
		if lang != "" && strings.TrimSpace(existing.Text("language")) == "" {
			patchFields["language"] = lang
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
		if kind != "code" && strings.HasPrefix(strings.ToLower(val), "http") && !strings.HasPrefix(strings.ToLower(existing.Text("value")), "http") {
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
	if lang != "" {
		fields["language"] = lang
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
// projectID and its descendant tasks from related lane.worktree, task.defaultDir,
// and outgoing RelDirectory relations, sorted by path.
func (s *Store) ProjectWorktrees(projectID string) ([]ProjectWorktree, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, nil
	}
	scope, err := s.ProjectScope(projectID, nil, false)
	if err != nil {
		return nil, err
	}
	lanes, err := s.ProjectRecords(scope, KindLane, false)
	if err != nil {
		return nil, err
	}
	return s.ProjectScopeWorktrees(scope, lanes), nil
}

// ProjectScopeWorktrees returns the worktrees attached to the scoped lanes and tasks.
func (s *Store) ProjectScopeWorktrees(scope ProjectScope, lanes []Record) []ProjectWorktree {
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

	for _, lane := range lanes {
		if lane.IsArchived() {
			continue
		}
		_, repoLabels := RecordRepos(s, lane)
		add(ProjectWorktree{
			Path: lane.Text("worktree"), Branch: strings.TrimSpace(lane.Text("branch")),
			Repo: strings.Join(repoLabels, ", "), Source: "lane", Ref: lane.Ref(),
		})
	}
	activeTaskIDs := map[string]bool{}
	for _, task := range scope.Tasks {
		if task.IsArchived() {
			continue
		}
		activeTaskIDs[task.ID] = true
		_, repoLabels := RecordRepos(s, task)
		add(ProjectWorktree{Path: task.Text("defaultDir"), Repo: strings.Join(repoLabels, ", "), Source: "task", Ref: task.Ref()})
	}
	for _, rel := range scope.Relations {
		if rel.Kind != RelDirectory || rel.From.Kind != KindTask || !activeTaskIDs[rel.From.ID] {
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
	return out
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
		rawVal := rec.Text("value")
		val := strings.TrimSpace(rawVal)
		title := strings.TrimSpace(rec.Text("title"))
		kind := strings.TrimSpace(rec.Text("contextKind"))
		if kind == "" {
			kind = ClassifyContextKind(val, "")
		} else if norm := NormalizeContextKind(kind); norm != "" {
			kind = norm
		}
		if kind == "code" && val != "" {
			codeBody := strings.Trim(strings.ReplaceAll(rawVal, "\r\n", "\n"), "\n")
			lang := sanitizeCodeLanguage(rec.Text("language"))
			if strings.Contains(codeBody, "\n") || lang != "" {
				label := title
				if label == "" {
					label = CodeContextTitle(codeBody, lang)
				}
				fmt.Fprintf(&b, "- [code] **%s**\n", label)
				block := FormatCodeMarkdownBlock(codeBody, lang)
				for _, ln := range strings.Split(block, "\n") {
					if ln == "" {
						b.WriteByte('\n')
					} else {
						fmt.Fprintf(&b, "  %s\n", ln)
					}
				}
			} else {
				inline := FormatInlineCodeMarkdown(codeBody)
				if title != "" && title != codeBody {
					fmt.Fprintf(&b, "- [code] **%s**: %s\n", title, inline)
				} else {
					fmt.Fprintf(&b, "- [code] %s\n", inline)
				}
			}
		} else {
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
