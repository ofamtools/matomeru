package mdweb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func doJSONPost(t *testing.T, h http.Handler, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStandaloneServerWorkflow(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	outsideSecret := filepath.Join(outside, "secret.png")
	mustWriteFile(t, outsideSecret, "NOT-ALLOWED")

	mustWriteFile(t, filepath.Join(root, "images", "logo.svg"), `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"></svg>`)
	mustWriteFile(t, filepath.Join(root, "diagrams", "arch.mmd"), "flowchart LR\n  A[Client] --> B[Server]\n")

	demoMD := strings.Join([]string{
		"# Launch Checklist",
		"",
		"## Tasks",
		"",
		"- [ ] Ship release",
		"- [x] Verify tests",
		"",
		`Owner: {{input:owner="alice" label="Owner"}}`,
		"",
		"## Media",
		"",
		"![Logo](images/logo.svg)",
		"",
		"```mermaid title=\"Inline Flow\"",
		"flowchart LR",
		"  Start --> Done",
		"```",
		"",
		"{{mermaid:diagrams/arch.mmd}}",
		"",
	}, "\n")
	demoPath := filepath.Join(root, "demo.md")
	mustWriteFile(t, demoPath, demoMD)

	h := NewHandler(Config{RootDir: root})
	defer h.Close()

	// 1. GET / renders full page with rewritten image URLs, Mermaid cards, and static assets.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: status=%d body=%s", rec.Code, rec.Body.String())
	}
	page := rec.Body.String()
	for _, want := range []string{
		"Launch Checklist",
		`/static/app.css`,
		`/static/beautiful-mermaid.js`,
		`/static/app.js`,
		`window.__MD_LIVE__`,
		`/assets?file=demo.md&amp;src=images%2Flogo.svg`,
		`data-mermaid-flippable="1"`,
		`data-mermaid-dir="LR"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("GET / missing expected snippet %q", want)
		}
	}

	// 2. GET /api/files discovers both .md and .mmd files.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/files", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/files: status=%d", rec.Code)
	}
	var filesResp struct {
		Root  string   `json:"root"`
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &filesResp); err != nil {
		t.Fatalf("decode /api/files: %v", err)
	}
	if len(filesResp.Files) != 2 {
		t.Fatalf("expected 2 discovered files, got %v", filesResp.Files)
	}

	// 3. GET /api/doc returns IR widgets, revision, HTML, and source.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/doc?file=demo.md", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/doc: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var docResp struct {
		File     string        `json:"file"`
		Revision string        `json:"revision"`
		Title    string        `json:"title"`
		Stats    DocumentStats `json:"stats"`
		Widgets  []WidgetSpec  `json:"widgets"`
		HTML     string        `json:"html"`
		TOC      string        `json:"toc"`
		Source   string        `json:"source"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &docResp); err != nil {
		t.Fatalf("decode /api/doc: %v", err)
	}
	if docResp.File != "demo.md" || docResp.Revision == "" {
		t.Fatalf("unexpected doc metadata: %+v", docResp)
	}

	var chkID, inputID, inlineMermaidID, extMermaidID string
	for _, w := range docResp.Widgets {
		switch w.Kind {
		case WidgetCheckbox:
			if !w.Checked && chkID == "" {
				chkID = w.ID
			}
		case WidgetInput:
			if w.Name == "owner" {
				inputID = w.ID
			}
		case WidgetMermaid:
			if w.SourceFile != "" {
				extMermaidID = w.ID
			} else if inlineMermaidID == "" {
				inlineMermaidID = w.ID
			}
		}
	}
	if chkID == "" || inputID == "" || inlineMermaidID == "" || extMermaidID == "" {
		t.Fatalf("missing expected widgets: chk=%q input=%q inlineMmd=%q extMmd=%q",
			chkID, inputID, inlineMermaidID, extMermaidID)
	}

	// 4. POST /api/mutate toggles checkbox and persists to disk.
	staleRev := docResp.Revision
	rec = doJSONPost(t, h, "/api/mutate", map[string]any{
		"file":     "demo.md",
		"revision": staleRev,
		"mutation": MutationRequest{Kind: WidgetCheckbox, ID: chkID, Checked: true},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/mutate checkbox: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var mutResp struct {
		OK       bool          `json:"ok"`
		Changed  bool          `json:"changed"`
		Revision string        `json:"revision"`
		Stats    DocumentStats `json:"stats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &mutResp); err != nil {
		t.Fatalf("decode mutate response: %v", err)
	}
	if !mutResp.OK || !mutResp.Changed || mutResp.Revision == staleRev {
		t.Fatalf("expected changed mutation with new revision, got %+v", mutResp)
	}
	if mutResp.Stats.CheckboxChecked != 2 {
		t.Errorf("expected 2 checked boxes, got %d", mutResp.Stats.CheckboxChecked)
	}
	diskAfterChk, _ := os.ReadFile(demoPath)
	if !strings.Contains(string(diskAfterChk), "- [x] Ship release") {
		t.Errorf("expected checkbox persisted on disk, got:\n%s", string(diskAfterChk))
	}

	// 5. POST /api/mutate with stale revision returns 409 Conflict.
	rec = doJSONPost(t, h, "/api/mutate", map[string]any{
		"file":     "demo.md",
		"revision": staleRev,
		"mutation": MutationRequest{Kind: WidgetInput, ID: inputID, Value: "should-conflict"},
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on stale revision, got %d body=%s", rec.Code, rec.Body.String())
	}

	// 6. POST /api/mutate updates inline input using current revision.
	curRev := mutResp.Revision
	rec = doJSONPost(t, h, "/api/mutate", map[string]any{
		"file":     "demo.md",
		"revision": curRev,
		"mutation": MutationRequest{Kind: WidgetInput, ID: inputID, Value: "bob"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/mutate input: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &mutResp); err != nil {
		t.Fatalf("decode input mutate: %v", err)
	}
	curRev = mutResp.Revision
	diskAfterInput, _ := os.ReadFile(demoPath)
	if !strings.Contains(string(diskAfterInput), `{{input:owner="bob"`) {
		t.Errorf("expected updated input persisted on disk, got:\n%s", string(diskAfterInput))
	}

	// 7. POST /api/mermaid/flip flips flowchart LR -> TD and supports action:"set".
	rec = doJSONPost(t, h, "/api/mermaid/flip", map[string]any{
		"code":   "flowchart LR\n  Start --> Done",
		"action": "flip",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/mermaid/flip: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var flipResp struct {
		Code        string `json:"code"`
		Direction   string `json:"direction"`
		Flippable   bool   `json:"flippable"`
		DiagramType string `json:"diagramType"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &flipResp); err != nil {
		t.Fatalf("decode flip response: %v", err)
	}
	if !flipResp.Flippable || flipResp.Direction != "TD" || !strings.Contains(flipResp.Code, "flowchart TD") {
		t.Fatalf("unexpected flip result: %+v", flipResp)
	}

	// 8. Save flipped inline Mermaid block back to demo.md.
	rec = doJSONPost(t, h, "/api/mutate", map[string]any{
		"file":     "demo.md",
		"revision": curRev,
		"mutation": MutationRequest{Kind: WidgetMermaid, ID: inlineMermaidID, Code: flipResp.Code},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/mutate inline mermaid: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &mutResp); err != nil {
		t.Fatalf("decode inline mermaid mutate: %v", err)
	}
	curRev = mutResp.Revision
	diskAfterInlineMmd, _ := os.ReadFile(demoPath)
	if !strings.Contains(string(diskAfterInlineMmd), "flowchart TD\n  Start --> Done") {
		t.Errorf("expected inline mermaid persisted on disk, got:\n%s", string(diskAfterInlineMmd))
	}

	// 9. Save flipped external .mmd file via {{mermaid:diagrams/arch.mmd}}.
	rec = doJSONPost(t, h, "/api/mutate", map[string]any{
		"file":     "demo.md",
		"revision": curRev,
		"mutation": MutationRequest{
			Kind: WidgetMermaid,
			ID:   extMermaidID,
			Code: "flowchart TD\n  A[Client] --> B[Server]",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/mutate external mermaid: status=%d body=%s", rec.Code, rec.Body.String())
	}
	extBytes, err := os.ReadFile(filepath.Join(root, "diagrams", "arch.mmd"))
	if err != nil {
		t.Fatalf("read external mmd: %v", err)
	}
	if !strings.Contains(string(extBytes), "flowchart TD") {
		t.Errorf("expected external .mmd updated to TD, got:\n%s", string(extBytes))
	}

	// 10. GET /api/actions returns recorded action history.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/actions?file=demo.md", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/actions: status=%d", rec.Code)
	}
	var actions []ActionEvent
	if err := json.Unmarshal(rec.Body.Bytes(), &actions); err != nil {
		t.Fatalf("decode /api/actions: %v", err)
	}
	if len(actions) < 4 {
		t.Fatalf("expected at least 4 recorded actions, got %d", len(actions))
	}

	// 11. GET /assets serves relative image and rejects path traversal.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets?file=demo.md&src=images/logo.svg", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets valid svg: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "image/svg+xml") {
		t.Errorf("expected image/svg+xml content type, got %q", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "<svg") {
		t.Errorf("expected svg body, got %q", rec.Body.String())
	}

	relEscape, _ := filepath.Rel(root, outsideSecret)
	for _, badSrc := range []string{"../secret.png", "../../etc/passwd", "/etc/passwd", filepath.ToSlash(relEscape)} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets?file=demo.md&src="+url.QueryEscape(badSrc), nil))
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusForbidden {
			t.Errorf("expected 400/403 for traversal src=%q, got %d", badSrc, rec.Code)
		}
	}

	// 12. GET /static/beautiful-mermaid.js returns embedded JS with ETag & 304 support.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/beautiful-mermaid.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/beautiful-mermaid.js: status=%d", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Errorf("unexpected static headers: content-type=%q etag=%q", rec.Header().Get("Content-Type"), etag)
	}
	if !strings.Contains(rec.Body.String(), "beautifulMermaid") {
		t.Errorf("expected beautifulMermaid bundle in response")
	}

	req304 := httptest.NewRequest(http.MethodGet, "/static/beautiful-mermaid.js", nil)
	req304.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req304)
	if rec.Code != http.StatusNotModified {
		t.Errorf("expected 304 Not Modified on matching ETag, got %d", rec.Code)
	}
}

func TestStandaloneSSEExternalEditAndActions(t *testing.T) {
	root := t.TempDir()
	docPath := filepath.Join(root, "live.md")
	mustWriteFile(t, docPath, "# Live\n\n- [ ] Item A\n")

	h := NewHandler(Config{RootDir: root})
	defer h.Close()

	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	// Subscribe to /api/events and verify connected + external edit doc:updated.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events?file=live.md", nil)
	if err != nil {
		t.Fatalf("new events req: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	waitEvent := func(want string) string {
		t.Helper()
		var eventName, dataLine string
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				eventName = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				dataLine = strings.TrimPrefix(line, "data: ")
			} else if line == "" && eventName != "" {
				if eventName == want {
					return dataLine
				}
				eventName = ""
				dataLine = ""
			}
		}
		t.Fatalf("SSE stream ended before receiving %q (err=%v)", want, scanner.Err())
		return ""
	}

	connData := waitEvent("connected")
	if !strings.Contains(connData, `"file":"live.md"`) {
		t.Fatalf("unexpected connected payload: %s", connData)
	}

	// Simulate an external edit on disk; 250ms poller should emit doc:updated.
	mustWriteFile(t, docPath, "# Live\n\n- [x] Item A updated externally\n")
	updData := waitEvent("doc:updated")
	if !strings.Contains(updData, `"file":"live.md"`) {
		t.Fatalf("unexpected doc:updated payload: %s", updData)
	}

	cancel()
}

func TestDaemonModeArbitraryLocalFiles(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "diagram.png")
	mustWriteFile(t, imgPath, "\x89PNG\r\n\x1a\n")

	docPath := filepath.Join(dir, "notes.md")
	mustWriteFile(t, docPath, "# Daemon Notes\n\n- [ ] Review PR\n\n![Pic](diagram.png)\n")

	h := NewHandler(Config{
		BasePath:          "/md",
		AllowAnyLocalFile: true,
	})
	defer h.Close()

	// 1. Wrong prefix returns 404.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/other", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 outside BasePath, got %d", rec.Code)
	}

	// 2. GET /md/ renders index landing page with open form and CLI hint.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/md/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /md/: status=%d body=%s", rec.Code, rec.Body.String())
	}
	indexHTML := rec.Body.String()
	for _, want := range []string{`action="/md/view"`, `mtm web open`, `/md/static/app.css`} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("GET /md/ index missing %q", want)
		}
	}

	// 3. GET /md/view?file=<abs> renders full page with /md prefix.
	viewURL := "/md/view?file=" + url.QueryEscape(docPath)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, viewURL, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status=%d body=%s", viewURL, rec.Code, rec.Body.String())
	}
	viewHTML := rec.Body.String()
	for _, want := range []string{
		"Daemon Notes",
		`"basePath":"/md"`,
		`/md/static/app.css`,
		`/md/static/app.js`,
		`/md/assets?file=`,
		`id="live-toggle"`,
	} {
		if !strings.Contains(viewHTML, want) {
			t.Errorf("GET /md/view missing %q", want)
		}
	}

	// 4. GET /md/ now lists the recently viewed file.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/md/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), docPath) {
		t.Errorf("expected recently viewed file %q on /md/ index page", docPath)
	}

	// 5. POST /md/api/mutate mutates checkbox on the arbitrary local file.
	docRec := httptest.NewRecorder()
	h.ServeHTTP(docRec, httptest.NewRequest(http.MethodGet, "/md/api/doc?file="+url.QueryEscape(docPath), nil))
	if docRec.Code != http.StatusOK {
		t.Fatalf("GET /md/api/doc: status=%d body=%s", docRec.Code, docRec.Body.String())
	}
	var docData struct {
		Revision string       `json:"revision"`
		Widgets  []WidgetSpec `json:"widgets"`
	}
	if err := json.Unmarshal(docRec.Body.Bytes(), &docData); err != nil {
		t.Fatalf("decode /md/api/doc: %v", err)
	}
	if len(docData.Widgets) == 0 {
		t.Fatalf("expected at least 1 widget in notes.md")
	}

	rec = doJSONPost(t, h, "/md/api/mutate", map[string]any{
		"file":     docPath,
		"revision": docData.Revision,
		"mutation": MutationRequest{Kind: WidgetCheckbox, ID: docData.Widgets[0].ID, Checked: true},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /md/api/mutate: status=%d body=%s", rec.Code, rec.Body.String())
	}
	after, _ := os.ReadFile(docPath)
	if !strings.Contains(string(after), "- [x] Review PR") {
		t.Errorf("expected daemon mutation persisted to disk, got:\n%s", string(after))
	}

	// 6. GET /md/assets serves relative image and rejects absolute/unsafe paths.
	assetURL := "/md/assets?file=" + url.QueryEscape(docPath) + "&src=diagram.png"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, assetURL, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "image/png") {
		t.Fatalf("GET /md/assets valid: status=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
	}

	for _, bad := range []string{"/etc/passwd", "file:///etc/passwd", "notes.exe"} {
		badURL := "/md/assets?file=" + url.QueryEscape(docPath) + "&src=" + url.QueryEscape(bad)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, badURL, nil))
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusForbidden {
			t.Errorf("expected 400/403 for daemon bad asset %q, got %d", bad, rec.Code)
		}
	}
}

func TestOptInLiveControlsAndLazyMermaidScript(t *testing.T) {
	root := t.TempDir()
	plainPath := filepath.Join(root, "plain.md")
	mustWriteFile(t, plainPath, "# Plain Doc\n\nJust markdown, no diagrams.\n")

	h := NewHandler(Config{RootDir: root})
	defer h.Close()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/view?file=plain.md", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /view?file=plain.md: status=%d", rec.Code)
	}
	page := rec.Body.String()
	for _, want := range []string{`id="refresh-btn"`, `id="live-toggle"`, `aria-pressed="false"`, `Live: Off`} {
		if !strings.Contains(page, want) {
			t.Errorf("expected opt-in live control %q in page HTML", want)
		}
	}
	if strings.Contains(page, `/static/beautiful-mermaid.js`) {
		t.Errorf("plain markdown page without mermaid blocks should not eagerly load beautiful-mermaid.js")
	}
}
