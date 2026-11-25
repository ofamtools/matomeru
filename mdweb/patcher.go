package mdweb

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var (
	directivePatchRe = regexp.MustCompile(`^(\{\{(?:input|text|select|radio):[a-zA-Z0-9_.-]+)(?:=(?:"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|[^\s}]+))?((?s:.*)\}\})$`)
	newlineRe        = regexp.MustCompile(`\r?\n`)
	fenceInjectRe    = regexp.MustCompile("(?m)^([ \\t]*`{3,})")
)

func escapeDirectiveValue(val string) string {
	s := strings.ReplaceAll(val, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = newlineRe.ReplaceAllString(s, `\n`)
	return s
}

func replaceDirectiveValue(token, nextValue string) (string, error) {
	m := directivePatchRe.FindStringSubmatch(token)
	if m == nil {
		return "", fmt.Errorf("invalid directive token at source span: %s", token)
	}
	return m[1] + `="` + escapeDirectiveValue(nextValue) + `"` + m[2], nil
}

func detectEOL(source string) string {
	if strings.Contains(source, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func normalizeBlockBody(val string) string {
	s := strings.ReplaceAll(val, "\r\n", "\n")
	s = fenceInjectRe.ReplaceAllString(s, "  $1")
	s = strings.TrimSuffix(s, "\n")
	return s
}

// ApplyMutation applies an interactive widget mutation to source using the
// exact UTF-8 byte spans recorded in ir, leaving all surrounding bytes intact.
func ApplyMutation(source string, ir *DocumentIR, req MutationRequest) (PatchResult, error) {
	if ir == nil {
		return PatchResult{}, errors.New("document IR is nil")
	}

	var widget WidgetSpec
	found := false
	for _, w := range ir.Widgets {
		if w.ID == req.ID {
			widget = w
			found = true
			break
		}
	}
	if !found {
		return PatchResult{}, fmt.Errorf("widget %q not found in document revision %s", req.ID, ir.Revision)
	}
	if widget.Kind != req.Kind {
		return PatchResult{}, fmt.Errorf("widget %q is kind %q, not %q", req.ID, widget.Kind, req.Kind)
	}

	switch req.Kind {
	case WidgetAction:
		newVal := widget.Payload
		if newVal == "" {
			newVal = widget.Label
		}
		return PatchResult{
			Changed:    false,
			NextSource: source,
			OldValue:   nil,
			NewValue:   newVal,
			Widget:     widget,
		}, nil

	case WidgetCheckbox:
		start, end := widget.ValueSpan.Start, widget.ValueSpan.End
		if start < 0 || end > len(source) || start >= end {
			return PatchResult{}, fmt.Errorf("invalid checkbox span [%d:%d] for %q", start, end, widget.ID)
		}
		currentChar := source[start:end]
		if currentChar != " " && currentChar != "x" && currentChar != "X" {
			return PatchResult{}, fmt.Errorf("refusing to patch checkbox %q: expected [ ] or [x], found %q", widget.ID, currentChar)
		}
		nextChar := " "
		if req.Checked {
			nextChar = "x"
		}
		nextSource := source[:start] + nextChar + source[end:]
		return PatchResult{
			Changed:    widget.Checked != req.Checked,
			NextSource: nextSource,
			OldValue:   widget.Checked,
			NewValue:   req.Checked,
			Widget:     widget,
		}, nil

	case WidgetInput, WidgetSelect, WidgetRadio:
		if (req.Kind == WidgetSelect || req.Kind == WidgetRadio) && len(widget.Options) > 0 {
			if !slices.Contains(widget.Options, req.Value) {
				return PatchResult{}, fmt.Errorf("invalid %s value %q for %q", req.Kind, req.Value, widget.Name)
			}
		}
		start, end := widget.Span.Start, widget.Span.End
		if start < 0 || end > len(source) || start >= end {
			return PatchResult{}, fmt.Errorf("invalid directive span [%d:%d] for %q", start, end, widget.ID)
		}
		currentToken := source[start:end]
		if !strings.HasPrefix(currentToken, "{{") || !strings.HasSuffix(currentToken, "}}") {
			return PatchResult{}, fmt.Errorf("refusing to patch %q: source span no longer matches directive token", widget.ID)
		}
		sanitized := newlineRe.ReplaceAllString(req.Value, " ")
		replacement, err := replaceDirectiveValue(currentToken, sanitized)
		if err != nil {
			return PatchResult{}, err
		}
		nextSource := source[:start] + replacement + source[end:]
		return PatchResult{
			Changed:    widget.Value != sanitized,
			NextSource: nextSource,
			OldValue:   widget.Value,
			NewValue:   sanitized,
			Widget:     widget,
		}, nil

	case WidgetField:
		start, end := widget.ValueSpan.Start, widget.ValueSpan.End
		if start < 0 || end > len(source) || start > end {
			return PatchResult{}, fmt.Errorf("invalid field span [%d:%d] for %q", start, end, widget.ID)
		}
		eol := detectEOL(source)
		normalized := normalizeBlockBody(req.Value)
		bodyWithEOL := ""
		if len(normalized) > 0 {
			bodyWithEOL = strings.ReplaceAll(normalized, "\n", eol) + eol
		}
		nextSource := source[:start] + bodyWithEOL + source[end:]
		return PatchResult{
			Changed:    widget.Value != normalized,
			NextSource: nextSource,
			OldValue:   widget.Value,
			NewValue:   normalized,
			Widget:     widget,
		}, nil

	case WidgetMermaid:
		eol := detectEOL(source)
		normalized := normalizeBlockBody(req.Code)

		// Standalone .mmd/.mermaid file replaces the full file body.
		if widget.ValueSpan.Start == 0 && widget.ValueSpan.End == len(source) &&
			(widget.SourceFile == "" || isMermaidFilename(ir.File)) {
			nextSource := strings.ReplaceAll(normalized, "\n", eol) + eol
			return PatchResult{
				Changed:    strings.TrimRight(widget.Code, " \t\r\n") != normalized,
				NextSource: nextSource,
				OldValue:   widget.Code,
				NewValue:   normalized,
				Widget:     widget,
			}, nil
		}

		// External Mermaid embed: caller writes widget.SourceFile; Markdown source stays unchanged.
		if widget.SourceFile != "" {
			return PatchResult{
				Changed:    strings.TrimRight(widget.Code, " \t\r\n") != normalized,
				NextSource: source,
				OldValue:   widget.Code,
				NewValue:   normalized,
				Widget:     widget,
			}, nil
		}

		start, end := widget.ValueSpan.Start, widget.ValueSpan.End
		if start < 0 || end > len(source) || start > end {
			return PatchResult{}, fmt.Errorf("invalid mermaid span [%d:%d] for %q", start, end, widget.ID)
		}
		bodyWithEOL := ""
		if len(normalized) > 0 {
			bodyWithEOL = strings.ReplaceAll(normalized, "\n", eol) + eol
		}
		nextSource := source[:start] + bodyWithEOL + source[end:]
		return PatchResult{
			Changed:    widget.Code != normalized,
			NextSource: nextSource,
			OldValue:   widget.Code,
			NewValue:   normalized,
			Widget:     widget,
		}, nil

	default:
		return PatchResult{}, fmt.Errorf("unsupported mutation kind %q", req.Kind)
	}
}
