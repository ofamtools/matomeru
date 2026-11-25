package mdweb

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// PageRenderOptions configures RenderFullPage.
type PageRenderOptions struct {
	IR         *DocumentIR
	Source     string
	Files      []string
	RootLabel  string
	BasePath   string
	AssetQuery string
}

var (
	safeHrefSchemeRe = regexp.MustCompile(`(?i)^(?:https?:|mailto:|#|/|\.\.?/)`)
	safeHrefRelRe    = regexp.MustCompile(`^[a-zA-Z0-9_.-]+(?:/[a-zA-Z0-9_./-]+)*$`)
	externalSchemeRe = regexp.MustCompile(`(?i)^https?:`)
	assetPassthruRe  = regexp.MustCompile(`(?i)^(?:https?:|data:|/)`)
)

func normalizeBasePath(raw string) string {
	b := strings.TrimRight(strings.TrimSpace(raw), "/")
	if b == "" {
		return ""
	}
	if !strings.HasPrefix(b, "/") {
		return "/" + b
	}
	return b
}

func formatAssetQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" || strings.HasPrefix(q, "?") {
		return q
	}
	return "?" + q
}

func sanitizeHref(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if safeHrefSchemeRe.MatchString(trimmed) || safeHrefRelRe.MatchString(trimmed) {
		return trimmed
	}
	return "#"
}

func rewriteAssetSrc(src, basePath, docFile string) string {
	trimmed := strings.TrimSpace(src)
	if trimmed == "" || assetPassthruRe.MatchString(trimmed) {
		return trimmed
	}
	return normalizeBasePath(basePath) + "/assets?file=" + url.QueryEscape(docFile) + "&src=" + url.QueryEscape(trimmed)
}

func escapeHTML(s string) string { return html.EscapeString(s) }

func shortRevision(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}

func isValidAlignment(a string) bool {
	return a == "left" || a == "center" || a == "right"
}

func alignStyle(aligns []string, idx int) string {
	if idx < len(aligns) && isValidAlignment(aligns[idx]) {
		return ` style="text-align:` + aligns[idx] + `"`
	}
	return ""
}

// RenderDocumentHTML renders the parsed DocumentIR into document body HTML and sticky TOC HTML.
func RenderDocumentHTML(ir *DocumentIR, basePath, docFile string) (bodyHTML, tocHTML string) {
	if ir == nil {
		return "", ""
	}
	if docFile == "" {
		docFile = ir.File
	}
	var body strings.Builder
	renderBlocks(&body, ir.Blocks, normalizeBasePath(basePath), docFile)
	return body.String(), renderTOC(ir)
}

func renderTOC(ir *DocumentIR) string {
	var items []HeadingInfo
	for _, h := range ir.Headings {
		if h.Level >= 1 && h.Level <= 3 {
			items = append(items, h)
		}
	}
	if len(items) < 2 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<nav class="toc" aria-label="Table of contents">` + "\n  <div class=\"toc-title\">On this page</div>\n  <ul>")
	for _, h := range items {
		fmt.Fprintf(&sb, `<li class="toc-l%d"><a href="#%s">%s</a></li>`, h.Level, escapeHTML(h.ID), escapeHTML(h.Text))
	}
	sb.WriteString("</ul>\n</nav>")
	return sb.String()
}

func renderBlocks(sb *strings.Builder, blocks []BlockNode, basePath, docFile string) {
	for _, b := range blocks {
		switch b.Type {
		case "heading":
			level := min(max(b.Level, 1), 6)
			tag := "h" + strconv.Itoa(level)
			fmt.Fprintf(sb, `<%s id="%s" data-line="%d"><a class="anchor" href="#%s" aria-label="Link to section">#</a>`,
				tag, escapeHTML(b.ID), b.Span.Line, escapeHTML(b.ID))
			renderInlines(sb, b.Inlines, basePath, docFile)
			fmt.Fprintf(sb, `</%s>`, tag)

		case "paragraph":
			fmt.Fprintf(sb, `<p data-line="%d">`, b.Span.Line)
			renderInlines(sb, b.Inlines, basePath, docFile)
			sb.WriteString(`</p>`)

		case "hr":
			sb.WriteString(`<hr>`)

		case "blockquote":
			cls, title := "", ""
			if b.Alert != "" {
				cls = fmt.Sprintf(` class="alert alert-%s"`, escapeHTML(b.Alert))
				title = fmt.Sprintf(`<div class="alert-title">%s</div>`, escapeHTML(strings.ToUpper(b.Alert)))
			}
			fmt.Fprintf(sb, `<blockquote%s data-line="%d">%s`, cls, b.Span.Line, title)
			renderBlocks(sb, b.Children, basePath, docFile)
			sb.WriteString(`</blockquote>`)

		case "list":
			hasTasks := false
			for _, it := range b.Items {
				if it.Checkbox != nil {
					hasTasks = true
					break
				}
			}
			tag := "ul"
			if b.Ordered {
				tag = "ol"
			}
			sb.WriteString("<" + tag)
			if hasTasks {
				sb.WriteString(` class="task-list"`)
			}
			if b.Ordered && b.Start != 1 && b.Start > 0 {
				fmt.Fprintf(sb, ` start="%d"`, b.Start)
			}
			sb.WriteString(">")
			for _, it := range b.Items {
				renderListItem(sb, it, basePath, docFile)
			}
			sb.WriteString("</" + tag + ">")

		case "table":
			fmt.Fprintf(sb, `<div class="table-wrap" data-line="%d"><table><thead><tr>`, b.Span.Line)
			for idx, cell := range b.Headers {
				fmt.Fprintf(sb, `<th%s>`, alignStyle(b.Alignments, idx))
				renderInlines(sb, cell, basePath, docFile)
				sb.WriteString("</th>")
			}
			sb.WriteString(`</tr></thead><tbody>`)
			for _, row := range b.Rows {
				sb.WriteString("<tr>")
				for idx := range b.Headers {
					fmt.Fprintf(sb, `<td%s>`, alignStyle(b.Alignments, idx))
					if idx < len(row) {
						renderInlines(sb, row[idx], basePath, docFile)
					}
					sb.WriteString("</td>")
				}
				sb.WriteString("</tr>")
			}
			sb.WriteString(`</tbody></table></div>`)

		case "code":
			lang := b.Lang
			if lang == "" {
				lang = "text"
			}
			fmt.Fprintf(sb, `<div class="code-card" data-line="%d"><div class="code-bar"><span class="code-lang">%s</span><button type="button" class="mini-btn copy-code-btn">Copy</button></div><pre><code>%s</code></pre></div>`,
				b.Span.Line, escapeHTML(lang), escapeHTML(b.Code))

		case "mermaid":
			renderMermaidBlock(sb, b)

		case "field":
			if b.Widget == nil {
				continue
			}
			w := b.Widget
			rows := w.Rows
			if rows <= 0 {
				rows = 4
			}
			idEsc := escapeHTML(w.ID)
			nameEsc := escapeHTML(w.Name)
			fmt.Fprintf(sb, `<div class="field-card" data-widget-wrap="%s" data-line="%d"><div class="field-bar"><label class="field-label" for="fld-%s">%s</label><span class="field-meta">field:%s · line %d <span class="save-dot" aria-hidden="true"></span></span></div><textarea id="fld-%s" class="md-textarea" rows="%d" data-widget-kind="field" data-widget-id="%s" data-widget-name="%s" data-line="%d" placeholder="%s">%s</textarea></div>`,
				idEsc, w.Line, idEsc, escapeHTML(w.Label), nameEsc, w.Line,
				idEsc, rows, idEsc, nameEsc, w.Line, escapeHTML(w.Placeholder), escapeHTML(w.Value))
		}
	}
}

func renderMermaidBlock(sb *strings.Builder, b BlockNode) {
	label := b.Title
	if label == "" {
		label = b.SourceFile
	}
	if label == "" {
		label = "Mermaid diagram"
	}

	flippable, dir := b.Flippable, b.Direction
	if b.Error == "" && !flippable && dir == "" && strings.TrimSpace(b.Code) != "" {
		orient := DetectMermaidOrientation(b.Code)
		flippable, dir = orient.Flippable, orient.Direction
	}

	sourceTag := fmt.Sprintf(`<span class="mermaid-file">line %d</span>`, b.Span.Line)
	if b.SourceFile != "" {
		sourceTag = `<span class="mermaid-file">` + escapeHTML(b.SourceFile) + `</span>`
	}
	dirBadge := ""
	if flippable && dir != "" {
		dirBadge = `<span class="mermaid-dir-pill" data-mermaid-dir-badge>` + escapeHTML(dir) + `</span>`
	}
	flipBtns := ""
	if flippable && b.Error == "" {
		flipBtns = `<button type="button" class="mini-btn" data-mermaid-flip title="Flip diagram orientation">Flip</button><button type="button" class="mini-btn save-mmd-btn" data-mermaid-save hidden title="Write flipped orientation back to source file">Save</button>`
	}

	var exportBtns, diagramBody, stageClass, stageTitle string
	if b.Error != "" {
		exportBtns = `<button type="button" class="mini-btn" data-mermaid-dl="mmd">MMD</button>`
		diagramBody = `<div class="mermaid-error"><strong>Mermaid render error:</strong> ` + escapeHTML(b.Error) + `</div>`
	} else {
		exportBtns = `<button type="button" class="mini-btn" data-mermaid-lightbox title="Open zoomable PNG lightbox">Lightbox</button><span class="btn-sep" aria-hidden="true"></span><button type="button" class="mini-btn" data-mermaid-dl="png" title="Download high-res PNG">PNG</button><button type="button" class="mini-btn" data-mermaid-dl="svg" title="Download SVG">SVG</button><button type="button" class="mini-btn" data-mermaid-dl="mmd" title="Download Mermaid source">MMD</button>`
		stageClass = " is-zoomable"
		stageTitle = ` title="Click to open PNG lightbox"`
		diagramBody = `<div class="mermaid-svg mermaid-light"></div><div class="mermaid-svg mermaid-dark"></div>`
	}

	flipFlag := "0"
	if flippable {
		flipFlag = "1"
	}
	labelEsc := escapeHTML(label)
	fmt.Fprintf(sb, `<figure class="mermaid-card" data-mermaid-id="%s" data-mermaid-title="%s" data-mermaid-flippable="%s" data-mermaid-dir="%s" data-line="%d"><figcaption class="mermaid-bar"><div class="mermaid-meta"><span class="mermaid-title">%s</span>%s%s</div><div class="mermaid-actions"><button type="button" class="mini-btn is-active" data-mermaid-view="diagram">Diagram</button><button type="button" class="mini-btn" data-mermaid-view="source">Source</button>%s%s</div></figcaption><div class="mermaid-stage%s"%s>%s</div><pre class="mermaid-source" hidden><code>%s</code></pre></figure>`,
		escapeHTML(b.ID), labelEsc, flipFlag, escapeHTML(dir), b.Span.Line,
		labelEsc, dirBadge, sourceTag, flipBtns, exportBtns,
		stageClass, stageTitle, diagramBody, escapeHTML(b.Code))
}

func renderListItem(sb *strings.Builder, item ListItemNode, basePath, docFile string) {
	if item.Checkbox != nil {
		w := item.Checkbox
		cls, attr := "", ""
		if w.Checked {
			cls, attr = " is-checked", " checked"
		}
		fmt.Fprintf(sb, `<li class="task-item%s" data-line="%d"><label class="task-label"><input type="checkbox" class="md-checkbox" data-widget-kind="checkbox" data-widget-id="%s"%s><span class="task-text">`,
			cls, w.Line, escapeHTML(w.ID), attr)
		renderInlines(sb, item.Inlines, basePath, docFile)
		sb.WriteString(`</span></label>`)
	} else {
		fmt.Fprintf(sb, `<li data-line="%d">`, item.Line)
		renderInlines(sb, item.Inlines, basePath, docFile)
	}
	if len(item.Children) > 0 {
		renderBlocks(sb, item.Children, basePath, docFile)
	}
	sb.WriteString(`</li>`)
}

func widgetLabelHTML(label string) string {
	if label == "" {
		return ""
	}
	return `<span class="widget-label">` + escapeHTML(label) + `</span>`
}

func renderInlines(sb *strings.Builder, nodes []InlineNode, basePath, docFile string) {
	for _, n := range nodes {
		switch n.Type {
		case "text":
			sb.WriteString(escapeHTML(n.Value))
		case "code":
			sb.WriteString("<code>" + escapeHTML(n.Value) + "</code>")
		case "strong", "em", "del":
			sb.WriteString("<" + n.Type + ">")
			renderInlines(sb, n.Children, basePath, docFile)
			sb.WriteString("</" + n.Type + ">")
		case "br":
			sb.WriteString("<br>")
		case "link":
			href := sanitizeHref(n.Href)
			ext := ""
			if externalSchemeRe.MatchString(href) {
				ext = ` target="_blank" rel="noopener noreferrer"`
			}
			fmt.Fprintf(sb, `<a href="%s"%s>`, escapeHTML(href), ext)
			renderInlines(sb, n.Children, basePath, docFile)
			sb.WriteString("</a>")
		case "image":
			fmt.Fprintf(sb, `<img class="md-img" src="%s" alt="%s" loading="lazy">`,
				escapeHTML(rewriteAssetSrc(n.Src, basePath, docFile)), escapeHTML(n.Alt))
		case "checkbox":
			if w := n.Widget; w != nil {
				attr := ""
				if w.Checked {
					attr = " checked"
				}
				fmt.Fprintf(sb, `<label class="inline-chk" data-line="%d"><input type="checkbox" class="md-checkbox" data-widget-kind="checkbox" data-widget-id="%s"%s aria-label="%s"></label> `,
					w.Line, escapeHTML(w.ID), attr, escapeHTML(w.Label))
			}
		case "input":
			if w := n.Widget; w != nil {
				typ := w.InputType
				if typ == "" {
					typ = "text"
				}
				idEsc := escapeHTML(w.ID)
				fmt.Fprintf(sb, `<span class="widget-inline" data-widget-wrap="%s">%s<input class="md-input" type="%s" data-widget-kind="input" data-widget-id="%s" data-widget-name="%s" data-line="%d" value="%s" placeholder="%s"><span class="save-dot" aria-hidden="true"></span></span>`,
					idEsc, widgetLabelHTML(w.Label), escapeHTML(typ), idEsc,
					escapeHTML(w.Name), w.Line, escapeHTML(w.Value), escapeHTML(w.Placeholder))
			}
		case "select":
			if w := n.Widget; w != nil {
				idEsc := escapeHTML(w.ID)
				fmt.Fprintf(sb, `<span class="widget-inline" data-widget-wrap="%s">%s<select class="md-select" data-widget-kind="select" data-widget-id="%s" data-widget-name="%s" data-line="%d">`,
					idEsc, widgetLabelHTML(w.Label), idEsc, escapeHTML(w.Name), w.Line)
				for _, opt := range w.Options {
					sel := ""
					if opt == w.Value {
						sel = " selected"
					}
					optEsc := escapeHTML(opt)
					fmt.Fprintf(sb, `<option value="%s"%s>%s</option>`, optEsc, sel, optEsc)
				}
				sb.WriteString(`</select><span class="save-dot" aria-hidden="true"></span></span>`)
			}
		case "radio":
			if w := n.Widget; w != nil {
				aria := w.Label
				if aria == "" {
					aria = w.Name
				}
				idEsc, nameEsc := escapeHTML(w.ID), escapeHTML(w.Name)
				fmt.Fprintf(sb, `<span class="widget-radio-group" role="group" aria-label="%s" data-widget-wrap="%s">%s`,
					escapeHTML(aria), idEsc, widgetLabelHTML(w.Label))
				for _, opt := range w.Options {
					active, pressed := "", "false"
					if opt == w.Value {
						active, pressed = " is-selected", "true"
					}
					optEsc := escapeHTML(opt)
					fmt.Fprintf(sb, `<button type="button" class="md-radio-pill%s" data-widget-kind="radio" data-widget-id="%s" data-widget-name="%s" data-radio-value="%s" data-line="%d" aria-pressed="%s">%s</button>`,
						active, idEsc, nameEsc, optEsc, w.Line, pressed, optEsc)
				}
				sb.WriteString(`<span class="save-dot" aria-hidden="true"></span></span>`)
			}
		case "action":
			if w := n.Widget; w != nil {
				style := w.Style
				if style == "" {
					style = "default"
				}
				fmt.Fprintf(sb, `<button type="button" class="md-action-btn style-%s" data-widget-kind="action" data-widget-id="%s" data-widget-name="%s" data-line="%d">%s</button>`,
					escapeHTML(style), escapeHTML(w.ID), escapeHTML(w.Name), w.Line, escapeHTML(w.Label))
			}
		}
	}
}

func renderStatChips(stats DocumentStats) string {
	var chips strings.Builder
	if stats.CheckboxTotal > 0 {
		fmt.Fprintf(&chips, `<span class="stat-pill" id="stat-checklists">%d / %d checked</span>`, stats.CheckboxChecked, stats.CheckboxTotal)
	}
	if stats.InputCount > 0 {
		s := "s"
		if stats.InputCount == 1 {
			s = ""
		}
		fmt.Fprintf(&chips, `<span class="stat-pill">%d field%s</span>`, stats.InputCount, s)
	}
	if stats.MermaidCount > 0 {
		s := "s"
		if stats.MermaidCount == 1 {
			s = ""
		}
		fmt.Fprintf(&chips, `<span class="stat-pill">%d diagram%s</span>`, stats.MermaidCount, s)
	}
	return chips.String()
}

func safeBootstrapJSON(v any) string {
	b, _ := json.Marshal(v)
	return strings.ReplaceAll(string(b), "<", `\u003c`)
}

// RenderFullPage renders the complete interactive HTML page for a parsed document.
func RenderFullPage(opts PageRenderOptions) string {
	ir := opts.IR
	if ir == nil {
		return RenderIndexPage(opts.BasePath, opts.RootLabel, opts.Files)
	}
	base := normalizeBasePath(opts.BasePath)
	query := formatAssetQuery(opts.AssetQuery)
	bodyHTML, tocHTML := RenderDocumentHTML(ir, base, ir.File)

	var fileOpts strings.Builder
	for _, f := range opts.Files {
		sel := ""
		if f == ir.File {
			sel = " selected"
		}
		fEsc := escapeHTML(f)
		fmt.Fprintf(&fileOpts, `<option value="%s"%s>%s</option>`, fEsc, sel, fEsc)
	}

	bootstrap := safeBootstrapJSON(struct {
		File     string        `json:"file"`
		Revision string        `json:"revision"`
		Stats    DocumentStats `json:"stats"`
		BasePath string        `json:"basePath"`
	}{ir.File, ir.Revision, ir.Stats, base})

	shellClass := "shell"
	if tocHTML != "" {
		shellClass = "shell has-toc"
	}

	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s · md-live</title>
<link rel="stylesheet" href="%s/static/app.css%s">
</head>
<body>
<header class="topbar">
  <div class="topbar-inner">
    <div class="brand-group">
      <a class="brand-tag" href="%s/" title="Root: %s">md-live</a>
      <select class="file-picker" id="file-picker" aria-label="Select markdown file">%s</select>
      %s
    </div>
    <div class="status-group">
      <span class="sync-pill" id="sync-pill"><span class="sync-dot"></span><span id="sync-text">Synced · %s</span></span>
      <button type="button" class="bar-btn" id="drawer-toggle">Source &amp; Actions</button>
      <button type="button" class="bar-btn" id="theme-toggle">Theme</button>
    </div>
  </div>
</header>

<div class="%s" id="shell">
  %s
  <main class="doc-card"><article class="doc-body" id="doc-body">%s</article></main>
  <aside class="drawer" id="drawer" aria-label="Live markdown source and actions">
    <div class="drawer-tabs">
      <button type="button" class="drawer-tab is-active" data-drawer-tab="source">Live .md Source</button>
      <button type="button" class="drawer-tab" data-drawer-tab="actions">Action Stream</button>
    </div>
    <div class="drawer-pane" id="pane-source"><pre class="source-pre" id="live-source">%s</pre></div>
    <div class="drawer-pane" id="pane-actions" hidden><div class="action-list" id="action-list"><div class="action-empty">Click a checkbox, edit a field, or press an action button to see live events streamed from the server.</div></div></div>
  </aside>
</div>

<div class="lightbox" id="mermaid-lightbox" hidden role="dialog" aria-modal="true" aria-label="Mermaid PNG lightbox">
  <div class="lb-bar">
    <div class="lb-left">
      <span class="lb-title" id="lb-title">Diagram</span>
      <span class="mermaid-dir-pill" id="lb-dir" hidden>LR</span>
      <span class="lb-meta" id="lb-dims">PNG</span>
    </div>
    <div class="lb-right">
      <button type="button" class="mini-btn" id="lb-flip" hidden title="Flip orientation (F)">Flip</button>
      <button type="button" class="mini-btn save-mmd-btn" id="lb-save" hidden title="Save flipped orientation to source file">Save to file</button>
      <button type="button" class="mini-btn" id="lb-theme" title="Toggle PNG background theme">Dark PNG</button>
      <span class="btn-sep" aria-hidden="true"></span>
      <button type="button" class="mini-btn" id="lb-zoom-out" title="Zoom out (-)">-</button>
      <button type="button" class="mini-btn lb-zoom-readout" id="lb-zoom-reset" title="Reset to 100%% (1)">100%%</button>
      <button type="button" class="mini-btn" id="lb-zoom-in" title="Zoom in (+)">+</button>
      <button type="button" class="mini-btn" id="lb-zoom-fit" title="Fit to viewport (0)">Fit</button>
      <span class="btn-sep" aria-hidden="true"></span>
      <button type="button" class="mini-btn" id="lb-copy-png" title="Copy PNG image to clipboard">Copy PNG</button>
      <button type="button" class="mini-btn" id="lb-dl-png" title="Download PNG">PNG</button>
      <button type="button" class="mini-btn" id="lb-dl-svg" title="Download SVG">SVG</button>
      <button type="button" class="mini-btn" id="lb-dl-mmd" title="Download Mermaid source">MMD</button>
      <span class="btn-sep" aria-hidden="true"></span>
      <button type="button" class="mini-btn" id="lb-close" title="Close (Esc)">Close</button>
    </div>
  </div>
  <div class="lb-viewport" id="lb-viewport">
    <div class="lb-canvas" id="lb-canvas"><img class="lb-img" id="lb-img" alt="Mermaid diagram PNG" draggable="false"></div>
    <div class="lb-hint">Scroll to zoom · Drag to pan · Double-click to toggle fit · F to flip · Esc to close</div>
  </div>
</div>

<div class="toast" id="toast" role="status" aria-live="polite"></div>

<script>window.__MD_LIVE__ = %s;</script>
<script src="%s/static/beautiful-mermaid.js%s"></script>
<script src="%s/static/app.js%s"></script>
</body>
</html>`,
		escapeHTML(ir.Title), base, query, escapeHTML(base), escapeHTML(opts.RootLabel),
		fileOpts.String(), renderStatChips(ir.Stats), escapeHTML(shortRevision(ir.Revision)),
		shellClass, tocHTML, bodyHTML, escapeHTML(opts.Source),
		bootstrap, base, query, base, query)
}

// RenderIndexPage renders the landing page shown when no ?file= is active yet.
func RenderIndexPage(basePath, rootLabel string, files []string) string {
	base := normalizeBasePath(basePath)
	var listHTML strings.Builder
	if len(files) == 0 {
		listHTML.WriteString(`<p class="index-empty">No files opened yet. Paste a local path above or run <code>mtm web open &lt;file&gt;</code>.</p>`)
	} else {
		listHTML.WriteString(`<ul class="index-list">`)
		for _, f := range files {
			href := base + "/view?file=" + url.QueryEscape(f)
			fmt.Fprintf(&listHTML, `<li class="index-item"><a href="%s">%s</a></li>`, escapeHTML(href), escapeHTML(f))
		}
		listHTML.WriteString(`</ul>`)
	}
	bootstrap := safeBootstrapJSON(struct {
		BasePath string `json:"basePath"`
	}{base})

	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>md-live</title>
<link rel="stylesheet" href="%s/static/app.css">
</head>
<body>
<header class="topbar">
  <div class="topbar-inner">
    <div class="brand-group"><a class="brand-tag" href="%s/" title="Root: %s">md-live</a></div>
    <div class="status-group"><button type="button" class="bar-btn" id="theme-toggle">Theme</button></div>
  </div>
</header>
<main class="index-shell">
  <section class="index-card">
    <h1>Live Markdown &amp; Mermaid Viewer</h1>
    <p class="index-lead">Open any local <code>.md</code>, <code>.markdown</code>, <code>.mmd</code>, or <code>.mermaid</code> document to interact with checklists, fields, and diagrams.</p>
    <form class="index-form" method="get" action="%s/view">
      <input class="index-input" type="text" name="file" placeholder="/path/to/document.md" aria-label="Markdown file path" required>
      <button class="index-submit" type="submit">Open</button>
    </form>
    <div class="index-hint">CLI shortcut: <code>mtm web open &lt;file&gt;</code></div>
    <h2 class="index-section-title">Recent &amp; available files</h2>
    %s
  </section>
</main>
<script>window.__MD_LIVE__ = %s;</script>
<script src="%s/static/app.js"></script>
</body>
</html>`,
		base, escapeHTML(base), escapeHTML(rootLabel), escapeHTML(base), listHTML.String(), bootstrap, base)
}
