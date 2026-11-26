package data

import (
	"fmt"
	"strings"

	"ofam.dev/ochazuke/core"
)

// ClauseLabels names the values a query's clauses hold: a reference
// field's value by its target's presentation (never kind:id), any other
// value as itself. The zero value names every value as itself.
type ClauseLabels struct {
	schema Schema
	refs   ResolvedRefs
	byText map[string]core.Ref
}

// NewClauseLabels indexes the references records hold in s's reference
// fields, to be named through refs.
func NewClauseLabels(s Schema, records []Record, refs ResolvedRefs) ClauseLabels {
	l := ClauseLabels{schema: s, refs: refs, byText: map[string]core.Ref{}}
	for _, field := range s.RefFieldNames() {
		for _, rec := range records {
			if ref, ok := AsRef(rec.Fields[field]); ok {
				l.byText[RefText(ref)] = ref
			}
		}
	}
	for ref := range refs.Records {
		l.byText[RefText(ref)] = ref
	}
	return l
}

// RefFieldNames lists the schema's reference-typed fields.
func (s Schema) RefFieldNames() []string {
	var out []string
	for _, f := range s.Fields {
		if f.Type == core.ParamRef {
			out = append(out, f.Name)
		}
	}
	return out
}

// QueryRefs lists the distinct references records hold in s's reference
// fields: one ResolveRefs batch names every option and clause.
func QueryRefs(s Schema, records []Record) []core.Ref {
	seen := map[core.Ref]bool{}
	var out []core.Ref
	for _, field := range s.RefFieldNames() {
		for _, ref := range FieldRefs(records, field) {
			if !seen[ref] {
				seen[ref] = true
				out = append(out, ref)
			}
		}
	}
	return out
}

// Refs is the resolved batch the labels name references through.
func (l ClauseLabels) Refs() ResolvedRefs { return l.refs }

// isRefField reports whether field holds references.
func (l ClauseLabels) isRefField(field string) bool {
	for _, f := range l.schema.Fields {
		if f.Name == field {
			return f.Type == core.ParamRef
		}
	}
	return false
}

// Presentation is how value of field presents as a chip.
func (l ClauseLabels) Presentation(field, value string) ResolvedPresentation {
	plain := ResolvedPresentation{Form: FormChip, Label: value}
	if l.refs.Presentations == nil || !l.isRefField(field) {
		return plain
	}
	ref, ok := l.byText[value]
	if !ok {
		kind, id, cut := strings.Cut(value, ":")
		if !cut || kind == "" || id == "" {
			return plain
		}
		ref = core.Ref{Kind: kind, ID: id}
	}
	return l.refs.Presentation(ref, FormChip)
}

// Value is the label of value of field.
func (l ClauseLabels) Value(field, value string) string {
	return l.Presentation(field, value).Label
}

// Clause is a concise human-readable label for a clause token.
func (l ClauseLabels) Clause(tok ExprToken) string {
	prefix := ""
	if tok.Invert {
		prefix = "NOT "
	}
	values := make([]string, len(tok.Values))
	for i, v := range tok.Values {
		values[i] = l.Value(tok.Field, v)
	}
	if len(values) == 1 {
		return fmt.Sprintf("%s%s=%s", prefix, tok.Field, values[0])
	}
	quant := "any"
	if tok.All {
		quant = "all"
	}
	return fmt.Sprintf("%s%s=%s(%s)", prefix, tok.Field, quant, strings.Join(values, ", "))
}

// Token is the label of one expression token.
func (l ClauseLabels) Token(tok ExprToken) string {
	switch tok.Kind {
	case ExprTokenLParen:
		return groupQuantifier(tok.Op) + "("
	case ExprTokenRParen:
		return ")"
	case ExprTokenOp:
		return tok.Op
	}
	return l.Clause(tok)
}

// groupQuantifier is the quantifier a "(" token's join stands for.
func groupQuantifier(join string) string {
	switch join {
	case ExprOpOr:
		return "any"
	case ExprOpAnd:
		return "all"
	}
	return ""
}

// Summary is q's filter as a readable boolean expression.
func (l ClauseLabels) Summary(q QuerySpec) string {
	parts := make([]string, 0, len(q.Tokens))
	for _, tok := range q.Tokens {
		if tok.Kind == ExprTokenClause && len(tok.Values) == 0 {
			continue
		}
		parts = append(parts, l.Token(tok))
	}
	return strings.Join(parts, " ")
}
