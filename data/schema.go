// Package data is Matomeru v2's pure Data API: schemas, records, relations,
// immutable change sets and a SQLite store.
//
// data imports only the standard library, modernc.org/sqlite and
// ofam.dev/ochazuke/core (for Ref, ParamSpec and Actor). It never imports a UI
// package.
package data

import (
	"slices"
	"strings"
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
	RelRelated   = "related"
	RelDirectory = "directory"
	RelContext   = "context"
	RelPage      = "page"
	RelRepo      = "repo"
	// Lane relations: the panes working a lane, its plan, what it made and ran.
	RelClaim   = "claim"
	RelPlan    = "plan"
	RelCreated = "created"
	RelRun     = "run"
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
	KindLane     = "lane"
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
	// KindSignal is one event from outside (a notification, a mention, a CI
	// result) waiting to be triaged; KindTriageRule is a rule that triages them.
	KindSignal     = "signal"
	KindTriageRule = "triage.rule"
	// First-class daily workflow nouns: questions asked by agents, suggested
	// commands to run, and harvested/explicit context items (URLs, PRs, files).
	KindQuestion = "question"
	KindCommand  = "command"
	KindContext  = "context"
	KindConcept  = "concept"
	// KindQueue is a named line of agent work that tasks and lanes join and
	// agent panes watch.
	KindQueue = "queue"
	// KindRepo is a tracked git repository linked to tasks and lanes via RelRepo.
	KindRepo = "repo"
	// KindCapture is a persistent dictation or composed prompt capture record.
	KindCapture = "capture"
)

// Pane roles. A pane with no role is a normal one.
const (
	RoleNormal      = "normal"
	RoleScratchTerm = "scratch-term" // the one global scratch terminal
	RoleScratchVim  = "scratch-vim"  // the one global scratch editor
	RoleSideTerm    = "side-term"    // a terminal opened beside parentPane
	RoleSideVim     = "side-vim"     // an editor opened beside parentPane
)

// PaneRoles lists the pane roles.
var PaneRoles = []string{RoleNormal, RoleScratchTerm, RoleScratchVim, RoleSideTerm, RoleSideVim}

// Triage states of a signal and actions of a triage rule.
const (
	TriageUnreviewed = "unreviewed"
	TriageActionable = "actionable"
	TriageNoise      = "noise"
	TriageDone       = "done"

	ActionMarkNoise      = "mark_noise"
	ActionMarkActionable = "mark_actionable"
	ActionSnooze         = "snooze"
	ActionTag            = "tag"
	ActionRelateTask     = "relate_task"
)

// TriageActions lists what a triage rule can do to a signal it matches.
var TriageActions = []string{ActionMarkNoise, ActionMarkActionable, ActionSnooze, ActionTag, ActionRelateTask}

// ShowFormat values declare how a Show page renders a field's full content.
const (
	ShowFormatMarkdown = "markdown"
	ShowFormatCode     = "code"
	ShowFormatText     = "text"
	ShowFormatList     = "list"
	ShowFormatViewNode = "viewNode"
)

// FieldRole values declare a field's structural role on Show pages, lists, and
// edit forms so presenters never branch on noun kinds or hardcoded field names.
const (
	FieldRoleTitle  = "title"
	FieldRoleProp   = "prop"
	FieldRoleBody   = "body"
	FieldRoleHidden = "hidden"
)

// FieldSpec declares one field of a Schema. Its flat fields mirror
// core.ParamSpec and carry declarative schema metadata such as
// ShowIncludeArchived, ShowFormat, Role, and ContentFile so views never branch
// on noun kinds or hardcoded field names.
type FieldSpec struct {
	Name                string         `json:"name"`
	Type                core.ParamType `json:"type"`
	Role                string         `json:"role,omitempty"`
	Required            bool           `json:"required,omitempty"`
	Default             any            `json:"default,omitempty"`
	Enum                []string       `json:"enum,omitempty"`
	RefKinds            []string       `json:"refKinds,omitempty"`
	SourceQuery         string         `json:"sourceQuery,omitempty"`
	Description         string         `json:"description,omitempty"`
	ShowIncludeArchived bool           `json:"showIncludeArchived,omitempty"`
	ShowFormat          string         `json:"showFormat,omitempty"`
	ContentFile         bool           `json:"contentFile,omitempty"`
	VisibleWhen         string         `json:"visibleWhen,omitempty"`
	Repeatable          bool           `json:"repeatable,omitempty"`
	AddLabel            string         `json:"addLabel,omitempty"`
	FormHidden          bool           `json:"formHidden,omitempty"`
}

// EffectiveRole returns the field's declared role, falling back cleanly from
// existing declarative schema properties (SummaryField, ShowFormat, ContentFile)
// so current catalogs work without manual edits.
func (f FieldSpec) EffectiveRole(summaryField string) string {
	if f.Role != "" {
		return f.Role
	}
	if summaryField != "" && f.Name == summaryField {
		return FieldRoleTitle
	}
	if f.ShowFormat != "" || f.ContentFile {
		return FieldRoleBody
	}
	return FieldRoleProp
}

// ParamSpec converts f into its equivalent core.ParamSpec for forms and CLI flags.
func (f FieldSpec) ParamSpec() core.ParamSpec {
	return core.ParamSpec{
		Name:        f.Name,
		Type:        f.Type,
		Required:    f.Required,
		Default:     f.Default,
		Enum:        slices.Clone(f.Enum),
		RefKinds:    slices.Clone(f.RefKinds),
		SourceQuery: f.SourceQuery,
		Description: f.Description,
		VisibleWhen: f.VisibleWhen,
		Repeatable:  f.Repeatable,
		AddLabel:    f.AddLabel,
	}
}

// HierarchySpec declares how records of a noun nest into a tree.
type HierarchySpec struct {
	ParentField string `json:"parentField"`
	OrderField  string `json:"orderField,omitempty"`
	RootTitle   string `json:"rootTitle,omitempty"`
	DefaultView string `json:"defaultView,omitempty"` // "tree" or "table"
}

// StyleRule declaratively assigns row or cell tones/icons when an expression holds.
type StyleRule struct {
	When     string `json:"when"`
	Target   string `json:"target"` // "row" or a field/column name
	Tone     string `json:"tone,omitempty"`
	Icon     string `json:"icon,omitempty"`
	IconTone string `json:"iconTone,omitempty"`
}

// BadgeRule declaratively attaches a badge chip when an expression holds.
type BadgeRule struct {
	When string `json:"when"`
	Text string `json:"text"`
	Tone string `json:"tone,omitempty"`
}

// GutterRule declaratively assigns a 1-character left gutter glyph and tone
// when an expression holds. The first matching rule wins.
type GutterRule struct {
	When  string `json:"when"`
	Glyph string `json:"glyph"`
	Tone  string `json:"tone,omitempty"`
}

// StatusValue is one value of a status column.
type StatusValue struct {
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	Tone     string `json:"tone,omitempty"`
	RowTone  string `json:"rowTone,omitempty"`
	Icon     string `json:"icon,omitempty"`
	IconTone string `json:"iconTone,omitempty"`
	Position int    `json:"position"`
	Starts   bool   `json:"starts,omitempty"`
	Ends     bool   `json:"ends,omitempty"`
	Open     bool   `json:"open,omitempty"`
	Archives bool   `json:"archives,omitempty"`
}

// StatusColumn is a stored field with a finite, declared set of values.
type StatusColumn struct {
	Name            string        `json:"name"`
	Title           string        `json:"title,omitempty"`
	Values          []StatusValue `json:"values"`
	Default         string        `json:"default,omitempty"`
	PrimaryRowStyle bool          `json:"primaryRowStyle,omitempty"`
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
	Kind            string   `json:"kind"`
	Title           string   `json:"title,omitempty"`
	TargetKinds     []string `json:"targetKinds,omitempty"`
	Direction       string   `json:"direction,omitempty"` // outgoing (default), incoming, both
	InForm          bool     `json:"inForm,omitempty"`
	FormField       string   `json:"formField,omitempty"`
	FormAddLabel    string   `json:"formAddLabel,omitempty"`
	FormDescription string   `json:"formDescription,omitempty"`
}

// Schema declares one noun.
type Schema struct {
	Kind           string         `json:"kind"`
	Title          string         `json:"title"`
	Plural         string         `json:"plural"`
	Fields         []FieldSpec    `json:"fields"`
	StatusColumns  []StatusColumn `json:"statusColumns,omitempty"`
	DefaultColumns []string       `json:"defaultColumns,omitempty"`
	Relations      []RelationSpec `json:"relations,omitempty"`
	SummaryField   string         `json:"summaryField,omitempty"`

	// UniqueFields names the natural keys of the noun (an extension's name, a
	// page's slug). A record's ID stays opaque and stable; Store.GetByField
	// finds a record by one of these. UniqueScope, when set, names the
	// reference field a value is unique within (a page's slug is unique per
	// pane). A write that repeats a value is not refused: it is recorded as
	// a Conflict of kind ConflictUnique on its change set.
	UniqueFields []string `json:"uniqueFields,omitempty"`
	UniqueScope  string   `json:"uniqueScope,omitempty"`

	// Declarative presenter and hierarchy metadata.
	ShowTimeline bool           `json:"showTimeline,omitempty"`
	UnreadField  string         `json:"unreadField,omitempty"`
	FactFields   []string       `json:"factFields,omitempty"`
	BadgeRules   []BadgeRule    `json:"badgeRules,omitempty"`
	GutterRules  []GutterRule   `json:"gutterRules,omitempty"`
	StyleRules   []StyleRule    `json:"styleRules,omitempty"`
	Hierarchy    *HierarchySpec `json:"hierarchy,omitempty"`

	// Presentations declares the noun's own presentation forms (chip,
	// inline, row, card, detail), as an extension catalog does; the
	// PresentationRegistry lays the embedded and user files over them.
	Presentations map[string]PresentationSpec `json:"presentations,omitempty"`
}

// Field returns the declared field named name.
func (s Schema) Field(name string) (FieldSpec, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return FieldSpec{}, false
}

// ParamSpecs returns the schema's fields converted to core.ParamSpec values.
func (s Schema) ParamSpecs() []core.ParamSpec {
	out := make([]core.ParamSpec, len(s.Fields))
	for i, f := range s.Fields {
		out[i] = f.ParamSpec()
	}
	return out
}

// FormRelations returns the outgoing relations declared for inclusion in record forms.
func (s Schema) FormRelations() []RelationSpec {
	var out []RelationSpec
	for _, rel := range s.Relations {
		if !rel.InForm || rel.FormField == "" {
			continue
		}
		if rel.Direction != "" && rel.Direction != DirectionOutgoing && rel.Direction != DirectionBoth {
			continue
		}
		out = append(out, rel)
	}
	return out
}

// EffectiveHierarchy returns the schema's explicit HierarchySpec, or infers one
// when the schema declares a self-referencing ParamRef field.
func (s Schema) EffectiveHierarchy() *HierarchySpec {
	if s.Hierarchy != nil {
		cp := *s.Hierarchy
		return &cp
	}
	if s.Kind == "" {
		return nil
	}
	for _, f := range s.Fields {
		if f.Type == core.ParamRef && slices.Contains(f.RefKinds, s.Kind) {
			return &HierarchySpec{
				ParentField: f.Name,
				DefaultView: "tree",
			}
		}
	}
	return nil
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

// PrimaryStatusField returns the schema's primary cyclable status field name
// and ordered enum values, derived declaratively from schema metadata without
// per-noun special cases. Declared StatusColumns are checked first (preferring
// PrimaryRowStyle), followed by the first declared enum field named "status" or
// ending with "Status" (excluding universal archiveStatus and starStatus).
func (s Schema) PrimaryStatusField() (string, []string, bool) {
	for _, c := range s.StatusColumns {
		if c.PrimaryRowStyle && len(c.Values) > 0 && c.Name != ColArchiveStatus && c.Name != ColStarStatus {
			return c.Name, c.Names(), true
		}
	}
	for _, c := range s.StatusColumns {
		if len(c.Values) > 0 && c.Name != ColArchiveStatus && c.Name != ColStarStatus {
			return c.Name, c.Names(), true
		}
	}
	for _, f := range s.Fields {
		if len(f.Enum) > 0 && f.Name != ColArchiveStatus && f.Name != ColStarStatus &&
			(f.Name == "status" || strings.HasSuffix(f.Name, "Status")) {
			return f.Name, slices.Clone(f.Enum), true
		}
	}
	return "", nil, false
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

func str(name, desc string) FieldSpec {
	return FieldSpec{Name: name, Type: core.ParamString, Description: desc}
}

func strFmt(name, desc, format string) FieldSpec {
	return FieldSpec{Name: name, Type: core.ParamString, Description: desc, ShowFormat: format}
}

func refTo(name, kind, desc string) FieldSpec {
	return FieldSpec{Name: name, Type: core.ParamRef, RefKinds: []string{kind}, Description: desc}
}

// statusField is the field behind a status column: its enum and default come
// from the column, so the two never drift.
func statusField(col StatusColumn, desc string) FieldSpec {
	f := FieldSpec{Name: col.Name, Type: core.ParamString, Enum: col.Names(), Description: desc}
	if col.Default != "" {
		f.Default = col.Default
	}
	return f
}

// universalRelations are the relation sections every core noun shows.
func universalRelations(extra ...RelationSpec) []RelationSpec {
	return append(extra,
		RelationSpec{Kind: RelAbout, Title: "Notes", TargetKinds: []string{KindNote}, Direction: DirectionIncoming},
		RelationSpec{Kind: RelContext, Title: "Context", Direction: DirectionOutgoing},
		RelationSpec{Kind: RelTag, Title: "Tags", TargetKinds: []string{KindTag}, Direction: DirectionOutgoing},
	)
}

// DefaultSchemas returns a registry holding Matomeru v2's core schemas.
func DefaultSchemas() *Registry {
	work := StatusColumn{Name: "workStatus", Title: "Status", Default: "todo", PrimaryRowStyle: true, Values: []StatusValue{
		{Name: "todo", Label: "Todo", Tone: "state.planned", RowTone: "row.muted", Icon: "○", IconTone: "state.planned", Position: 0, Open: true},
		{Name: "active", Label: "Active", Tone: "state.working", RowTone: "row.active", Icon: "▶", IconTone: "state.working", Position: 1, Starts: true, Open: true},
		{Name: "blocked", Label: "Blocked", Tone: "state.blocked", RowTone: "row.attention", Icon: "!", IconTone: "state.blocked", Position: 2, Open: true},
		{Name: "done", Label: "Done", Tone: "state.done.dim", RowTone: "row.dim", Icon: "✓", IconTone: "state.done.dim", Position: 3, Ends: true},
	}}
	agentStatus := StatusColumn{Name: "agentStatus", Title: "Agent", Default: AgentStatusNone, PrimaryRowStyle: false, Values: []StatusValue{
		{Name: AgentStatusNone, Label: "None", Position: 0},
		{Name: AgentStatusQueued, Label: "Queued", Tone: "accent", Icon: "◆", Position: 1, Open: true},
		{Name: AgentStatusWorking, Label: "Working", Tone: "state.working", Icon: "▶", Position: 2, Starts: true, Open: true},
		{Name: AgentStatusNeedsYou, Label: "Needs you", Tone: "state.needs-input", Icon: "?", Position: 3, Open: true},
		{Name: AgentStatusDone, Label: "Done", Tone: "state.done.dim", Icon: "✓", Position: 4, Ends: true},
	}}
	paneRun := StatusColumn{Name: "runStatus", Title: "State", Default: "unknown", PrimaryRowStyle: true, Values: []StatusValue{
		{Name: "needs_you", Label: "Needs you", Tone: "state.needs-input", RowTone: "row.attention", Icon: "◆", IconTone: "state.needs-input", Position: 0, Open: true},
		{Name: "running", Label: "Running", Tone: "state.working", RowTone: "row.active", Icon: "▶", IconTone: "state.working", Position: 1, Starts: true, Open: true},
		{Name: "waiting", Label: "Waiting", Tone: "warning", RowTone: "row.attention", Icon: "◔", IconTone: "warning", Position: 2, Open: true},
		{Name: "idle", Label: "Idle", Tone: "muted", RowTone: "row.muted", Icon: "○", IconTone: "muted", Position: 3, Open: true},
		{Name: "done", Label: "Done", Tone: "state.done.dim", RowTone: "row.dim", Icon: "✓", IconTone: "state.done.dim", Position: 4, Ends: true},
		{Name: "dead", Label: "Dead", Tone: "state.errored", RowTone: "row.error", Icon: "×", IconTone: "state.errored", Position: 5, Ends: true},
		{Name: "exited", Label: "Exited", Tone: "muted", RowTone: "row.dim", Icon: "×", IconTone: "muted", Position: 6, Ends: true},
		{Name: "unknown", Label: "Unknown", Tone: "dim", RowTone: "row.muted", Icon: "·", IconTone: "dim", Position: 7, Open: true},
	}}
	jobRun := StatusColumn{Name: "runStatus", Title: "State", Default: "queued", PrimaryRowStyle: true, Values: []StatusValue{
		{Name: "queued", Label: "Queued", Tone: "state.planned", RowTone: "row.muted", Icon: "○", IconTone: "state.planned", Position: 0, Open: true},
		{Name: "running", Label: "Running", Tone: "state.working", RowTone: "row.active", Icon: "▶", IconTone: "state.working", Position: 1, Starts: true, Open: true},
		{Name: "done", Label: "Done", Tone: "state.done.dim", RowTone: "row.dim", Icon: "✓", IconTone: "state.done.dim", Position: 2, Ends: true},
		{Name: "failed", Label: "Failed", Tone: "state.errored", RowTone: "row.error", Icon: "✕", IconTone: "state.errored", Position: 3, Ends: true},
		{Name: "cancelled", Label: "Cancelled", Tone: "state.cancelled", RowTone: "row.cancelled", Icon: "✕", IconTone: "state.cancelled", Position: 4, Ends: true},
	}}
	extRun := StatusColumn{Name: "runStatus", Title: "Health", Default: "offline", PrimaryRowStyle: true, Values: []StatusValue{
		{Name: "healthy", Label: "Healthy", Tone: "state.done", RowTone: "row.active", Icon: "●", IconTone: "state.done", Position: 0, Open: true},
		{Name: "degraded", Label: "Degraded", Tone: "warning", RowTone: "row.attention", Icon: "◐", IconTone: "warning", Position: 1, Open: true},
		{Name: "offline", Label: "Offline", Tone: "muted", RowTone: "row.muted", Icon: "○", IconTone: "muted", Position: 2, Ends: true},
		{Name: "available", Label: "Available", Tone: "accent", RowTone: "row.muted", Icon: "↓", IconTone: "accent", Position: 3, Open: true},
	}}
	outcome := StatusColumn{Name: "outcomeStatus", Title: "Outcome", Default: "pending", PrimaryRowStyle: true, Values: []StatusValue{
		{Name: "pending", Label: "Pending", Tone: "warning", RowTone: "row.attention", Icon: "○", IconTone: "warning", Position: 0, Open: true},
		{Name: "done", Label: "Done", Tone: "state.done.dim", RowTone: "row.dim", Icon: "✓", IconTone: "state.done.dim", Position: 1, Ends: true},
		{Name: "skipped", Label: "Skipped", Tone: "muted", RowTone: "row.muted", Icon: "–", IconTone: "muted", Position: 2, Ends: true},
		{Name: "failed", Label: "Failed", Tone: "state.errored", RowTone: "row.error", Icon: "✕", IconTone: "state.errored", Position: 3, Ends: true},
	}}

	laneStatus := laneStatusColumn()

	triage := StatusColumn{Name: "triageState", Title: "Triage", Default: TriageUnreviewed, PrimaryRowStyle: true, Values: []StatusValue{
		{Name: TriageUnreviewed, Label: "Unreviewed", Tone: "warning", RowTone: "row.attention", Icon: "●", IconTone: "warning", Position: 0, Open: true},
		{Name: TriageActionable, Label: "Actionable", Tone: "state.working", RowTone: "row.active", Icon: "▶", IconTone: "state.working", Position: 1, Starts: true, Open: true},
		{Name: TriageNoise, Label: "Noise", Tone: "muted", RowTone: "row.dim", Icon: "·", IconTone: "muted", Position: 2, Ends: true},
		{Name: TriageDone, Label: "Done", Tone: "state.done.dim", RowTone: "row.dim", Icon: "✓", IconTone: "state.done.dim", Position: 3, Ends: true},
	}}

	questionStatus := StatusColumn{Name: "status", Title: "Status", Default: "open", PrimaryRowStyle: true, Values: []StatusValue{
		{Name: "open", Label: "Open", Tone: "state.needs-input", RowTone: "row.attention", Icon: "?", IconTone: "state.needs-input", Position: 0, Starts: true, Open: true},
		{Name: "answered", Label: "Answered", Tone: "state.done.dim", RowTone: "row.dim", Icon: "✓", IconTone: "state.done.dim", Position: 1, Ends: true},
		{Name: "dismissed", Label: "Dismissed", Tone: "muted", RowTone: "row.muted", Icon: "✕", IconTone: "muted", Position: 2, Ends: true},
	}}

	commandStatus := StatusColumn{Name: "status", Title: "Status", Default: "pending", PrimaryRowStyle: true, Values: []StatusValue{
		{Name: "pending", Label: "Pending", Tone: "warning", RowTone: "row.attention", Icon: "▶", IconTone: "warning", Position: 0, Starts: true, Open: true},
		{Name: "ran", Label: "Ran", Tone: "state.done.dim", RowTone: "row.dim", Icon: "✓", IconTone: "state.done.dim", Position: 1, Ends: true},
		{Name: "dismissed", Label: "Dismissed", Tone: "muted", RowTone: "row.muted", Icon: "✕", IconTone: "muted", Position: 2, Ends: true},
	}}

	r := NewRegistry()
	r.Register(
		Schema{
			Kind: KindTask, Title: "Task", Plural: "Tasks", SummaryField: "title",
			Fields: []FieldSpec{
				{Name: "title", Type: core.ParamString, Required: true, Description: "what to do"},
				strFmt("description", "details", ShowFormatMarkdown),
				statusField(work, "one status set for every task, root or not"),
				{Name: "priority", Type: core.ParamString, Enum: []string{"P0", "P1", "P2", "P3", "P5"}, Description: "priority"},
				refTo("parent", KindTask, "parent task; empty for a root task, which displays as a project"),
				{Name: "defaultDir", Type: core.ParamPath, Description: "default directory; empty uses the parent's"},
				{Name: "repo", Type: core.ParamString, FormHidden: true, Description: "repository; empty uses the parent's"},
				str("defaultHarness", "default agent harness; empty uses the parent's"),
				str("projectTheme", "optional theme override for this project portal"),
				{Name: "defaultContextMd", Type: core.ParamString, Role: FieldRoleBody, ShowFormat: ShowFormatMarkdown, Description: "editable default context Markdown prose auto-injected into agent panes for this project"},
				{Name: "order", Type: core.ParamInt, Description: "sibling sort order in the task outline"},
				{Name: "forAgent", Type: core.ParamBool, Default: false, Description: "flagged for agent queue pickup"},
				statusField(agentStatus, "agent workflow state for this task"),
				str("agentAssignee", "agent harness or worker assigned to this task"),
				str("agentDispatchedAt", "when the queued task was last dispatched to an agent pane (RFC 3339)"),
				refTo("agentDispatchedPane", KindPane, "agent pane the task was last dispatched to"),
				{Name: "attachments", Type: core.ParamList, ShowFormat: ShowFormatList, Description: "attached files, images, URLs, or context references"},
				{Name: "threadLocked", Type: core.ParamBool, Default: false, Description: "prevent automatic agent replies in this task's note thread"},
				str("readAt", "when the owner read the task (RFC 3339)"),
				{Name: FieldTaskType, Type: core.ParamString, Enum: []string{TaskTypeHuman, TaskTypeRewrite, TaskTypeJoin, TaskTypeSplit}, Default: TaskTypeHuman, Description: "how the task originated"},
				queueNameField(),
				queueRefField(),
			},
			StatusColumns:  []StatusColumn{work, agentStatus},
			DefaultColumns: []string{"title", "workStatus", "priority", "parent"},
			Relations: universalRelations(
				RelationSpec{Kind: RelRepo, Title: "Repos", TargetKinds: []string{KindRepo}, Direction: DirectionOutgoing, InForm: true, FormField: "repos", FormAddLabel: "repo", FormDescription: "linked repositories"},
				RelationSpec{Kind: RelDirectory, Title: "Directories", Direction: DirectionOutgoing},
				RelationSpec{Kind: RelDerivedFrom, Title: "Rewrites", TargetKinds: []string{KindTask}, Direction: DirectionBoth},
				RelationSpec{Kind: RelSplitFrom, Title: "Splits", TargetKinds: []string{KindTask}, Direction: DirectionBoth},
				RelationSpec{Kind: RelJoinedFrom, Title: "Joins", TargetKinds: []string{KindTask}, Direction: DirectionBoth},
			),
			Hierarchy:  &HierarchySpec{ParentField: "parent", OrderField: "order", RootTitle: "Project", DefaultView: "tree"},
			FactFields: []string{"forAgent", "agentStatus", "agentDispatchedAt", "threadLocked"},
			BadgeRules: []BadgeRule{
				{When: `fields.agentStatus == "queued"`, Text: "agent:queued", Tone: "accent"},
				{When: `fields.agentStatus == "working"`, Text: "agent:working", Tone: "state.working"},
				{When: `fields.agentStatus == "needs_you"`, Text: "agent:needs you", Tone: "state.needs-input"},
				{When: `fields.agentStatus == "done"`, Text: "agent:done", Tone: "state.done.dim"},
			},
			GutterRules: []GutterRule{
				{When: `fields.agentStatus == "needs_you"`, Glyph: "?", Tone: "state.needs-input"},
				{When: `fields.agentStatus == "working"`, Glyph: "●", Tone: "state.working"},
				{When: `fields.agentStatus == "done"`, Glyph: "✓", Tone: "state.done"},
				{When: `fields.agentStatus == "queued" && fields.agentDispatchedAt`, Glyph: "↗", Tone: "accent"},
				{When: `fields.agentStatus == "queued" && !fields.agentDispatchedAt`, Glyph: "◇", Tone: "muted"},
			},
			StyleRules: []StyleRule{
				{When: `fields.priority == "P0"`, Target: "priority", Tone: "error"},
				{When: `fields.priority == "P1"`, Target: "priority", Tone: "warning"},
			},
		},
		Schema{
			// A lane is one piece of work an agent does: what it is for, where
			// it happens (repo, branch, worktree) and how far it got. Lanes nest
			// by parentLane; any number of panes may claim one.
			Kind: KindLane, Title: "Lane", Plural: "Lanes", SummaryField: "title",
			Fields: []FieldSpec{
				{Name: "title", Type: core.ParamString, Required: true, Description: "what the lane is for"},
				strFmt("ownerWords", "the owner's own wording of what this lane is for and why it matters", ShowFormatMarkdown),
				strFmt("description", "detailed context, scope, and motivation", ShowFormatMarkdown),
				statusField(laneStatus, "where the work stands"),
				strFmt("summary", "what happened so far", ShowFormatMarkdown),
				refTo("task", KindTask, "task the lane works on"),
				refTo("parentLane", KindLane, "lane this is a part of; empty for a root lane"),
				{Name: "repo", Type: core.ParamString, FormHidden: true, Description: "repository"},
				str("branch", "branch the work is on"),
				{Name: "worktree", Type: core.ParamPath, Description: "worktree directory"},
				str("baseCommit", "commit the work started from"),
				str("headCommit", "newest commit of the work"),
				str("harness", "agent harness doing the work"),
				str("agentModel", "model the agent runs"),
				queueNameField(),
				queueRefField(),
			},
			StatusColumns:  []StatusColumn{laneStatus},
			DefaultColumns: []string{"title", "laneStatus", "branch", "task", "parentLane"},
			Relations: universalRelations(
				RelationSpec{Kind: RelRepo, Title: "Repos", TargetKinds: []string{KindRepo}, Direction: DirectionOutgoing, InForm: true, FormField: "repos", FormAddLabel: "repo", FormDescription: "linked repositories"},
				RelationSpec{Kind: RelClaim, Title: "Claimed by", TargetKinds: []string{KindPane}, Direction: DirectionOutgoing},
				RelationSpec{Kind: RelPlan, Title: "Plan", TargetKinds: []string{KindDocument}, Direction: DirectionOutgoing},
				RelationSpec{Kind: RelCreated, Title: "Created", TargetKinds: []string{KindDocument, KindPage, KindQuestion, KindCommand, KindContext}, Direction: DirectionOutgoing},
				RelationSpec{Kind: RelRun, Title: "Runs", TargetKinds: []string{KindRun}, Direction: DirectionOutgoing},
			),
			ShowTimeline: true,
			Hierarchy:    &HierarchySpec{ParentField: "parentLane", DefaultView: "tree"},
		},
		Schema{
			Kind: KindPane, Title: "Pane", Plural: "Panes", SummaryField: "title",
			Fields: []FieldSpec{
				str("title", "pane title"),
				{Name: "cwd", Type: core.ParamPath, Description: "working directory"},
				{Name: "pid", Type: core.ParamInt, Description: "PID of the current process"},
				str("harness", "agent harness or shell running in the pane"),
				statusField(paneRun, "observed terminal state"),
				str("promptedAt", "when the owner last submitted a prompt (RFC 3339); human prompts only, never agent input"),
				str("lastAgentInputAt", "when an agent last sent input to the pane (RFC 3339)"),
				str("lastAgentBy", "which agent last sent input to the pane"),
				{Name: "task", Type: core.ParamRef, RefKinds: []string{KindTask}, Description: "task the pane works on", ShowIncludeArchived: true},
				strFmt("command", "command line a live session of the pane runs; empty runs $SHELL", ShowFormatCode),
				{Name: "role", Type: core.ParamString, Enum: PaneRoles, Description: "what the pane is for: a normal pane, a global scratch, or a side pane of parentPane; empty is normal"},
				refTo("parentPane", KindPane, "pane a side pane was opened beside"),
				str("transcriptPath", "path to the agent session transcript log file"),
				str("sessionId", "agent harness session or conversation identifier"),
				{Name: FieldWatchMode, Type: core.ParamString, Enum: WatchModes, Description: "when the pane takes the next queued item: on_done, schedule, or manual"},
				str(FieldWatchQueues, `queues the pane watches: "any" or comma-separated queue names`),
				{Name: FieldWatchIntervalSec, Type: core.ParamInt, Description: "seconds between polls when watchMode is schedule"},
				{Name: "pinned", Type: core.ParamBool, Default: false, Description: "pinned/bookmarked agent pane for quick anywhere jump, cycling, and prompt send"},
				str("pinnedAt", "when the pane was pinned (RFC 3339)"),
			},
			StatusColumns:  []StatusColumn{paneRun},
			DefaultColumns: []string{"title", "runStatus", "harness", "cwd", "task"},
			Relations:      universalRelations(),
			Hierarchy:      &HierarchySpec{ParentField: "parentPane", DefaultView: "table"},
			FactFields:     []string{"pinned"},
			BadgeRules: []BadgeRule{
				{When: `fields.pinned == true`, Text: "★ pinned", Tone: "accent"},
			},
		},
		Schema{
			Kind: KindPage, Title: "Page", Plural: "Pages", SummaryField: "title",
			Fields: []FieldSpec{
				{Name: "title", Type: core.ParamString, Required: true, Description: "page title"},
				str("slug", "stable name within the pane's site"),
				{Name: "pageKind", Type: core.ParamString, Default: "custom", Description: "what the page is for",
					Enum: []string{"screen", "questions", "decisions", "plan", "response", "custom", "outline", "signals"}},
				{Name: "pinned", Type: core.ParamBool, Description: "kept at the top of the table of contents"},
				{Name: "root", Type: core.ParamObject, ShowFormat: ShowFormatViewNode, Description: "the page's ViewNode tree"},
				strFmt("markdown", "Markdown content for a page without a canvas", ShowFormatMarkdown),
				strFmt("body", "inline page body", ShowFormatMarkdown),
				strFmt("summary", "page summary", ShowFormatMarkdown),
				str("viewedAt", "when the owner last viewed the page (RFC 3339)"),
				refTo("pane", KindPane, "pane whose site holds the page"),
				refTo("task", KindTask, "task the page belongs to"),
			},
			DefaultColumns: []string{"title", "pageKind", "pinned", "pane", "task"},
			Relations:      universalRelations(),
			UniqueFields:   []string{"slug"}, UniqueScope: "pane",
			UnreadField: "viewedAt",
		},
		Schema{
			// A document is the larger, static, usually file-backed text.
			Kind: KindDocument, Title: "Document", Plural: "Documents", SummaryField: "title",
			Fields: []FieldSpec{
				str("title", "document title"),
				{Name: "filePath", Type: core.ParamPath, ContentFile: true, Description: "file on disk"},
				strFmt("summary", "what the document covers", ShowFormatMarkdown),
				strFmt("body", "Markdown body, when the document is kept inline", ShowFormatMarkdown),
				refTo("task", KindTask, "task the document belongs to"),
			},
			DefaultColumns: []string{"title", "filePath", "summary", "task"},
			Relations:      universalRelations(),
		},
		Schema{
			// A note is small and changes often. It attaches to any record by
			// an "about" relation from the note to that record.
			Kind: KindNote, Title: "Note", Plural: "Notes", SummaryField: "title",
			Fields: []FieldSpec{
				str("title", "note title"),
				strFmt("body", "the note", ShowFormatMarkdown),
				{Name: "filePath", Type: core.ParamPath, ContentFile: true, Description: "file on disk"},
				refTo("task", KindTask, "task the note belongs to"),
				{Name: "authorKind", Type: core.ParamString, Enum: []string{"owner", "agent", "system"}, Default: "owner", Description: "who authored the note"},
				str("authorLabel", "display name of the note author"),
				refTo("pane", KindPane, "pane the note was written from"),
				str("readAt", "when the owner read the note (RFC 3339)"),
				{Name: "attachments", Type: core.ParamList, ShowFormat: ShowFormatList, Description: "attached files, images, URLs, or context references"},
			},
			DefaultColumns: []string{"title", "body", "task"},
			Relations: universalRelations(
				RelationSpec{Kind: RelAbout, Title: "About", Direction: DirectionOutgoing},
			),
			FactFields: []string{"authorKind"},
			BadgeRules: []BadgeRule{
				{When: `fields.authorKind == "agent"`, Text: "agent", Tone: "accent"},
				{When: `fields.authorKind == "system"`, Text: "system", Tone: "muted"},
			},
		},
		Schema{
			// A schedule is a reminder, a command to run or a check, on a cron
			// expression. Reminders are schedules of kind "remind".
			Kind: KindSchedule, Title: "Schedule", Plural: "Schedules", SummaryField: "title",
			Fields: []FieldSpec{
				{Name: "title", Type: core.ParamString, Required: true, Description: "what the schedule is for"},
				{Name: "scheduleKind", Type: core.ParamString, Enum: []string{"remind", "run", "check", "agent_queue"}, Default: "remind", Description: "remind the owner, run a command, check something, or drain the agent queue"},
				str("cron", "cron expression"),
				strFmt("command", "command line, for run and check", ShowFormatCode),
				str("nextDueAt", "when it is next due (RFC 3339)"),
				{Name: "paused", Type: core.ParamBool, Default: false, Description: "paused schedules do not fire"},
				refTo("task", KindTask, "task the schedule belongs to"),
				str("harness", "agent harness for agent_queue schedules"),
				refTo("pane", KindPane, "pane for agent_queue schedules"),
				strFmt("prompt", "extra prompt instructions for agent_queue schedules", ShowFormatMarkdown),
			},
			DefaultColumns: []string{"title", "scheduleKind", "cron", "nextDueAt", "task"},
			Relations:      universalRelations(),
			FactFields:     []string{"paused"},
			BadgeRules: []BadgeRule{
				{When: "fields.paused == true", Text: "paused", Tone: "warning"},
			},
			StyleRules: []StyleRule{
				{When: "fields.paused == true", Target: "row", Tone: "row.muted"},
			},
		},
		Schema{
			Kind: KindOccurrence, Title: "Occurrence", Plural: "Occurrences", SummaryField: "dueAt",
			Fields: []FieldSpec{
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
			Fields: []FieldSpec{
				str("title", "label"),
				strFmt("command", "command line", ShowFormatCode),
				{Name: "cwd", Type: core.ParamPath, Description: "working directory"},
				statusField(jobRun, "observed job state"),
				{Name: "exitCode", Type: core.ParamInt, Description: "exit status"},
				strFmt("output", "captured output", ShowFormatCode),
				refTo("task", KindTask, "task the run belongs to"),
				{Name: "args", Type: core.ParamList, ShowFormat: ShowFormatList, Description: "argv the run executed directly, when it did not run under a shell"},
				str("startedAt", "when the run started (RFC 3339)"),
				str("finishedAt", "when the run ended (RFC 3339)"),
				{Name: "durationMs", Type: core.ParamInt, Description: "how long the run took, in milliseconds"},
				{Name: "outputTruncated", Type: core.ParamBool, Description: "only the tail of the output is kept"},
			},
			StatusColumns:  []StatusColumn{jobRun},
			DefaultColumns: []string{"title", "runStatus", "exitCode", "command", "task"},
			Relations:      universalRelations(),
			StyleRules: []StyleRule{
				{When: "fields.exitCode != 0 && fields.exitCode != null", Target: "exitCode", Tone: "error"},
			},
		},
		Schema{
			Kind: KindExtension, Title: "Extension", Plural: "Extensions", SummaryField: "name",
			Fields: []FieldSpec{
				{Name: "name", Type: core.ParamString, Required: true, Description: "extension name"},
				str("binary", "binary that serves the extension"),
				statusField(extRun, "observed health"),
				strFmt("summary", "what the extension adds", ShowFormatMarkdown),
				{Name: "capabilities", Type: core.ParamList, ShowFormat: ShowFormatList, Description: "what the extension can do (nouns, pages, MCP tools)"},
				{Name: "commands", Type: core.ParamList, ShowFormat: ShowFormatList, Description: "extension-specific commands, such as setup"},
				str("version", "version the binary reports"),
				str("installPkg", "Go module package for go install"),
				str("category", "catalog group: core or optional"),
			},
			StatusColumns:  []StatusColumn{extRun},
			DefaultColumns: []string{"name", "runStatus", "binary", "summary"},
			Relations:      universalRelations(),
			UniqueFields:   []string{"name"},
		},
		Schema{
			// A signal is something that happened elsewhere and may want the
			// owner: triage rules sort the noise from what is actionable.
			Kind: KindSignal, Title: "Signal", Plural: "Signals", SummaryField: "title",
			Fields: []FieldSpec{
				str("title", "what happened"),
				str("source", "where it came from: github, slack, ci, mail"),
				str("eventKind", "what kind of event it is: mention, review_request, failure"),
				str("author", "who or what caused it"),
				str("container", "where it happened: a repository, a channel, a thread"),
				strFmt("body", "the text of the event", ShowFormatMarkdown),
				str("url", "link to the event"),
				str("occurredAt", "when it happened (RFC 3339)"),
				statusField(triage, "where the signal stands in triage"),
				refTo("task", KindTask, "task the signal belongs to"),
			},
			StatusColumns:  []StatusColumn{triage},
			DefaultColumns: []string{"title", "source", "author", "triageState", "task"},
			Relations:      universalRelations(),
		},
		Schema{
			// A triage rule is an expression over a signal's fields and what
			// to do with the signals it holds for.
			Kind: KindTriageRule, Title: "Triage rule", Plural: "Triage rules", SummaryField: "title",
			Fields: []FieldSpec{
				{Name: "title", Type: core.ParamString, Required: true, Description: "what the rule is for"},
				strFmt("when", "expression over the signal's fields: source == \"github\" && author == \"dependabot[bot]\"", ShowFormatCode),
				{Name: "action", Type: core.ParamString, Enum: TriageActions, Default: ActionMarkNoise, Description: "what to do with a signal the rule matches"},
				str("actionValue", "the tag to add, the task ID to relate, or how long to snooze (24h)"),
				{Name: "priority", Type: core.ParamInt, Default: 0, Description: "rules run from the highest priority to the lowest"},
				{Name: "enabled", Type: core.ParamBool, Default: true, Description: "a rule that is off is kept and never runs"},
			},
			DefaultColumns: []string{"title", "when", "action", "actionValue", "priority", "enabled"},
			Relations:      universalRelations(),
		},
		Schema{
			// A question asked by an agent (decision) or a Q&A logged for the owner (qa).
			Kind: KindQuestion, Title: "Question", Plural: "Questions", SummaryField: "prompt",
			Fields: []FieldSpec{
				{Name: "prompt", Type: core.ParamString, Required: true, ShowFormat: ShowFormatMarkdown, Description: "the question"},
				{Name: "questionKind", Type: core.ParamString, Enum: []string{"qa", "decision"}, Default: "qa", Description: "qa (owner asked agent, answered) or decision (agent asked owner to decide)"},
				strFmt("details", "context shown under the question", ShowFormatMarkdown),
				{Name: "choices", Type: core.ParamList, ShowFormat: ShowFormatList, Description: "pick-one options (list or JSON)"},
				strFmt("answer", "recorded answer", ShowFormatMarkdown),
				statusField(questionStatus, "whether the question is open, answered or dismissed"),
				str("answeredAt", "when the question was answered (RFC 3339)"),
				str("cardId", "ID of the linked ui.page question card"),
				refTo("pane", KindPane, "pane that asked the question"),
				refTo("task", KindTask, "task the question belongs to"),
				refTo("page", KindPage, "ui.page holding the question card"),
			},
			StatusColumns:  []StatusColumn{questionStatus},
			DefaultColumns: []string{"prompt", "questionKind", "status", "answer", "pane", "task"},
			Relations:      universalRelations(),
		},
		Schema{
			// A suggested shell command or script invocation to run.
			Kind: KindCommand, Title: "Command", Plural: "Commands", SummaryField: "title",
			Fields: []FieldSpec{
				{Name: "title", Type: core.ParamString, Required: true, Description: "what the command does"},
				{Name: "command", Type: core.ParamString, Required: true, ShowFormat: ShowFormatCode, Description: "literal shell command or script invocation"},
				{Name: "scriptPath", Type: core.ParamPath, Description: "optional script file path"},
				{Name: "cwd", Type: core.ParamPath, Description: "working directory"},
				strFmt("notes", "details or caveats", ShowFormatMarkdown),
				statusField(commandStatus, "whether the command is pending, ran or dismissed"),
				refTo("lastRun", KindRun, "most recent run of the command"),
				refTo("pane", KindPane, "pane that suggested the command"),
				refTo("task", KindTask, "task the command belongs to"),
			},
			StatusColumns:  []StatusColumn{commandStatus},
			DefaultColumns: []string{"title", "command", "status", "pane", "task"},
			Relations:      universalRelations(),
		},
		Schema{
			// A URL, PR reference, file, document, or code snippet linked as context to a pane/task.
			Kind: KindContext, Title: "Context", Plural: "Context", SummaryField: "title",
			Fields: []FieldSpec{
				{Name: "title", Type: core.ParamString, Required: true, Description: "display label"},
				{Name: "value", Type: core.ParamString, Required: true, ShowFormat: ShowFormatText, Description: "URL, PR reference, file path, or code snippet"},
				{Name: "contextKind", Type: core.ParamString, Enum: ValidContextKinds, Default: "url", Description: "kind of context item"},
				str("language", "language tag for code blocks/snippets"),
				{Name: "source", Type: core.ParamString, Enum: []string{"prompt", "response", "screen", "tool", "curated"}, Default: "response", Description: "where the context item came from"},
				{Name: "official", Type: core.ParamBool, Default: false, Description: "curated official project context automatically included for agents"},
				{Name: "pinned", Type: core.ParamBool, Default: false, Description: "pinned for quick access on the Project Page"},
				strFmt("notes", "why this context item matters to agents and humans", ShowFormatMarkdown),
				{Name: "line", Type: core.ParamInt, Description: "optional 1-based line number"},
				{Name: "col", Type: core.ParamInt, Description: "optional 1-based column number"},
				str("canonKey", "canonical deduplication key"),
				refTo("pane", KindPane, "pane where the context item appeared"),
				refTo("task", KindTask, "task the context item belongs to"),
			},
			DefaultColumns: []string{"title", "contextKind", "value", "pane", "task"},
			Relations:      universalRelations(),
			FactFields:     []string{"official", "pinned", "contextKind", "source"},
			BadgeRules: []BadgeRule{
				{When: `fields.official == true`, Text: "official · auto", Tone: "accent"},
				{When: `fields.pinned == true && fields.official != true`, Text: "pinned", Tone: "info"},
				{When: `fields.contextKind == "gdoc"`, Text: "gdoc", Tone: "info"},
				{When: `fields.contextKind == "sheet"`, Text: "sheet", Tone: "success"},
				{When: `fields.contextKind == "code"`, Text: "code", Tone: "accent"},
			},
		},
		conceptSchema(),
		queueSchema(),
		repoSchema(),
		captureSchema(),
		messageSchema(),
	)
	return r
}
