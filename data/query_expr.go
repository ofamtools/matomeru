package data

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"ofam.dev/ochazuke/core"
)

// QueryMode selects which section of a unified Zero-Field query is being edited.
const (
	QueryModeFilter = "filter"
	QueryModeGroup  = "group"
	QueryModeSort   = "sort"
)

// ExprTokenKind identifies one token in a boolean Zero-Field filter expression.
type ExprTokenKind string

const (
	ExprTokenClause ExprTokenKind = "clause"
	ExprTokenOp     ExprTokenKind = "op"
	ExprTokenLParen ExprTokenKind = "lparen"
	ExprTokenRParen ExprTokenKind = "rparen"
)

// ExprToken is one clause, boolean operator (AND/OR), or parenthesis in QuerySpec.
type ExprToken struct {
	Kind   ExprTokenKind `json:"kind"`
	Field  string        `json:"field,omitempty"`
	Values []string      `json:"values,omitempty"`
	All    bool          `json:"all,omitempty"` // false = any/in, true = all
	Invert bool          `json:"invert,omitempty"`
	Op     string        `json:"op,omitempty"` // "AND" or "OR"
}

// QuerySpec is the pure serializable data representation of a unified
// Filter + Group + Sort query for any noun.
type QuerySpec struct {
	Tokens  []ExprToken `json:"tokens,omitempty"`
	GroupBy string      `json:"groupBy,omitempty"`
	SortBy  string      `json:"sortBy,omitempty"`
	SortDir string      `json:"sortDir,omitempty"` // "asc" | "desc"
}

// QueryFieldDef describes one queryable field derived automatically from a noun's Schema.
type QueryFieldDef struct {
	Name      string
	Label     string
	Type      string
	Options   []string
	Substring bool
}

// SchemaFieldDefs enumerates every queryable data point of a Schema in stable order:
// id, kind, all declared schema fields, starStatus, archiveStatus, createdAt, updatedAt.
func SchemaFieldDefs(s Schema) []QueryFieldDef {
	defs := []QueryFieldDef{
		{Name: "id", Label: "id", Type: "string", Substring: true},
		{Name: "kind", Label: "kind", Type: "string"},
	}
	seen := map[string]bool{"id": true, "kind": true}
	for _, f := range s.Fields {
		if f.Name == "" || seen[f.Name] {
			continue
		}
		seen[f.Name] = true
		fd := QueryFieldDef{
			Name:  f.Name,
			Label: f.Name,
			Type:  string(f.Type),
		}
		switch {
		case len(f.Enum) > 0:
			fd.Options = slices.Clone(f.Enum)
		case f.Type == core.ParamBool:
			fd.Options = []string{"true", "false"}
		case f.Type == core.ParamString:
			fd.Substring = true
		}
		defs = append(defs, fd)
	}
	for _, extra := range []QueryFieldDef{
		{Name: "starStatus", Label: "starred", Type: "enum", Options: []string{"starred", "unstarred"}},
		{Name: "archiveStatus", Label: "archived", Type: "enum", Options: []string{"active", "archived"}},
		{Name: "createdAt", Label: "createdAt", Type: "time", Substring: true},
		{Name: "updatedAt", Label: "updatedAt", Type: "time", Substring: true},
	} {
		if !seen[extra.Name] {
			defs = append(defs, extra)
		}
	}
	return defs
}

// FieldOptions returns candidate values for a field: declared enum/bool options
// first, supplemented by sorted distinct live values from records.
func FieldOptions(s Schema, field string, records []Record, limit int) []string {
	if limit <= 0 {
		limit = 12
	}
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] || len(out) >= limit {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	for _, fd := range SchemaFieldDefs(s) {
		if fd.Name == field {
			for _, opt := range fd.Options {
				add(opt)
			}
			break
		}
	}
	var live []string
	liveSeen := map[string]bool{}
	for _, rec := range records {
		v := strings.TrimSpace(RecordFieldText(rec, field))
		if v != "" && !seen[v] && !liveSeen[v] {
			liveSeen[v] = true
			live = append(live, v)
		}
	}
	sort.Strings(live)
	for _, v := range live {
		add(v)
	}
	return out
}

// RecordFieldText extracts the string value of any schema or metadata field from rec.
func RecordFieldText(rec Record, field string) string {
	switch field {
	case "id":
		return rec.ID
	case "kind":
		return rec.Kind
	case "starStatus":
		if rec.StarStatus == "" {
			return "unstarred"
		}
		return rec.StarStatus
	case "archiveStatus":
		if rec.ArchiveStatus == "" {
			return "active"
		}
		return rec.ArchiveStatus
	case "createdAt":
		if rec.CreatedAt.IsZero() {
			return ""
		}
		return rec.CreatedAt.UTC().Format("2006-01-02")
	case "updatedAt":
		if rec.UpdatedAt.IsZero() {
			return ""
		}
		return rec.UpdatedAt.UTC().Format("2006-01-02")
	}
	if v, ok := rec.Fields[field]; ok && v != nil {
		if b, isBool := v.(bool); isBool {
			if b {
				return "true"
			}
			return "false"
		}
		return strings.TrimSpace(Text(v))
	}
	return ""
}

// FormatClause returns a concise human-readable label for a clause token.
func FormatClause(tok ExprToken) string {
	prefix := ""
	if tok.Invert {
		prefix = "NOT "
	}
	if len(tok.Values) == 1 {
		return fmt.Sprintf("%s%s=%s", prefix, tok.Field, tok.Values[0])
	}
	quant := "any"
	if tok.All {
		quant = "all"
	}
	return fmt.Sprintf("%s%s %s (%s)", prefix, tok.Field, quant, strings.Join(tok.Values, ", "))
}

// FilterSummary returns a readable boolean expression string for the query's filter clauses.
func (q QuerySpec) FilterSummary() string {
	if len(q.Tokens) == 0 {
		return ""
	}
	parts := make([]string, 0, len(q.Tokens))
	for _, tok := range q.Tokens {
		switch tok.Kind {
		case ExprTokenLParen:
			parts = append(parts, "(")
		case ExprTokenRParen:
			parts = append(parts, ")")
		case ExprTokenOp:
			parts = append(parts, tok.Op)
		case ExprTokenClause:
			if len(tok.Values) > 0 {
				parts = append(parts, FormatClause(tok))
			}
		}
	}
	return strings.Join(parts, " ")
}

// HasFilter reports whether q contains at least one active filter clause.
func (q QuerySpec) HasFilter() bool {
	for _, tok := range q.Tokens {
		if tok.Kind == ExprTokenClause && len(tok.Values) > 0 {
			return true
		}
	}
	return false
}

// Matches evaluates q's boolean filter tokens against rec using s's field definitions.
func (q QuerySpec) Matches(rec Record, s Schema) bool {
	if !q.HasFilter() {
		return true
	}
	fieldMap := map[string]QueryFieldDef{}
	for _, fd := range SchemaFieldDefs(s) {
		fieldMap[fd.Name] = fd
	}
	toks := q.Tokens
	pos := 0
	var parseOr, parseAnd, parseAtom func() bool
	parseAtom = func() bool {
		if pos >= len(toks) {
			return true
		}
		tok := toks[pos]
		switch tok.Kind {
		case ExprTokenLParen:
			pos++
			res := parseOr()
			if pos < len(toks) && toks[pos].Kind == ExprTokenRParen {
				pos++
			}
			return res
		case ExprTokenClause:
			pos++
			return clauseMatchesRecord(rec, tok, fieldMap[tok.Field])
		default:
			pos++
			return true
		}
	}
	parseAnd = func() bool {
		left := parseAtom()
		for pos < len(toks) && toks[pos].Kind == ExprTokenOp && toks[pos].Op == "AND" {
			pos++
			left = left && parseAtom()
		}
		return left
	}
	parseOr = func() bool {
		left := parseAnd()
		for pos < len(toks) && toks[pos].Kind == ExprTokenOp && toks[pos].Op == "OR" {
			pos++
			left = left || parseAnd()
		}
		return left
	}
	return parseOr()
}

func clauseMatchesRecord(rec Record, tok ExprToken, fd QueryFieldDef) bool {
	val := RecordFieldText(rec, tok.Field)
	lower := strings.ToLower(val)
	matchOne := func(candidate string) bool {
		if fd.Substring {
			return strings.Contains(lower, strings.ToLower(candidate))
		}
		return strings.EqualFold(val, candidate)
	}
	var matched bool
	if tok.All {
		matched = len(tok.Values) > 0
		for _, v := range tok.Values {
			if !matchOne(v) {
				matched = false
				break
			}
		}
	} else {
		for _, v := range tok.Values {
			if matchOne(v) {
				matched = true
				break
			}
		}
	}
	if tok.Invert {
		return !matched
	}
	return matched
}

// ToSimpleTableFilter translates a simple single-clause QuerySpec into a table
// field+query pair (returning field, query, true), or ("", summary, false) for
// compound boolean expressions that evaluate directly on QuerySpec.
func (q QuerySpec) ToSimpleTableFilter() (field, query string, simple bool) {
	var clauses []ExprToken
	for _, tok := range q.Tokens {
		if tok.Kind == ExprTokenClause && len(tok.Values) > 0 {
			clauses = append(clauses, tok)
		} else if tok.Kind == ExprTokenOp || tok.Kind == ExprTokenLParen {
			return "", q.FilterSummary(), false
		}
	}
	if len(clauses) == 0 {
		return "", "", true
	}
	if len(clauses) == 1 && !clauses[0].Invert && len(clauses[0].Values) == 1 {
		return clauses[0].Field, clauses[0].Values[0], true
	}
	return "", q.FilterSummary(), false
}
