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

// Boolean operators of a filter expression. NOT is a prefix operator that
// negates the operand after it; a clause's own Invert flag is the same thing
// written on the clause.
const (
	ExprOpAnd = "AND"
	ExprOpOr  = "OR"
	ExprOpNot = "NOT"
)

// ExprToken is one clause, boolean operator (AND/OR/NOT), or parenthesis in QuerySpec.
type ExprToken struct {
	Kind   ExprTokenKind `json:"kind"`
	Field  string        `json:"field,omitempty"`
	Values []string      `json:"values,omitempty"`
	All    bool          `json:"all,omitempty"` // false = any/in, true = all
	Invert bool          `json:"invert,omitempty"`
	Op     string        `json:"op,omitempty"` // "AND", "OR" or "NOT"
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

// Matches evaluates q's filter against one record. A caller that tests many
// records compiles once (Compile) and asks the CompiledQuery instead.
func (q QuerySpec) Matches(rec Record, s Schema) bool {
	return q.Compile(s).Matches(rec)
}

// recordPredicate is one compiled node of a filter expression.
type recordPredicate func(Record) bool

// CompiledQuery is a QuerySpec's filter parsed once against a Schema. It is
// the one predicate the Zero-Field preview, an applied table and a replayed
// preset all ask, so the three always agree on which records match.
//
// Precedence is NOT > AND > OR, and parentheses group. Compiling never fails,
// because the spec is edited one token at a time: a clause without values is
// ignored, an operator with a missing operand is dropped, two operands with
// no operator between them are joined by AND, an unclosed "(" is closed at
// the end, and a ")" that closes nothing is skipped.
type CompiledQuery struct {
	spec  QuerySpec
	match recordPredicate // nil: no active clause, every record matches
}

// Compile parses q's filter tokens against s's field definitions.
func (q QuerySpec) Compile(s Schema) CompiledQuery {
	fields := map[string]QueryFieldDef{}
	for _, fd := range SchemaFieldDefs(s) {
		fields[fd.Name] = fd
	}
	p := exprParser{fields: fields}
	for _, tok := range q.Tokens {
		if tok.Kind != ExprTokenClause || len(tok.Values) > 0 {
			p.toks = append(p.toks, tok)
		}
	}
	return CompiledQuery{spec: q, match: p.expression()}
}

// Spec returns the query this was compiled from.
func (c CompiledQuery) Spec() QuerySpec { return c.spec }

// HasFilter reports whether the compiled filter can exclude a record.
func (c CompiledQuery) HasFilter() bool { return c.match != nil }

// Matches reports whether rec passes the filter. It parses nothing.
func (c CompiledQuery) Matches(rec Record) bool {
	return c.match == nil || c.match(rec)
}

// Filter returns the records that match, in their given order.
func (c CompiledQuery) Filter(recs []Record) []Record {
	if c.match == nil {
		return recs
	}
	out := make([]Record, 0, len(recs))
	for _, rec := range recs {
		if c.match(rec) {
			out = append(out, rec)
		}
	}
	return out
}

// exprParser is a recursive-descent parser over filter tokens. Every parse
// step returns nil for "no operand here", which its caller skips.
type exprParser struct {
	toks   []ExprToken
	pos    int
	fields map[string]QueryFieldDef
}

func (p *exprParser) peek() (ExprToken, bool) {
	if p.pos >= len(p.toks) {
		return ExprToken{}, false
	}
	return p.toks[p.pos], true
}

func (p *exprParser) atOp(op string) bool {
	tok, ok := p.peek()
	return ok && tok.Kind == ExprTokenOp && strings.EqualFold(strings.TrimSpace(tok.Op), op)
}

// startsOperand reports whether the next token can begin an operand, which is
// what makes two adjacent operands an implicit AND.
func (p *exprParser) startsOperand() bool {
	tok, ok := p.peek()
	return ok && (tok.Kind == ExprTokenClause || tok.Kind == ExprTokenLParen || p.atOp(ExprOpNot))
}

// expression parses the whole token list. A ")" that closes nothing is
// skipped, and what follows it is joined by AND.
func (p *exprParser) expression() recordPredicate {
	var out recordPredicate
	for p.pos < len(p.toks) {
		before := p.pos
		out = allOf(out, p.or())
		if p.pos == before {
			p.pos++
		}
	}
	return out
}

func (p *exprParser) or() recordPredicate {
	left := p.and()
	for p.atOp(ExprOpOr) {
		p.pos++
		left = anyOf(left, p.and())
	}
	return left
}

func (p *exprParser) and() recordPredicate {
	left := p.not()
	for {
		switch {
		case p.atOp(ExprOpAnd):
			p.pos++
		case !p.startsOperand():
			return left
		}
		left = allOf(left, p.not())
	}
}

func (p *exprParser) not() recordPredicate {
	negate := false
	for p.atOp(ExprOpNot) {
		p.pos++
		negate = !negate
	}
	operand := p.atom()
	if !negate || operand == nil {
		return operand
	}
	return func(r Record) bool { return !operand(r) }
}

func (p *exprParser) atom() recordPredicate {
	tok, ok := p.peek()
	if !ok {
		return nil
	}
	switch tok.Kind {
	case ExprTokenClause:
		p.pos++
		return clausePredicate(tok, p.fields[tok.Field])
	case ExprTokenLParen:
		p.pos++
		inner := p.or()
		if next, ok := p.peek(); ok && next.Kind == ExprTokenRParen {
			p.pos++
		}
		return inner
	case ExprTokenOp:
		if !p.atOp(ExprOpAnd) && !p.atOp(ExprOpOr) {
			p.pos++ // an operator nobody knows: skip it
			return p.atom()
		}
	}
	return nil // ")" or a binary operator: no operand here
}

// allOf is left AND right; a missing side leaves the other.
func allOf(left, right recordPredicate) recordPredicate {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	return func(r Record) bool { return left(r) && right(r) }
}

// anyOf is left OR right; a missing side leaves the other.
func anyOf(left, right recordPredicate) recordPredicate {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	return func(r Record) bool { return left(r) || right(r) }
}

// clausePredicate compiles one clause: any (or all) of its values match the
// record's field, by substring for free-text fields and by equality otherwise.
func clausePredicate(tok ExprToken, fd QueryFieldDef) recordPredicate {
	field, substring, all, invert := tok.Field, fd.Substring, tok.All, tok.Invert
	values := make([]string, len(tok.Values))
	for i, v := range tok.Values {
		values[i] = strings.ToLower(v)
	}
	return func(rec Record) bool {
		text := strings.ToLower(RecordFieldText(rec, field))
		matchOne := func(v string) bool {
			if substring {
				return strings.Contains(text, v)
			}
			return text == v
		}
		matched := slices.ContainsFunc(values, matchOne)
		if all {
			matched = !slices.ContainsFunc(values, func(v string) bool { return !matchOne(v) })
		}
		return matched != invert
	}
}
