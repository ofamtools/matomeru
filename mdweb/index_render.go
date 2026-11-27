package mdweb

import (
	"fmt"
	"net/url"
	"strings"
)

// IndexRenderOptions configures RenderIndexView.
type IndexRenderOptions struct {
	BasePath    string
	RootLabel   string
	Snapshot    IndexSnapshot
	RecentFiles []string
}

// RenderIndexView renders the rich interactive Index page showing indexed entries
// sorted newest-first, paired Markdown+HTML links, and add/scan controls.
func RenderIndexView(opts IndexRenderOptions) string {
	base := normalizeBasePath(opts.BasePath)
	bootstrap := safeBootstrapJSON(struct {
		BasePath string        `json:"basePath"`
		Snapshot IndexSnapshot `json:"snapshot"`
	}{base, opts.Snapshot})

	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Index · md-live</title>
<link rel="stylesheet" href="%s/static/app.css">
</head>
<body>
<header class="topbar">
  <div class="topbar-inner">
    <div class="brand-group">
      <a class="brand-tag" href="%s/" title="Root: %s">md-live</a>
      <a class="bar-btn is-active" href="%s/index">Index</a>
      <span class="stat-pill" id="index-count-pill">%d indexed</span>
    </div>
    <div class="status-group">
      <button type="button" class="bar-btn" id="index-refresh-btn" title="Rescan all sources on disk">Refresh</button>
      <button type="button" class="bar-btn" id="theme-toggle">Theme</button>
    </div>
  </div>
</header>
<main class="index-shell index-shell-wide">
  <section class="index-card">
    <div class="index-header-row">
      <div>
        <h1>Document Index</h1>
        <p class="index-lead">Browse paired Markdown &amp; HTML documents sorted newest-first, or add files, directories, and base-directory scans.</p>
      </div>
    </div>

    <div class="index-controls-grid">
      <form class="index-panel" id="form-add-file">
        <label class="index-panel-title" for="input-add-file">Add Single File</label>
        <div class="index-form">
          <input class="index-input" id="input-add-file" type="text" name="path" placeholder="/path/to/report.md or report.html" required>
          <button class="index-submit" type="submit">Add File</button>
        </div>
      </form>

      <form class="index-panel" id="form-add-dir">
        <label class="index-panel-title" for="input-add-dir">Add Single Directory</label>
        <div class="index-form">
          <input class="index-input" id="input-add-dir" type="text" name="path" placeholder="/path/to/docs-folder" required>
          <button class="index-submit" type="submit">Add Dir</button>
        </div>
      </form>

      <form class="index-panel index-panel-wide" id="form-scan-dir">
        <label class="index-panel-title" for="input-scan-dir">Scan Base Directory</label>
        <div class="index-form">
          <input class="index-input" id="input-scan-dir" type="text" name="dir" placeholder="/path/to/workspace (scans .md, .markdown, .html, .htm)" required>
          <label class="index-check-label"><input type="checkbox" id="input-scan-recursive" name="recursive" checked> Recursive</label>
          <button class="index-submit" type="submit">Scan</button>
        </div>
      </form>
    </div>

    <div class="index-toolbar">
      <input class="index-input index-filter-input" id="index-filter" type="search" placeholder="Filter by title, path, directory, or type (paired, md, html)..." aria-label="Filter index entries">
      <span class="index-filter-meta" id="index-filter-meta">Newest first</span>
    </div>

    <div class="index-sources-wrap" id="index-sources-wrap">
      %s
    </div>

    <h2 class="index-section-title">Indexed Documents (Newest First)</h2>
    <div id="index-entries">%s</div>

    <details class="index-quick-open">
      <summary>Quick Open File &amp; CLI Shortcut</summary>
      <form class="index-form" method="get" action="%s/view">
        <input class="index-input" type="text" name="file" placeholder="/path/to/document.md" aria-label="Markdown file path" required>
        <button class="index-submit" type="submit">Open</button>
      </form>
      <div class="index-hint">CLI shortcut: <code>mtm web open &lt;file&gt;</code></div>
    </details>
    %s
  </section>
</main>
<div class="toast" id="toast" role="status" aria-live="polite"></div>
<script>window.__MD_LIVE__ = %s;</script>
<script src="%s/static/app.js"></script>
<script src="%s/static/index.js"></script>
</body>
</html>`,
		base, escapeHTML(base), escapeHTML(opts.RootLabel), escapeHTML(base), opts.Snapshot.Count,
		renderIndexSourcesHTML(opts.Snapshot.Sources),
		renderIndexEntriesHTML(base, opts.Snapshot.Entries),
		escapeHTML(base),
		renderRecentSectionHTML(base, opts.RecentFiles, opts.Snapshot.Entries),
		bootstrap, base, base)
}

func renderIndexSourcesHTML(sources []IndexSource) string {
	if len(sources) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<h2 class="index-section-title">Sources</h2><div class="index-sources">`)
	for _, src := range sources {
		kindLabel := string(src.Kind)
		if src.Kind == SourceScan && src.Recursive {
			kindLabel = "scan · recursive"
		}
		fmt.Fprintf(&sb,
			`<div class="source-chip" data-source-id="%s"><span class="source-kind">%s</span><span class="source-path" title="%s">%s</span><button type="button" class="mini-btn source-remove-btn" data-remove-source="%s" title="Remove source">Remove</button></div>`,
			escapeHTML(src.ID), escapeHTML(kindLabel), escapeHTML(src.Path), escapeHTML(src.Path), escapeHTML(src.ID))
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

func renderIndexEntriesHTML(base string, entries []IndexEntry) string {
	if len(entries) == 0 {
		return `<p class="index-empty">No indexed documents yet. Add a single file, add a directory, or scan a base directory above.</p>`
	}
	var sb strings.Builder
	sb.WriteString(`<ul class="index-entry-list">`)
	for _, ent := range entries {
		sb.WriteString(renderIndexEntryRow(base, ent))
	}
	sb.WriteString(`</ul>`)
	return sb.String()
}

func renderIndexEntryRow(base string, ent IndexEntry) string {
	badgeClass, badgeText, typeToken := entryBadgeInfo(ent)
	searchText := strings.ToLower(strings.Join([]string{ent.Title, ent.Dir, ent.MarkdownPath, ent.HTMLPath, typeToken}, " "))
	dateStr := FormatSortDate(ent.SortTime)
	dateTitle := fmt.Sprintf("Updated: %s | Created: %s", FormatSortDate(ent.UpdatedAt), FormatSortDate(ent.CreatedAt))

	var links strings.Builder
	if ent.MarkdownPath != "" {
		mdHref := base + "/?file=" + url.QueryEscape(ent.MarkdownPath)
		fmt.Fprintf(&links, `<a class="entry-link entry-link-md" href="%s">Markdown</a>`, escapeHTML(mdHref))
	}
	if ent.HTMLPath != "" {
		htmlHref := base + "/html?file=" + url.QueryEscape(ent.HTMLPath)
		fmt.Fprintf(&links, `<a class="entry-link entry-link-html" href="%s">HTML</a>`, escapeHTML(htmlHref))
	}

	return fmt.Sprintf(
		`<li class="index-entry-row" data-search="%s"><div class="entry-main"><div class="entry-title-row"><span class="entry-title">%s</span><span class="entry-badge %s">%s</span></div><div class="entry-meta"><span class="entry-date" title="%s">%s</span><span class="entry-dir" title="%s">%s</span></div></div><div class="entry-actions">%s</div></li>`,
		escapeHTML(searchText), escapeHTML(ent.Title), badgeClass, badgeText,
		escapeHTML(dateTitle), escapeHTML(dateStr),
		escapeHTML(ent.Dir), escapeHTML(ent.Dir), links.String())
}

func entryBadgeInfo(ent IndexEntry) (cls, label, token string) {
	if ent.Paired || (ent.MarkdownPath != "" && ent.HTMLPath != "") {
		return "badge-paired", "MD + HTML", "paired md html"
	}
	if ent.HTMLPath != "" {
		return "badge-html", "HTML", "html"
	}
	return "badge-md", "Markdown", "md markdown"
}

func renderRecentSectionHTML(base string, files []string, indexed []IndexEntry) string {
	if len(files) == 0 {
		return ""
	}
	seen := make(map[string]bool, len(indexed)*2)
	for _, ent := range indexed {
		if ent.MarkdownPath != "" {
			seen[ent.MarkdownPath] = true
		}
		if ent.HTMLPath != "" {
			seen[ent.HTMLPath] = true
		}
	}
	var extra []string
	for _, f := range files {
		if !seen[f] {
			extra = append(extra, f)
		}
	}
	if len(extra) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<h2 class="index-section-title index-recent-title">Recent &amp; Available Files</h2><ul class="index-list">`)
	for _, f := range extra {
		href := base + "/view?file=" + url.QueryEscape(f)
		fmt.Fprintf(&sb, `<li class="index-item"><a href="%s">%s</a></li>`, escapeHTML(href), escapeHTML(f))
	}
	sb.WriteString(`</ul>`)
	return sb.String()
}
