// Package panesearch provides pure global search across panes: pane metadata,
// live PTY scrollback, hook telemetry chat turns, SQLite pane-linked records,
// and external agent session transcripts. It imports no UI or Bubble Tea packages.
package panesearch

import (
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
)

// Source filters which chunk categories are searched.
type Source string

const (
	SourceAll        Source = "all"
	SourceScrollback Source = "scrollback"
	SourceChat       Source = "chat"
	SourceRecords    Source = "records"
)

// AllSources lists the source filters in cycle order.
var AllSources = []Source{SourceAll, SourceScrollback, SourceChat, SourceRecords}

// Chunk badges displayed in search snippets and previews.
const (
	BadgeMeta       = "meta"
	BadgeScrollback = "scrollback"
	BadgeChat       = "chat"
	BadgeQA         = "qa"
	BadgePage       = "page"
	BadgeContext    = "context"
	BadgeCmd        = "cmd"
	BadgeNote       = "note"
	BadgeDoc        = "doc"
	BadgeTranscript = "transcript"
)

// Ranking score tiers: title/project > recent chat/Q&A > page/context/record > live scrollback > deep transcript.
const (
	ScoreTierMeta       = 1000
	ScoreTierChat       = 800
	ScoreTierRecord     = 600
	ScoreTierScrollback = 400
	ScoreTierTranscript = 200
)

// NormalizeSource parses a source name (defaulting empty to SourceAll).
func NormalizeSource(raw string) (Source, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(SourceAll):
		return SourceAll, true
	case string(SourceScrollback):
		return SourceScrollback, true
	case string(SourceChat):
		return SourceChat, true
	case string(SourceRecords):
		return SourceRecords, true
	default:
		return "", false
	}
}

// CycleSource advances all -> scrollback -> chat -> records -> all.
func CycleSource(cur Source) Source {
	for i, s := range AllSources {
		if s == cur {
			return AllSources[(i+1)%len(AllSources)]
		}
	}
	return SourceAll
}

// LiveFrame holds plain-text terminal history lines and a revision number for caching.
type LiveFrame struct {
	Lines    []string
	Revision uint64
}

// Turn is one recent prompt/response/summary turn from daemon hook telemetry.
type Turn struct {
	Prompt   string
	Response string
	Summary  string
	At       time.Time
}

// Chunk is one indexed source block belonging to a pane Document.
type Chunk struct {
	Sources []Source `json:"sources,omitempty"`
	Badge   string   `json:"badge"`
	Label   string   `json:"label,omitempty"`
	Weight  int      `json:"weight"`
	Lines   []string `json:"lines"`
}

// MatchesSource reports whether c participates in a search filtered by src.
func (c Chunk) MatchesSource(src Source) bool {
	if src == "" || src == SourceAll {
		return true
	}
	return slices.Contains(c.Sources, src)
}

// Document is the complete searchable corpus for a single pane.
type Document struct {
	Pane           data.Record `json:"-"`
	PaneID         string      `json:"paneId"`
	Title          string      `json:"title"`
	Project        string      `json:"project,omitempty"`
	TaskTitle      string      `json:"taskTitle,omitempty"`
	Harness        string      `json:"harness,omitempty"`
	Cwd            string      `json:"cwd,omitempty"`
	Command        string      `json:"command,omitempty"`
	RunStatus      string      `json:"runStatus,omitempty"`
	Archived       bool        `json:"archived,omitempty"`
	PromptedAt     time.Time   `json:"promptedAt,omitempty"`
	TranscriptPath string      `json:"transcriptPath,omitempty"`
	SessionID      string      `json:"sessionId,omitempty"`
	Chunks         []Chunk     `json:"chunks,omitempty"`
}

// Keywords returns searchable text lines for src (used by pickers and fast filtering).
func (d Document) Keywords(src Source) []string {
	var out []string
	for _, ch := range d.Chunks {
		if !ch.MatchesSource(src) {
			continue
		}
		if ch.Label != "" {
			out = append(out, ch.Label)
		}
		out = append(out, ch.Lines...)
	}
	return out
}

// Snippet is one matching excerpt with its source badge and surrounding context.
type Snippet struct {
	Badge  string   `json:"badge"`
	Label  string   `json:"label,omitempty"`
	Line   string   `json:"line"`
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
	Score  int      `json:"score"`
}

// Result is one ranked pane match returned by Search or MatchDocument.
type Result struct {
	PaneID         string    `json:"paneId"`
	Title          string    `json:"title"`
	Project        string    `json:"project,omitempty"`
	TaskTitle      string    `json:"taskTitle,omitempty"`
	Harness        string    `json:"harness,omitempty"`
	Cwd            string    `json:"cwd,omitempty"`
	RunStatus      string    `json:"runStatus,omitempty"`
	Archived       bool      `json:"archived,omitempty"`
	TranscriptPath string    `json:"transcriptPath,omitempty"`
	Score          int       `json:"score"`
	MatchCount     int       `json:"matchCount"`
	Badges         []string  `json:"badges,omitempty"`
	Snippets       []Snippet `json:"snippets,omitempty"`
}

// BuildOptions configures BuildDocuments.
type BuildOptions struct {
	IncludeArchived    bool
	IncludeTranscripts bool
	MaxLogBytes        int64
	HomeDir            string
	Frames             map[string]LiveFrame
	FallbackLines      map[string][]string
	Turns              map[string][]Turn
	TranscriptPaths    map[string]string
	SessionIDs         map[string]string
	Cache              *Cache
}

// SearchOptions configures Search.
type SearchOptions struct {
	Source      Source
	Mode        traits.MatchMode
	Limit       int
	MaxSnippets int
}

// SearchResult is the structured output of Search.
type SearchResult struct {
	Query     string           `json:"query"`
	Source    Source           `json:"source"`
	MatchMode traits.MatchMode `json:"matchMode"`
	FilterErr string           `json:"filterErr,omitempty"`
	Total     int              `json:"total"`
	Results   []Result         `json:"results"`
}

// BuildDocuments builds one searchable Document per pane from SQLite records,
// live PTY scrollback, hook telemetry turns, and optional disk transcripts.
// Panes are ordered by promptedAt descending (matching the TUI rail).
func BuildDocuments(store *data.Store, opts BuildOptions) ([]Document, error) {
	if store == nil {
		return nil, nil
	}
	paneRes, err := store.Query(data.Query{Kind: data.KindPane, IncludeArchived: opts.IncludeArchived})
	if err != nil {
		return nil, err
	}
	panes := paneRes.Items
	slices.SortStableFunc(panes, func(a, b data.Record) int {
		return b.Time("promptedAt").Compare(a.Time("promptedAt"))
	})

	tasksByID := map[string]data.Record{}
	if taskRes, err := store.Query(data.Query{Kind: data.KindTask, IncludeArchived: true}); err == nil {
		for _, t := range taskRes.Items {
			tasksByID[t.ID] = t
		}
	}

	paneQuestions := map[string][]data.Record{}
	panePages := map[string][]data.Record{}
	paneContext := map[string][]data.Record{}
	paneCommands := map[string][]data.Record{}
	paneNotes := map[string][]data.Record{}
	paneDocs := map[string][]data.Record{}

	collectByPane := func(kind string, dst map[string][]data.Record) {
		res, qerr := store.Query(data.Query{Kind: kind, IncludeArchived: opts.IncludeArchived})
		if qerr != nil {
			return
		}
		for _, rec := range res.Items {
			if pRef, ok := rec.RefField("pane"); ok && pRef.Kind == data.KindPane && pRef.ID != "" {
				dst[pRef.ID] = append(dst[pRef.ID], rec)
			}
		}
	}
	collectByPane(data.KindQuestion, paneQuestions)
	collectByPane(data.KindPage, panePages)
	collectByPane(data.KindContext, paneContext)
	collectByPane(data.KindCommand, paneCommands)
	collectByPane(data.KindNote, paneNotes)
	collectByPane(data.KindDocument, paneDocs)

	docs := make([]Document, 0, len(panes))
	for _, pane := range panes {
		paneID := pane.ID
		title := pane.Text("title")
		if strings.TrimSpace(title) == "" {
			title = paneID
		}
		taskTitle, projTitle := resolvePaneTaskTitles(pane, tasksByID)
		harness := pane.Text("harness")
		cwd := pane.Text("cwd")
		command := pane.Text("command")
		runStatus := pane.Text("runStatus")

		sessionID := strings.TrimSpace(opts.SessionIDs[paneID])
		if sessionID == "" {
			sessionID = strings.TrimSpace(pane.Text("sessionId"))
		}
		explicitTranscript := strings.TrimSpace(opts.TranscriptPaths[paneID])
		if explicitTranscript == "" {
			explicitTranscript = strings.TrimSpace(pane.Text("transcriptPath"))
		}

		doc := Document{
			Pane:       pane,
			PaneID:     paneID,
			Title:      title,
			Project:    projTitle,
			TaskTitle:  taskTitle,
			Harness:    harness,
			Cwd:        cwd,
			Command:    command,
			RunStatus:  runStatus,
			Archived:   pane.IsArchived(),
			PromptedAt: pane.Time("promptedAt"),
			SessionID:  sessionID,
		}

		// 1. Pane metadata chunk.
		var metaLines []string
		for _, s := range []string{title, projTitle, taskTitle, harness, cwd, command} {
			if s = strings.TrimSpace(s); s != "" {
				metaLines = append(metaLines, s)
			}
		}
		if len(metaLines) > 0 {
			doc.Chunks = append(doc.Chunks, Chunk{
				Sources: []Source{SourceAll},
				Badge:   BadgeMeta,
				Weight:  ScoreTierMeta,
				Lines:   metaLines,
			})
		}

		// 2. Live PTY scrollback + screen (or fallback synthetic terminal lines).
		var scrollLines []string
		if fr, ok := opts.Frames[paneID]; ok && len(fr.Lines) > 0 {
			scrollLines = opts.Cache.FrameLines(paneID, fr.Revision, fr.Lines)
		} else if fb := opts.FallbackLines[paneID]; len(fb) > 0 {
			scrollLines = opts.Cache.FrameLines(paneID, 0, fb)
		}
		if len(scrollLines) > 0 {
			doc.Chunks = append(doc.Chunks, Chunk{
				Sources: []Source{SourceScrollback},
				Badge:   BadgeScrollback,
				Weight:  ScoreTierScrollback,
				Lines:   scrollLines,
			})
		}

		// 3a. Hook telemetry recent turns ([chat]).
		if turns := opts.Turns[paneID]; len(turns) > 0 {
			var chatLines []string
			for _, turn := range turns {
				if p := strings.TrimSpace(turn.Prompt); p != "" {
					appendSanitizedLines(&chatLines, "user", p)
				}
				if r := strings.TrimSpace(turn.Response); r != "" {
					appendSanitizedLines(&chatLines, "assistant", r)
				}
				if turn.Prompt == "" && turn.Response == "" && strings.TrimSpace(turn.Summary) != "" {
					appendSanitizedLines(&chatLines, "", turn.Summary)
				}
			}
			if len(chatLines) > 0 {
				doc.Chunks = append(doc.Chunks, Chunk{
					Sources: []Source{SourceChat},
					Badge:   BadgeChat,
					Weight:  ScoreTierChat,
					Lines:   chatLines,
				})
			}
		}

		// 3b. SQLite pane-linked records.
		for _, q := range paneQuestions[paneID] {
			var lines []string
			appendSanitizedLines(&lines, "Q", q.Text("prompt"))
			appendSanitizedLines(&lines, "", q.Text("details"))
			appendSanitizedLines(&lines, "A", q.Text("answer"))
			if len(lines) > 0 {
				doc.Chunks = append(doc.Chunks, Chunk{
					Sources: []Source{SourceChat, SourceRecords},
					Badge:   BadgeQA,
					Label:   q.Text("prompt"),
					Weight:  ScoreTierChat,
					Lines:   lines,
				})
			}
		}

		for _, pg := range panePages[paneID] {
			var lines []string
			pgTitle := pg.Text("title")
			appendSanitizedLines(&lines, "", pgTitle)
			if rootVal, ok := pg.Value("root"); ok {
				flattenViewNodeValue(rootVal, &lines)
			}
			if len(lines) > 0 {
				doc.Chunks = append(doc.Chunks, Chunk{
					Sources: []Source{SourceRecords},
					Badge:   BadgePage,
					Label:   pgTitle,
					Weight:  ScoreTierRecord,
					Lines:   lines,
				})
			}
		}

		for _, ctxRec := range paneContext[paneID] {
			var lines []string
			appendSanitizedLines(&lines, "", ctxRec.Text("title"))
			appendSanitizedLines(&lines, "", ctxRec.Text("value"))
			if len(lines) > 0 {
				doc.Chunks = append(doc.Chunks, Chunk{
					Sources: []Source{SourceRecords},
					Badge:   BadgeContext,
					Label:   ctxRec.Text("title"),
					Weight:  ScoreTierRecord,
					Lines:   lines,
				})
			}
		}

		for _, cmdRec := range paneCommands[paneID] {
			var lines []string
			appendSanitizedLines(&lines, "", cmdRec.Text("title"))
			appendSanitizedLines(&lines, "", cmdRec.Text("command"))
			appendSanitizedLines(&lines, "", cmdRec.Text("notes"))
			if len(lines) > 0 {
				doc.Chunks = append(doc.Chunks, Chunk{
					Sources: []Source{SourceRecords},
					Badge:   BadgeCmd,
					Label:   cmdRec.Text("title"),
					Weight:  ScoreTierRecord,
					Lines:   lines,
				})
			}
		}

		// Notes & Documents linked by pane field OR relation (about, created, context).
		seenRecs := map[core.Ref]bool{}
		addNote := func(rec data.Record) {
			if seenRecs[rec.Ref()] || (rec.IsArchived() && !opts.IncludeArchived) {
				return
			}
			seenRecs[rec.Ref()] = true
			var lines []string
			appendSanitizedLines(&lines, "", rec.Text("title"))
			appendSanitizedLines(&lines, "", rec.Text("body"))
			if len(lines) > 0 {
				doc.Chunks = append(doc.Chunks, Chunk{
					Sources: []Source{SourceRecords},
					Badge:   BadgeNote,
					Label:   rec.Text("title"),
					Weight:  ScoreTierRecord,
					Lines:   lines,
				})
			}
		}
		addDoc := func(rec data.Record) {
			if seenRecs[rec.Ref()] || (rec.IsArchived() && !opts.IncludeArchived) {
				return
			}
			seenRecs[rec.Ref()] = true
			var lines []string
			appendSanitizedLines(&lines, "", rec.Text("title"))
			appendSanitizedLines(&lines, "", rec.Text("summary"))
			appendSanitizedLines(&lines, "", rec.Text("filePath"))
			appendSanitizedLines(&lines, "", rec.Text("body"))
			if len(lines) > 0 {
				doc.Chunks = append(doc.Chunks, Chunk{
					Sources: []Source{SourceRecords},
					Badge:   BadgeDoc,
					Label:   rec.Text("title"),
					Weight:  ScoreTierRecord,
					Lines:   lines,
				})
			}
		}
		for _, n := range paneNotes[paneID] {
			addNote(n)
		}
		for _, d := range paneDocs[paneID] {
			addDoc(d)
		}
		if rels, rerr := store.Related(pane.Ref(), "", data.Both); rerr == nil {
			for _, rel := range rels {
				other := rel.Other(pane.Ref())
				if other.Kind != data.KindNote && other.Kind != data.KindDocument {
					continue
				}
				if seenRecs[other] {
					continue
				}
				rec, gerr := store.Get(other)
				if gerr != nil || rec == nil {
					continue
				}
				if rec.Kind == data.KindNote {
					addNote(*rec)
				} else {
					addDoc(*rec)
				}
			}
		}

		// 4. External agent session transcript ([transcript]).
		if opts.IncludeTranscripts {
			tp := ResolveTranscriptPath(harness, cwd, sessionID, explicitTranscript, opts.HomeDir)
			if tp != "" {
				doc.TranscriptPath = tp
				var tLines []string
				var terr error
				if opts.Cache != nil {
					tLines, terr = opts.Cache.ReadTranscript(tp, opts.MaxLogBytes)
				} else {
					tLines, terr = ReadTranscript(tp, opts.MaxLogBytes)
				}
				if terr == nil && len(tLines) > 0 {
					doc.Chunks = append(doc.Chunks, Chunk{
						Sources: []Source{SourceChat},
						Badge:   BadgeTranscript,
						Weight:  ScoreTierTranscript,
						Lines:   tLines,
					})
				}
			}
		}

		docs = append(docs, doc)
	}
	return docs, nil
}

// Search matches query against docs using traits.CompileFilter and returns
// ranked pane results with snippets.
func Search(docs []Document, query string, opts SearchOptions) SearchResult {
	src, ok := NormalizeSource(string(opts.Source))
	if !ok {
		src = SourceAll
	}
	mode := opts.Mode
	if mode == "" {
		mode = traits.MatchSubstring
	}
	maxSnippets := opts.MaxSnippets
	if maxSnippets <= 0 {
		maxSnippets = 6
	}

	matcher, filterErr := traits.CompileFilter(query, mode, traits.FilterConfig{})
	out := SearchResult{
		Query:     query,
		Source:    src,
		MatchMode: mode,
		FilterErr: filterErr,
		Results:   []Result{},
	}

	var matched []Result
	for _, doc := range docs {
		if res, hit := MatchDocument(doc, matcher, src, maxSnippets); hit {
			matched = append(matched, res)
		}
	}

	if strings.TrimSpace(query) != "" {
		slices.SortStableFunc(matched, func(a, b Result) int {
			return b.Score - a.Score
		})
	}

	out.Total = len(matched)
	if opts.Limit > 0 && len(matched) > opts.Limit {
		matched = matched[:opts.Limit]
	}
	out.Results = matched
	return out
}

// ScoreDocument computes the ranking score of doc against query/mode/source.
// Returns 0 when doc does not match.
func ScoreDocument(doc Document, query string, mode traits.MatchMode, src Source) int {
	matcher, _ := traits.CompileFilter(query, mode, traits.FilterConfig{})
	res, ok := MatchDocument(doc, matcher, src, 1)
	if !ok {
		return 0
	}
	return res.Score
}

// MatchDocument evaluates doc against a compiled matcher and source filter,
// returning its aggregated score, matched badges, and top snippets with
// surrounding context lines.
func MatchDocument(doc Document, matcher traits.Matcher, src Source, maxSnippets int) (Result, bool) {
	if src == "" {
		src = SourceAll
	}
	if maxSnippets <= 0 {
		maxSnippets = 6
	}

	res := Result{
		PaneID:         doc.PaneID,
		Title:          doc.Title,
		Project:        doc.Project,
		TaskTitle:      doc.TaskTitle,
		Harness:        doc.Harness,
		Cwd:            doc.Cwd,
		RunStatus:      doc.RunStatus,
		Archived:       doc.Archived,
		TranscriptPath: doc.TranscriptPath,
	}

	// Empty query: include pane if it has chunks for src (or always for SourceAll),
	// returning recent preview snippets.
	if matcher.Match("") && !matcherNonEmpty(matcher) {
		var previews []Snippet
		seenBadge := map[string]bool{}
		hasSourceChunk := src == SourceAll
		for _, ch := range doc.Chunks {
			if !ch.MatchesSource(src) {
				continue
			}
			hasSourceChunk = true
			if ch.Badge == BadgeMeta || len(ch.Lines) == 0 {
				continue
			}
			if !seenBadge[ch.Badge] {
				seenBadge[ch.Badge] = true
				res.Badges = append(res.Badges, ch.Badge)
			}
			if len(previews) < maxSnippets {
				idx := len(ch.Lines) - 1
				previews = append(previews, makeSnippet(ch, idx, ch.Weight))
			}
		}
		if !hasSourceChunk {
			return Result{}, false
		}
		res.Snippets = previews
		return res, true
	}

	bestTier := 0
	totalMatches := 0
	seenBadge := map[string]bool{}
	var snippets []Snippet

	for _, ch := range doc.Chunks {
		if !ch.MatchesSource(src) {
			continue
		}
		chunkHits := 0
		n := len(ch.Lines)
		// Walk newest-first for scrollback/chat/transcript so recent hits surface first.
		reverse := ch.Badge == BadgeScrollback || ch.Badge == BadgeChat || ch.Badge == BadgeTranscript
		for step := range n {
			i := step
			if reverse {
				i = n - 1 - step
			}
			line := ch.Lines[i]
			if !matcher.Match(line) {
				continue
			}
			totalMatches++
			chunkHits++
			score := ch.Weight
			if reverse && n > 1 && i >= n/2 {
				score += 20 // mild recency tie-break inside the same tier
			}
			if score > bestTier {
				bestTier = score
			}
			if !seenBadge[ch.Badge] {
				seenBadge[ch.Badge] = true
				res.Badges = append(res.Badges, ch.Badge)
			}
			if chunkHits <= 3 {
				snippets = append(snippets, makeSnippet(ch, i, score))
			}
		}
	}

	if totalMatches == 0 {
		return Result{}, false
	}

	slices.SortStableFunc(snippets, func(a, b Snippet) int {
		return b.Score - a.Score
	})
	if len(snippets) > maxSnippets {
		snippets = snippets[:maxSnippets]
	}

	res.MatchCount = totalMatches
	res.Score = bestTier + min(totalMatches*5, 50)
	res.Snippets = snippets
	return res, true
}

func matcherNonEmpty(m traits.Matcher) bool {
	// Zero/empty matcher matches a sentinel string with zero highlighted runes.
	for _, hit := range m.MatchRunes("panesearch_probe_token") {
		if hit {
			return true
		}
	}
	return !m.Match("panesearch_probe_token")
}

func makeSnippet(ch Chunk, idx, score int) Snippet {
	sn := Snippet{
		Badge: ch.Badge,
		Label: ch.Label,
		Line:  ch.Lines[idx],
		Score: score,
	}
	if ch.Badge != BadgeMeta {
		if idx > 0 {
			sn.Before = []string{ch.Lines[idx-1]}
		}
		if idx+1 < len(ch.Lines) {
			sn.After = []string{ch.Lines[idx+1]}
		}
	}
	return sn
}

func resolvePaneTaskTitles(pane data.Record, tasks map[string]data.Record) (taskTitle, projectTitle string) {
	tRef, ok := pane.RefField("task")
	if !ok || tRef.Kind != data.KindTask || tRef.ID == "" {
		return "", ""
	}
	cur, ok := tasks[tRef.ID]
	if !ok {
		return "", ""
	}
	taskTitle = cur.Text("title")
	seen := map[string]bool{cur.ID: true}
	for range 32 {
		pRef, hasParent := cur.RefField("parent")
		if !hasParent || pRef.Kind != data.KindTask || pRef.ID == "" || seen[pRef.ID] {
			break
		}
		parent, exists := tasks[pRef.ID]
		if !exists {
			break
		}
		seen[parent.ID] = true
		cur = parent
	}
	projectTitle = cur.Text("title")
	return taskTitle, projectTitle
}

// flattenViewNodeValue recursively extracts readable text lines from a stored
// ui.page ViewNode JSON map without importing ochazuke/ui.
func flattenViewNodeValue(v any, out *[]string) {
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return
	}
	if title, ok := m["title"].(string); ok && strings.TrimSpace(title) != "" {
		appendSanitizedLines(out, "", title)
	}
	if props, ok := m["props"].(map[string]any); ok {
		for _, k := range []string{"markdown", "text", "summary", "details", "prompt", "answer", "url", "filePath", "path", "command", "label"} {
			if s, ok := props[k].(string); ok && strings.TrimSpace(s) != "" {
				appendSanitizedLines(out, "", s)
			}
		}
		if choices, ok := props["choices"].([]any); ok {
			for _, ch := range choices {
				if cm, ok := ch.(map[string]any); ok {
					for _, k := range []string{"label", "description"} {
						if s, ok := cm[k].(string); ok && strings.TrimSpace(s) != "" {
							appendSanitizedLines(out, "", s)
						}
					}
				}
			}
		}
	}
	if children, ok := m["children"].([]any); ok {
		for _, child := range children {
			flattenViewNodeValue(child, out)
		}
	}
}
