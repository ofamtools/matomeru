package mcpui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// Data tool names. Reads are open: they are never refused and carry no
// rights check. The two writes add things and move nothing a human set.
const (
	ToolSchemas          = "data.schemas"
	ToolQuery            = "data.query"
	ToolGet              = "data.get"
	ToolRelated          = "data.related"
	ToolTaskTree         = "data.task_tree"
	ToolHistory          = "data.history"
	ToolCreateNote       = "data.create_note"
	ToolRecordAgentInput = "data.record_agent_input"
)

// DefaultAgentID is the actor ID of writes made over MCP without a "by".
const DefaultAgentID = "mcp"

// defaultHistoryLimit bounds data.history when no limit is given.
const defaultHistoryLimit = 50

// DataTools serves the data.* tools over a store.
type DataTools struct {
	store *data.Store
}

// NewDataTools returns the data tools over store.
func NewDataTools(store *data.Store) *DataTools { return &DataTools{store: store} }

// --- arguments and results ----------------------------------------------------

// SchemasArgs are the arguments of data.schemas.
type SchemasArgs struct {
	Kind string `json:"kind,omitempty"`
}

// SchemasResult is what data.schemas returns. Each schema's StatusColumns
// include the universal ones every record has.
type SchemasResult struct {
	Schemas []data.Schema `json:"schemas"`
}

// QueryArgs are the arguments of data.query, mapped onto data.Query.
type QueryArgs struct {
	Kind            string    `json:"kind,omitempty"`
	Query           string    `json:"query,omitempty"`
	FilterField     string    `json:"filterField,omitempty"`
	MatchMode       string    `json:"matchMode,omitempty"`
	StarredOnly     bool      `json:"starredOnly,omitempty"`
	PinnedOnly      bool      `json:"pinnedOnly,omitempty"`
	MarkedOnly      bool      `json:"markedOnly,omitempty"`
	Tag             string    `json:"tag,omitempty"`
	IncludeArchived bool      `json:"includeArchived,omitempty"`
	Parent          *core.Ref `json:"parent,omitempty"`
	ParentField     string    `json:"parentField,omitempty"`
	Roots           bool      `json:"roots,omitempty"`
	Sort            string    `json:"sort,omitempty"`
	SortDesc        bool      `json:"sortDesc,omitempty"`
	Limit           int       `json:"limit,omitempty"`
	Offset          int       `json:"offset,omitempty"`
}

// GetArgs are the arguments of data.get.
type GetArgs struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// RelatedArgs are the arguments of data.related.
type RelatedArgs struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Relation  string `json:"relation,omitempty"`
	Direction string `json:"direction,omitempty"`
}

// RelatedResult is what data.related returns: the relations on the ref and
// the stored records at their other ends.
type RelatedResult struct {
	Relations []data.Relation `json:"relations"`
	Records   []data.Record   `json:"records"`
}

// TaskTreeArgs are the arguments of data.task_tree.
type TaskTreeArgs struct {
	IncludeArchived bool `json:"includeArchived,omitempty"`
}

// TaskTreeResult is what data.task_tree returns.
type TaskTreeResult struct {
	Tasks []data.TaskNode `json:"tasks"`
}

// HistoryArgs are the arguments of data.history. Kind alone selects the
// change sets that touched a noun; kind and id, one record.
type HistoryArgs struct {
	Kind  string `json:"kind,omitempty"`
	ID    string `json:"id,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// HistoryResult is what data.history returns, newest first.
type HistoryResult struct {
	ChangeSets []data.ChangeSet `json:"changeSets"`
	Latest     int64            `json:"latest"`
}

// CreateNoteArgs are the arguments of data.create_note.
type CreateNoteArgs struct {
	ID    string    `json:"id,omitempty"`
	Title string    `json:"title,omitempty"`
	Body  string    `json:"body,omitempty"`
	Task  string    `json:"task,omitempty"`
	About *core.Ref `json:"about,omitempty"`
	By    string    `json:"by,omitempty"`
}

// CreateNoteResult is what data.create_note returns.
type CreateNoteResult struct {
	Note     *data.Record   `json:"note"`
	Relation *data.Relation `json:"relation,omitempty"`
}

// RecordAgentInputArgs are the arguments of data.record_agent_input.
type RecordAgentInputArgs struct {
	Pane string `json:"pane"`
	By   string `json:"by,omitempty"`
}

// --- schemas --------------------------------------------------------------------

func refSchema(desc string) props {
	return props{"type": "object", "description": desc, "required": []string{"kind", "id"}, "additionalProperties": false,
		"properties": props{"kind": text("record kind"), "id": text("record ID")}}
}

func count(desc string) props { return props{"type": "integer", "minimum": 0, "description": desc} }

// ToolSpecs returns the data tools with JSON schemas of their real
// parameters.
func (d *DataTools) ToolSpecs() []ToolSpec {
	by := text("who is writing: an agent ID recorded on the change set; default " + DefaultAgentID)
	kind := text("record kind, such as task, pane, note, ui.page")
	return []ToolSpec{
		{
			Name:        ToolSchemas,
			Description: "List the registered schemas: kind, fields, status columns (with the universal star and archive ones) and relation sections.",
			InputSchema: object([]string{}, props{"kind": text("only this kind's schema")}),
		},
		{
			Name:        ToolQuery,
			Description: "Query records of any kind. Returns {items, total, filterErr}; total counts every match before limit and offset.",
			InputSchema: object([]string{}, props{
				"kind":            text("record kind; omit for every kind"),
				"query":           text("text to match, ignoring case"),
				"filterField":     text("field to match query against; omit for every field"),
				"matchMode":       oneOf("how query matches; default substring", []string{string(data.MatchSubstring), string(data.MatchFuzzy), string(data.MatchRegex)}),
				"starredOnly":     flag("only starred records"),
				"pinnedOnly":      flag("only pinned records"),
				"markedOnly":      flag("only marked records"),
				"tag":             text("only records with this tag"),
				"includeArchived": flag("also return archived records"),
				"parent":          refSchema("only records whose parentField refers to this record"),
				"parentField":     text("reference field parent and roots read; default parent"),
				"roots":           flag("only records whose parentField is empty"),
				"sort":            text("field to sort by, or id, kind, createdAt, updatedAt, archiveStatus, starStatus; default creation order"),
				"sortDesc":        flag("sort descending"),
				"limit":           count("at most this many items; 0 for all"),
				"offset":          count("skip this many items"),
			}),
		},
		{
			Name:        ToolGet,
			Description: "Get one record by kind and ID, archived or not.",
			InputSchema: object([]string{"kind", "id"}, props{"kind": kind, "id": text("record ID")}),
		},
		{
			Name:        ToolRelated,
			Description: "List the relations on a record (notes about it, tags, context, pages, pins) and the stored records at their other ends.",
			InputSchema: object([]string{"kind", "id"}, props{
				"kind":      kind,
				"id":        text("record ID"),
				"relation":  text("only this relation kind, such as about, tag, context, page"),
				"direction": oneOf("which end the record is on; default both", []string{data.DirectionOutgoing, data.DirectionIncoming, data.DirectionBoth}),
			}),
		},
		{
			Name:        ToolTaskTree,
			Description: "The recursive task tree: root tasks (projects) at depth 0, each with its subtasks.",
			InputSchema: object([]string{}, props{"includeArchived": flag("also include archived tasks and their subtrees")}),
		},
		{
			Name:        ToolHistory,
			Description: "Recent change sets, newest first: every one, those that touched a kind, or those that touched one record. Returns {changeSets, latest}.",
			InputSchema: object([]string{}, props{
				"kind":  text("only change sets that touched this kind"),
				"id":    text("with kind: only change sets that touched this record"),
				"limit": count(fmt.Sprintf("at most this many; default %d, 0 for the default", defaultHistoryLimit)),
			}),
		},
		{
			Name:        ToolCreateNote,
			Description: "Create a note and attach it to any record with an about relation. Returns {note, relation}.",
			InputSchema: object([]string{}, props{
				"id":    text("note ID; generated when omitted"),
				"title": text("note title"),
				"body":  text("the note"),
				"task":  text("task ID the note belongs to"),
				"about": refSchema("the record the note is about"),
				"by":    by,
			}),
		},
		{
			Name:        ToolRecordAgentInput,
			Description: "Record that an agent sent input to a pane: sets lastAgentInputAt and lastAgentBy. The owner's promptedAt, which orders the pane rail, does not move.",
			InputSchema: object([]string{"pane"}, props{"pane": text("pane ID"), "by": by}),
		},
	}
}

// --- dispatch -------------------------------------------------------------------

// HasTool reports whether name is one of the data tools.
func (d *DataTools) HasTool(name string) bool {
	for _, spec := range d.ToolSpecs() {
		if spec.Name == name {
			return true
		}
	}
	return false
}

// agentActor is the actor of a data write: always an agent, so a write over
// MCP never passes for the owner's own (RecordPaneInput moves promptedAt only
// for a user).
func agentActor(base core.Actor, by string) core.Actor {
	actor := core.Actor{Kind: "agent", ID: base.ID, Label: base.Label}
	if by != "" {
		actor.ID = by
	}
	if actor.ID == "" {
		actor.ID = DefaultAgentID
	}
	return actor
}

// Call runs one data tool with JSON arguments. actor is the default writer;
// a tool's "by" argument names another agent.
func (d *DataTools) Call(ctx context.Context, actor core.Actor, toolName string, rawArgs json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var (
		out any
		err error
	)
	switch toolName {
	case ToolSchemas:
		var args SchemasArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = d.Schemas(args)
		}
	case ToolQuery:
		var args QueryArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = d.Query(args)
		}
	case ToolGet:
		var args GetArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = d.Get(args)
		}
	case ToolRelated:
		var args RelatedArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = d.Related(args)
		}
	case ToolTaskTree:
		var args TaskTreeArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = d.TaskTree(args)
		}
	case ToolHistory:
		var args HistoryArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = d.History(args)
		}
	case ToolCreateNote:
		var args CreateNoteArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = d.CreateNote(agentActor(actor, args.By), args)
		}
	case ToolRecordAgentInput:
		var args RecordAgentInputArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = d.RecordAgentInput(agentActor(actor, args.By), args)
		}
	default:
		return nil, fmt.Errorf("mcpui: unknown tool %q", toolName)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", toolName, err)
	}
	return out, nil
}

// --- reads ----------------------------------------------------------------------

// Schemas is data.schemas.
func (d *DataTools) Schemas(args SchemasArgs) (*SchemasResult, error) {
	out := &SchemasResult{Schemas: []data.Schema{}}
	for _, s := range d.store.Registry().List() {
		if args.Kind != "" && s.Kind != args.Kind {
			continue
		}
		s.StatusColumns = s.AllStatusColumns()
		out.Schemas = append(out.Schemas, s)
	}
	return out, nil
}

// Query is data.query.
func (d *DataTools) Query(args QueryArgs) (*data.QueryResult, error) {
	res, err := d.store.Query(data.Query{
		Kind: args.Kind, Parent: args.Parent, ParentField: args.ParentField, Roots: args.Roots,
		Query: args.Query, FilterField: args.FilterField, MatchMode: data.MatchMode(args.MatchMode),
		IncludeArchived: args.IncludeArchived, StarredOnly: args.StarredOnly, PinnedOnly: args.PinnedOnly,
		MarkedOnly: args.MarkedOnly, Tag: args.Tag,
		SortField: args.Sort, SortDesc: args.SortDesc, Limit: args.Limit, Offset: args.Offset,
	})
	if err != nil {
		return nil, err
	}
	if res.Items == nil {
		res.Items = []data.Record{}
	}
	return &res, nil
}

func needRef(kind, id string) (core.Ref, error) {
	if kind == "" || id == "" {
		return core.Ref{}, errors.New("kind and id are required")
	}
	return core.Ref{Kind: kind, ID: id}, nil
}

// Get is data.get.
func (d *DataTools) Get(args GetArgs) (*data.Record, error) {
	ref, err := needRef(args.Kind, args.ID)
	if err != nil {
		return nil, err
	}
	return d.store.Get(ref)
}

// Related is data.related.
func (d *DataTools) Related(args RelatedArgs) (*RelatedResult, error) {
	ref, err := needRef(args.Kind, args.ID)
	if err != nil {
		return nil, err
	}
	dir := data.Both
	switch strings.ToLower(args.Direction) {
	case "", data.DirectionBoth:
	case data.DirectionOutgoing:
		dir = data.Outgoing
	case data.DirectionIncoming:
		dir = data.Incoming
	default:
		return nil, fmt.Errorf("direction %q is not one of outgoing, incoming, both", args.Direction)
	}
	out := &RelatedResult{Relations: []data.Relation{}, Records: []data.Record{}}
	rels, err := d.store.Related(ref, args.Relation, dir)
	if err != nil {
		return nil, err
	}
	recs, err := d.store.RelatedRecords(ref, args.Relation, dir)
	if err != nil {
		return nil, err
	}
	out.Relations = append(out.Relations, rels...)
	out.Records = append(out.Records, recs...)
	return out, nil
}

// TaskTree is data.task_tree.
func (d *DataTools) TaskTree(args TaskTreeArgs) (*TaskTreeResult, error) {
	tree, err := d.store.TaskTree(args.IncludeArchived)
	if err != nil {
		return nil, err
	}
	if tree == nil {
		tree = []data.TaskNode{}
	}
	return &TaskTreeResult{Tasks: tree}, nil
}

// History is data.history.
func (d *DataTools) History(args HistoryArgs) (*HistoryResult, error) {
	var ref *core.Ref
	switch {
	case args.Kind != "":
		ref = &core.Ref{Kind: args.Kind, ID: args.ID}
	case args.ID != "":
		return nil, errors.New("id needs a kind")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = defaultHistoryLimit
	}
	sets, err := d.store.History(ref, limit)
	if err != nil {
		return nil, err
	}
	latest, err := d.store.LatestChangeSetNumber()
	if err != nil {
		return nil, err
	}
	if sets == nil {
		sets = []data.ChangeSet{}
	}
	return &HistoryResult{ChangeSets: sets, Latest: latest}, nil
}

// --- safe writes ------------------------------------------------------------------

// CreateNote is data.create_note: it stores a note and relates it to the
// record it is about. The record does not have to exist: the relation keeps
// the reference.
func (d *DataTools) CreateNote(actor core.Actor, args CreateNoteArgs) (*CreateNoteResult, error) {
	fields := map[string]any{}
	if args.Title != "" {
		fields["title"] = args.Title
	}
	if args.Body != "" {
		fields["body"] = args.Body
	}
	if args.Task != "" {
		fields["task"] = taskRef(args.Task)
	}
	note, _, err := d.store.Create(data.CreateInput{Kind: data.KindNote, ID: args.ID, Fields: fields}, actor)
	if err != nil {
		return nil, err
	}
	if root := d.store.FilesRoot(); root != "" && args.Body != "" {
		managedPath, werr := data.WriteManagedFile(root, data.CategoryNotes, note.ID, args.Title, ".md", args.Body)
		if werr != nil {
			return nil, werr
		}
		if updated, _, perr := d.store.Patch(data.PatchInput{
			Ref:     note.Ref(),
			Fields:  map[string]any{"filePath": managedPath, "path": managedPath},
			Summary: "record managed file path",
		}, actor); perr == nil && updated != nil {
			note = updated
		}
	}
	out := &CreateNoteResult{Note: note}
	if args.About != nil && args.About.Kind != "" && args.About.ID != "" {
		if out.Relation, _, err = d.store.Relate(data.RelateInput{From: note.Ref(), Kind: data.RelAbout, To: *args.About}, actor); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// RecordAgentInput is data.record_agent_input. The actor is always an agent,
// so the pane's promptedAt does not move.
func (d *DataTools) RecordAgentInput(actor core.Actor, args RecordAgentInputArgs) (*data.Record, error) {
	if args.Pane == "" {
		return nil, errors.New("pane is required")
	}
	rec, _, err := d.store.RecordPaneInput(paneRef(args.Pane), agentActor(actor, ""))
	return rec, err
}
