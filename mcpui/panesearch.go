package mcpui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/panesearch"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
)

// ToolPaneSearch is the MCP tool name for global pane & chat-log search.
const ToolPaneSearch = "pane.search"

// PaneSearchLiveSnapshot supplies optional live daemon PTY scrollback and hook
// telemetry turns to PaneSearchTools. When nil, pane.search reads SQLite
// pane-linked records and external disk transcripts directly.
type PaneSearchLiveSnapshot struct {
	Frames          map[string]panesearch.LiveFrame
	Turns           map[string][]panesearch.Turn
	TranscriptPaths map[string]string
	SessionIDs      map[string]string
}

// PaneSearchTools serves pane.search over a store (and optional live daemon data).
type PaneSearchTools struct {
	store    *data.Store
	cache    *panesearch.Cache
	LiveData func() PaneSearchLiveSnapshot
	HomeDir  string
}

// NewPaneSearchTools returns PaneSearchTools over store.
func NewPaneSearchTools(store *data.Store) *PaneSearchTools {
	return &PaneSearchTools{
		store: store,
		cache: panesearch.NewCache(),
	}
}

// PaneSearchArgs are the JSON arguments for pane.search.
type PaneSearchArgs struct {
	Query              string `json:"query"`
	Source             string `json:"source,omitempty"`
	Mode               string `json:"mode,omitempty"`
	IncludeArchived    bool   `json:"includeArchived,omitempty"`
	IncludeTranscripts *bool  `json:"includeTranscripts,omitempty"`
	Limit              int    `json:"limit,omitempty"`
}

// ToolSpecs returns the MCP ToolSpec for pane.search.
func (p *PaneSearchTools) ToolSpecs() []ToolSpec {
	return []ToolSpec{
		{
			Name:        ToolPaneSearch,
			Description: "Search across all panes: pane metadata, live PTY scrollback, recent agent chat turns, SQLite pane-linked records (Q&A, pages, context, commands, notes, documents), and external agent session transcripts. Returns ranked panes with matched snippets.",
			InputSchema: object([]string{}, props{
				"query":              text("search query (empty lists panes with recent excerpts)"),
				"source":             oneOf("restrict search to one source; default all", []string{"all", "scrollback", "chat", "records"}),
				"mode":               oneOf("query match mode; default substring", []string{"substring", "fuzzy", "regex"}),
				"includeArchived":    flag("also search archived panes"),
				"includeTranscripts": flag("include external session transcripts on disk; default true"),
				"limit":              props{"type": "integer", "description": "maximum number of ranked panes to return (default 20, 0 = unlimited)"},
			}),
		},
	}
}

// HasTool reports whether name is handled by PaneSearchTools.
func (p *PaneSearchTools) HasTool(name string) bool {
	return name == ToolPaneSearch
}

// Call runs pane.search with JSON arguments.
func (p *PaneSearchTools) Call(ctx context.Context, _ core.Actor, toolName string, rawArgs json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if toolName != ToolPaneSearch {
		return nil, errors.New("unknown tool " + toolName)
	}
	var args PaneSearchArgs
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	return p.Search(args)
}

// Search executes a global pane search over SQLite, optional live daemon state,
// and external session transcripts.
func (p *PaneSearchTools) Search(args PaneSearchArgs) (*panesearch.SearchResult, error) {
	if p == nil || p.store == nil {
		return nil, errors.New("pane.search: store is nil")
	}
	src, ok := panesearch.NormalizeSource(args.Source)
	if !ok {
		return nil, fmt.Errorf("invalid source %q (want all, scrollback, chat, or records)", args.Source)
	}
	mode := traits.MatchSubstring
	switch strings.ToLower(strings.TrimSpace(args.Mode)) {
	case "", string(traits.MatchSubstring):
		mode = traits.MatchSubstring
	case string(traits.MatchFuzzy):
		mode = traits.MatchFuzzy
	case string(traits.MatchRegex):
		mode = traits.MatchRegex
	default:
		return nil, fmt.Errorf("invalid mode %q (want substring, fuzzy, or regex)", args.Mode)
	}

	includeTranscripts := true
	if args.IncludeTranscripts != nil {
		includeTranscripts = *args.IncludeTranscripts
	}

	buildOpts := panesearch.BuildOptions{
		IncludeArchived:    args.IncludeArchived,
		IncludeTranscripts: includeTranscripts,
		MaxLogBytes:        panesearch.DefaultMaxLogBytes,
		HomeDir:            p.HomeDir,
		Cache:              p.cache,
	}
	if p.LiveData != nil {
		live := p.LiveData()
		buildOpts.Frames = live.Frames
		buildOpts.Turns = live.Turns
		buildOpts.TranscriptPaths = live.TranscriptPaths
		buildOpts.SessionIDs = live.SessionIDs
	}

	docs, err := panesearch.BuildDocuments(p.store, buildOpts)
	if err != nil {
		return nil, err
	}
	limit := args.Limit
	if limit < 0 {
		limit = 0
	}
	res := panesearch.Search(docs, args.Query, panesearch.SearchOptions{
		Source:      src,
		Mode:        mode,
		Limit:       limit,
		MaxSnippets: 6,
	})
	return &res, nil
}
