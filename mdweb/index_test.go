package mdweb

import (
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

func mustTouchTime(t *testing.T, path string, ts time.Time) {
	t.Helper()
	if err := os.Chtimes(path, ts, ts); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func decodeSnapshot(t *testing.T, rec *httptest.ResponseRecorder) IndexSnapshot {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	var snap IndexSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode IndexSnapshot: %v (body=%s)", err, rec.Body.String())
	}
	return snap
}

func TestIndexSingleFileAndDirAdd(t *testing.T) {
	work := t.TempDir()
	idxFile := filepath.Join(t.TempDir(), "mdweb-index.json")

	reportMD := filepath.Join(work, "report.md")
	reportHTML := filepath.Join(work, "report.html")
	soloHTML := filepath.Join(work, "landing.html")
	dirGroup := filepath.Join(work, "group")
	groupMD := filepath.Join(dirGroup, "brief.md")
	groupHTML := filepath.Join(dirGroup, "brief.html")
	nestedMD := filepath.Join(dirGroup, "nested", "deep.md")

	mustWriteFile(t, reportMD, "# Quarterly Report\n\nBody.\n")
	mustWriteFile(t, reportHTML, "<!doctype html><html><head><title>Quarterly Report HTML</title></head><body><h1>Report</h1></body></html>")
	mustWriteFile(t, soloHTML, "<!doctype html><html><head><title>Landing Page</title></head><body>Hello</body></html>")
	mustWriteFile(t, groupMD, "# Design Brief\n")
	mustWriteFile(t, groupHTML, "<html><head><title>Design Brief</title></head><body>Brief</body></html>")
	mustWriteFile(t, nestedMD, "# Deep Nested\n")

	h := NewHandler(Config{AllowAnyLocalFile: true, IndexPath: idxFile})
	defer h.Close()

	// 1. Add single .md file.
	snap := decodeSnapshot(t, doJSONPost(t, h, "/api/index/add", map[string]any{
		"path": reportMD, "mode": "file",
	}))
	if snap.Count != 1 || snap.Entries[0].MarkdownPath != reportMD || snap.Entries[0].HTMLPath != "" || snap.Entries[0].Paired {
		t.Fatalf("unexpected single md entry: %+v", snap.Entries)
	}
	if snap.Entries[0].Title != "Quarterly Report" {
		t.Errorf("expected title 'Quarterly Report', got %q", snap.Entries[0].Title)
	}

	// 2. Add single standalone .html file.
	snap = decodeSnapshot(t, doJSONPost(t, h, "/api/index/add", map[string]any{
		"path": soloHTML, "mode": "file",
	}))
	if snap.Count != 2 {
		t.Fatalf("expected 2 entries, got %d", snap.Count)
	}

	// 3. Add matching report.html as a single file -> pairs with report.md into ONE entry!
	snap = decodeSnapshot(t, doJSONPost(t, h, "/api/index/add", map[string]any{
		"path": reportHTML, "mode": "file",
	}))
	if snap.Count != 2 {
		t.Fatalf("expected report.md + report.html to merge into 1 paired entry (total 2), got %d: %+v", snap.Count, snap.Entries)
	}
	var foundPaired bool
	for _, e := range snap.Entries {
		if e.MarkdownPath == reportMD && e.HTMLPath == reportHTML && e.Paired {
			foundPaired = true
		}
	}
	if !foundPaired {
		t.Fatalf("expected paired entry for report.md + report.html, got %+v", snap.Entries)
	}

	// 4. Add single directory (non-recursive) -> picks up paired brief.md+brief.html, skips nested/deep.md.
	snap = decodeSnapshot(t, doJSONPost(t, h, "/api/index/add", map[string]any{
		"path": dirGroup, "mode": "dir",
	}))
	if snap.Count != 3 {
		t.Fatalf("expected 3 entries after adding single dir, got %d: %+v", snap.Count, snap.Entries)
	}
	for _, e := range snap.Entries {
		if e.MarkdownPath == nestedMD {
			t.Errorf("non-recursive single dir add should not include nested file %s", nestedMD)
		}
	}
}

func TestIndexScanBaseDirRecursiveAndPairing(t *testing.T) {
	base := t.TempDir()
	idxFile := filepath.Join(t.TempDir(), "mdweb-index.json")

	// Exact stem pair
	repMD := filepath.Join(base, "reports", "2026-11-26-report.md")
	repHTML := filepath.Join(base, "reports", "2026-11-26-report.html")
	mustWriteFile(t, repMD, "# System Report\n")
	mustWriteFile(t, repHTML, "<html><head><title>System Report</title></head><body>OK</body></html>")

	// Unambiguous prefix pair (.markdown + -rendered.htm)
	archMD := filepath.Join(base, "guides", "architecture.markdown")
	archHTML := filepath.Join(base, "guides", "architecture-rendered.htm")
	mustWriteFile(t, archMD, "# Architecture\n")
	mustWriteFile(t, archHTML, "<html><head><title>Architecture</title></head></html>")

	// Standalone MD and standalone HTML
	soloMD := filepath.Join(base, "notes", "solo.md")
	soloHTML := filepath.Join(base, "static", "overview.html")
	mustWriteFile(t, soloMD, "# Solo Note\n")
	mustWriteFile(t, soloHTML, "<html><head><title>Overview Page</title></head></html>")

	// Ambiguous prefix: 1 MD + 2 HTMLs -> must remain 3 separate entries
	ambMD := filepath.Join(base, "ambiguous", "spec.md")
	ambLight := filepath.Join(base, "ambiguous", "spec-light.html")
	ambDark := filepath.Join(base, "ambiguous", "spec-dark.html")
	mustWriteFile(t, ambMD, "# Spec\n")
	mustWriteFile(t, ambLight, "<html><head><title>Spec Light</title></head></html>")
	mustWriteFile(t, ambDark, "<html><head><title>Spec Dark</title></head></html>")

	// Ignored directories: .git, node_modules, .worktrees
	mustWriteFile(t, filepath.Join(base, ".git", "ignored.md"), "# Git\n")
	mustWriteFile(t, filepath.Join(base, "node_modules", "pkg", "README.md"), "# Pkg\n")
	mustWriteFile(t, filepath.Join(base, ".worktrees", "lane", "draft.md"), "# Worktree\n")

	h := NewHandler(Config{AllowAnyLocalFile: true, IndexPath: idxFile})
	defer h.Close()

	snap := decodeSnapshot(t, doJSONPost(t, h, "/api/index/scan", map[string]any{
		"dir":       base,
		"recursive": true,
	}))

	// Expect: rep pair (1) + arch pair (1) + soloMD (1) + soloHTML (1) + ambiguous (3) = 7 entries.
	if snap.Count != 7 {
		t.Fatalf("expected 7 entries, got %d: %+v", snap.Count, snap.Entries)
	}

	byTitle := map[string]IndexEntry{}
	for _, e := range snap.Entries {
		byTitle[e.Title] = e
		for _, bad := range []string{".git", "node_modules", ".worktrees"} {
			if strings.Contains(e.MarkdownPath, bad) || strings.Contains(e.HTMLPath, bad) {
				t.Errorf("ignored directory %q leaked into entry: %+v", bad, e)
			}
		}
	}

	if rep, ok := byTitle["System Report"]; !ok || !rep.Paired || rep.MarkdownPath != repMD || rep.HTMLPath != repHTML {
		t.Errorf("expected paired System Report entry, got %+v", rep)
	}
	if arch, ok := byTitle["Architecture"]; !ok || !arch.Paired || arch.MarkdownPath != archMD || arch.HTMLPath != archHTML {
		t.Errorf("expected paired Architecture entry, got %+v", arch)
	}
	if ov, ok := byTitle["Overview Page"]; !ok || ov.Paired || ov.HTMLPath != soloHTML || ov.MarkdownPath != "" {
		t.Errorf("expected standalone HTML Overview Page entry, got %+v", ov)
	}
	if spec, ok := byTitle["Spec"]; !ok || spec.Paired {
		t.Errorf("expected ambiguous Spec MD to stay unpaired, got %+v", spec)
	}

	// Refresh picks up a newly created file on disk.
	addedLater := filepath.Join(base, "notes", "fresh.md")
	mustWriteFile(t, addedLater, "# Fresh Doc\n")
	snap = decodeSnapshot(t, doJSONPost(t, h, "/api/index/refresh", map[string]any{}))
	if snap.Count != 8 {
		t.Fatalf("expected 8 entries after refresh, got %d", snap.Count)
	}

	// Remove source clears its entries.
	if len(snap.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(snap.Sources))
	}
	snap = decodeSnapshot(t, doJSONPost(t, h, "/api/index/remove", map[string]any{
		"id": snap.Sources[0].ID,
	}))
	if snap.Count != 0 || len(snap.Sources) != 0 {
		t.Fatalf("expected empty index after remove, got count=%d sources=%d", snap.Count, len(snap.Sources))
	}
}

func TestIndexNewestFirstSortOrder(t *testing.T) {
	dir := t.TempDir()
	idxFile := filepath.Join(t.TempDir(), "mdweb-index.json")

	oldest := filepath.Join(dir, "oldest.md")
	middle := filepath.Join(dir, "middle.html")
	pairMD := filepath.Join(dir, "paired.md")
	pairHTML := filepath.Join(dir, "paired.html")
	newest := filepath.Join(dir, "newest.md")

	mustWriteFile(t, oldest, "# Oldest\n")
	mustWriteFile(t, middle, "<html><head><title>Middle</title></head></html>")
	mustWriteFile(t, pairMD, "# Paired\n")
	mustWriteFile(t, pairHTML, "<html><head><title>Paired</title></head></html>")
	mustWriteFile(t, newest, "# Newest\n")

	tOld := time.Date(2025, 1, 10, 9, 0, 0, 0, time.UTC)
	tMid := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	tPairMD := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	tPairHTML := time.Date(2026, 11, 20, 16, 0, 0, 0, time.UTC)
	tNew := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)

	mustTouchTime(t, oldest, tOld)
	mustTouchTime(t, middle, tMid)
	mustTouchTime(t, pairMD, tPairMD)
	mustTouchTime(t, pairHTML, tPairHTML)
	mustTouchTime(t, newest, tNew)

	h := NewHandler(Config{AllowAnyLocalFile: true, IndexPath: idxFile})
	defer h.Close()

	snap := decodeSnapshot(t, doJSONPost(t, h, "/api/index/add", map[string]any{
		"path": dir, "mode": "dir",
	}))
	wantOrder := []string{"Newest", "Paired", "Middle", "Oldest"}
	if len(snap.Entries) != len(wantOrder) {
		t.Fatalf("expected %d entries, got %d", len(wantOrder), len(snap.Entries))
	}
	for i, want := range wantOrder {
		if snap.Entries[i].Title != want {
			t.Errorf("index[%d] = %q, want %q (sortTime=%v)", i, snap.Entries[i].Title, want, snap.Entries[i].SortTime)
		}
	}
	if !snap.Entries[1].UpdatedAt.Equal(tPairHTML) || !snap.Entries[1].SortTime.Equal(tPairHTML) {
		t.Errorf("expected paired entry SortTime=%v, got %v", tPairHTML, snap.Entries[1].SortTime)
	}

	// Equal mtimes fall back to filename YYYY-MM-DD descending.
	tieDir := t.TempDir()
	tieOlder := filepath.Join(tieDir, "2026-10-01-alpha.md")
	tieNewer := filepath.Join(tieDir, "2026-11-26-beta.md")
	mustWriteFile(t, tieOlder, "# Tie Older\n")
	mustWriteFile(t, tieNewer, "# Tie Newer\n")
	sameTS := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	mustTouchTime(t, tieOlder, sameTS)
	mustTouchTime(t, tieNewer, sameTS)

	h2 := NewHandler(Config{AllowAnyLocalFile: true, IndexPath: filepath.Join(t.TempDir(), "tie.json")})
	defer h2.Close()
	tieSnap := decodeSnapshot(t, doJSONPost(t, h2, "/api/index/add", map[string]any{"path": tieDir, "mode": "dir"}))
	if len(tieSnap.Entries) != 2 || tieSnap.Entries[0].Title != "Tie Newer" || tieSnap.Entries[1].Title != "Tie Older" {
		t.Fatalf("expected filename date fallback newest-first, got %+v", tieSnap.Entries)
	}
}

func TestIndexHTMLServingAndUIRoutes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	idxFile := filepath.Join(t.TempDir(), "mdweb-index.json")

	repMD := filepath.Join(root, "report.md")
	repHTML := filepath.Join(root, "report.html")
	repCSS := filepath.Join(root, "style.css")
	secretHTML := filepath.Join(outside, "secret.html")

	mustWriteFile(t, repMD, "# Paired Report\n\n- [ ] Ship index\n")
	mustWriteFile(t, repHTML, "<!doctype html><html><head><title>Paired Report</title></head><body><h1>Rendered HTML</h1></body></html>")
	mustWriteFile(t, repCSS, "body { color: navy; }")
	mustWriteFile(t, secretHTML, "<html><body>SECRET</body></html>")

	h := NewHandler(Config{RootDir: root, IndexPath: idxFile})
	defer h.Close()

	_ = decodeSnapshot(t, doJSONPost(t, h, "/api/index/add", map[string]any{"path": root, "mode": "dir"}))

	// 1. GET /index renders both Markdown (/?file=...) and HTML (/html?file=...) links for the paired entry.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/index", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /index: status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	wantMDLink := `/?file=` + url.QueryEscape(repMD)
	wantHTMLLink := `/html?file=` + url.QueryEscape(repHTML)
	for _, want := range []string{"Paired Report", "MD + HTML", wantMDLink, wantHTMLLink, `/static/index.js`, `id="index-filter"`} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /index missing %q", want)
		}
	}

	// 2. Rendered Markdown doc page includes Index nav link and paired HTML link.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?file="+url.QueryEscape(repMD), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /?file=<repMD>: status=%d body=%s", rec.Code, rec.Body.String())
	}
	docPage := rec.Body.String()
	for _, want := range []string{`href="/index"`, `>Index</a>`, wantHTMLLink} {
		if !strings.Contains(docPage, want) {
			t.Errorf("doc page missing %q", want)
		}
	}

	// 3. GET /html?file=<repHTML> serves text/html; charset=utf-8 cleanly.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/html?file="+url.QueryEscape(repHTML), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /html: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html content type, got %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "<h1>Rendered HTML</h1>") {
		t.Errorf("unexpected /html body: %s", rec.Body.String())
	}

	// 4. GET /html sibling asset works and blocks traversal.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/html?file="+url.QueryEscape(repHTML)+"&src=style.css", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("GET /html sibling css: status=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/html?file="+url.QueryEscape(repHTML)+"&src=../secret.css", nil))
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400/403 on html asset traversal, got %d", rec.Code)
	}

	// 5. Security: unindexed outside HTML and non-HTML files are rejected.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/html?file="+url.QueryEscape(secretHTML), nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for unindexed outside html, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/html?file="+url.QueryEscape(repMD), nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-html file on /html, got %d", rec.Code)
	}

	// 6. Static /static/index.js is served.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/index.js", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("GET /static/index.js: status=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestIndexPersistenceAcrossRestart(t *testing.T) {
	docsDir := t.TempDir()
	singleDir := t.TempDir()
	idxFile := filepath.Join(t.TempDir(), "persist", "mdweb-index.json")

	oldMD := filepath.Join(docsDir, "old.md")
	oldHTML := filepath.Join(docsDir, "old.html")
	newMD := filepath.Join(singleDir, "new.md")

	mustWriteFile(t, oldMD, "# Older Paired\n")
	mustWriteFile(t, oldHTML, "<html><head><title>Older Paired</title></head></html>")
	mustWriteFile(t, newMD, "# Newer Single\n")
	mustTouchTime(t, oldMD, time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC))
	mustTouchTime(t, oldHTML, time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC))
	mustTouchTime(t, newMD, time.Date(2026, 11, 27, 15, 0, 0, 0, time.UTC))

	// First handler instance: add single file + scan dir.
	h1 := NewHandler(Config{BasePath: "/md", AllowAnyLocalFile: true, IndexPath: idxFile})
	_ = decodeSnapshot(t, doJSONPost(t, h1, "/md/api/index/add", map[string]any{"path": newMD, "mode": "file"}))
	_ = decodeSnapshot(t, doJSONPost(t, h1, "/md/api/index/scan", map[string]any{"dir": docsDir, "recursive": true}))
	h1.Close()

	if _, err := os.Stat(idxFile); err != nil {
		t.Fatalf("expected persisted index file at %s: %v", idxFile, err)
	}

	// Second handler instance after restart: loads persisted index without re-adding.
	h2 := NewHandler(Config{BasePath: "/md", AllowAnyLocalFile: true, IndexPath: idxFile})
	defer h2.Close()

	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/md/api/index", nil))
	snap := decodeSnapshot(t, rec)
	if snap.Count != 2 || len(snap.Sources) != 2 {
		t.Fatalf("expected 2 entries and 2 sources after restart, got count=%d sources=%d", snap.Count, len(snap.Sources))
	}
	if snap.Entries[0].Title != "Newer Single" || snap.Entries[1].Title != "Older Paired" || !snap.Entries[1].Paired {
		t.Fatalf("unexpected persisted order/pairing: %+v", snap.Entries)
	}

	// GET /md/ renders the persisted index with /md basePath prefixes on both links.
	rec = httptest.NewRecorder()
	h2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/md/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /md/: status=%d", rec.Code)
	}
	page := rec.Body.String()
	for _, want := range []string{
		"Newer Single",
		"Older Paired",
		`/md/?file=` + url.QueryEscape(oldMD),
		`/md/html?file=` + url.QueryEscape(oldHTML),
	} {
		if !strings.Contains(page, want) {
			t.Errorf("GET /md/ missing %q after restart", want)
		}
	}
}
