package views

import (
	"fmt"
	"strings"

	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Scopes and node IDs emitted by the dumb read-only Vim code viewer presenter.
const (
	ScopeCodeView    = "code.view"
	CodeViewerBodyID = "code.viewer.body"
)

// CodeViewerFacts projects truthful read-only facts from CodeViewerState so
// declarative keymap guards can compose behavior in pure data.
func CodeViewerFacts(st *CodeViewerState) map[string]any {
	if st == nil {
		return map[string]any{
			"hasHighlightAtCursor": false,
			"highlightKind":        "",
			"highlightId":          "",
			"highlightColor":       "",
			"visualActive":         false,
			"visualMode":           "",
			"searchActive":         false,
			"hasSearch":            false,
			"line":                 0,
			"col":                  0,
			"totalLines":           0,
			"file":                 "",
		}
	}
	h, hasH := st.HighlightAt(st.CursorLine)
	hKind, hID, hColor := "", "", ""
	if hasH {
		hKind = h.Kind
		hID = h.ID
		hColor = h.EffectiveColor()
	}
	return map[string]any{
		"hasHighlightAtCursor": hasH,
		"highlightKind":        hKind,
		"highlightId":          hID,
		"highlightColor":       hColor,
		"visualActive":         st.VisualActive(),
		"visualMode":           st.VisualMode,
		"searchActive":         st.SearchActive,
		"hasSearch":            st.SearchQuery != "",
		"line":                 st.CursorLine,
		"col":                  st.CursorCol + 1,
		"totalLines":           st.LineCount(),
		"file":                 st.FilePath,
	}
}

// CodeViewerView projects CodeViewerState into a pure ui.ViewNode tree with
// zero store access and zero behavioral policy.
func CodeViewerView(st *CodeViewerState, width, height int) ui.ViewNode {
	if st == nil {
		return ui.ViewNode{
			ID:        CodeViewerBodyID,
			Kind:      ui.KindText,
			Scope:     ScopeCodeView,
			Focusable: true,
			Tone:      "muted",
			Facts:     CodeViewerFacts(nil),
			Props:     map[string]any{"text": "No file loaded."},
		}
	}
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 28
	}
	hover, hasHover := st.HighlightAt(st.CursorLine)
	footerH := codeViewerFooterHeight(st, hasHover)
	codeH := max(height-footerH-2, 5)
	st.ViewportH = codeH
	st.EnsureCursorVisible(3)

	children := []ui.ViewNode{
		codeViewerHeaderNode(st),
		codeViewerLinesNode(st, codeH),
		codeViewerFooterNode(st, hover, hasHover),
	}
	return ui.ViewNode{
		ID:        CodeViewerBodyID,
		Kind:      ui.KindVStack,
		Scope:     ScopeCodeView,
		Focusable: true,
		Facts:     CodeViewerFacts(st),
		Children:  children,
	}
}

func codeViewerFooterHeight(st *CodeViewerState, hasHover bool) int {
	if st.SearchActive {
		return 2
	}
	if hasHover {
		return 3
	}
	return 1
}

func codeViewerHeaderNode(st *CodeViewerState) ui.ViewNode {
	modeLabel := "NORMAL"
	modeTone := "accent"
	if st.SearchActive {
		modeLabel, modeTone = "SEARCH", "warning"
	} else if st.VisualMode == CodeVisualLine {
		modeLabel, modeTone = "V-LINE", "warning"
	} else if st.VisualMode == CodeVisualChar {
		modeLabel, modeTone = "VISUAL", "warning"
	}
	srcCount, annCount, srcColor, annColor := countHighlightLegend(st.Highlights)
	parts := []string{
		fmt.Sprintf(" %s ", modeLabel),
		fmt.Sprintf("  L%d:%d / %d", st.CursorLine, st.CursorCol+1, st.LineCount()),
	}
	spans := []ui.ToneSpan{{From: 0, To: len([]rune(parts[0])), Tone: "badge." + modeTone}}
	cursor := len([]rune(parts[0])) + len([]rune(parts[1]))
	if srcCount > 0 {
		seg := fmt.Sprintf("  ·  ┃ source(%d:%s)", srcCount, srcColor)
		srcStyle := ResolveCodeHighlightStyle(srcColor, DefaultSourceHighlightColor)
		spans = append(spans, ui.ToneSpan{From: cursor + 5, To: cursor + len([]rune(seg)), Tone: srcStyle.Tone})
		parts = append(parts, seg)
		cursor += len([]rune(seg))
	}
	if annCount > 0 {
		seg := fmt.Sprintf("  ·  ✎ notes(%d:%s)", annCount, annColor)
		annStyle := ResolveCodeHighlightStyle(annColor, DefaultAnnotationHighlightColor)
		spans = append(spans, ui.ToneSpan{From: cursor + 5, To: cursor + len([]rune(seg)), Tone: annStyle.Tone})
		parts = append(parts, seg)
		cursor += len([]rune(seg))
	}
	if st.SearchQuery != "" {
		seg := fmt.Sprintf("  ·  /%s", st.SearchQuery)
		spans = append(spans, ui.ToneSpan{From: cursor + 5, To: cursor + len([]rune(seg)), Tone: "match"})
		parts = append(parts, seg)
	}
	return ui.ViewNode{
		ID:    "code.viewer.header",
		Kind:  ui.KindText,
		Tone:  "muted",
		Box:   layout.Constraints{FixedH: 1},
		Props: map[string]any{"text": strings.Join(parts, ""), "spans": spans},
	}
}

func countHighlightLegend(hs []CodeHighlight) (srcCount, annCount int, srcColor, annColor string) {
	srcColor = DefaultSourceHighlightColor
	annColor = DefaultAnnotationHighlightColor
	for _, h := range hs {
		if h.Kind == CodeHighlightAnnotation {
			annCount++
			annColor = h.EffectiveColor()
			continue
		}
		srcCount++
		srcColor = h.EffectiveColor()
	}
	return srcCount, annCount, srcColor, annColor
}

func codeViewerLinesNode(st *CodeViewerState, codeH int) ui.ViewNode {
	numWidth := max(len(fmt.Sprint(st.LineCount())), 3)
	startIdx := min(max(st.ScrollTop, 0), max(st.LineCount()-1, 0))
	endIdx := min(startIdx+codeH, st.LineCount())
	rows := make([]ui.ViewNode, 0, codeH)
	for idx := startIdx; idx < endIdx; idx++ {
		rows = append(rows, codeViewerLineNode(st, idx+1, numWidth))
	}
	for len(rows) < codeH {
		rows = append(rows, ui.ViewNode{
			Kind:  ui.KindText,
			Tone:  "dim",
			Box:   layout.Constraints{FixedH: 1},
			Props: map[string]any{"text": "~"},
		})
	}
	return ui.ViewNode{
		ID:       "code.viewer.lines",
		Kind:     ui.KindVStack,
		Box:      layout.Constraints{Flex: 1},
		Children: rows,
	}
}

func codeViewerLineNode(st *CodeViewerState, lineNum, numWidth int) ui.ViewNode {
	raw := ""
	if lineNum >= 1 && lineNum <= len(st.Lines) {
		raw = strings.ReplaceAll(st.Lines[lineNum-1], "\t", "    ")
	}
	hits := st.HighlightsAt(lineNum)
	primary, hasH := st.HighlightAt(lineNum)
	gutterGlyph, gutterTone := codeLineGutter(hits, primary, hasH)
	prefix := fmt.Sprintf("%s %*d │ ", gutterGlyph, numWidth, lineNum)
	prefixLen := len([]rune(prefix))
	codeRunes := []rune(raw)
	tag := codeLineInlineTag(hits, lineNum)
	fullText := prefix + raw + tag

	spans := buildCodeLineSpans(st, lineNum, prefixLen, len(codeRunes), tag, gutterTone, primary, hasH, raw)
	return ui.ViewNode{
		ID:    fmt.Sprintf("code.line.%d", lineNum),
		Kind:  ui.KindText,
		Tone:  "text",
		Box:   layout.Constraints{FixedH: 1},
		Props: map[string]any{"text": fullText, "spans": spans},
	}
}

func codeLineGutter(hits []CodeHighlight, primary CodeHighlight, hasH bool) (string, string) {
	if !hasH {
		return " ", "muted"
	}
	hasSrc, hasAnn := false, false
	for _, h := range hits {
		if h.Kind == CodeHighlightAnnotation {
			hasAnn = true
		} else {
			hasSrc = true
		}
	}
	glyph := "┃"
	if hasSrc && hasAnn {
		glyph = "▌"
	} else if hasAnn {
		glyph = "✎"
	}
	return glyph, primary.Style().Tone
}

func codeLineInlineTag(hits []CodeHighlight, lineNum int) string {
	for _, h := range hits {
		s, _ := h.NormalizedBounds()
		if h.Kind == CodeHighlightAnnotation && s == lineNum {
			summary := strings.TrimSpace(h.Label)
			if summary == "" {
				summary = strings.TrimSpace(h.Note)
			}
			if summary == "" {
				summary = "note"
			}
			if rs := []rune(summary); len(rs) > 28 {
				summary = string(rs[:27]) + "…"
			}
			return "   ✎ " + summary
		}
	}
	return ""
}

func buildCodeLineSpans(st *CodeViewerState, lineNum, prefixLen, codeLen int, tag, gutterTone string, primary CodeHighlight, hasH bool, raw string) []ui.ToneSpan {
	isCur := lineNum == st.CursorLine
	spans := make([]ui.ToneSpan, 0, 8)
	spans = append(spans, ui.ToneSpan{From: 0, To: 1, Tone: gutterTone})
	numTone := "muted"
	if isCur {
		numTone = "accent"
	}
	spans = append(spans, ui.ToneSpan{From: 1, To: prefixLen, Tone: numTone})
	if hasH {
		hst := primary.Style()
		spans = append(spans, ui.ToneSpan{From: prefixLen, To: prefixLen + max(codeLen, 1), Tone: "text", Bg: hst.Bg})
		if tag != "" {
			spans = append(spans, ui.ToneSpan{From: prefixLen + codeLen, To: prefixLen + codeLen + len([]rune(tag)), Tone: hst.Tone})
		}
	} else if isCur {
		spans = append(spans, ui.ToneSpan{From: prefixLen, To: prefixLen + max(codeLen, 1), Tone: "text", Bg: "surface.focus"})
	}
	spans = appendSearchSpans(spans, st.SearchQuery, raw, prefixLen)
	spans = appendVisualSpans(spans, st, lineNum, codeLen, prefixLen)
	if isCur {
		cCol := min(max(st.CursorCol, 0), max(codeLen-1, 0))
		spans = append(spans, ui.ToneSpan{From: prefixLen + cCol, To: prefixLen + cCol + 1, Tone: "badge.accent"})
	}
	return spans
}

func appendSearchSpans(spans []ui.ToneSpan, query, raw string, prefixLen int) []ui.ToneSpan {
	if query == "" {
		return spans
	}
	qLen := len([]rune(query))
	for _, col := range FindAllMatchCols(raw, query) {
		spans = append(spans, ui.ToneSpan{From: prefixLen + col, To: prefixLen + col + qLen, Tone: "match"})
	}
	return spans
}

func appendVisualSpans(spans []ui.ToneSpan, st *CodeViewerState, lineNum, codeLen, prefixLen int) []ui.ToneSpan {
	from, to, ok := st.SelectedColRange(lineNum, codeLen)
	if !ok {
		return spans
	}
	return append(spans, ui.ToneSpan{From: prefixLen + from, To: prefixLen + to, Tone: "selected"})
}

func codeViewerFooterNode(st *CodeViewerState, hover CodeHighlight, hasHover bool) ui.ViewNode {
	if st.SearchActive {
		return ui.ViewNode{
			ID:     "code.viewer.search",
			Kind:   ui.KindPanel,
			Border: false,
			Box:    layout.Constraints{FixedH: 2},
			Children: []ui.ViewNode{
				{
					ID:    "code.viewer.search.input",
					Kind:  ui.KindText,
					Tone:  "warning",
					Box:   layout.Constraints{FixedH: 1},
					Props: map[string]any{"text": "/" + st.SearchDraft + "█"},
				},
				{
					ID:    "code.viewer.search.hint",
					Kind:  ui.KindText,
					Tone:  "muted",
					Box:   layout.Constraints{FixedH: 1},
					Props: map[string]any{"text": "enter jump to match · esc cancel"},
				},
			},
		}
	}
	if hasHover {
		return codeViewerHunkInspector(hover)
	}
	return ui.ViewNode{
		ID:    "code.viewer.footer",
		Kind:  ui.KindText,
		Tone:  "muted",
		Box:   layout.Constraints{FixedH: 1},
		Props: map[string]any{"text": "j/k/h/l w/b/e gg/G / n/N · v/V select · a annotate · c customize · T to-do · ctrl+o/q/backspace back"},
	}
}

func codeViewerHunkInspector(h CodeHighlight) ui.ViewNode {
	hst := h.Style()
	kindLabel := "SOURCE"
	icon := "┃"
	if h.Kind == CodeHighlightAnnotation {
		kindLabel = "ANNOTATION"
		icon = "✎"
	}
	title := strings.TrimSpace(h.Label)
	if title == "" {
		title = h.LineRangeText()
	}
	head := fmt.Sprintf(" %s %s · %s · color:%s · %s", icon, kindLabel, h.LineRangeText(), h.EffectiveColor(), title)
	noteLine := strings.TrimSpace(strings.ReplaceAll(h.Note, "\n", " "))
	if noteLine == "" {
		noteLine = "c customize color/note · T promote to task · a add annotation"
	}
	return ui.ViewNode{
		ID:     "code.viewer.inspector",
		Kind:   ui.KindVStack,
		Box:    layout.Constraints{FixedH: 3},
		Border: false,
		Children: []ui.ViewNode{
			{
				ID:    "code.viewer.inspector.head",
				Kind:  ui.KindText,
				Tone:  hst.Tone,
				Box:   layout.Constraints{FixedH: 1},
				Props: map[string]any{"text": head},
			},
			{
				ID:    "code.viewer.inspector.note",
				Kind:  ui.KindText,
				Tone:  "text",
				Box:   layout.Constraints{FixedH: 1},
				Props: map[string]any{"text": "   " + noteLine},
			},
			{
				ID:    "code.viewer.inspector.hint",
				Kind:  ui.KindText,
				Tone:  "muted",
				Box:   layout.Constraints{FixedH: 1},
				Props: map[string]any{"text": "   c customize highlight · T promote to task · a annotate · ]h/[h jump hunks · ctrl+o/q back"},
			},
		},
	}
}
