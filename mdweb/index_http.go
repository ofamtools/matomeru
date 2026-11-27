package mdweb

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var htmlAssetMimeByExt = map[string]string{
	".css":   "text/css; charset=utf-8",
	".js":    "application/javascript; charset=utf-8",
	".mjs":   "application/javascript; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".html":  "text/html; charset=utf-8",
	".htm":   "text/html; charset=utf-8",
}

func (h *Handler) handleIndexRoute(w http.ResponseWriter, r *http.Request, path string) bool {
	switch path {
	case "/index":
		h.handleIndexPage(w, r)
		return true
	case "/html":
		h.handleHTMLView(w, r)
		return true
	case "/api/index":
		h.handleAPIIndex(w, r)
		return true
	case "/api/index/add":
		h.handleAPIIndexAdd(w, r)
		return true
	case "/api/index/scan":
		h.handleAPIIndexScan(w, r)
		return true
	case "/api/index/remove":
		h.handleAPIIndexRemove(w, r)
		return true
	case "/api/index/refresh":
		h.handleAPIIndexRefresh(w, r)
		return true
	}
	return false
}

func (h *Handler) renderIndexResponse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	snap := h.index.Snapshot()
	var extraFiles []string
	if h.cfg.AllowAnyLocalFile {
		extraFiles = h.recentFilesSnapshot()
	} else {
		extraFiles = discoverFiles(h.rootDir, 2)
	}
	_, _ = io.WriteString(w, RenderIndexView(IndexRenderOptions{
		BasePath:    h.basePath,
		RootLabel:   h.rootLabel(),
		Snapshot:    snap,
		RecentFiles: extraFiles,
	}))
}

func (h *Handler) handleIndexPage(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	h.renderIndexResponse(w, r)
}

func (h *Handler) handleAPIIndex(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, h.index.Snapshot())
}

func (h *Handler) handleAPIIndexAdd(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Path      string `json:"path"`
		Mode      string `json:"mode"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		writeJSONError(w, http.StatusBadRequest, `missing "path" in request body`)
		return
	}
	snap, err := h.index.Add(body.Path, IndexSourceKind(strings.TrimSpace(body.Mode)), body.Recursive)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (h *Handler) handleAPIIndexScan(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Dir       string `json:"dir"`
		Path      string `json:"path"`
		Recursive *bool  `json:"recursive"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	target := strings.TrimSpace(body.Dir)
	if target == "" {
		target = strings.TrimSpace(body.Path)
	}
	if target == "" {
		writeJSONError(w, http.StatusBadRequest, `missing "dir" in request body`)
		return
	}
	recursive := true
	if body.Recursive != nil {
		recursive = *body.Recursive
	}
	snap, err := h.index.Scan(target, recursive)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (h *Handler) handleAPIIndexRemove(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	target := strings.TrimSpace(body.ID)
	if target == "" {
		target = strings.TrimSpace(body.Path)
	}
	snap, removed, err := h.index.Remove(target)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if !removed {
		writeJSONError(w, http.StatusNotFound, "index source not found")
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (h *Handler) handleAPIIndexRefresh(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	snap, err := h.index.Refresh()
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (h *Handler) resolveHTMLPath(fileParam string) (string, error) {
	if fileParam == "" || strings.ContainsRune(fileParam, 0) {
		return "", &httpError{http.StatusBadRequest, "missing or invalid file parameter"}
	}
	if !isHTMLFile(fileParam) {
		return "", &httpError{http.StatusForbidden, "unsupported file type (expected .html or .htm)"}
	}
	if filepath.IsAbs(fileParam) {
		cleaned := filepath.Clean(fileParam)
		if h.cfg.AllowAnyLocalFile || h.index.AllowsHTMLPath(cleaned) {
			return cleaned, nil
		}
		if h.rootDir != "" && h.checkJailed(cleaned) == nil {
			return cleaned, nil
		}
		return "", &httpError{http.StatusForbidden, "path escapes served root directory"}
	}
	if h.rootDir == "" {
		return "", &httpError{http.StatusBadRequest, "file path must be absolute"}
	}
	abs, _, err := h.resolveJailedPath(fileParam)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func (h *Handler) resolveHTMLSiblingAsset(htmlAbsPath, src string) (string, string, error) {
	src = strings.TrimSpace(src)
	if src == "" || strings.ContainsRune(src, 0) || hasSchemePrefix(src) {
		return "", "", &httpError{http.StatusBadRequest, "invalid relative asset path"}
	}
	if filepath.IsAbs(src) || strings.HasPrefix(src, "/") || strings.HasPrefix(src, `\`) {
		return "", "", &httpError{http.StatusForbidden, "absolute asset paths are not allowed"}
	}
	ext := strings.ToLower(filepath.Ext(src))
	mime, ok := htmlAssetMimeByExt[ext]
	if !ok {
		return "", "", &httpError{http.StatusForbidden, "unsupported asset file type"}
	}
	baseDir := filepath.Dir(htmlAbsPath)
	target := filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(src)))
	if !containsPath(baseDir, target) {
		return "", "", &httpError{http.StatusForbidden, "asset path escapes document directory"}
	}
	if evalTarget, err := filepath.EvalSymlinks(target); err == nil {
		evalBase := baseDir
		if eb, err := filepath.EvalSymlinks(baseDir); err == nil {
			evalBase = eb
		}
		if !containsPath(filepath.Clean(evalBase), filepath.Clean(evalTarget)) {
			return "", "", &httpError{http.StatusForbidden, "asset path escapes document directory"}
		}
	}
	return target, mime, nil
}

func (h *Handler) handleHTMLView(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	absPath, err := h.resolveHTMLPath(r.URL.Query().Get("file"))
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if src := r.URL.Query().Get("src"); src != "" {
		h.serveHTMLSiblingAsset(w, r, absPath, src)
		return
	}
	info, err := os.Stat(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(w, http.StatusNotFound, "html file not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if info.IsDir() {
		writeJSONError(w, http.StatusNotFound, "path is a directory")
		return
	}
	f, err := os.Open(absPath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filepath.Base(absPath), info.ModTime(), f)
}

func (h *Handler) serveHTMLSiblingAsset(w http.ResponseWriter, r *http.Request, htmlAbsPath, src string) {
	target, mime, err := h.resolveHTMLSiblingAsset(htmlAbsPath, src)
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
	w.Header().Set("Content-Type", mime)
	http.ServeContent(w, r, filepath.Base(target), info.ModTime(), f)
}

func (h *Handler) resolveIndexedDocPath(fileParam string) (string, string, bool) {
	if !filepath.IsAbs(fileParam) {
		return "", "", false
	}
	cleaned := filepath.Clean(fileParam)
	if !h.index.AllowsMarkdownPath(cleaned) || !isRenderableFile(cleaned) {
		return "", "", false
	}
	return cleaned, cleaned, true
}

func (h *Handler) pairedHTMLForDoc(absPath string) string {
	if absPath == "" {
		return ""
	}
	if paired := h.index.PairedHTMLForMarkdown(absPath); paired != "" {
		return paired
	}
	return ""
}

func (h *Handler) checkAssetBoundary(docAbsPath, target string) error {
	if h.rootDir != "" && h.checkJailed(target) == nil {
		return nil
	}
	if docAbsPath != "" && h.index.AllowsMarkdownPath(docAbsPath) {
		docDir := filepath.Dir(filepath.Clean(docAbsPath))
		if containsPath(docDir, target) {
			if eval, err := filepath.EvalSymlinks(target); err == nil {
				evalBase := docDir
				if eb, err := filepath.EvalSymlinks(docDir); err == nil {
					evalBase = eb
				}
				if !containsPath(filepath.Clean(evalBase), filepath.Clean(eval)) {
					return &httpError{http.StatusForbidden, "path escapes served root directory"}
				}
			}
			return nil
		}
	}
	return &httpError{http.StatusForbidden, "path escapes served root directory"}
}
