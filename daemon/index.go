package daemon

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/panesearch"
	"ofam.dev/ochazuke/core"
)

var (
	hookURLRe     = regexp.MustCompile(`https?://[^\s<>"{}|\\^` + "`" + `]+`)
	hookPRRefRe   = regexp.MustCompile(`(?:^|[\s(\[{"',;:])([A-Za-z0-9][A-Za-z0-9_.-]{0,38}/[A-Za-z0-9_.-]{1,100}#[1-9][0-9]*)\b`)
	hookGHPRURLRe = regexp.MustCompile(`^https?://github\.com/([A-Za-z0-9][A-Za-z0-9_.-]{0,38}/[A-Za-z0-9_.-]{1,100})/pull/([1-9][0-9]*)(?:[/?#].*)?$`)
	hookPathRe    = regexp.MustCompile(`[A-Za-z0-9@%+=_.~/-]+(?::([0-9]+)(?::([0-9]+))?)?`)
)

type extractedContext struct {
	title       string
	value       string
	contextKind string
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
		if strings.HasPrefix(strings.ToLower(item.value), "http") && !strings.HasPrefix(strings.ToLower(existing.Text("value")), "http") {
			patchFields["value"] = item.value
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
	seen := map[string]bool{}
	var out []extractedContext
	var spans [][2]int

	for _, loc := range hookURLRe.FindAllStringIndex(text, -1) {
		raw := text[loc[0]:loc[1]]
		cleaned := trimURLPunctuation(raw)
		if cleaned == "" {
			continue
		}
		spans = append(spans, [2]int{loc[0], loc[0] + len(cleaned)})

		var it extractedContext
		if m := hookGHPRURLRe.FindStringSubmatch(cleaned); len(m) == 3 {
			prRef := m[1] + "#" + m[2]
			it = extractedContext{
				title:       prRef,
				value:       cleaned,
				contextKind: "pr",
				canonKey:    "pr:" + strings.ToLower(prRef),
			}
		} else {
			it = extractedContext{
				title:       cleaned,
				value:       cleaned,
				contextKind: "url",
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
		if overlapsHookText(spans, start, end) {
			continue
		}
		spans = append(spans, [2]int{start, end})
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
			canonKey:    key,
		})
	}
	for _, m := range hookPathRe.FindAllStringSubmatchIndex(text, -1) {
		start, end := m[0], m[1]
		if overlapsHookText(spans, start, end) {
			continue
		}
		if item, ok := responseFile(text, m); ok {
			out = append(out, item)
		}
	}
	return out
}

func overlapsHookText(spans [][2]int, start, end int) bool {
	for _, span := range spans {
		if start < span[1] && end > span[0] {
			return true
		}
	}
	return false
}

func responseFile(text string, match []int) (extractedContext, bool) {
	start, end := match[0], match[1]
	pathEnd := end
	line, col := 0, 0
	if match[2] >= 0 {
		pathEnd = match[2] - 1
		line, _ = strconv.Atoi(text[match[2]:match[3]])
	}
	if match[4] >= 0 {
		col, _ = strconv.Atoi(text[match[4]:match[5]])
	}
	path := strings.TrimRight(text[start:pathEnd], ".")
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || clean == "/" {
		return extractedContext{}, false
	}

	explicit := strings.HasPrefix(path, "/") || strings.HasPrefix(path, "./") ||
		strings.HasPrefix(path, "../") || strings.HasPrefix(path, "~/")
	extension := filepath.Ext(clean)
	fileSuffix := len(extension) > 1 && unicode.IsLetter(rune(extension[1]))
	nested := strings.Contains(clean, "/")
	marked := start > 0 && end < len(text) && (text[start-1] == '`' && text[end] == '`' ||
		start > 1 && text[start-2:start] == "](" && text[end] == ')')
	if !explicit && !(nested && (fileSuffix || line > 0)) &&
		!knownResponseFileExtension(extension) && !(marked && fileSuffix) &&
		!(line > 0 && knownResponseFileName(clean)) {
		return extractedContext{}, false
	}
	value := path
	if pathEnd < end {
		value += text[pathEnd:end]
	}
	return extractedContext{
		title: filepath.Base(clean), value: value, contextKind: "file",
		canonKey: "file:" + clean, line: line, col: col,
	}, true
}

// Bare words need a known file suffix or a recognized extensionless filename
// with a line number. This leaves identifiers, dotted prose, and versions out.
func knownResponseFileExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case ".go", ".mod", ".sum",
		".md", ".markdown", ".txt", ".rst", ".org", ".pdf", ".docx",
		".json", ".jsonc", ".jsonl", ".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf", ".env", ".xml",
		".csv", ".tsv", ".sql", ".db", ".sqlite",
		".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".css", ".scss", ".html", ".htm", ".svg",
		".py", ".rb", ".rs", ".c", ".h", ".cc", ".cpp", ".hpp", ".java", ".kt", ".swift", ".zig", ".lua", ".php",
		".sh", ".bash", ".zsh", ".fish",
		".proto", ".graphql", ".gql", ".tf", ".hcl",
		".log", ".diff", ".patch", ".lock",
		".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func knownResponseFileName(name string) bool {
	switch strings.ToLower(name) {
	case "makefile", "dockerfile", "justfile", "procfile", "vagrantfile",
		"readme", "license", "changelog", "agents", "claude", "gemfile", "rakefile":
		return true
	default:
		return false
	}
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
