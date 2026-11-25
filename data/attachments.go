package data

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Attachment is a pure value object representing a file, image, context item,
// or URL attached to a task, note, or prompt.
type Attachment struct {
	Kind   string `json:"kind"`             // "image" | "file" | "context" | "url"
	Path   string `json:"path"`             // absolute path on disk or URL
	Label  string `json:"label,omitempty"`  // display name
	Mime   string `json:"mime,omitempty"`   // e.g. "image/png"
	Source string `json:"source,omitempty"` // "clipboard" | "picker" | "mention" | "agent"
	Ref    string `json:"ref,omitempty"`    // optional "context:<id>" / "document:<id>"
}

// RichTokens holds deterministic inline references extracted from free-form text.
type RichTokens struct {
	Files    []string `json:"files,omitempty"`    // @path/to/file
	Refs     []string `json:"refs,omitempty"`     // @task:<id>, @context:<id>, @agent:<name>
	Skills   []string `json:"skills,omitempty"`   // $skill-name
	Commands []string `json:"commands,omitempty"` // /command-name
}

// ParseRichTokens extracts @files, @kind:id refs, $skills, and /commands from
// text, deduplicated in order of first appearance. Email addresses such as
// foo@bar.com, currency values such as $100, and filesystem paths such as
// /usr/local/bin or https://example.com/path are ignored.
func ParseRichTokens(text string) RichTokens {
	var out RichTokens
	seenFiles := map[string]bool{}
	seenRefs := map[string]bool{}
	seenSkills := map[string]bool{}
	seenCommands := map[string]bool{}

	add := func(dst *[]string, seen map[string]bool, val string) {
		if val == "" || seen[val] {
			return
		}
		seen[val] = true
		*dst = append(*dst, val)
	}

	n := len(text)
	for i := 0; i < n; {
		sigil := text[i]
		if sigil != '@' && sigil != '$' && sigil != '/' {
			_, w := utf8.DecodeRuneInString(text[i:])
			i += w
			continue
		}
		if i > 0 {
			prev, _ := utf8.DecodeLastRuneInString(text[:i])
			if !isTokenBoundary(prev, sigil) {
				i++
				continue
			}
		}

		// Scan raw token body until whitespace or closing/hard delimiters.
		end := i + 1
		for end < n {
			r, w := utf8.DecodeRuneInString(text[end:])
			if unicode.IsSpace(r) || isTokenStop(r) {
				break
			}
			end += w
		}
		raw := strings.TrimRight(text[i+1:end], ".,;:!?)]}\"'`")
		if raw == "" {
			i = end
			continue
		}

		switch sigil {
		case '@':
			if isRefToken(raw) {
				add(&out.Refs, seenRefs, raw)
			} else {
				add(&out.Files, seenFiles, raw)
			}
		case '$':
			if isIdentToken(raw) {
				add(&out.Skills, seenSkills, raw)
			}
		case '/':
			if !strings.ContainsRune(raw, '/') && isIdentToken(raw) {
				add(&out.Commands, seenCommands, raw)
			}
		}
		i = end
	}
	return out
}

func isTokenBoundary(prev rune, sigil byte) bool {
	if unicode.IsSpace(prev) {
		return true
	}
	switch prev {
	case '(', '[', '{', '<', '"', '\'', '`', ',', ';':
		return true
	case ':':
		return sigil != '/'
	}
	return false
}

func isTokenStop(r rune) bool {
	switch r {
	case '(', ')', '[', ']', '{', '}', '<', '>', '"', '\'', '`', ',', ';':
		return true
	}
	return false
}

func isIdentToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if i == 0 {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
				return false
			}
			continue
		}
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}

func isRefToken(s string) bool {
	colon := strings.IndexByte(s, ':')
	if colon <= 0 || colon >= len(s)-1 {
		return false
	}
	kind, id := s[:colon], s[colon+1:]
	if strings.ContainsAny(kind, "/\\") || strings.ContainsAny(id, "/\\") {
		return false
	}
	if !isRefKind(kind) {
		return false
	}
	// Avoid misclassifying file:line references such as main.go:42.
	if strings.ContainsRune(kind, '.') && !isKnownDottedKind(kind) && isAllDigits(id) {
		return false
	}
	return true
}

func isRefKind(kind string) bool {
	if kind == "" {
		return false
	}
	for i := 0; i < len(kind); i++ {
		c := kind[i]
		if i == 0 {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
				return false
			}
			continue
		}
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func isKnownDottedKind(kind string) bool {
	switch kind {
	case KindPage, KindOccurrence, KindTriageRule:
		return true
	}
	return false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// DecodeAttachments converts a field value ([]Attachment, []any of map[string]any,
// or any JSON-roundtripped list) into []Attachment. It returns nil when absent
// or empty.
func DecodeAttachments(v any) []Attachment {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case []Attachment:
		if len(x) == 0 {
			return nil
		}
		return slices.Clone(x)
	case *[]Attachment:
		if x == nil || len(*x) == 0 {
			return nil
		}
		return slices.Clone(*x)
	case []any:
		if len(x) == 0 {
			return nil
		}
		out := make([]Attachment, 0, len(x))
		for _, item := range x {
			if a, ok := decodeOneAttachment(item); ok {
				out = append(out, a)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	list, err := decodeAs[[]Attachment](v)
	if err != nil || len(list) == 0 {
		return nil
	}
	out := make([]Attachment, 0, len(list))
	for _, a := range list {
		if !isEmptyAttachment(a) {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func decodeOneAttachment(v any) (Attachment, bool) {
	switch x := v.(type) {
	case Attachment:
		return x, !isEmptyAttachment(x)
	case *Attachment:
		if x == nil || isEmptyAttachment(*x) {
			return Attachment{}, false
		}
		return *x, true
	case map[string]any:
		a := Attachment{
			Kind:   Text(x["kind"]),
			Path:   Text(x["path"]),
			Label:  Text(x["label"]),
			Mime:   Text(x["mime"]),
			Source: Text(x["source"]),
			Ref:    Text(x["ref"]),
		}
		return a, !isEmptyAttachment(a)
	default:
		a, err := decodeAs[Attachment](v)
		if err != nil || isEmptyAttachment(a) {
			return Attachment{}, false
		}
		return a, true
	}
}

func isEmptyAttachment(a Attachment) bool {
	return a.Kind == "" && a.Path == "" && a.Label == "" && a.Mime == "" && a.Source == "" && a.Ref == ""
}

// EncodeAttachments converts a slice of Attachments into a JSON-friendly []any
// of map[string]any suitable for Record.Fields["attachments"].
func EncodeAttachments(list []Attachment) []any {
	if len(list) == 0 {
		return nil
	}
	out := make([]any, 0, len(list))
	for _, a := range list {
		m := map[string]any{
			"kind": a.Kind,
			"path": a.Path,
		}
		if a.Label != "" {
			m["label"] = a.Label
		}
		if a.Mime != "" {
			m["mime"] = a.Mime
		}
		if a.Source != "" {
			m["source"] = a.Source
		}
		if a.Ref != "" {
			m["ref"] = a.Ref
		}
		out = append(out, m)
	}
	return out
}
