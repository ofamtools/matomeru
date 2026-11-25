package mdweb

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type lineInfo struct {
	text               string
	start, end, lineNo int
	eol                string
}

type parseContext struct {
	headings               []HeadingInfo
	headingSlugCounts      map[string]int
	widgets                []WidgetSpec
	widgetIDCounts         map[string]int
	checkboxCount          int
	mermaidCount           int
	tableCount             int
	resolveExternalMermaid func(relPath string) (string, error)
}

type textSegment struct {
	text             string
	rawStart, lineNo int
}

type offsetMapper struct {
	combined string
	segments [][4]int // localStart, localEnd, rawStart, lineNo
}

type inlineOptions struct {
	allowInlineCheckbox bool
	inTableCell         bool
}

var (
	slugStripRe      = regexp.MustCompile("[`*_~\\[\\]()]")
	slugNonAlnumRe   = regexp.MustCompile(`[^a-z0-9]+`)
	widgetCleanRe    = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)
	directiveAttrRe  = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_-]*)(?:\s*=\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|([^\s"']+)))?`)
	directiveTokenRe = regexp.MustCompile(`^\{\{(input|text|select|radio|action):([a-zA-Z0-9_.-]+)(?:=(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|([^\s}]+)))?((?:\s+[a-zA-Z_][a-zA-Z0-9_-]*(?:\s*=\s*(?:"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|[^\s}]+))?)*)\s*\}\}`)
	brTagRe          = regexp.MustCompile(`(?i)^<br\s*/?>`)
	tableDelimCharRe = regexp.MustCompile(`^\|?[\s:|-]+\|?$`)
	tableCellDelimRe = regexp.MustCompile(`^:?-{1,}:?$`)
	listItemRe       = regexp.MustCompile(`^(\s*)([-*+]|(\d+)\.)(\s+)(.*)$`)
	checkboxPrefixRe = regexp.MustCompile(`^\[([ xX])\](?:\s+(.*)|$)`)
	fenceOpenRe      = regexp.MustCompile("^(\\s{0,3})(`{3,}|~{3,})(.*)$")
	mermaidEmbedRe   = regexp.MustCompile(`(?i)^\s*(?:\{\{mermaid:([^}]+)\}\}|!\[\[([^\]]+\.(?:mmd|mermaid))\]\]|!\[([^\]]*)\]\(([^)]+\.(?:mmd|mermaid))\))\s*$`)
	blockquoteRe     = regexp.MustCompile(`^\s{0,3}> ?`)
	alertHeaderRe    = regexp.MustCompile(`(?i)^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*$`)
	attrUnescaper    = strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, "\n")
)

// ComputeRevision returns the first 16 lowercase hex characters of the SHA-256 digest of source.
func ComputeRevision(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])[:16]
}

func isMermaidFilename(file string) bool {
	lower := strings.ToLower(file)
	return strings.HasSuffix(lower, ".mmd") || strings.HasSuffix(lower, ".mermaid")
}

func rangeSpan(a, b lineInfo) SourceSpan {
	return SourceSpan{Start: a.start, End: b.end, Line: a.lineNo, EndLine: b.lineNo}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func nextUnique(counts map[string]int, base, sep string) string {
	n := counts[base]
	counts[base] = n + 1
	if n == 0 {
		return base
	}
	return fmt.Sprintf("%s%s%d", base, sep, n+1)
}

func addWidget(ctx *parseContext, w WidgetSpec) *WidgetSpec {
	ctx.widgets = append(ctx.widgets, w)
	wCopy := w
	return &wCopy
}

func countRun(s string, i, end int, ch byte) int {
	n := 0
	for i+n < end && s[i+n] == ch {
		n++
	}
	return n
}

func skipBlanks(lines []lineInfo, idx int) int {
	for idx < len(lines) && strings.TrimSpace(lines[idx].text) == "" {
		idx++
	}
	return idx
}

func splitLinesWithOffsets(source string) []lineInfo {
	lines := make([]lineInfo, 0, 16)
	for i, lineNo, n := 0, 1, len(source); i < n; lineNo++ {
		start := i
		for i < n && source[i] != '\n' && source[i] != '\r' {
			i++
		}
		end, eol := i, ""
		if i < n {
			if source[i] == '\r' && i+1 < n && source[i+1] == '\n' {
				eol, i = "\r\n", i+2
			} else {
				eol, i = source[i:i+1], i+1
			}
		}
		lines = append(lines, lineInfo{source[start:end], start, end, lineNo, eol})
	}
	if len(lines) == 0 {
		return []lineInfo{{lineNo: 1}}
	}
	return lines
}

func parseDirectiveAttrs(raw string) map[string]string {
	attrs := make(map[string]string)
	for _, idx := range directiveAttrRe.FindAllStringSubmatchIndex(raw, -1) {
		k := raw[idx[2]:idx[3]]
		switch {
		case idx[4] != -1:
			attrs[k] = attrUnescaper.Replace(raw[idx[4]:idx[5]])
		case idx[6] != -1:
			attrs[k] = raw[idx[6]:idx[7]]
		case idx[8] != -1:
			attrs[k] = raw[idx[8]:idx[9]]
		default:
			attrs[k] = "true"
		}
	}
	return attrs
}

func (m *offsetMapper) locate(localIdx int) (int, int) {
	for _, seg := range m.segments {
		if localIdx >= seg[0] && localIdx <= seg[1] {
			return seg[2] + (localIdx - seg[0]), seg[3]
		}
	}
	if len(m.segments) == 0 {
		return 0, 1
	}
	last := m.segments[len(m.segments)-1]
	return last[2] + (last[1] - last[0]), last[3]
}

func (m *offsetMapper) toSpan(localStart, localEnd int) SourceSpan {
	sRaw, sLine := m.locate(localStart)
	if localEnd <= localStart {
		return SourceSpan{Start: sRaw, End: sRaw, Line: sLine, EndLine: sLine}
	}
	eRaw, eLine := m.locate(localEnd - 1)
	return SourceSpan{Start: sRaw, End: eRaw + 1, Line: sLine, EndLine: eLine}
}

func plainTextOfInlines(nodes []InlineNode) string {
	var b strings.Builder
	for _, n := range nodes {
		switch n.Type {
		case "text", "code":
			b.WriteString(n.Value)
		case "strong", "em", "del", "link":
			b.WriteString(plainTextOfInlines(n.Children))
		case "image":
			b.WriteString(n.Alt)
		case "br":
			b.WriteByte(' ')
		case "input", "select", "radio":
			if n.Widget != nil {
				b.WriteString(firstNonEmpty(n.Widget.Label, n.Widget.Name))
			}
		case "action":
			if n.Widget != nil {
				b.WriteString(n.Widget.Label)
			}
		case "checkbox":
			if n.Widget != nil && n.Widget.Checked {
				b.WriteString("[x]")
			} else {
				b.WriteString("[ ]")
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func isASCIIWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}

func isASCIIAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func truncateRunes(s string, maxRunes int) string {
	for i, c := range []rune(s) {
		_ = c
		if i == maxRunes {
			return string([]rune(s)[:maxRunes])
		}
	}
	return s
}

func splitPipeOptions(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, "|") {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseInlines(segments []textSegment, ctx *parseContext, opts inlineOptions) []InlineNode {
	m := &offsetMapper{segments: make([][4]int, 0, len(segments))}
	var b strings.Builder
	for i, seg := range segments {
		if i > 0 {
			b.WriteByte('\n')
		}
		start := b.Len()
		b.WriteString(seg.text)
		m.segments = append(m.segments, [4]int{start, b.Len(), seg.rawStart, seg.lineNo})
	}
	m.combined = b.String()
	return parseInlineRange(m.combined, 0, len(m.combined), m, ctx, opts)
}

func parseInlineRange(text string, start, end int, mapper *offsetMapper, ctx *parseContext, opts inlineOptions) []InlineNode {
	nodes := make([]InlineNode, 0, 4)
	var textBuf strings.Builder
	flushText := func() {
		if textBuf.Len() > 0 {
			nodes = append(nodes, InlineNode{Type: "text", Value: textBuf.String()})
			textBuf.Reset()
		}
	}

	for i := start; i < end; {
		slice := text[i:end]

		// Inline checkbox in table cells: [ ] or [x] at start or after whitespace.
		if opts.allowInlineCheckbox && (i == start || isASCIIWhitespace(text[i-1])) &&
			len(slice) >= 3 && slice[0] == '[' && slice[2] == ']' &&
			(slice[1] == ' ' || slice[1] == 'x' || slice[1] == 'X') &&
			(len(slice) == 3 || isASCIIWhitespace(slice[3])) {
			flushText()
			markSpan := mapper.toSpan(i+1, i+2)
			id := fmt.Sprintf("chk-%d", ctx.checkboxCount)
			ctx.checkboxCount++
			label := firstNonEmpty(truncateRunes(strings.TrimSpace(slice[3:]), 80), fmt.Sprintf("Item %d", ctx.checkboxCount))
			wPtr := addWidget(ctx, WidgetSpec{
				Kind: WidgetCheckbox, ID: id, Name: id, Checked: slice[1] == 'x' || slice[1] == 'X',
				Label: label, Span: mapper.toSpan(i, i+3), ValueSpan: markSpan, Line: markSpan.Line,
			})
			nodes = append(nodes, InlineNode{Type: "checkbox", Widget: wPtr})
			i += 3
			if i < end && text[i] == ' ' {
				i++
			}
			continue
		}

		// Interactive directives: {{input:...}}, {{text:...}}, {{select:...}}, {{radio:...}}, {{action:...}}
		if strings.HasPrefix(slice, "{{") {
			if dm := directiveTokenRe.FindStringSubmatchIndex(slice); dm != nil {
				flushText()
				var val, attrRaw string
				switch {
				case dm[6] != -1:
					val = attrUnescaper.Replace(slice[dm[6]:dm[7]])
				case dm[8] != -1:
					val = slice[dm[8]:dm[9]]
				case dm[10] != -1:
					val = slice[dm[10]:dm[11]]
				}
				if dm[12] != -1 {
					attrRaw = slice[dm[12]:dm[13]]
				}
				nodes = append(nodes, buildDirectiveInline(slice[dm[2]:dm[3]], slice[dm[4]:dm[5]], val, parseDirectiveAttrs(attrRaw), mapper.toSpan(i, i+dm[1]), ctx))
				i += dm[1]
				continue
			}
		}

		// Safe <br> or <br/>
		if text[i] == '<' {
			if loc := brTagRe.FindStringIndex(slice); loc != nil {
				flushText()
				nodes = append(nodes, InlineNode{Type: "br"})
				i += loc[1]
				continue
			}
		}

		// Backslash escape
		if text[i] == '\\' && i+1 < end && strings.IndexByte("\\`*_{}[]()#+-.!|~<>", text[i+1]) >= 0 {
			textBuf.WriteByte(text[i+1])
			i += 2
			continue
		}

		// Inline code span (`...` or ``...``)
		if text[i] == '`' {
			ticks := countRun(text, i, end, '`')
			if closeIdx := findClosingBackticks(text, i+ticks, end, ticks); closeIdx != -1 {
				flushText()
				codeVal := text[i+ticks : closeIdx]
				if len(codeVal) >= 2 && codeVal[0] == ' ' && codeVal[len(codeVal)-1] == ' ' && strings.TrimSpace(codeVal) != "" {
					codeVal = codeVal[1 : len(codeVal)-1]
				}
				if opts.inTableCell {
					codeVal = strings.ReplaceAll(codeVal, `\|`, "|")
				}
				nodes = append(nodes, InlineNode{Type: "code", Value: codeVal})
				i = closeIdx + ticks
				continue
			}
		}

		// Image ![alt](src) and Link [text](href)
		if (text[i] == '!' && i+1 < end && text[i+1] == '[') || text[i] == '[' {
			isImg := text[i] == '!'
			openBr := i
			if isImg {
				openBr = i + 1
			}
			if closeBr := findMatchingPair(text, openBr, end, '[', ']', true); closeBr != -1 && closeBr+1 < end && text[closeBr+1] == '(' {
				if closeParen := findMatchingPair(text, closeBr+1, end, '(', ')', false); closeParen != -1 {
					flushText()
					target := strings.TrimSpace(text[closeBr+2 : closeParen])
					if isImg {
						nodes = append(nodes, InlineNode{Type: "image", Alt: text[openBr+1 : closeBr], Src: target})
					} else {
						nodes = append(nodes, InlineNode{Type: "link", Href: target, Children: parseInlineRange(text, openBr+1, closeBr, mapper, ctx, opts)})
					}
					i = closeParen + 1
					continue
				}
			}
		}

		// Strikethrough ~~...~~ and Strong **...** / __...__
		if i+1 < end && text[i] == text[i+1] && (text[i] == '~' || text[i] == '*' || text[i] == '_') {
			var closeIdx int
			nodeType := "strong"
			if text[i] == '~' {
				nodeType = "del"
				if rel := strings.Index(text[i+2:end], "~~"); rel > 0 {
					closeIdx = i + 2 + rel
				} else {
					closeIdx = -1
				}
			} else {
				closeIdx = findClosingDelimiter(text, i+2, end, text[i:i+2])
			}
			if closeIdx != -1 {
				flushText()
				nodes = append(nodes, InlineNode{Type: nodeType, Children: parseInlineRange(text, i+2, closeIdx, mapper, ctx, opts)})
				i = closeIdx + 2
				continue
			}
		}

		// Emphasis *...* or _..._
		if (text[i] == '*' || text[i] == '_') && i+1 < end && text[i+1] != ' ' && text[i+1] != text[i] &&
			!(text[i] == '_' && i > start && isASCIIAlnum(text[i-1])) {
			if closeIdx := findClosingDelimiter(text, i+1, end, text[i:i+1]); closeIdx > i+1 && text[closeIdx-1] != ' ' &&
				!(text[i] == '_' && closeIdx+1 < end && isASCIIAlnum(text[closeIdx+1])) {
				flushText()
				nodes = append(nodes, InlineNode{Type: "em", Children: parseInlineRange(text, i+1, closeIdx, mapper, ctx, opts)})
				i = closeIdx + 1
				continue
			}
		}

		if text[i] == '\n' {
			textBuf.WriteByte(' ')
		} else {
			textBuf.WriteByte(text[i])
		}
		i++
	}

	flushText()
	return nodes
}

func buildDirectiveInline(kindRaw, name, val string, attrs map[string]string, span SourceSpan, ctx *parseContext) InlineNode {
	idFor := func(prefix string) string {
		return nextUnique(ctx.widgetIDCounts, prefix+":"+firstNonEmpty(widgetCleanRe.ReplaceAllString(name, "_"), "field"), "#")
	}
	switch kindRaw {
	case "input", "text":
		inputType := attrs["type"]
		if !slices.Contains([]string{"text", "number", "date", "email", "url"}, inputType) {
			inputType = "text"
		}
		return InlineNode{Type: "input", Widget: addWidget(ctx, WidgetSpec{
			Kind: WidgetInput, ID: idFor("input"), Name: name, Value: val, InputType: inputType,
			Placeholder: attrs["placeholder"], Label: attrs["label"], Span: span, ValueSpan: span, Line: span.Line,
		})}
	case "select":
		opts := splitPipeOptions(attrs["options"])
		if val != "" && !slices.Contains(opts, val) {
			opts = append([]string{val}, opts...)
		}
		if val == "" && len(opts) > 0 {
			val = opts[0]
		}
		return InlineNode{Type: "select", Widget: addWidget(ctx, WidgetSpec{
			Kind: WidgetSelect, ID: idFor("select"), Name: name, Value: val, Options: opts,
			Label: attrs["label"], Span: span, ValueSpan: span, Line: span.Line,
		})}
	case "radio":
		return InlineNode{Type: "radio", Widget: addWidget(ctx, WidgetSpec{
			Kind: WidgetRadio, ID: idFor("radio"), Name: name, Value: val, Options: splitPipeOptions(attrs["options"]),
			Label: attrs["label"], Span: span, ValueSpan: span, Line: span.Line,
		})}
	default:
		style := attrs["style"]
		if style != "primary" && style != "danger" {
			style = "default"
		}
		label, hasLabel := attrs["label"]
		if !hasLabel {
			label = firstNonEmpty(val, name)
		}
		payload, hasPayload := attrs["payload"]
		if !hasPayload {
			payload = val
		}
		return InlineNode{Type: "action", Widget: addWidget(ctx, WidgetSpec{
			Kind: WidgetAction, ID: idFor("action"), Name: name, Label: label, Style: style,
			Payload: payload, Span: span, ValueSpan: span, Line: span.Line,
		})}
	}
}

func findClosingBackticks(text string, from, end, ticks int) int {
	for i := from; i < end; i++ {
		if text[i] == '`' {
			run := countRun(text, i, end, '`')
			if run == ticks {
				return i
			}
			i += run - 1
		}
	}
	return -1
}

func findMatchingPair(text string, openIdx, end int, openCh, closeCh byte, skipCode bool) int {
	for i, depth := openIdx, 0; i < end; i++ {
		if text[i] == '\\' {
			i++
		} else if skipCode && text[i] == '`' {
			ticks := countRun(text, i, end, '`')
			if closeIdx := findClosingBackticks(text, i+ticks, end, ticks); closeIdx != -1 {
				i = closeIdx + ticks - 1
			}
		} else if text[i] == openCh {
			depth++
		} else if text[i] == closeCh {
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

func findClosingDelimiter(text string, from, end int, marker string) int {
	for i, mlen := from, len(marker); i <= end-mlen; i++ {
		if text[i] == '\\' {
			i++
		} else if text[i] == '`' {
			ticks := countRun(text, i, end, '`')
			if closeIdx := findClosingBackticks(text, i+ticks, end, ticks); closeIdx != -1 {
				i = closeIdx + ticks - 1
			}
		} else if text[i:i+mlen] == marker {
			return i
		}
	}
	return -1
}

func splitTableCellsWithOffsets(line lineInfo) []textSegment {
	s := line.text
	start, end := countRun(s, 0, len(s), ' '), len(s)
	for end > start && s[end-1] == ' ' {
		end--
	}
	if start < end && s[start] == '|' {
		start++
	}
	if end > start && s[end-1] == '|' && (end-2 < start || s[end-2] != '\\') {
		end--
	}
	trimCell := func(a, b int) textSegment {
		a += countRun(s, a, b, ' ')
		for b > a && s[b-1] == ' ' {
			b--
		}
		return textSegment{text: s[a:b], rawStart: line.start + a, lineNo: line.lineNo}
	}
	cells := make([]textSegment, 0, 4)
	cellStart, inCode, codeTicks, inDir := start, false, 0, false
	for i := start; i < end; i++ {
		if s[i] == '\\' && i+1 < end {
			i++
			continue
		}
		if !inCode && i+1 < end && s[i] == '{' && s[i+1] == '{' {
			inDir = true
			i++
			continue
		}
		if inDir && i+1 < end && s[i] == '}' && s[i+1] == '}' {
			inDir = false
			i++
			continue
		}
		if !inDir && s[i] == '`' {
			ticks := countRun(s, i, end, '`')
			if !inCode {
				inCode, codeTicks = true, ticks
			} else if ticks == codeTicks {
				inCode, codeTicks = false, 0
			}
			i += ticks - 1
			continue
		}
		if !inCode && !inDir && s[i] == '|' {
			cells = append(cells, trimCell(cellStart, i))
			cellStart = i + 1
		}
	}
	return append(cells, trimCell(cellStart, end))
}

func isTableDelimiterLine(text string) []string {
	trimmed := strings.TrimSpace(text)
	if !strings.Contains(trimmed, "-") || !tableDelimCharRe.MatchString(trimmed) {
		return nil
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(trimmed, "|"), "|"), "|")
	aligns := make([]string, 0, len(parts))
	for _, rawPart := range parts {
		part := strings.TrimSpace(rawPart)
		if !tableCellDelimRe.MatchString(part) {
			return nil
		}
		left, right := strings.HasPrefix(part, ":"), strings.HasSuffix(part, ":")
		switch {
		case left && right:
			aligns = append(aligns, "center")
		case right:
			aligns = append(aligns, "right")
		case left:
			aligns = append(aligns, "left")
		default:
			aligns = append(aligns, "")
		}
	}
	return aligns
}

func parseATXHeading(text string) (level int, content string, contentOffset int, ok bool) {
	i := min(countRun(text, 0, len(text), ' '), 3)
	level = countRun(text, i, len(text), '#')
	i += level
	if level < 1 || level > 6 || (i < len(text) && text[i] != ' ' && text[i] != '\t') {
		return 0, "", 0, false
	}
	for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
		i++
	}
	trimmed := strings.TrimRight(text[i:], " \t")
	noHash := strings.TrimRight(trimmed, "#")
	if len(noHash) < len(trimmed) && (noHash == "" || noHash[len(noHash)-1] == ' ' || noHash[len(noHash)-1] == '\t') {
		trimmed = strings.TrimRight(noHash, " \t")
	}
	return level, trimmed, i, true
}

func isHorizontalRule(text string) bool {
	i := min(countRun(text, 0, len(text), ' '), 3)
	if i >= len(text) || (text[i] != '-' && text[i] != '*' && text[i] != '_') {
		return false
	}
	marker, count := text[i], 0
	for ; i < len(text); i++ {
		if text[i] == marker {
			count++
		} else if text[i] != ' ' && text[i] != '\t' {
			return false
		}
	}
	return count >= 3
}

func isBlockStart(lines []lineInfo, idx int) bool {
	t := lines[idx].text
	if strings.TrimSpace(t) == "" || isHorizontalRule(t) || fenceOpenRe.MatchString(t) ||
		blockquoteRe.MatchString(t) || listItemRe.MatchString(t) || mermaidEmbedRe.MatchString(t) {
		return true
	}
	if _, _, _, ok := parseATXHeading(t); ok {
		return true
	}
	return idx+1 < len(lines) && strings.Contains(t, "|") && isTableDelimiterLine(lines[idx+1].text) != nil
}

func parseBlocks(lines []lineInfo, ctx *parseContext) []BlockNode {
	blocks := make([]BlockNode, 0, 8)
	for i := 0; i < len(lines); {
		line := lines[i]
		if strings.TrimSpace(line.text) == "" {
			i++
			continue
		}

		// Standalone external Mermaid embed: {{mermaid:file.mmd}}, ![[file.mmd]], ![Title](file.mmd)
		if embed := mermaidEmbedRe.FindStringSubmatch(line.text); embed != nil {
			sourceFile := strings.TrimSpace(firstNonEmpty(embed[1], embed[2], embed[4]))
			title := firstNonEmpty(strings.TrimSpace(embed[3]), sourceFile)
			var code, errStr string
			if ctx.resolveExternalMermaid != nil {
				var err error
				if code, err = ctx.resolveExternalMermaid(sourceFile); err != nil {
					errStr = err.Error()
				}
			} else {
				errStr = fmt.Sprintf("External Mermaid file %q cannot be resolved in this context.", sourceFile)
			}
			orient := DetectMermaidOrientation(code)
			id := fmt.Sprintf("mermaid-%d", ctx.mermaidCount)
			ctx.mermaidCount++
			span := rangeSpan(line, line)
			addWidget(ctx, WidgetSpec{
				Kind: WidgetMermaid, ID: id, Name: id, Label: title, Code: code,
				SourceFile: sourceFile, ValueSpan: span, Span: span, Line: line.lineNo,
			})
			blocks = append(blocks, BlockNode{
				Type: "mermaid", ID: id, Code: code, Title: title, SourceFile: sourceFile,
				Direction: orient.Direction, Flippable: orient.Flippable, Error: errStr, Span: span,
			})
			i++
			continue
		}

		if fence := fenceOpenRe.FindStringSubmatch(line.text); fence != nil {
			block, nextIdx := parseFencedBlock(lines, i, fence, ctx)
			blocks = append(blocks, block)
			i = nextIdx
			continue
		}

		if level, content, contentOffset, ok := parseATXHeading(line.text); ok {
			inlines := parseInlines([]textSegment{{content, line.start + contentOffset, line.lineNo}}, ctx, inlineOptions{})
			plain := plainTextOfInlines(inlines)
			slugBase := strings.Trim(slugNonAlnumRe.ReplaceAllString(slugStripRe.ReplaceAllString(strings.ToLower(plain), ""), "-"), "-")
			id := nextUnique(ctx.headingSlugCounts, firstNonEmpty(slugBase, "section"), "-")
			ctx.headings = append(ctx.headings, HeadingInfo{ID: id, Level: level, Text: plain, Line: line.lineNo})
			blocks = append(blocks, BlockNode{Type: "heading", Level: level, ID: id, Text: plain, Inlines: inlines, Span: rangeSpan(line, line)})
			i++
			continue
		}

		if isHorizontalRule(line.text) {
			blocks = append(blocks, BlockNode{Type: "hr", Span: rangeSpan(line, line)})
			i++
			continue
		}

		// Blockquote / GitHub Alert
		if blockquoteRe.MatchString(line.text) {
			var quoteLines []lineInfo
			j := i
			for ; j < len(lines); j++ {
				loc := blockquoteRe.FindStringIndex(lines[j].text)
				if loc == nil {
					break
				}
				quoteLines = append(quoteLines, lineInfo{lines[j].text[loc[1]:], lines[j].start + loc[1], lines[j].end, lines[j].lineNo, lines[j].eol})
			}
			alert := ""
			if len(quoteLines) > 0 {
				if m := alertHeaderRe.FindStringSubmatch(strings.TrimSpace(quoteLines[0].text)); m != nil {
					alert, quoteLines = strings.ToLower(m[1]), quoteLines[1:]
				}
			}
			blocks = append(blocks, BlockNode{Type: "blockquote", Alert: alert, Children: parseBlocks(quoteLines, ctx), Span: rangeSpan(lines[i], lines[j-1])})
			i = j
			continue
		}

		if i+1 < len(lines) && strings.Contains(line.text, "|") {
			if alignments := isTableDelimiterLine(lines[i+1].text); alignments != nil {
				block, nextIdx := parseTableBlock(lines, i, alignments, ctx)
				blocks, i = append(blocks, block), nextIdx
				continue
			}
		}

		if listMatch := listItemRe.FindStringSubmatch(line.text); listMatch != nil {
			block, nextIdx := parseListBlock(lines, i, listMatch, ctx)
			blocks, i = append(blocks, block), nextIdx
			continue
		}

		var paraSegments []textSegment
		startIdx := i
		for i < len(lines) && (i == startIdx || !isBlockStart(lines, i)) {
			paraSegments = append(paraSegments, textSegment{lines[i].text, lines[i].start, lines[i].lineNo})
			i++
		}
		blocks = append(blocks, BlockNode{Type: "paragraph", Inlines: parseInlines(paraSegments, ctx, inlineOptions{}), Span: rangeSpan(lines[startIdx], lines[i-1])})
	}
	return blocks
}

func parseFencedBlock(lines []lineInfo, i int, fence []string, ctx *parseContext) (BlockNode, int) {
	openLine := lines[i]
	fenceChar, fenceLen := fence[2][0], len(fence[2])
	fields := strings.Fields(strings.TrimSpace(fence[3]))
	firstToken, attrStr := "", ""
	if len(fields) > 0 {
		firstToken, attrStr = fields[0], strings.Join(fields[1:], " ")
	}
	lang := strings.ToLower(firstToken)

	var bodyLines []lineInfo
	var closeLine *lineInfo
	j := i + 1
	for ; j < len(lines); j++ {
		lead := min(countRun(lines[j].text, 0, len(lines[j].text), ' '), 3)
		run := countRun(lines[j].text, lead, len(lines[j].text), fenceChar)
		if run >= fenceLen && strings.Trim(lines[j].text[lead+run:], " \t") == "" {
			cl := lines[j]
			closeLine = &cl
			j++
			break
		}
		bodyLines = append(bodyLines, lines[j])
	}

	endLine := openLine
	if closeLine != nil {
		endLine = *closeLine
	} else if len(bodyLines) > 0 {
		endLine = bodyLines[len(bodyLines)-1]
	}
	blockSpan := rangeSpan(openLine, endLine)

	bodyParts := make([]string, len(bodyLines))
	for k, bl := range bodyLines {
		bodyParts[k] = bl.text
	}
	bodyText := strings.Join(bodyParts, "\n")

	valueStart, valueEnd := openLine.end+len(openLine.eol), endLine.end
	if closeLine != nil {
		valueEnd = closeLine.start
	}
	vLine, vEndLine := openLine.lineNo+1, openLine.lineNo+1
	if len(bodyLines) > 0 {
		vLine, vEndLine = bodyLines[0].lineNo, bodyLines[len(bodyLines)-1].lineNo
	}
	valueSpan := SourceSpan{Start: valueStart, End: valueEnd, Line: vLine, EndLine: vEndLine}

	if lang == "mermaid" {
		attrs := parseDirectiveAttrs(attrStr)
		id := fmt.Sprintf("mermaid-%d", ctx.mermaidCount)
		ctx.mermaidCount++
		orient := DetectMermaidOrientation(bodyText)
		addWidget(ctx, WidgetSpec{
			Kind: WidgetMermaid, ID: id, Name: id, Label: firstNonEmpty(attrs["title"], fmt.Sprintf("Diagram %d", ctx.mermaidCount)),
			Code: bodyText, ValueSpan: valueSpan, Span: blockSpan, Line: openLine.lineNo,
		})
		return BlockNode{
			Type: "mermaid", ID: id, Code: bodyText, Title: attrs["title"],
			Direction: orient.Direction, Flippable: orient.Flippable, Span: blockSpan,
		}, j
	}

	if lang == "field" || lang == "textarea" || strings.HasPrefix(lang, "field:") {
		colonName := ""
		if strings.HasPrefix(lang, "field:") {
			colonName = firstToken[len("field:"):]
		}
		attrs := parseDirectiveAttrs(attrStr)
		name := firstNonEmpty(attrs["name"], colonName, fmt.Sprintf("field_%d", len(ctx.widgets)+1))
		rows, err := strconv.Atoi(attrs["rows"])
		if err != nil || rows <= 0 {
			rows = min(12, max(3, len(bodyLines)+1))
		}
		label, hasLabel := attrs["label"]
		if !hasLabel {
			label = name
		}
		id := nextUnique(ctx.widgetIDCounts, "field:"+firstNonEmpty(widgetCleanRe.ReplaceAllString(name, "_"), "field"), "#")
		wPtr := addWidget(ctx, WidgetSpec{
			Kind: WidgetField, ID: id, Name: name, Value: bodyText, Label: label,
			Placeholder: attrs["placeholder"], Rows: max(3, rows), Span: blockSpan, ValueSpan: valueSpan, Line: openLine.lineNo,
		})
		return BlockNode{Type: "field", Widget: wPtr, Span: blockSpan}, j
	}

	return BlockNode{Type: "code", Lang: firstToken, Code: bodyText, Span: blockSpan}, j
}

func parseTableBlock(lines []lineInfo, i int, alignments []string, ctx *parseContext) (BlockNode, int) {
	parseRow := func(l lineInfo, opts inlineOptions) [][]InlineNode {
		cells := splitTableCellsWithOffsets(l)
		out := make([][]InlineNode, len(cells))
		for idx, c := range cells {
			out[idx] = parseInlines([]textSegment{c}, ctx, opts)
		}
		return out
	}
	headers := parseRow(lines[i], inlineOptions{inTableCell: true})
	rows := make([][][]InlineNode, 0, 4)
	j := i + 2
	for ; j < len(lines) && strings.TrimSpace(lines[j].text) != "" && strings.Contains(lines[j].text, "|"); j++ {
		rows = append(rows, parseRow(lines[j], inlineOptions{allowInlineCheckbox: true, inTableCell: true}))
	}
	ctx.tableCount++
	return BlockNode{Type: "table", Headers: headers, Alignments: alignments, Rows: rows, Span: rangeSpan(lines[i], lines[j-1])}, j
}

func parseListBlock(lines []lineInfo, i int, firstMatch []string, ctx *parseContext) (BlockNode, int) {
	baseIndent, ordered, startNum := len(firstMatch[1]), firstMatch[3] != "", 1
	if ordered {
		if n, err := strconv.Atoi(firstMatch[3]); err == nil {
			startNum = n
		}
	}

	items := make([]ListItemNode, 0, 4)
	j := i
	for j < len(lines) {
		cur := lines[j]
		if strings.TrimSpace(cur.text) == "" {
			if k := skipBlanks(lines, j+1); k < len(lines) {
				if nextList := listItemRe.FindStringSubmatch(lines[k].text); nextList != nil && len(nextList[1]) == baseIndent && (nextList[3] != "") == ordered {
					j = k
					continue
				}
			}
			break
		}

		itemMatch := listItemRe.FindStringSubmatch(cur.text)
		if itemMatch == nil || len(itemMatch[1]) != baseIndent || (itemMatch[3] != "") != ordered {
			break
		}

		contentStart := cur.start + len(itemMatch[1]) + len(itemMatch[2]) + len(itemMatch[4])
		contentText := itemMatch[5]
		checkboxIdx := -1
		if chkMatch := checkboxPrefixRe.FindStringSubmatch(contentText); chkMatch != nil {
			ch := chkMatch[1][0]
			boxSpan := SourceSpan{Start: contentStart, End: contentStart + 3, Line: cur.lineNo, EndLine: cur.lineNo}
			markSpan := SourceSpan{Start: contentStart + 1, End: contentStart + 2, Line: cur.lineNo, EndLine: cur.lineNo}
			contentStart += len(contentText) - len(chkMatch[2])
			contentText = chkMatch[2]
			id := fmt.Sprintf("chk-%d", ctx.checkboxCount)
			ctx.checkboxCount++
			addWidget(ctx, WidgetSpec{
				Kind: WidgetCheckbox, ID: id, Name: id, Checked: ch == 'x' || ch == 'X',
				Label: truncateRunes(strings.TrimSpace(contentText), 120), Span: boxSpan, ValueSpan: markSpan, Line: cur.lineNo,
			})
			checkboxIdx = len(ctx.widgets) - 1
		}

		itemSegments := []textSegment{{contentText, contentStart, cur.lineNo}}
		var childLines []lineInfo
		for j++; j < len(lines); j++ {
			next := lines[j]
			if strings.TrimSpace(next.text) == "" {
				if k := skipBlanks(lines, j+1); k < len(lines) && countRun(lines[k].text, 0, len(lines[k].text), ' ') > baseIndent {
					childLines = append(childLines, next)
					continue
				}
				break
			}
			indent := countRun(next.text, 0, len(next.text), ' ')
			if indent <= baseIndent {
				break
			}
			if len(childLines) == 0 && !listItemRe.MatchString(next.text) && !fenceOpenRe.MatchString(next.text) && !strings.HasPrefix(strings.TrimLeft(next.text, " \t"), ">") {
				itemSegments = append(itemSegments, textSegment{next.text[indent:], next.start + indent, next.lineNo})
			} else {
				strip := min(indent, baseIndent+2)
				childLines = append(childLines, lineInfo{
					text: next.text[strip:], start: next.start + strip, end: next.end,
					lineNo: next.lineNo, eol: next.eol,
				})
			}
		}

		inlines := parseInlines(itemSegments, ctx, inlineOptions{})
		var checkboxPtr *WidgetSpec
		if checkboxIdx >= 0 {
			ctx.widgets[checkboxIdx].Label = firstNonEmpty(plainTextOfInlines(inlines), ctx.widgets[checkboxIdx].ID)
			wCopy := ctx.widgets[checkboxIdx]
			checkboxPtr = &wCopy
		}
		var children []BlockNode
		if len(childLines) > 0 {
			children = parseBlocks(childLines, ctx)
		}
		items = append(items, ListItemNode{Checkbox: checkboxPtr, Inlines: inlines, Children: children, Line: cur.lineNo})
	}

	return BlockNode{Type: "list", Ordered: ordered, Start: startNum, Items: items, Span: rangeSpan(lines[i], lines[j-1])}, j
}

// ParseMarkdown deterministically parses a Markdown or standalone Mermaid
// source document into a DocumentIR with exact UTF-8 byte source spans.
func ParseMarkdown(source string, opts ParseOptions) *DocumentIR {
	file, lines := firstNonEmpty(opts.File, "untitled.md"), splitLinesWithOffsets(source)
	if isMermaidFilename(file) {
		span, orient := SourceSpan{Start: 0, End: len(source), Line: 1, EndLine: len(lines)}, DetectMermaidOrientation(source)
		return &DocumentIR{
			File: file, Revision: ComputeRevision(source), Title: file, Headings: []HeadingInfo{},
			Blocks:  []BlockNode{{Type: "mermaid", ID: "mermaid-0", Code: source, Title: file, SourceFile: file, Direction: orient.Direction, Flippable: orient.Flippable, Span: span}},
			Widgets: []WidgetSpec{{Kind: WidgetMermaid, ID: "mermaid-0", Name: "mermaid-0", Label: file, Code: source, SourceFile: file, ValueSpan: span, Span: span, Line: 1}},
			Stats:   DocumentStats{MermaidCount: 1, LineCount: len(lines)},
		}
	}

	ctx := &parseContext{
		headings: make([]HeadingInfo, 0), headingSlugCounts: make(map[string]int),
		widgets: make([]WidgetSpec, 0), widgetIDCounts: make(map[string]int),
		resolveExternalMermaid: opts.ResolveExternalMermaid,
	}
	blocks := parseBlocks(lines, ctx)

	title := file
	for idx, h := range ctx.headings {
		if idx == 0 || h.Level == 1 {
			title = firstNonEmpty(h.Text, file)
			if h.Level == 1 {
				break
			}
		}
	}

	stats := DocumentStats{TableCount: ctx.tableCount, MermaidCount: ctx.mermaidCount, LineCount: len(lines)}
	for _, w := range ctx.widgets {
		if w.Kind == WidgetCheckbox {
			stats.CheckboxTotal++
			if w.Checked {
				stats.CheckboxChecked++
			}
		} else if w.Kind == WidgetInput || w.Kind == WidgetSelect || w.Kind == WidgetRadio || w.Kind == WidgetField {
			stats.InputCount++
		}
	}

	return &DocumentIR{
		File: file, Revision: ComputeRevision(source), Title: title,
		Headings: ctx.headings, Blocks: blocks, Widgets: ctx.widgets, Stats: stats,
	}
}
