package mdweb

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config configures an mdweb HTTP/SSE Handler.
type Config struct {
	RootDir           string
	DefaultFile       string
	BasePath          string
	AllowAnyLocalFile bool
}

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

type loadedDoc struct {
	absPath, displayFile, source string
	ir                           *DocumentIR
}

type actionSubscriber struct {
	fileFilter string
	ch         chan string
}

type filePoller struct {
	stopCh   chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	lastRev  string
}

func (p *filePoller) stop() { p.stopOnce.Do(func() { close(p.stopCh) }) }

// Handler serves the interactive Markdown/Mermaid web UI, REST APIs, and SSE streams.
type Handler struct {
	cfg       Config
	rootDir   string
	evalRoot  string
	basePath  string
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup

	mu         sync.Mutex
	seq        int64
	actionLog  []ActionEvent
	recent     []string
	fileLocks  map[string]*sync.Mutex
	docSubs    map[string]map[chan string]struct{}
	pollers    map[string]*filePoller
	actionSubs map[*actionSubscriber]struct{}
}

var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, ".worktrees": true, "vendor": true, "dist": true,
}

var mimeByExt = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".svg": "image/svg+xml",
	".bmp": "image/bmp", ".ico": "image/x-icon", ".avif": "image/avif",
	".css": "text/css; charset=utf-8", ".json": "application/json; charset=utf-8",
	".mmd": "text/plain; charset=utf-8", ".mermaid": "text/plain; charset=utf-8",
	".md": "text/markdown; charset=utf-8", ".markdown": "text/markdown; charset=utf-8",
}

// NewHandler constructs a new mdweb HTTP handler.
func NewHandler(cfg Config) *Handler {
	root := cfg.RootDir
	if root == "" && !cfg.AllowAnyLocalFile {
		root = "."
	}
	var absRoot, evalRoot string
	if root != "" {
		if a, err := filepath.Abs(root); err == nil {
			absRoot = filepath.Clean(a)
		} else {
			absRoot = filepath.Clean(root)
		}
		if e, err := filepath.EvalSymlinks(absRoot); err == nil {
			evalRoot = filepath.Clean(e)
		} else {
			evalRoot = absRoot
		}
	}
	return &Handler{
		cfg:        cfg,
		rootDir:    absRoot,
		evalRoot:   evalRoot,
		basePath:   normalizeBasePath(cfg.BasePath),
		done:       make(chan struct{}),
		fileLocks:  make(map[string]*sync.Mutex),
		docSubs:    make(map[string]map[chan string]struct{}),
		pollers:    make(map[string]*filePoller),
		actionSubs: make(map[*actionSubscriber]struct{}),
	}
}

// Close shuts down all SSE streams and background pollers.
func (h *Handler) Close() {
	h.closeOnce.Do(func() { close(h.done) })
	h.wg.Wait()
}

// ServeHTTP dispatches all mdweb routes after stripping Config.BasePath.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path, ok := h.stripBasePath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(path, "/static/") {
		if serveStaticAsset(w, r, strings.TrimPrefix(path, "/static/")) {
			return
		}
		http.NotFound(w, r)
		return
	}
	switch path {
	case "/", "/view":
		h.handleView(w, r)
	case "/api/files":
		h.handleFiles(w, r)
	case "/api/doc":
		h.handleDoc(w, r)
	case "/api/mutate":
		h.handleMutate(w, r)
	case "/api/mermaid/flip":
		h.handleMermaidFlip(w, r)
	case "/api/events":
		h.handleEvents(w, r)
	case "/api/actions":
		h.handleActions(w, r)
	case "/api/actions/stream":
		h.handleActionStream(w, r)
	case "/assets":
		h.handleAssets(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) stripBasePath(p string) (string, bool) {
	if h.basePath == "" {
		if p == "" {
			return "/", true
		}
		return p, true
	}
	if p == h.basePath {
		return "/", true
	}
	if strings.HasPrefix(p, h.basePath+"/") {
		return p[len(h.basePath):], true
	}
	return "", false
}

func isRenderableFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown", ".mmd", ".mermaid":
		return true
	}
	return false
}

func discoverFiles(rootDir string, maxDepth int) []string {
	if rootDir == "" {
		return nil
	}
	var results []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, ent := range entries {
			name := ent.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			full := filepath.Join(dir, name)
			if ent.IsDir() {
				if depth < maxDepth && !skipDirs[name] {
					walk(full, depth+1)
				}
			} else if isRenderableFile(name) {
				if rel, err := filepath.Rel(rootDir, full); err == nil {
					results = append(results, filepath.ToSlash(rel))
				}
			}
		}
	}
	walk(rootDir, 0)
	sort.Strings(results)
	return results
}

func containsPath(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (h *Handler) checkJailed(target string) error {
	if !containsPath(h.rootDir, target) {
		return &httpError{http.StatusForbidden, "path escapes served root directory"}
	}
	if eval, err := filepath.EvalSymlinks(target); err == nil && !containsPath(h.evalRoot, filepath.Clean(eval)) {
		return &httpError{http.StatusForbidden, "path escapes served root directory"}
	}
	return nil
}

func (h *Handler) resolveJailedPath(relPath string) (string, string, error) {
	if relPath == "" || strings.ContainsRune(relPath, 0) {
		return "", "", &httpError{http.StatusBadRequest, "invalid file path"}
	}
	if filepath.IsAbs(relPath) || strings.HasPrefix(relPath, "/") || strings.HasPrefix(relPath, `\`) {
		return "", "", &httpError{http.StatusForbidden, "path escapes served root directory"}
	}
	abs := filepath.Clean(filepath.Join(h.rootDir, filepath.FromSlash(relPath)))
	if err := h.checkJailed(abs); err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(h.rootDir, abs)
	if err != nil {
		return "", "", &httpError{http.StatusForbidden, "path escapes served root directory"}
	}
	return abs, filepath.ToSlash(rel), nil
}

func (h *Handler) resolveDocPath(fileParam string) (string, string, error) {
	if strings.ContainsRune(fileParam, 0) {
		return "", "", &httpError{http.StatusBadRequest, "invalid file path"}
	}
	if !h.cfg.AllowAnyLocalFile {
		return h.resolveJailedPath(fileParam)
	}
	if fileParam == "" {
		return "", "", &httpError{http.StatusBadRequest, "missing file parameter"}
	}
	if !filepath.IsAbs(fileParam) {
		return "", "", &httpError{http.StatusBadRequest, "file path must be absolute"}
	}
	cleaned := filepath.Clean(fileParam)
	if !isRenderableFile(cleaned) {
		return "", "", &httpError{http.StatusForbidden, "unsupported file type"}
	}
	return cleaned, cleaned, nil
}

func hasSchemePrefix(raw string) bool {
	if idx := strings.IndexByte(raw, ':'); idx >= 0 {
		slash := strings.IndexAny(raw, `/\`)
		return slash == -1 || idx < slash
	}
	return false
}

func (h *Handler) resolveRelativeAsset(docAbsPath, src string, mermaidOnly bool) (string, error) {
	src = strings.TrimSpace(src)
	if src == "" || strings.ContainsRune(src, 0) || hasSchemePrefix(src) {
		return "", &httpError{http.StatusBadRequest, "invalid relative path"}
	}
	if filepath.IsAbs(src) || strings.HasPrefix(src, "/") || strings.HasPrefix(src, `\`) {
		return "", &httpError{http.StatusForbidden, "absolute asset paths are not allowed"}
	}
	ext := strings.ToLower(filepath.Ext(src))
	if mermaidOnly {
		if !isRenderableFile(src) {
			return "", &httpError{http.StatusForbidden, "unsupported mermaid source extension"}
		}
	} else if _, ok := mimeByExt[ext]; !ok {
		return "", &httpError{http.StatusForbidden, "unsupported asset file type"}
	}

	baseDir := h.rootDir
	if docAbsPath != "" {
		baseDir = filepath.Dir(docAbsPath)
	}
	target := filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(src)))
	if !h.cfg.AllowAnyLocalFile {
		if err := h.checkJailed(target); err != nil {
			return "", err
		}
		return target, nil
	}

	depth := 0
	for _, seg := range strings.Split(filepath.ToSlash(baseDir), "/") {
		if seg != "" {
			depth++
		}
	}
	cur := depth
	for _, seg := range strings.Split(filepath.ToSlash(src), "/") {
		if seg == ".." {
			cur--
			if cur < 0 {
				return "", &httpError{http.StatusForbidden, "path traversal above root"}
			}
		} else if seg != "" && seg != "." {
			cur++
		}
	}
	return target, nil
}

func (h *Handler) loadDocument(fileParam string) (*loadedDoc, error) {
	absPath, displayFile, err := h.resolveDocPath(fileParam)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &httpError{http.StatusNotFound, "file not found: " + displayFile}
		}
		return nil, &httpError{http.StatusInternalServerError, err.Error()}
	}
	if info.IsDir() {
		return nil, &httpError{http.StatusNotFound, "path is a directory: " + displayFile}
	}
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return nil, &httpError{http.StatusInternalServerError, err.Error()}
	}
	source := string(raw)
	ir := ParseMarkdown(source, ParseOptions{
		File: displayFile,
		ResolveExternalMermaid: func(embedRel string) (string, error) {
			target, err := h.resolveRelativeAsset(absPath, embedRel, true)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(target)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return "", fmt.Errorf("mermaid source file not found: %s", embedRel)
				}
				return "", err
			}
			return string(data), nil
		},
	})
	if h.cfg.AllowAnyLocalFile {
		h.recordRecentFile(displayFile)
	}
	return &loadedDoc{absPath: absPath, displayFile: displayFile, source: source, ir: ir}, nil
}

func (h *Handler) recordRecentFile(file string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	next := make([]string, 0, min(len(h.recent)+1, 30))
	next = append(next, file)
	for _, existing := range h.recent {
		if existing != file && len(next) < 30 {
			next = append(next, existing)
		}
	}
	h.recent = next
}

func (h *Handler) recentFilesSnapshot() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.recent))
	copy(out, h.recent)
	return out
}

func (h *Handler) filesForDocument(doc *loadedDoc) []string {
	if !h.cfg.AllowAnyLocalFile {
		files := discoverFiles(h.rootDir, 2)
		for _, f := range files {
			if f == doc.displayFile {
				return files
			}
		}
		return append([]string{doc.displayFile}, files...)
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(doc.displayFile)
	if entries, err := os.ReadDir(filepath.Dir(doc.absPath)); err == nil {
		var siblings []string
		for _, ent := range entries {
			if !ent.IsDir() && !strings.HasPrefix(ent.Name(), ".") && isRenderableFile(ent.Name()) {
				siblings = append(siblings, filepath.Join(filepath.Dir(doc.absPath), ent.Name()))
			}
		}
		sort.Strings(siblings)
		for _, s := range siblings {
			add(s)
		}
	}
	for _, r := range h.recentFilesSnapshot() {
		add(r)
	}
	return out
}

func (h *Handler) pickDefaultFile() string {
	if h.cfg.AllowAnyLocalFile {
		return ""
	}
	files := discoverFiles(h.rootDir, 2)
	if h.cfg.DefaultFile != "" {
		def := filepath.ToSlash(strings.TrimLeft(h.cfg.DefaultFile, "/"))
		for _, f := range files {
			if f == def {
				return def
			}
		}
		if abs, display, err := h.resolveJailedPath(def); err == nil {
			if info, err := os.Stat(abs); err == nil && !info.IsDir() {
				return display
			}
		}
	}
	for _, f := range files {
		if f == "demo.md" {
			return f
		}
	}
	if len(files) > 0 {
		return files[0]
	}
	return ""
}

func (h *Handler) rootLabel() string {
	if h.rootDir != "" {
		return h.rootDir
	}
	return "local files"
}

func (h *Handler) fileLock(key string) *sync.Mutex {
	h.mu.Lock()
	defer h.mu.Unlock()
	m, ok := h.fileLocks[key]
	if !ok {
		m = &sync.Mutex{}
		h.fileLocks[key] = m
	}
	return m
}

func atomicWriteFile(absPath, content string) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(absPath); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(absPath), ".mdweb-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, absPath); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func requireMethod(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, m := range methods {
		if r.Method == m {
			return true
		}
	}
	writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	return false
}

func (h *Handler) handleView(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	fileParam := r.URL.Query().Get("file")
	if fileParam == "" {
		fileParam = h.pickDefaultFile()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if fileParam == "" {
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, RenderIndexPage(h.basePath, h.rootLabel(), h.recentFilesSnapshot()))
		}
		return
	}
	doc, err := h.loadDocument(fileParam)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, RenderFullPage(PageRenderOptions{
		IR: doc.ir, Source: doc.source, Files: h.filesForDocument(doc),
		RootLabel: h.rootLabel(), BasePath: h.basePath,
	}))
}

func (h *Handler) handleFiles(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	var files []string
	if h.cfg.AllowAnyLocalFile {
		files = h.recentFilesSnapshot()
	} else {
		files = discoverFiles(h.rootDir, 2)
	}
	if files == nil {
		files = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"root": h.rootLabel(), "files": files})
}

func (h *Handler) handleDoc(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	fileParam := r.URL.Query().Get("file")
	if fileParam == "" {
		fileParam = h.pickDefaultFile()
	}
	doc, err := h.loadDocument(fileParam)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	bodyHTML, tocHTML := RenderDocumentHTML(doc.ir, h.basePath, doc.displayFile)
	writeJSON(w, http.StatusOK, map[string]any{
		"file": doc.ir.File, "revision": doc.ir.Revision, "title": doc.ir.Title,
		"stats": doc.ir.Stats, "widgets": doc.ir.Widgets, "headings": doc.ir.Headings,
		"html": bodyHTML, "toc": tocHTML, "source": doc.source,
	})
}

func (h *Handler) handleMutate(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		File     string           `json:"file"`
		Revision string           `json:"revision"`
		Mutation *MutationRequest `json:"mutation"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.File == "" || body.Mutation == nil {
		writeJSONError(w, http.StatusBadRequest, `missing "file" or "mutation" in request body`)
		return
	}
	_, key, err := h.resolveDocPath(body.File)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	lock := h.fileLock(key)
	lock.Lock()
	defer lock.Unlock()

	doc, err := h.loadDocument(body.File)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if body.Revision != "" && body.Revision != doc.ir.Revision {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Document changed on disk.", "revision": doc.ir.Revision})
		return
	}
	patch, err := ApplyMutation(doc.source, doc.ir, *body.Mutation)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if patch.Changed {
		targetPath, content := doc.absPath, patch.NextSource
		if patch.Widget.Kind == WidgetMermaid && patch.Widget.SourceFile != "" && patch.Widget.SourceFile != doc.displayFile {
			target, err := h.resolveRelativeAsset(doc.absPath, patch.Widget.SourceFile, true)
			if err != nil {
				writeHTTPError(w, err)
				return
			}
			targetPath = target
			content = strings.ReplaceAll(fmt.Sprint(patch.NewValue), "\r\n", "\n") + "\n"
		}
		if err := atomicWriteFile(targetPath, content); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	nextDoc, err := h.loadDocument(doc.displayFile)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	label := patch.Widget.Label
	if label == "" {
		label = patch.Widget.Name
	}
	ev := h.recordAction(ActionEvent{
		TS: time.Now().UTC().Format(time.RFC3339Nano), File: doc.displayFile,
		Kind: patch.Widget.Kind, WidgetID: patch.Widget.ID, Name: patch.Widget.Name,
		Label: label, Line: patch.Widget.Line, OldValue: patch.OldValue,
		NewValue: patch.NewValue, Revision: nextDoc.ir.Revision,
	})
	if patch.Changed {
		h.broadcastDocUpdate(doc.displayFile, nextDoc.ir.Revision)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "changed": patch.Changed, "revision": nextDoc.ir.Revision,
		"stats": nextDoc.ir.Stats, "source": nextDoc.source, "event": ev,
	})
}

func (h *Handler) handleMermaidFlip(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Code      *string `json:"code"`
		Action    string  `json:"action"`
		Direction string  `json:"direction"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Code == nil {
		writeJSONError(w, http.StatusBadRequest, `missing "code" in request body`)
		return
	}
	target := ""
	if body.Action == "set" && body.Direction != "" {
		target = body.Direction
	}
	nextCode, info := SetMermaidDirection(*body.Code, target)
	writeJSON(w, http.StatusOK, map[string]any{
		"code": nextCode, "direction": info.Direction,
		"flippable": info.Flippable, "diagramType": info.DiagramType,
	})
}

func (h *Handler) recordAction(ev ActionEvent) ActionEvent {
	h.mu.Lock()
	h.seq++
	ev.Seq = h.seq
	h.actionLog = append(h.actionLog, ev)
	if len(h.actionLog) > 500 {
		h.actionLog = h.actionLog[len(h.actionLog)-500:]
	}
	if data, err := json.Marshal(ev); err == nil {
		msg := "event: action\ndata: " + string(data) + "\n\n"
		for sub := range h.actionSubs {
			if sub.fileFilter == "" || sub.fileFilter == ev.File {
				select {
				case sub.ch <- msg:
				default:
				}
			}
		}
	}
	h.mu.Unlock()
	return ev
}

func (h *Handler) broadcastDocUpdate(displayFile, revision string) {
	payload, _ := json.Marshal(map[string]string{"file": displayFile, "revision": revision})
	msg := "event: doc:updated\ndata: " + string(payload) + "\n\n"
	h.mu.Lock()
	if p, ok := h.pollers[displayFile]; ok {
		p.mu.Lock()
		p.lastRev = revision
		p.mu.Unlock()
	}
	for ch := range h.docSubs[displayFile] {
		select {
		case ch <- msg:
		default:
		}
	}
	h.mu.Unlock()
}

func (h *Handler) subscribeDoc(displayFile, absPath, initialRev string) (chan string, func()) {
	ch := make(chan string, 16)
	h.mu.Lock()
	if h.docSubs[displayFile] == nil {
		h.docSubs[displayFile] = make(map[chan string]struct{})
	}
	h.docSubs[displayFile][ch] = struct{}{}
	if _, exists := h.pollers[displayFile]; !exists {
		p := &filePoller{stopCh: make(chan struct{}), lastRev: initialRev}
		h.pollers[displayFile] = p
		h.wg.Add(1)
		go h.runFilePoller(displayFile, absPath, p)
	}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		if subs := h.docSubs[displayFile]; subs != nil {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(h.docSubs, displayFile)
				if p, ok := h.pollers[displayFile]; ok {
					delete(h.pollers, displayFile)
					p.stop()
				}
			}
		}
		h.mu.Unlock()
	}
}

func (h *Handler) runFilePoller(displayFile, absPath string, p *filePoller) {
	defer h.wg.Done()
	var lastMod time.Time
	var lastSize int64 = -1
	if info, err := os.Stat(absPath); err == nil {
		lastMod, lastSize = info.ModTime(), info.Size()
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for ticks := 0; ; {
		select {
		case <-h.done:
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			ticks++
			info, err := os.Stat(absPath)
			if err != nil || info.IsDir() {
				continue
			}
			if info.ModTime() == lastMod && info.Size() == lastSize && (ticks%4 != 0 || info.Size() > 512*1024) {
				continue
			}
			raw, err := os.ReadFile(absPath)
			if err != nil {
				continue
			}
			lastMod, lastSize = info.ModTime(), info.Size()
			rev := ComputeRevision(string(raw))
			p.mu.Lock()
			prev := p.lastRev
			p.mu.Unlock()
			if rev != prev {
				h.broadcastDocUpdate(displayFile, rev)
			}
		}
	}
}

func (h *Handler) serveSSE(w http.ResponseWriter, r *http.Request, initialMsg string, ch <-chan string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, err := io.WriteString(w, initialMsg); err != nil {
		return
	}
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.done:
			return
		case msg := <-ch:
			if _, err := io.WriteString(w, msg); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *Handler) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	fileParam := r.URL.Query().Get("file")
	if fileParam == "" {
		fileParam = h.pickDefaultFile()
	}
	doc, err := h.loadDocument(fileParam)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	ch, unsub := h.subscribeDoc(doc.displayFile, doc.absPath, doc.ir.Revision)
	defer unsub()
	connected, _ := json.Marshal(map[string]string{"file": doc.displayFile, "revision": doc.ir.Revision})
	h.serveSSE(w, r, "event: connected\ndata: "+string(connected)+"\n\n", ch)
}

func (h *Handler) handleActions(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	fileFilter := r.URL.Query().Get("file")
	h.mu.Lock()
	out := make([]ActionEvent, 0, len(h.actionLog))
	for _, ev := range h.actionLog {
		if fileFilter == "" || ev.File == fileFilter {
			out = append(out, ev)
		}
	}
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) handleActionStream(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	sub := &actionSubscriber{fileFilter: r.URL.Query().Get("file"), ch: make(chan string, 32)}
	h.mu.Lock()
	h.actionSubs[sub] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.actionSubs, sub)
		h.mu.Unlock()
	}()
	h.serveSSE(w, r, "event: connected\ndata: {\"ok\":true}\n\n", sub.ch)
}

func (h *Handler) handleAssets(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	src := r.URL.Query().Get("src")
	fileParam := r.URL.Query().Get("file")
	var docAbs string
	if fileParam != "" {
		abs, _, err := h.resolveDocPath(fileParam)
		if err != nil {
			writeHTTPError(w, err)
			return
		}
		docAbs = abs
	} else if h.cfg.AllowAnyLocalFile {
		writeJSONError(w, http.StatusBadRequest, "missing file parameter")
		return
	}
	target, err := h.resolveRelativeAsset(docAbs, src, false)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(target)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	if mime, ok := mimeByExt[strings.ToLower(filepath.Ext(target))]; ok {
		w.Header().Set("Content-Type", mime)
	}
	http.ServeContent(w, r, filepath.Base(target), info.ModTime(), f)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeHTTPError(w http.ResponseWriter, err error) {
	var he *httpError
	if errors.As(err, &he) {
		writeJSONError(w, he.status, he.msg)
		return
	}
	writeJSONError(w, http.StatusInternalServerError, err.Error())
}
