package data

import (
	"errors"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke/core"
)

// KindClip is one item on the shared clipboard: a piece of text, a path, a
// URL, code or a record reference that the owner and agents hand to each other.
const KindClip = "clip"

// Clip kinds (clipKind): what the content is.
const (
	ClipKindText = "text"
	ClipKindPath = "path"
	ClipKindURL  = "url"
	ClipKindCode = "code"
	ClipKindRef  = "ref"
)

// Clip authors (authorKind): who put the clip on the shared clipboard.
const (
	ClipAuthorOwner = "owner"
	ClipAuthorAgent = "agent"
)

var (
	// ClipKinds lists the valid clipKind values.
	ClipKinds = []string{ClipKindText, ClipKindPath, ClipKindURL, ClipKindCode, ClipKindRef}
	// ClipAuthorKinds lists the valid authorKind values of a clip.
	ClipAuthorKinds = []string{ClipAuthorOwner, ClipAuthorAgent}
)

const fieldCopiedAt = "copiedAt"

func clipSchema() Schema {
	return Schema{
		Kind:         KindClip,
		Title:        "Clip",
		Plural:       "Clips",
		SummaryField: "title",
		Fields: []FieldSpec{
			str("title", "one-line label; derived from content when omitted"),
			strFmt("content", "the text, path, URL, code or reference that is copied", ShowFormatMarkdown),
			{Name: "clipKind", Type: core.ParamString, Enum: ClipKinds, Default: ClipKindText, Description: "text, path, url, code or ref"},
			{Name: "authorKind", Type: core.ParamString, Enum: ClipAuthorKinds, Default: ClipAuthorOwner, Description: "who shared the clip: owner or agent"},
			refTo("fromPane", KindPane, "pane the clip was shared from"),
			refTo("project", KindTask, "project the clip belongs to"),
			str(fieldCopiedAt, "when the clip was last copied to a clipboard (RFC 3339)"),
		},
		DefaultColumns: []string{"title", "clipKind", "authorKind", "fromPane", "project", fieldCopiedAt},
		Relations:      universalRelations(),
		FactFields:     []string{"clipKind", "authorKind"},
		BadgeRules: []BadgeRule{
			{When: `fields.authorKind == "agent"`, Text: "agent", Tone: "accent"},
			{When: `fields.authorKind == "owner"`, Text: "owner", Tone: "muted"},
			{When: `fields.clipKind == "path"`, Text: "path", Tone: "info"},
			{When: `fields.clipKind == "url"`, Text: "url", Tone: "info"},
			{When: `fields.clipKind == "code"`, Text: "code", Tone: "warning"},
			{When: `fields.clipKind == "ref"`, Text: "ref", Tone: "success"},
		},
	}
}

// ClipInput describes one clip to put on the shared clipboard.
type ClipInput struct {
	Title      string `json:"title,omitempty"`
	Content    string `json:"content"`
	Kind       string `json:"clipKind,omitempty"`
	AuthorKind string `json:"authorKind,omitempty"`
	FromPane   string `json:"fromPane,omitempty"`
	Project    string `json:"project,omitempty"`
}

// InferClipKind guesses the clipKind of content: a one-line http(s) URL is a
// url, a one-line absolute or home path is a path, anything else is text.
func InferClipKind(content string) string {
	line := strings.TrimSpace(content)
	if line == "" || strings.ContainsAny(line, "\n\t ") {
		return ClipKindText
	}
	switch {
	case strings.HasPrefix(line, "http://"), strings.HasPrefix(line, "https://"):
		return ClipKindURL
	case strings.HasPrefix(line, "/"), strings.HasPrefix(line, "~/"):
		return ClipKindPath
	}
	return ClipKindText
}

func enumOrDefault(value, fallback, name string, allowed []string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	if !slices.Contains(allowed, value) {
		return "", errors.New("clip: " + name + " must be one of " + strings.Join(allowed, ", "))
	}
	return value, nil
}

// fields validates in and returns the record fields of the clip.
func (in ClipInput) fields() (map[string]any, error) {
	if strings.TrimSpace(in.Content) == "" {
		return nil, errors.New("clip: content is required")
	}
	kind, err := enumOrDefault(in.Kind, InferClipKind(in.Content), "clipKind", ClipKinds)
	if err != nil {
		return nil, err
	}
	author, err := enumOrDefault(in.AuthorKind, ClipAuthorOwner, "authorKind", ClipAuthorKinds)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = MessageSubjectFromBody(in.Content, kind)
	}
	f := map[string]any{
		"title":      title,
		"content":    in.Content,
		"clipKind":   kind,
		"authorKind": author,
	}
	optionalRef(f, "fromPane", KindPane, strings.TrimPrefix(strings.TrimSpace(in.FromPane), KindPane+":"))
	optionalRef(f, "project", KindTask, strings.TrimPrefix(strings.TrimSpace(in.Project), KindTask+":"))
	return f, nil
}

// PutClip records one clip on the shared clipboard. The project is stored as
// its root project; an unknown pane or project is kept as a plain reference,
// so a clip is recorded first and never refused for a missing association.
func (s *Store) PutClip(in ClipInput, actor core.Actor) (*Record, error) {
	if p := strings.TrimPrefix(strings.TrimSpace(in.Project), KindTask+":"); p != "" {
		in.Project = s.RootProjectID(p)
	}
	fields, err := in.fields()
	if err != nil {
		return nil, err
	}
	rec, _, err := s.Create(CreateInput{Kind: KindClip, Fields: fields}, actor)
	return rec, err
}

// ClipFilter selects clips of the shared clipboard. Empty fields match all.
type ClipFilter struct {
	Project    string // project or task ID, walked to its root project
	AuthorKind string
	ClipKind   string
	Limit      int // keep the newest N (0 = all)
}

func (f ClipFilter) keeps(rec Record, project string) bool {
	if f.AuthorKind != "" && rec.Text("authorKind") != f.AuthorKind {
		return false
	}
	if f.ClipKind != "" && rec.Text("clipKind") != f.ClipKind {
		return false
	}
	if project == "" {
		return true
	}
	ref, _ := rec.RefField("project")
	return ref.ID == project
}

// ListClips returns the active clips matching f, newest first.
func (s *Store) ListClips(f ClipFilter) ([]Record, error) {
	res, err := s.Query(Query{Kind: KindClip, SortDesc: true})
	if err != nil {
		return nil, err
	}
	f.AuthorKind, f.ClipKind = strings.TrimSpace(f.AuthorKind), strings.TrimSpace(f.ClipKind)
	project := strings.TrimPrefix(strings.TrimSpace(f.Project), KindTask+":")
	if project != "" {
		project = s.RootProjectID(project)
	}
	out := make([]Record, 0, len(res.Items))
	for _, rec := range res.Items {
		if !f.keeps(rec, project) {
			continue
		}
		out = append(out, rec)
		if f.Limit > 0 && len(out) == f.Limit {
			break
		}
	}
	return out, nil
}

// GetClip returns the clip with the given ID (a "clip:" prefix is accepted).
func (s *Store) GetClip(id string) (*Record, error) {
	id = strings.TrimPrefix(strings.TrimSpace(id), KindClip+":")
	if id == "" {
		return nil, errors.New("clip: id is required")
	}
	rec, err := s.Get(core.Ref{Kind: KindClip, ID: id})
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, errors.New("clip: no clip " + id)
	}
	return rec, nil
}

// MarkClipCopied stamps copiedAt on clip id and returns the updated record.
func (s *Store) MarkClipCopied(id string, at time.Time, actor core.Actor) (*Record, error) {
	rec, err := s.GetClip(id)
	if err != nil {
		return nil, err
	}
	out, _, err := s.Patch(PatchInput{
		Ref:    rec.Ref(),
		Fields: map[string]any{fieldCopiedAt: at.UTC().Format(TimeFormat)},
	}, actor)
	return out, err
}
