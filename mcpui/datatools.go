package mcpui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// Data tool names. Reads are open: they are never refused and carry no
// rights check. Writes are open and prefer archive over delete.
const (
	ToolSchemas          = "data.schemas"
	ToolQuery            = "data.query"
	ToolGet              = "data.get"
	ToolRelated          = "data.related"
	ToolTaskTree         = "data.task_tree"
	ToolHistory          = "data.history"
	ToolCreateNote       = "data.create_note"
	ToolRecordAgentInput = "data.record_agent_input"
	ToolSuggestCommand   = "data.suggest_command"
	ToolCreateDocument   = "data.create_document"
	ToolAddContext       = "data.add_context"
	ToolLogQA            = "data.log_qa"
	ToolCreate           = "data.create"
	ToolUpdate           = "data.update"
)

// DefaultAgentID is the actor ID of writes made over MCP without a "by".
const DefaultAgentID = "mcp"

// defaultHistoryLimit bounds data.history when no limit is given.
const defaultHistoryLimit = 50

// DataTools serves the data.* tools over a store. Getenv reads the
// environment (nil falls back to os.Getenv).
type DataTools struct {
	store  *data.Store
	Getenv func(string) string
}

// NewDataTools returns the data tools over store.
func NewDataTools(store *data.Store) *DataTools { return &DataTools{store: store} }

func (d *DataTools) env(name string) string {
	if d.Getenv != nil {
		return d.Getenv(name)
	}
	return os.Getenv(name)
}

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

// SuggestCommandArgs are the arguments of data.suggest_command.
type SuggestCommandArgs struct {
	ID         string `json:"id,omitempty"`
	Title      string `json:"title"`
	Command    string `json:"command"`
	ScriptPath string `json:"scriptPath,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	Notes      string `json:"notes,omitempty"`
	Pane       string `json:"pane,omitempty"`
	Task       string `json:"task,omitempty"`
	By         string `json:"by,omitempty"`
}

// SuggestCommandResult is what data.suggest_command returns.
type SuggestCommandResult struct {
	Command *data.Record `json:"command"`
}

// CreateDocumentArgs are the arguments of data.create_document.
type CreateDocumentArgs struct {
	ID       string `json:"id,omitempty"`
	Title    string `json:"title,omitempty"`
	FilePath string `json:"filePath,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Body     string `json:"body,omitempty"`
	Pane     string `json:"pane,omitempty"`
	Task     string `json:"task,omitempty"`
	By       string `json:"by,omitempty"`
}

// CreateDocumentResult is what data.create_document returns.
type CreateDocumentResult struct {
	Document *data.Record   `json:"document"`
	Relation *data.Relation `json:"relation,omitempty"`
}

// AddContextArgs are the arguments of data.add_context.
type AddContextArgs struct {
	ID          string `json:"id,omitempty"`
	Title       string `json:"title,omitempty"`
	Value       string `json:"value"`
	ContextKind string `json:"contextKind,omitempty"`
	Source      string `json:"source,omitempty"`
	Line        int    `json:"line,omitempty"`
	Col         int    `json:"col,omitempty"`
	Pane        string `json:"pane,omitempty"`
	Task        string `json:"task,omitempty"`
	By          string `json:"by,omitempty"`
}

// AddContextResult is what data.add_context returns.
type AddContextResult struct {
	Context      *data.Record   `json:"context"`
	Relation     *data.Relation `json:"relation,omitempty"`
	Deduplicated bool           `json:"deduplicated,omitempty"`
}

// LogQAArgs are the arguments of data.log_qa.
type LogQAArgs struct {
	ID      string `json:"id,omitempty"`
	Prompt  string `json:"prompt"`
	Answer  string `json:"answer"`
	Details string `json:"details,omitempty"`
	Page    string `json:"page,omitempty"`
	Pane    string `json:"pane,omitempty"`
	Task    string `json:"task,omitempty"`
	By      string `json:"by,omitempty"`
}

// LogQAResult is what data.log_qa returns.
type LogQAResult struct {
	Question *data.Record `json:"question"`
	Page     *data.Record `json:"page,omitempty"`
	CardID   string       `json:"cardId,omitempty"`
}

// CreateRecordArgs are the arguments of schema-driven data.create.
type CreateRecordArgs struct {
	Kind   string         `json:"kind"`
	ID     string         `json:"id,omitempty"`
	Fields map[string]any `json:"fields,omitempty"`
	By     string         `json:"by,omitempty"`
}

// UpdateRecordArgs are the arguments of schema-driven data.update.
type UpdateRecordArgs struct {
	Kind       string         `json:"kind"`
	ID         string         `json:"id"`
	Fields     map[string]any `json:"fields"`
	IfRevision int64          `json:"ifRevision,omitempty"`
	By         string         `json:"by,omitempty"`
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
		{
			Name:        ToolSuggestCommand,
			Description: "Suggest a shell command or script invocation for the owner to review and run. Auto-binds pane from context/env and inherits task from the pane when omitted. Prefer archive over delete when retiring commands. Returns {command}.",
			InputSchema: object([]string{"title", "command"}, props{
				"id":         text("command ID; generated when omitted"),
				"title":      text("short label describing what the command does"),
				"command":    text("literal shell command or script invocation"),
				"scriptPath": text("optional script file path"),
				"cwd":        text("working directory for running the command"),
				"notes":      text("extra context or caveats"),
				"pane":       text("pane ID that suggested the command; auto-bound when omitted"),
				"task":       text("task ID; inherits the pane's task when omitted"),
				"by":         by,
			}),
		},
		{
			Name:        ToolCreateDocument,
			Description: "Create a document record so Markdown docs or reports appear in Documents. Auto-binds pane via a created relation and inherits task from the pane when omitted. Prefer archive over delete when retiring documents. Returns {document, relation}.",
			InputSchema: object([]string{}, props{
				"id":       text("document ID; generated when omitted"),
				"title":    text("document title; defaults to the filePath basename when omitted"),
				"filePath": text("path to the document file on disk"),
				"summary":  text("short summary of what the document covers"),
				"body":     text("inline Markdown body"),
				"pane":     text("pane ID that created the document; linked via created relation"),
				"task":     text("task ID; inherits the pane's task when omitted"),
				"by":       by,
			}),
		},
		{
			Name:        ToolAddContext,
			Description: "Create or deduplicate a context item (URL, PR ref, file or doc) linked to the pane and its task via context relations. Prefer archive over delete when retiring context items. Returns {context, relation, deduplicated}.",
			InputSchema: object([]string{"value"}, props{
				"id":          text("context ID; generated when omitted"),
				"title":       text("display title; derived from value when omitted"),
				"value":       text("URL, PR reference (owner/repo#123), or file path"),
				"contextKind": oneOf("kind of context item; inferred from value when omitted", []string{"url", "pr", "file", "doc", "gdoc", "sheet"}),
				"source":      oneOf("where the context item came from; default tool", []string{"prompt", "response", "screen", "tool", "curated"}),
				"line":        count("optional 1-based line number"),
				"col":         count("optional 1-based column number"),
				"pane":        text("pane ID; auto-bound when omitted"),
				"task":        text("task ID; inherits the pane's task when omitted"),
				"by":          by,
			}),
		},
		{
			Name:        ToolLogQA,
			Description: "Log an answered Q&A pair (owner asked the agent, agent answered): creates a KindQuestion record with questionKind \"qa\" and status \"answered\" (no attention/needs_you alert). Format answer as structured Markdown (headings, bullet lists, bold labels, fenced code blocks; never a flat prose wall). If an answer exceeds 10 lines of text, publish the full write-up as its own dedicated page with ui.create_page instead of stuffing it into a shared Q&A card. When page is given (e.g. \"qa\" or \"questions\"), also appends a read Q&A card to that page without unread nagging. Returns {question, page?, cardId?}.",
			InputSchema: object([]string{"prompt", "answer"}, props{
				"id":      text("question ID; generated when omitted"),
				"prompt":  text("the question the owner asked"),
				"answer":  text("the agent's answer. Format answer as structured Markdown (headings, bullet lists, bold labels, fenced code blocks; never a flat prose wall)."),
				"details": text("optional extra context or references"),
				"page":    text("optional page ID or slug (e.g. \"qa\" or \"questions\") to append a non-unread Q&A card"),
				"pane":    text("pane ID; auto-bound when omitted"),
				"task":    text("task ID; inherits the pane's task when omitted"),
				"by":      by,
			}),
		},
		{
			Name:        ToolCreate,
			Description: "Create a record of any registered schema kind. Backed directly by data.Registry; automatically binds pane from context/env and inherits task from the pane when the schema declares those fields. Prefer archive over delete when retiring records.",
			InputSchema: object([]string{"kind"}, props{
				"kind":   kind,
				"id":     text("record ID; generated when omitted"),
				"fields": props{"type": "object", "description": "field values matching the kind's schema (from data.schemas)"},
				"by":     by,
			}),
		},
		{
			Name:        ToolUpdate,
			Description: "Update fields on any record by kind and ID (set a field to null to remove it). Prefer archiving (archiveStatus: \"archived\") over deleting records.",
			InputSchema: object([]string{"kind", "id", "fields"}, props{
				"kind":       kind,
				"id":         text("record ID"),
				"fields":     props{"type": "object", "description": "fields to patch (null removes a field)"},
				"ifRevision": count("optional expected changeSetNumber for optimistic concurrency conflict tracking"),
				"by":         by,
			}),
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
	case ToolSuggestCommand:
		var args SuggestCommandArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = d.SuggestCommand(agentActor(actor, args.By), args)
		}
	case ToolCreateDocument:
		var args CreateDocumentArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = d.CreateDocument(agentActor(actor, args.By), args)
		}
	case ToolAddContext:
		var args AddContextArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = d.AddContext(agentActor(actor, args.By), args)
		}
	case ToolLogQA:
		var args LogQAArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = d.LogQA(agentActor(actor, args.By), args)
		}
	case ToolCreate:
		var args CreateRecordArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = d.CreateRecord(agentActor(actor, args.By), args)
		}
	case ToolUpdate:
		var args UpdateRecordArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = d.UpdateRecord(agentActor(actor, args.By), args)
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

func (d *DataTools) inheritPaneTask(paneID, taskID string) string {
	if taskID != "" || paneID == "" {
		return taskID
	}
	if pane, err := d.store.Get(paneRef(paneID)); err == nil {
		if t, ok := pane.RefField("task"); ok {
			return t.ID
		}
	}
	return ""
}

// SuggestCommand is data.suggest_command: it creates a KindCommand record
// bound to the pane and inheriting its task when omitted.
func (d *DataTools) SuggestCommand(actor core.Actor, args SuggestCommandArgs) (*SuggestCommandResult, error) {
	if strings.TrimSpace(args.Title) == "" {
		return nil, errors.New("title is required")
	}
	if strings.TrimSpace(args.Command) == "" {
		return nil, errors.New("command is required")
	}
	taskID := d.inheritPaneTask(args.Pane, args.Task)
	fields := map[string]any{
		"title":   strings.TrimSpace(args.Title),
		"command": args.Command,
		"status":  "pending",
	}
	if args.ScriptPath != "" {
		fields["scriptPath"] = args.ScriptPath
	}
	if args.Cwd != "" {
		fields["cwd"] = args.Cwd
	}
	if args.Notes != "" {
		fields["notes"] = args.Notes
	}
	if args.Pane != "" {
		fields["pane"] = paneRef(args.Pane)
	}
	if taskID != "" {
		fields["task"] = taskRef(taskID)
	}
	rec, _, err := d.store.Create(data.CreateInput{Kind: data.KindCommand, ID: args.ID, Fields: fields}, actor)
	if err != nil {
		return nil, err
	}
	return &SuggestCommandResult{Command: rec}, nil
}

// CreateDocument is data.create_document: it stores a KindDocument record,
// inherits the pane's task when omitted, and links pane -> document via RelCreated.
func (d *DataTools) CreateDocument(actor core.Actor, args CreateDocumentArgs) (*CreateDocumentResult, error) {
	title := strings.TrimSpace(args.Title)
	if title == "" && args.FilePath != "" {
		title = filepath.Base(args.FilePath)
	}
	if title == "" {
		return nil, errors.New("title or filePath is required")
	}
	taskID := d.inheritPaneTask(args.Pane, args.Task)
	fields := map[string]any{"title": title}
	if args.FilePath != "" {
		fields["filePath"] = args.FilePath
	}
	if args.Summary != "" {
		fields["summary"] = args.Summary
	}
	if args.Body != "" {
		fields["body"] = args.Body
	}
	if taskID != "" {
		fields["task"] = taskRef(taskID)
	}
	doc, _, err := d.store.Create(data.CreateInput{Kind: data.KindDocument, ID: args.ID, Fields: fields}, actor)
	if err != nil {
		return nil, err
	}
	if args.FilePath == "" && args.Body != "" {
		if root := d.store.FilesRoot(); root != "" {
			managedPath, werr := data.WriteManagedFile(root, data.CategoryDocuments, doc.ID, title, ".md", args.Body)
			if werr != nil {
				return nil, werr
			}
			if updated, _, perr := d.store.Patch(data.PatchInput{
				Ref:     doc.Ref(),
				Fields:  map[string]any{"filePath": managedPath},
				Summary: "record managed file path",
			}, actor); perr == nil && updated != nil {
				doc = updated
			}
		}
	}
	out := &CreateDocumentResult{Document: doc}
	if args.Pane != "" {
		if out.Relation, _, err = d.store.Relate(data.RelateInput{From: paneRef(args.Pane), Kind: data.RelCreated, To: doc.Ref()}, actor); err != nil {
			return nil, err
		}
	}
	return out, nil
}

var (
	validContextKinds   = []string{"url", "pr", "file", "doc", "gdoc", "sheet"}
	validContextSources = []string{"prompt", "response", "screen", "tool", "curated"}
	ghPRURLRe           = regexp.MustCompile(`^https?://github\.com/([A-Za-z0-9][A-Za-z0-9_.-]{0,38}/[A-Za-z0-9_.-]{1,100})/pull/([1-9][0-9]*)(?:[/?#].*)?$`)
)

func classifyContext(value, explicitKind, explicitTitle string) (kind, title, canonKey string) {
	val := strings.TrimSpace(value)
	kind = data.ClassifyContextKind(val, explicitKind)
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
			canonKey = "pr:" + strings.ToLower(val)
		}
	case "url", "gdoc", "sheet":
		canonKey = kind + ":" + strings.ToLower(strings.TrimRight(val, "/"))
	default:
		canonKey = kind + ":" + filepath.Clean(val)
	}
	return kind, title, canonKey
}

// AddContext is data.add_context: it creates or deduplicates a KindContext
// record bound to the pane and its task (plus RelContext relations).
func (d *DataTools) AddContext(actor core.Actor, args AddContextArgs) (*AddContextResult, error) {
	val := strings.TrimSpace(args.Value)
	if val == "" {
		return nil, errors.New("value is required")
	}
	if args.ContextKind != "" && !slices.Contains(validContextKinds, strings.ToLower(args.ContextKind)) {
		return nil, fmt.Errorf("contextKind %q is not one of %s", args.ContextKind, strings.Join(validContextKinds, ", "))
	}
	source := strings.ToLower(strings.TrimSpace(args.Source))
	if source == "" {
		source = "tool"
	}
	if !slices.Contains(validContextSources, source) {
		return nil, fmt.Errorf("source %q is not one of %s", args.Source, strings.Join(validContextSources, ", "))
	}
	taskID := d.inheritPaneTask(args.Pane, args.Task)
	kind, title, canonKey := classifyContext(val, args.ContextKind, args.Title)

	// Dedupe against existing active KindContext records with the same canonKey
	// within the same task (or same pane when no task is set).
	var existing *data.Record
	if qRes, err := d.store.Query(data.Query{Kind: data.KindContext, Query: canonKey, FilterField: "canonKey"}); err == nil {
		for i := range qRes.Items {
			rec := qRes.Items[i]
			if rec.Text("canonKey") != canonKey {
				continue
			}
			recTask, hasRecTask := rec.RefField("task")
			recPane, hasRecPane := rec.RefField("pane")
			sameScope := false
			switch {
			case taskID != "":
				sameScope = (hasRecTask && recTask.ID == taskID) || (!hasRecTask && args.Pane != "" && hasRecPane && recPane.ID == args.Pane)
			case args.Pane != "":
				sameScope = !hasRecTask && hasRecPane && recPane.ID == args.Pane
			default:
				sameScope = !hasRecTask && !hasRecPane
			}
			if sameScope {
				cp := rec
				existing = &cp
				break
			}
		}
	}

	var rec *data.Record
	deduped := false
	if existing != nil {
		deduped = true
		rec = existing
		patchFields := map[string]any{}
		if taskID != "" {
			if _, ok := rec.RefField("task"); !ok {
				patchFields["task"] = taskRef(taskID)
			}
		}
		if args.Pane != "" {
			if _, ok := rec.RefField("pane"); !ok {
				patchFields["pane"] = paneRef(args.Pane)
			}
		}
		if args.Line > 0 && rec.Text("line") == "" {
			patchFields["line"] = args.Line
		}
		if args.Col > 0 && rec.Text("col") == "" {
			patchFields["col"] = args.Col
		}
		// Upgrade bare PR ref to full URL if provided.
		if strings.HasPrefix(strings.ToLower(val), "http") && !strings.HasPrefix(strings.ToLower(rec.Text("value")), "http") {
			patchFields["value"] = val
		}
		if len(patchFields) > 0 {
			updated, _, err := d.store.Patch(data.PatchInput{Ref: rec.Ref(), Fields: patchFields, Summary: "update deduplicated context"}, actor)
			if err != nil {
				return nil, err
			}
			rec = updated
		}
	} else {
		fields := map[string]any{
			"title":       title,
			"value":       val,
			"contextKind": kind,
			"source":      source,
			"canonKey":    canonKey,
		}
		if args.Line > 0 {
			fields["line"] = args.Line
		}
		if args.Col > 0 {
			fields["col"] = args.Col
		}
		if args.Pane != "" {
			fields["pane"] = paneRef(args.Pane)
		}
		if taskID != "" {
			fields["task"] = taskRef(taskID)
		}
		var err error
		rec, _, err = d.store.Create(data.CreateInput{Kind: data.KindContext, ID: args.ID, Fields: fields}, actor)
		if err != nil {
			return nil, err
		}
	}

	out := &AddContextResult{Context: rec, Deduplicated: deduped}
	if args.Pane != "" {
		rel, _, err := d.store.Relate(data.RelateInput{From: paneRef(args.Pane), Kind: data.RelContext, To: rec.Ref()}, actor)
		if err != nil {
			return nil, err
		}
		out.Relation = rel
	}
	if taskID != "" {
		rel, _, err := d.store.Relate(data.RelateInput{From: taskRef(taskID), Kind: data.RelContext, To: rec.Ref()}, actor)
		if err != nil {
			return nil, err
		}
		if out.Relation == nil {
			out.Relation = rel
		}
	}
	return out, nil
}

// LogQA is data.log_qa: it records an owner-asked Q&A pair as a KindQuestion
// with questionKind "qa" and status "answered" (never triggering attention),
// and optionally appends a read Q&A card to the named page without unread nagging.
func (d *DataTools) LogQA(actor core.Actor, args LogQAArgs) (*LogQAResult, error) {
	prompt := strings.TrimSpace(args.Prompt)
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}
	answer := strings.TrimSpace(args.Answer)
	if answer == "" {
		return nil, errors.New("answer is required")
	}
	taskID := d.inheritPaneTask(args.Pane, args.Task)
	now := time.Now().UTC().Format(data.TimeFormat)

	out := &LogQAResult{}
	qFields := map[string]any{
		"prompt":       prompt,
		"questionKind": "qa",
		"answer":       answer,
		"status":       badgeAnswered,
		"answeredAt":   now,
	}
	if args.Details != "" {
		qFields["details"] = args.Details
	}
	if args.Pane != "" {
		qFields["pane"] = paneRef(args.Pane)
	}
	if taskID != "" {
		qFields["task"] = taskRef(taskID)
	}

	if slug := strings.TrimSpace(args.Page); slug != "" {
		r := New(d.store)
		page, err := r.findPage(args.Pane, slug)
		switch {
		case errors.Is(err, data.ErrNotFound):
			kind, title := "custom", "Q&A"
			if slices.Contains(pageKinds, slug) {
				kind, title = slug, strings.ToUpper(slug[:1])+slug[1:]
			} else if !strings.EqualFold(slug, "qa") {
				title = slug
			}
			created, cerr := r.CreatePage(actor, CreatePageArgs{
				Pane:     args.Pane,
				Task:     taskID,
				Title:    title,
				Slug:     slug,
				PageKind: kind,
				Root:     ui.ViewNode{Kind: ui.KindVStack, Gap: 1},
			})
			if cerr != nil {
				return nil, cerr
			}
			if page, err = d.store.Get(created.Ref); err != nil {
				return nil, err
			}
		case err != nil:
			return nil, err
		case page.IsArchived():
			if page, _, err = d.store.Restore(page.Ref(), actor); err != nil {
				return nil, err
			}
		}
		root, err := PageRoot(*page)
		if err != nil {
			return nil, err
		}
		cardID := "qa-" + fmt.Sprintf("%x", time.Now().UnixNano())
		body := answer
		if args.Details != "" {
			body = args.Details + "\n\n" + answer
		}
		card := ui.ViewNode{
			ID:        cardID,
			Kind:      ui.KindCard,
			Title:     prompt,
			Scope:     ScopeQuestion,
			Focusable: true,
			Unread:    false,
			Border:    true,
			Tone:      "success",
			Badges:    []ui.Badge{{Text: "qa", Tone: "muted"}},
			Facts:     map[string]any{"answered": true, "questionKind": "qa"},
			Props: map[string]any{
				propAnswer:     map[string]any{"answer": answer},
				propAnsweredAt: now,
				propAnsweredBy: actor,
			},
			Children: []ui.ViewNode{{
				ID:    cardID + ".body",
				Kind:  ui.KindMarkdown,
				Props: map[string]any{"markdown": body},
			}},
		}
		if err := insert(&root, card, ModeAppend, ""); err != nil {
			return nil, err
		}
		savedPage, err := r.savePage(page, root, map[string]any{"viewedAt": data.WriteTime{}}, actor)
		if err != nil {
			return nil, err
		}
		out.Page = savedPage
		out.CardID = cardID
		qFields["page"] = savedPage.Ref()
		qFields["cardId"] = cardID
	}

	qRec, _, err := d.store.Create(data.CreateInput{Kind: data.KindQuestion, ID: args.ID, Fields: qFields}, actor)
	if err != nil {
		return nil, err
	}
	out.Question = qRec
	return out, nil
}

func coerceSchemaFields(schema data.Schema, raw map[string]any, forUpdate bool) map[string]any {
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		if f, ok := schema.Field(k); ok && f.Type == core.ParamRef && len(f.RefKinds) == 1 {
			if s, isStr := v.(string); isStr {
				s = strings.TrimSpace(s)
				if s == "" {
					if forUpdate {
						out[k] = nil
					}
					continue
				}
				out[k] = core.Ref{Kind: f.RefKinds[0], ID: s}
				continue
			}
		}
		out[k] = v
	}
	return out
}

// CreateRecord is data.create: schema-driven record creation over data.Registry,
// auto-binding pane from context/env and inheriting task from the pane when the
// schema declares those fields.
func (d *DataTools) CreateRecord(actor core.Actor, args CreateRecordArgs) (*data.Record, error) {
	if strings.TrimSpace(args.Kind) == "" {
		return nil, errors.New("kind is required")
	}
	schema, registered := d.store.Registry().Get(args.Kind)
	if !registered {
		return nil, fmt.Errorf("unknown schema kind %q (see data.schemas)", args.Kind)
	}
	fields := coerceSchemaFields(schema, args.Fields, false)

	envPane := strings.TrimSpace(d.env(EnvPaneID))
	envTask := strings.TrimSpace(d.env(EnvTaskID))

	if f, hasPane := schema.Field("pane"); hasPane && f.Type == core.ParamRef {
		if _, set := fields["pane"]; !set && envPane != "" {
			fields["pane"] = paneRef(envPane)
		}
	}
	if f, hasTask := schema.Field("task"); hasTask && f.Type == core.ParamRef {
		if _, set := fields["task"]; !set {
			paneID := envPane
			if ref, ok := data.AsRef(fields["pane"]); ok && ref.Kind == data.KindPane {
				paneID = ref.ID
			}
			taskID := envTask
			if taskID == "" {
				taskID = d.inheritPaneTask(paneID, "")
			}
			if taskID != "" {
				fields["task"] = taskRef(taskID)
			}
		}
	}

	for _, f := range schema.Fields {
		if !f.Required || f.Default != nil {
			continue
		}
		v, ok := fields[f.Name]
		if !ok || v == nil || strings.TrimSpace(data.Text(v)) == "" {
			return nil, fmt.Errorf("field %q is required for %s", f.Name, args.Kind)
		}
	}

	rec, _, err := d.store.Create(data.CreateInput{Kind: args.Kind, ID: args.ID, Fields: fields}, actor)
	return rec, err
}

// UpdateRecord is data.update: schema-driven partial patch of any record.
func (d *DataTools) UpdateRecord(actor core.Actor, args UpdateRecordArgs) (*data.Record, error) {
	ref, err := needRef(args.Kind, args.ID)
	if err != nil {
		return nil, err
	}
	if len(args.Fields) == 0 {
		return nil, errors.New("fields is required")
	}
	fields := args.Fields
	if schema, ok := d.store.Registry().Get(args.Kind); ok {
		fields = coerceSchemaFields(schema, args.Fields, true)
	}
	rec, _, err := d.store.Patch(data.PatchInput{
		Ref:        ref,
		Fields:     fields,
		IfRevision: args.IfRevision,
	}, actor)
	return rec, err
}
