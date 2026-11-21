// Package data is Matomeru v2's pure Data API: schemas, records, relations,
// immutable change sets and a SQLite store.
//
// data imports only the standard library, modernc.org/sqlite and
// ofam.dev/ochazuke/core (for Ref, ParamSpec and Actor). It never imports a UI
// package.
package data

import (
	"slices"
	"sync"

	"ofam.dev/ochazuke/core"
)

// Universal status columns and their values, present on every record.
const (
	ColArchiveStatus = "archiveStatus"
	ColStarStatus    = "starStatus"
	// ColSnoozedUntil is the universal snooze column: a time (TimeFormat) or
	// nothing. It is written like a status, so snoozing never moves UpdatedAt.
	ColSnoozedUntil = "snoozedUntil"

	ArchiveActive   = "active"
	ArchiveArchived = "archived"
	StarUnstarred   = "unstarred"
	StarStarred     = "starred"
)

// Relation kinds any record can be on the end of.
const (
	RelPinned    = "pinned"
	RelMarked    = "marked"
	RelTag       = "tag"
	RelAbout     = "about"
	RelDirectory = "directory"
	RelContext   = "context"
	RelPage      = "page"
)

// Relation directions used by RelationSpec.Direction.
const (
	DirectionOutgoing = "outgoing"
	DirectionIncoming = "incoming"
	DirectionBoth     = "both"
)

// Record kinds of the core schemas.
const (
	KindTask     = "task"
	KindPane     = "pane"
	KindPage     = "ui.page"
	KindDocument = "document"
	KindNote     = "note"
	KindSchedule = "schedule"
	// KindOccurrence is one due time of a schedule.
	KindOccurrence = "schedule.occurrence"
	KindRun        = "run"
	KindExtension  = "extension"
	KindTag        = "tag"
	KindWorkspace  = "workspace"
)

// StatusValue is one value of a status column.
type StatusValue struct {
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	Tone     string `json:"tone,omitempty"`
	Position int    `json:"position"`
	Starts   bool   `json:"starts,omitempty"`
	Ends     bool   `json:"ends,omitempty"`
	Open     bool   `json:"open,omitempty"`
	Archives bool   `json:"archives,omitempty"`
}

// StatusColumn is a stored field with a finite, declared set of values.
type StatusColumn struct {
	Name    string        `json:"name"`
	Title   string        `json:"title,omitempty"`
	Values  []StatusValue `json:"values"`
	Default string        `json:"default,omitempty"`
}

// Value returns the declared value named name.
func (c StatusColumn) Value(name string) (StatusValue, bool) {
	for _, v := range c.Values {
		if v.Name == name {
			return v, true
		}
	}
	return StatusValue{}, false
}

// Names returns the value names in position order.
func (c StatusColumn) Names() []string {
	vals := slices.Clone(c.Values)
	slices.SortStableFunc(vals, func(a, b StatusValue) int { return a.Position - b.Position })
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = v.Name
	}
	return out
}

// RelationSpec declares a relation kind a noun shows a section for.
type RelationSpec struct {
	Kind        string   `json:"kind"`
	Title       string   `json:"title,omitempty"`
	TargetKinds []string `json:"targetKinds,omitempty"`
	Direction   string   `json:"direction,omitempty"` // outgoing (default), incoming, both
}

// Schema declares one noun.
type Schema struct {
	Kind           string           `json:"kind"`
	Title          string           `json:"title"`
	Plural         string           `json:"plural"`
	Fields         []core.ParamSpec `json:"fields"`
	StatusColumns  []StatusColumn   `json:"statusColumns,omitempty"`
	DefaultColumns []string         `json:"defaultColumns,omitempty"`
	Relations      []RelationSpec   `json:"relations,omitempty"`
	SummaryField   string           `json:"summaryField,omitempty"`

	// UniqueFields names the natural keys of the noun (an extension's name, a
	// page's slug). A record's ID stays opaque and stable; Store.GetByField
	// finds a record by one of these. UniqueScope, when set, names the
	// reference field a value is unique within (a page's slug is unique per
	// pane). A write that repeats a value is not refused: it is recorded as
	// a Conflict of kind ConflictUnique on its change set.
	UniqueFields []string `json:"uniqueFields,omitempty"`
	UniqueScope  string   `json:"uniqueScope,omitempty"`
}

// Field returns the declared field named name.
func (s Schema) Field(name string) (core.ParamSpec, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return core.ParamSpec{}, false
}

// StatusColumn returns the type's own or universal status column named name.
func (s Schema) StatusColumn(name string) (StatusColumn, bool) {
	for _, c := range s.AllStatusColumns() {
		if c.Name == name {
			return c, true
		}
	}
	return StatusColumn{}, false
}

// AllStatusColumns returns the type's own status columns followed by the
// universal ones every record has.
func (s Schema) AllStatusColumns() []StatusColumn {
	return append(slices.Clone(s.StatusColumns), UniversalStatusColumns()...)
}

// UniversalStatusColumns returns the status columns every record has.
func UniversalStatusColumns() []StatusColumn {
	return []StatusColumn{
		{Name: ColStarStatus, Title: "Star", Default: StarUnstarred, Values: []StatusValue{
			{Name: StarUnstarred, Label: "Unstarred", Position: 0, Open: true},
			{Name: StarStarred, Label: "Starred", Tone: "warning", Position: 1, Open: true},
		}},
		{Name: ColArchiveStatus, Title: "Archive", Default: ArchiveActive, Values: []StatusValue{
			{Name: ArchiveActive, Label: "Active", Position: 0, Open: true},
			{Name: ArchiveArchived, Label: "Archived", Tone: "muted", Position: 1, Ends: true, Archives: true},
		}},
	}
}

// Registry holds schemas by kind. It is safe for concurrent use.
type Registry struct {
	mu    sync.RWMutex
	byKey map[string]Schema
	order []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{byKey: map[string]Schema{}} }

// Register adds or replaces schemas.
func (r *Registry) Register(schemas ...Schema) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range schemas {
		if _, ok := r.byKey[s.Kind]; !ok {
			r.order = append(r.order, s.Kind)
		}
		r.byKey[s.Kind] = s
	}
}

// Get returns the schema for kind.
func (r *Registry) Get(kind string) (Schema, bool) {
	if r == nil {
		return Schema{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.byKey[kind]
	return s, ok
}

// List returns every schema in registration order.
func (r *Registry) List() []Schema {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Schema, len(r.order))
	for i, k := range r.order {
		out[i] = r.byKey[k]
	}
	return out
}

func str(name, desc string) core.ParamSpec {
	return core.ParamSpec{Name: name, Type: core.ParamString, Description: desc}
}

func refTo(name, kind, desc string) core.ParamSpec {
	return core.ParamSpec{Name: name, Type: core.ParamRef, RefKinds: []string{kind}, Description: desc}
}

// statusField is the field behind a status column: its enum and default come
// from the column, so the two never drift.
func statusField(col StatusColumn, desc string) core.ParamSpec {
	f := core.ParamSpec{Name: col.Name, Type: core.ParamString, Enum: col.Names(), Description: desc}
	if col.Default != "" {
		f.Default = col.Default
	}
	return f
}

// universalRelations are the relation sections every core noun shows.
func universalRelations(extra ...RelationSpec) []RelationSpec {
	return append(extra,
		RelationSpec{Kind: RelAbout, Title: "Notes", Direction: DirectionIncoming},
		RelationSpec{Kind: RelContext, Title: "Context", Direction: DirectionOutgoing},
		RelationSpec{Kind: RelTag, Title: "Tags", TargetKinds: []string{KindTag}, Direction: DirectionOutgoing},
	)
}

// DefaultSchemas returns a registry holding Matomeru v2's core schemas.
func DefaultSchemas() *Registry {
	work := StatusColumn{Name: "workStatus", Title: "Status", Default: "todo", Values: []StatusValue{
		{Name: "todo", Label: "Todo", Tone: "muted", Position: 0, Open: true},
		{Name: "active", Label: "Active", Tone: "state.working", Position: 1, Starts: true, Open: true},
		{Name: "blocked", Label: "Blocked", Tone: "warning", Position: 2, Open: true},
		{Name: "done", Label: "Done", Tone: "state.done", Position: 3, Ends: true},
	}}
	paneRun := StatusColumn{Name: "runStatus", Title: "State", Default: "unknown", Values: []StatusValue{
		{Name: "running", Label: "Running", Tone: "state.working", Position: 0, Starts: true, Open: true},
		{Name: "waiting", Label: "Waiting", Tone: "warning", Position: 1, Open: true},
		{Name: "exited", Label: "Exited", Tone: "muted", Position: 2, Ends: true},
		{Name: "unknown", Label: "Unknown", Tone: "dim", Position: 3, Open: true},
	}}
	jobRun := StatusColumn{Name: "runStatus", Title: "State", Default: "queued", Values: []StatusValue{
		{Name: "queued", Label: "Queued", Tone: "muted", Position: 0, Open: true},
		{Name: "running", Label: "Running", Tone: "state.working", Position: 1, Starts: true, Open: true},
		{Name: "done", Label: "Done", Tone: "state.done", Position: 2, Ends: true},
		{Name: "failed", Label: "Failed", Tone: "state.errored", Position: 3, Ends: true},
		{Name: "cancelled", Label: "Cancelled", Tone: "muted", Position: 4, Ends: true},
	}}
	extRun := StatusColumn{Name: "runStatus", Title: "Health", Default: "offline", Values: []StatusValue{
		{Name: "healthy", Label: "Healthy", Tone: "state.done", Position: 0, Open: true},
		{Name: "degraded", Label: "Degraded", Tone: "warning", Position: 1, Open: true},
		{Name: "offline", Label: "Offline", Tone: "muted", Position: 2, Ends: true},
	}}
	outcome := StatusColumn{Name: "outcomeStatus", Title: "Outcome", Default: "pending", Values: []StatusValue{
		{Name: "pending", Label: "Pending", Tone: "warning", Position: 0, Open: true},
		{Name: "done", Label: "Done", Tone: "state.done", Position: 1, Ends: true},
		{Name: "skipped", Label: "Skipped", Tone: "muted", Position: 2, Ends: true},
		{Name: "failed", Label: "Failed", Tone: "state.errored", Position: 3, Ends: true},
	}}

	r := NewRegistry()
	r.Register(
		Schema{
			Kind: KindTask, Title: "Task", Plural: "Tasks", SummaryField: "title",
			Fields: []core.ParamSpec{
				{Name: "title", Type: core.ParamString, Required: true, Description: "what to do"},
				str("description", "details"),
				statusField(work, "one status set for every task, root or not"),
				{Name: "priority", Type: core.ParamString, Enum: []string{"P0", "P1", "P2", "P3", "P5"}, Description: "priority"},
				refTo("parent", KindTask, "parent task; empty for a root task, which displays as a project"),
				{Name: "defaultDir", Type: core.ParamPath, Description: "default directory; empty uses the parent's"},
				str("repo", "repository; empty uses the parent's"),
				str("defaultHarness", "default agent harness; empty uses the parent's"),
			},
			StatusColumns:  []StatusColumn{work},
			DefaultColumns: []string{"title", "workStatus", "priority", "parent"},
			Relations: universalRelations(
				RelationSpec{Kind: RelDirectory, Title: "Directories", Direction: DirectionOutgoing},
			),
		},
		Schema{
			Kind: KindPane, Title: "Pane", Plural: "Panes", SummaryField: "title",
			Fields: []core.ParamSpec{
				str("title", "pane title"),
				{Name: "cwd", Type: core.ParamPath, Description: "working directory"},
				{Name: "pid", Type: core.ParamInt, Description: "PID of the current process"},
				str("harness", "agent harness or shell running in the pane"),
				statusField(paneRun, "observed terminal state"),
				str("promptedAt", "when the owner last submitted a prompt (RFC 3339); human prompts only, never agent input"),
				str("lastAgentInputAt", "when an agent last sent input to the pane (RFC 3339)"),
				str("lastAgentBy", "which agent last sent input to the pane"),
				refTo("task", KindTask, "task the pane works on"),
			},
			StatusColumns:  []StatusColumn{paneRun},
			DefaultColumns: []string{"title", "runStatus", "harness", "cwd", "task"},
			Relations:      universalRelations(),
		},
		Schema{
			Kind: KindPage, Title: "Page", Plural: "Pages", SummaryField: "title",
			Fields: []core.ParamSpec{
				{Name: "title", Type: core.ParamString, Required: true, Description: "page title"},
				str("slug", "stable name within the pane's site"),
				{Name: "pageKind", Type: core.ParamString, Default: "custom", Description: "what the page is for",
					Enum: []string{"screen", "questions", "decisions", "plan", "response", "custom"}},
				{Name: "pinned", Type: core.ParamBool, Description: "kept at the top of the table of contents"},
				{Name: "root", Type: core.ParamObject, Description: "the page's ViewNode tree"},
				str("viewedAt", "when the owner last viewed the page (RFC 3339)"),
				refTo("pane", KindPane, "pane whose site holds the page"),
				refTo("task", KindTask, "task the page belongs to"),
			},
			DefaultColumns: []string{"title", "pageKind", "pinned", "pane", "task"},
			Relations:      universalRelations(),
			UniqueFields:   []string{"slug"}, UniqueScope: "pane",
		},
		Schema{
			// A document is the larger, static, usually file-backed text.
			Kind: KindDocument, Title: "Document", Plural: "Documents", SummaryField: "title",
			Fields: []core.ParamSpec{
				str("title", "document title"),
				{Name: "filePath", Type: core.ParamPath, Description: "file on disk"},
				str("summary", "what the document covers"),
				str("body", "Markdown body, when the document is kept inline"),
				refTo("task", KindTask, "task the document belongs to"),
			},
			DefaultColumns: []string{"title", "filePath", "summary", "task"},
			Relations:      universalRelations(),
		},
		Schema{
			// A note is small and changes often. It attaches to any record by
			// an "about" relation from the note to that record.
			Kind: KindNote, Title: "Note", Plural: "Notes", SummaryField: "title",
			Fields: []core.ParamSpec{
				str("title", "note title"),
				str("body", "the note"),
				refTo("task", KindTask, "task the note belongs to"),
			},
			DefaultColumns: []string{"title", "body", "task"},
			Relations: universalRelations(
				RelationSpec{Kind: RelAbout, Title: "About", Direction: DirectionOutgoing},
			),
		},
		Schema{
			// A schedule is a reminder, a command to run or a check, on a cron
			// expression. Reminders are schedules of kind "remind".
			Kind: KindSchedule, Title: "Schedule", Plural: "Schedules", SummaryField: "title",
			Fields: []core.ParamSpec{
				{Name: "title", Type: core.ParamString, Required: true, Description: "what the schedule is for"},
				{Name: "scheduleKind", Type: core.ParamString, Enum: []string{"remind", "run", "check"}, Default: "remind", Description: "remind the owner, run a command, or check something"},
				str("cron", "cron expression"),
				str("command", "command line, for run and check"),
				str("nextDueAt", "when it is next due (RFC 3339)"),
				refTo("task", KindTask, "task the schedule belongs to"),
			},
			DefaultColumns: []string{"title", "scheduleKind", "cron", "nextDueAt", "task"},
			Relations:      universalRelations(),
		},
		Schema{
			Kind: KindOccurrence, Title: "Occurrence", Plural: "Occurrences", SummaryField: "dueAt",
			Fields: []core.ParamSpec{
				refTo("schedule", KindSchedule, "schedule this is one due time of"),
				str("dueAt", "when it was due (RFC 3339)"),
				statusField(outcome, "what came of it"),
				str("checkedAt", "when the outcome was recorded (RFC 3339)"),
			},
			StatusColumns:  []StatusColumn{outcome},
			DefaultColumns: []string{"dueAt", "outcomeStatus", "checkedAt", "schedule"},
			Relations:      universalRelations(),
		},
		Schema{
			Kind: KindRun, Title: "Run", Plural: "Runs", SummaryField: "title",
			Fields: []core.ParamSpec{
				str("title", "label"),
				str("command", "command line"),
				{Name: "cwd", Type: core.ParamPath, Description: "working directory"},
				statusField(jobRun, "observed job state"),
				{Name: "exitCode", Type: core.ParamInt, Description: "exit status"},
				str("output", "captured output"),
				refTo("task", KindTask, "task the run belongs to"),
			},
			StatusColumns:  []StatusColumn{jobRun},
			DefaultColumns: []string{"title", "runStatus", "exitCode", "command", "task"},
			Relations:      universalRelations(),
		},
		Schema{
			Kind: KindExtension, Title: "Extension", Plural: "Extensions", SummaryField: "name",
			Fields: []core.ParamSpec{
				{Name: "name", Type: core.ParamString, Required: true, Description: "extension name"},
				str("binary", "binary that serves the extension"),
				statusField(extRun, "observed health"),
				str("summary", "what the extension adds"),
				{Name: "capabilities", Type: core.ParamList, Description: "what the extension can do (nouns, pages, MCP tools)"},
				{Name: "commands", Type: core.ParamList, Description: "extension-specific commands, such as setup"},
			},
			StatusColumns:  []StatusColumn{extRun},
			DefaultColumns: []string{"name", "runStatus", "binary", "summary"},
			Relations:      universalRelations(),
			UniqueFields:   []string{"name"},
		},
	)
	return r
}
