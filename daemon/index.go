package daemon

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

var (
	hookURLRe     = regexp.MustCompile(`https?://[^\s<>"{}|\\^` + "`" + `]+`)
	hookPRRefRe   = regexp.MustCompile(`(?:^|[\s(\[{"',;:])([A-Za-z0-9][A-Za-z0-9_.-]{0,38}/[A-Za-z0-9_.-]{1,100}#[1-9][0-9]*)\b`)
	hookGHPRURLRe = regexp.MustCompile(`^https?://github\.com/([A-Za-z0-9][A-Za-z0-9_.-]{0,38}/[A-Za-z0-9_.-]{1,100})/pull/([1-9][0-9]*)(?:[/?#].*)?$`)
)

type extractedContext struct {
	title       string
	value       string
	contextKind string
	source      string
	canonKey    string
}

// indexHookEvent runs lightweight auto-indexing into s.cfg.Store for hook
// events carrying transcript metadata, written files, user prompts, or
// assistant responses.
func (s *Server) indexHookEvent(ev HookEvent) {
	if s.cfg.Store == nil {
		return
	}
	transcriptPath := extractTranscriptPath(ev.Payload)
	sessionID := extractSessionID(ev.Payload)
	hasContextWork := len(ev.Files) > 0 || strings.TrimSpace(ev.Prompt) != "" || strings.TrimSpace(ev.Response) != ""
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

	// 1. Written/edited files from PostToolUse.
	for _, rawPath := range ev.Files {
		p := strings.TrimSpace(rawPath)
		if p == "" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".md" || ext == ".markdown" {
			s.upsertHookDocument(p, paneID, taskID, actor)
		} else {
			s.upsertHookContext(extractedContext{
				title:       filepath.Base(p),
				value:       p,
				contextKind: "file",
				source:      "tool",
				canonKey:    "file:" + filepath.Clean(p),
			}, paneID, taskID, actor)
		}
	}

	// 2. URLs and owner/repo#123 PR refs from Prompt and Response.
	if prompt := strings.TrimSpace(ev.Prompt); prompt != "" {
		for _, item := range extractTextContext(prompt, "prompt") {
			s.upsertHookContext(item, paneID, taskID, actor)
		}
	}
	if resp := strings.TrimSpace(ev.Response); resp != "" {
		for _, item := range extractTextContext(resp, "response") {
			s.upsertHookContext(item, paneID, taskID, actor)
		}
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
		if strings.HasPrefix(strings.ToLower(item.value), "http") && !strings.HasPrefix(strings.ToLower(existing.Text("value")), "http") {
			patchFields["value"] = item.value
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
			"source":      item.source,
			"canonKey":    item.canonKey,
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

func extractTextContext(text, source string) []extractedContext {
	seen := map[string]bool{}
	var out []extractedContext
	var urlSpans [][2]int

	for _, loc := range hookURLRe.FindAllStringIndex(text, -1) {
		raw := text[loc[0]:loc[1]]
		cleaned := trimURLPunctuation(raw)
		if cleaned == "" {
			continue
		}
		urlSpans = append(urlSpans, [2]int{loc[0], loc[0] + len(cleaned)})

		var it extractedContext
		if m := hookGHPRURLRe.FindStringSubmatch(cleaned); len(m) == 3 {
			prRef := m[1] + "#" + m[2]
			it = extractedContext{
				title:       prRef,
				value:       cleaned,
				contextKind: "pr",
				source:      source,
				canonKey:    "pr:" + strings.ToLower(prRef),
			}
		} else {
			it = extractedContext{
				title:       cleaned,
				value:       cleaned,
				contextKind: "url",
				source:      source,
				canonKey:    "url:" + strings.ToLower(strings.TrimRight(cleaned, "/")),
			}
		}
		if !seen[it.canonKey] {
			seen[it.canonKey] = true
			out = append(out, it)
		}
	}

	for _, m := range hookPRRefRe.FindAllStringSubmatchIndex(text, -1) {
		if len(m) < 4 {
			continue
		}
		start, end := m[2], m[3]
		inURL := false
		for _, sp := range urlSpans {
			if start >= sp[0] && end <= sp[1] {
				inURL = true
				break
			}
		}
		if inURL {
			continue
		}
		ref := text[start:end]
		key := "pr:" + strings.ToLower(ref)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, extractedContext{
			title:       ref,
			value:       ref,
			contextKind: "pr",
			source:      source,
			canonKey:    key,
		})
	}
	return out
}

func trimURLPunctuation(u string) string {
	for len(u) > 0 {
		last := u[len(u)-1]
		switch last {
		case '.', ',', ';', ':', '!', '?', '"', '\'', '>', ']':
			u = u[:len(u)-1]
		case ')':
			if strings.Count(u, ")") > strings.Count(u, "(") {
				u = u[:len(u)-1]
			} else {
				return u
			}
		default:
			return u
		}
	}
	return u
}
