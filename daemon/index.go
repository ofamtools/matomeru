package daemon

import (
	"os"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/panesearch"
	"ofam.dev/ochazuke/core"
)

type extractedContext struct {
	title       string
	value       string
	contextKind string
	language    string
	canonKey    string
	line        int
	col         int
}

// indexHookEvent runs lightweight auto-indexing into s.cfg.Store for hook
// events carrying transcript metadata, Markdown documents, or final answers.
func (s *Server) indexHookEvent(ev HookEvent) {
	if s.cfg.Store == nil {
		return
	}
	transcriptPath := extractTranscriptPath(ev.Payload)
	sessionID := extractSessionID(ev.Payload)
	final := ""
	if ev.Kind == HookKindState && ev.State == StateDone && ev.SubagentID == "" && ev.Tool == "" {
		final = strings.TrimSpace(ev.FinalResponse)
	}
	hasContextWork := len(ev.Files) > 0 || final != ""
	if transcriptPath == "" && sessionID == "" && !hasContextWork {
		return
	}

	paneID := strings.TrimSpace(ev.PaneID)
	actorID := paneID
	if actorID == "" {
		actorID = "hook"
	}
	actor := core.Actor{Kind: "agent", ID: actorID, Label: ev.Harness}

	taskID := ""
	if paneID != "" {
		if pane, err := s.cfg.Store.Get(core.Ref{Kind: data.KindPane, ID: paneID}); err == nil {
			if t, ok := pane.RefField("task"); ok {
				taskID = t.ID
			}
			patchFields := map[string]any{}
			if transcriptPath != "" && pane.Text("transcriptPath") != transcriptPath {
				patchFields["transcriptPath"] = transcriptPath
			}
			if sessionID != "" && pane.Text("sessionId") != sessionID {
				patchFields["sessionId"] = sessionID
			}
			if len(patchFields) > 0 {
				_, _, _ = s.cfg.Store.Patch(data.PatchInput{
					Ref:     pane.Ref(),
					Fields:  patchFields,
					Summary: "record pane session telemetry",
				}, actor)
			}
		}
	}
	if !hasContextWork {
		return
	}

	// Markdown registration is independent of response context extraction.
	for _, rawPath := range ev.Files {
		p := strings.TrimSpace(rawPath)
		if p == "" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".md" || ext == ".markdown" {
			s.upsertHookDocument(p, paneID, taskID, actor)
		}
	}

	for _, item := range extractResponseContext(final) {
		s.upsertHookContext(item, paneID, taskID, actor)
	}
}

func (s *Server) upsertHookDocument(filePath, paneID, taskID string, actor core.Actor) {
	clean := filepath.Clean(filePath)
	title := markdownTitle(filePath)

	var existing *data.Record
	if res, err := s.cfg.Store.Query(data.Query{Kind: data.KindDocument, Query: filepath.Base(clean), FilterField: "filePath"}); err == nil {
		for i := range res.Items {
			rec := res.Items[i]
			if existingPath := strings.TrimSpace(rec.Text("filePath")); existingPath != "" && filepath.Clean(existingPath) == clean {
				cp := rec
				existing = &cp
				break
			}
		}
	}

	var docRef core.Ref
	if existing != nil {
		docRef = existing.Ref()
		if taskID != "" {
			if _, hasTask := existing.RefField("task"); !hasTask {
				_, _, _ = s.cfg.Store.Patch(data.PatchInput{
					Ref:     docRef,
					Fields:  map[string]any{"task": core.Ref{Kind: data.KindTask, ID: taskID}},
					Summary: "link document to pane task",
				}, actor)
			}
		}
	} else {
		fields := map[string]any{
			"title":    title,
			"filePath": filePath,
			"source":   data.DocumentSourceTool,
		}
		if taskID != "" {
			fields["task"] = core.Ref{Kind: data.KindTask, ID: taskID}
		}
		rec, _, err := s.cfg.Store.Create(data.CreateInput{Kind: data.KindDocument, Fields: fields}, actor)
		if err != nil {
			return
		}
		docRef = rec.Ref()
	}

	if paneID != "" {
		_, _, _ = s.cfg.Store.Relate(data.RelateInput{
			From: core.Ref{Kind: data.KindPane, ID: paneID},
			Kind: data.RelCreated,
			To:   docRef,
		}, actor)
	}
}

func (s *Server) upsertHookContext(item extractedContext, paneID, taskID string, actor core.Actor) {
	if item.value == "" || item.canonKey == "" {
		return
	}
	var existing *data.Record
	if res, err := s.cfg.Store.Query(data.Query{Kind: data.KindContext, Query: item.canonKey, FilterField: "canonKey"}); err == nil {
		for i := range res.Items {
			rec := res.Items[i]
			if rec.Text("canonKey") != item.canonKey {
				continue
			}
			recTask, hasRecTask := rec.RefField("task")
			recPane, hasRecPane := rec.RefField("pane")
			sameScope := false
			switch {
			case taskID != "":
				sameScope = (hasRecTask && recTask.ID == taskID) || (!hasRecTask && paneID != "" && hasRecPane && recPane.ID == paneID)
			case paneID != "":
				sameScope = !hasRecTask && hasRecPane && recPane.ID == paneID
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

	var ctxRef core.Ref
	if existing != nil {
		ctxRef = existing.Ref()
		patchFields := map[string]any{}
		if taskID != "" {
			if _, ok := existing.RefField("task"); !ok {
				patchFields["task"] = core.Ref{Kind: data.KindTask, ID: taskID}
			}
		}
		if paneID != "" {
			if _, ok := existing.RefField("pane"); !ok {
				patchFields["pane"] = core.Ref{Kind: data.KindPane, ID: paneID}
			}
		}
		if item.contextKind != "code" && strings.HasPrefix(strings.ToLower(item.value), "http") && !strings.HasPrefix(strings.ToLower(existing.Text("value")), "http") {
			patchFields["value"] = item.value
		}
		if item.language != "" && existing.Text("language") == "" {
			patchFields["language"] = item.language
		}
		if line := existing.Text("line"); (line == "" || line == "0") && item.line > 0 {
			patchFields["value"] = item.value
			patchFields["line"] = item.line
			if item.col > 0 {
				patchFields["col"] = item.col
			}
		}
		if src := existing.Text("source"); src != "response" && src != "curated" {
			patchFields["source"] = "response"
		}
		if len(patchFields) > 0 {
			_, _, _ = s.cfg.Store.Patch(data.PatchInput{
				Ref:     ctxRef,
				Fields:  patchFields,
				Summary: "update indexed context",
			}, actor)
		}
	} else {
		fields := map[string]any{
			"title":       item.title,
			"value":       item.value,
			"contextKind": item.contextKind,
			"source":      "response",
			"canonKey":    item.canonKey,
		}
		if item.language != "" {
			fields["language"] = item.language
		}
		if item.line > 0 {
			fields["line"] = item.line
		}
		if item.col > 0 {
			fields["col"] = item.col
		}
		if paneID != "" {
			fields["pane"] = core.Ref{Kind: data.KindPane, ID: paneID}
		}
		if taskID != "" {
			fields["task"] = core.Ref{Kind: data.KindTask, ID: taskID}
		}
		rec, _, err := s.cfg.Store.Create(data.CreateInput{Kind: data.KindContext, Fields: fields}, actor)
		if err != nil {
			return
		}
		ctxRef = rec.Ref()
	}

	if paneID != "" {
		_, _, _ = s.cfg.Store.Relate(data.RelateInput{
			From: core.Ref{Kind: data.KindPane, ID: paneID},
			Kind: data.RelContext,
			To:   ctxRef,
		}, actor)
	}
	if taskID != "" {
		_, _, _ = s.cfg.Store.Relate(data.RelateInput{
			From: core.Ref{Kind: data.KindTask, ID: taskID},
			Kind: data.RelContext,
			To:   ctxRef,
		}, actor)
	}
}

func markdownTitle(filePath string) string {
	if f, err := os.Open(filePath); err == nil {
		defer f.Close()
		buf := make([]byte, 4096)
		n, _ := f.Read(buf)
		for _, line := range strings.Split(string(buf[:n]), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
				if heading != "" {
					return heading
				}
			}
		}
	}
	return filepath.Base(filePath)
}

func extractResponseContext(text string) []extractedContext {
	text = panesearch.MaskSecrets(text)
	items := contextitems.Extract(text, contextitems.Options{Source: contextitems.SourceResponse})
	if len(items) == 0 {
		return nil
	}
	seen := map[string]int{}
	out := make([]extractedContext, 0, len(items))

	add := func(ec extractedContext) {
		if ec.value == "" || ec.canonKey == "" {
			return
		}
		if idx, exists := seen[ec.canonKey]; exists {
			if ec.contextKind != "code" && strings.HasPrefix(strings.ToLower(ec.value), "http") && !strings.HasPrefix(strings.ToLower(out[idx].value), "http") {
				out[idx].value = ec.value
			}
			if out[idx].line == 0 && ec.line > 0 {
				out[idx].value = ec.value
				out[idx].line = ec.line
				out[idx].col = ec.col
			}
			if out[idx].language == "" && ec.language != "" {
				out[idx].language = ec.language
				out[idx].title = ec.title
			}
			return
		}
		seen[ec.canonKey] = len(out)
		out = append(out, ec)
	}

	for _, it := range items {
		switch it.Kind {
		case contextitems.KindCode:
			title, canonKey := data.DeriveContextTitleAndCanon(it.Value, "code", "", it.Language)
			add(extractedContext{
				title:       title,
				value:       it.Value,
				contextKind: "code",
				language:    it.Language,
				canonKey:    canonKey,
			})
		case contextitems.KindURL, contextitems.KindOSC8:
			kind := data.ClassifyContextKind(it.Value, "")
			title, canonKey := data.DeriveContextTitleAndCanon(it.Value, kind, it.Label, "")
			add(extractedContext{
				title:       title,
				value:       it.Value,
				contextKind: kind,
				canonKey:    canonKey,
			})
		case contextitems.KindPR:
			title, canonKey := data.DeriveContextTitleAndCanon(it.Value, "pr", "", "")
			add(extractedContext{
				title:       title,
				value:       it.Value,
				contextKind: "pr",
				canonKey:    canonKey,
			})
		case contextitems.KindFile:
			p := it.Path
			if p == "" {
				p = it.Value
			}
			clean := filepath.Clean(p)
			if clean == "." || clean == ".." || clean == "/" {
				continue
			}
			add(extractedContext{
				title:       filepath.Base(clean),
				value:       it.Value,
				contextKind: "file",
				canonKey:    "file:" + clean,
				line:        it.Line,
				col:         it.Col,
			})
		}
	}
	return out
}
