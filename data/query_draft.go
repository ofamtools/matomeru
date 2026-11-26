package data

import "slices"

// Sort targets of a draft in Sort mode: the records' order, or (once the
// query groups) the groups' order.
const (
	SortTargetRecords = "records"
	SortTargetGroups  = "groups"
)

// QueryDraft is a Zero-Field query being edited: the query itself and the
// editing state its next key reads. It is a pure value object; every
// transition is a method, and the UI only asks it what to show.
type QueryDraft struct {
	Mode          string // QueryModeFilter | QueryModeGroup | QueryModeSort
	SelectedField string // the field whose values Filter mode offers
	Query         QuerySpec
	QuantAll      bool   // the next clause matches all its values, not any
	InvertNext    bool   // the next clause is negated
	PendingOp     string // the join of the next operand: AND or OR
	SortTarget    string // SortTargetRecords | SortTargetGroups
}

// NewQueryDraft starts a draft of q in mode, selecting the first field of
// fields that offers fixed options (or the first field).
func NewQueryDraft(mode string, q QuerySpec, fields []QueryFieldDef) QueryDraft {
	d := QueryDraft{Query: q.Clone(), PendingOp: ExprOpAnd, SortTarget: SortTargetRecords}
	for _, fd := range fields {
		if len(fd.Options) > 0 {
			d.SelectedField = fd.Name
			break
		}
	}
	if d.SelectedField == "" && len(fields) > 0 {
		d.SelectedField = fields[0].Name
	}
	d.SetMode(mode)
	return d
}

// SetMode switches the section being edited. An unknown mode is Filter.
func (d *QueryDraft) SetMode(mode string) {
	switch mode {
	case QueryModeGroup, QueryModeSort:
		d.Mode = mode
	default:
		d.Mode = QueryModeFilter
	}
	if !d.SupportsGroupSort() {
		d.SortTarget = SortTargetRecords
	}
}

// SupportsGroupSort reports whether the query groups, so Sort mode can
// order the groups as well as the records.
func (d QueryDraft) SupportsGroupSort() bool { return d.Query.GroupBy != "" }

// SortingGroups reports whether Sort mode is ordering the groups.
func (d QueryDraft) SortingGroups() bool {
	return d.Mode == QueryModeSort && d.SupportsGroupSort() && d.SortTarget == SortTargetGroups
}

// ToggleSortTarget switches Sort mode between ordering records and groups.
func (d *QueryDraft) ToggleSortTarget() {
	if !d.SupportsGroupSort() || d.SortTarget == SortTargetGroups {
		d.SortTarget = SortTargetRecords
		return
	}
	d.SortTarget = SortTargetGroups
}

// GroupSortBy is the field the groups are ordered by, "" when they keep
// their natural order.
func (d QueryDraft) GroupSortBy() string {
	if d.Query.GroupSortDir == "" {
		return ""
	}
	return d.Query.GroupBy
}

// GroupSortDir is the groups' order: "asc", "desc" or "".
func (d QueryDraft) GroupSortDir() string { return d.Query.GroupSortDir }

// ShowsField reports whether the current mode offers fd: Group mode only
// groupable fields, Sort mode on groups only the group field.
func (d QueryDraft) ShowsField(fd QueryFieldDef) bool {
	switch {
	case d.Mode == QueryModeGroup:
		return fd.Groupable()
	case d.SortingGroups():
		return fd.Name == d.Query.GroupBy
	}
	return true
}

// FieldActive reports whether fd is what the current mode has chosen.
func (d QueryDraft) FieldActive(fd QueryFieldDef) bool {
	switch {
	case d.Mode == QueryModeGroup:
		return fd.Name == d.Query.GroupBy
	case d.SortingGroups():
		return d.GroupSortBy() == fd.Name
	case d.Mode == QueryModeSort:
		return fd.Name == d.Query.SortBy
	}
	return fd.Name == d.SelectedField
}

// ShowsValues reports whether the draft offers values: only Filter mode,
// with a field selected.
func (d QueryDraft) ShowsValues() bool {
	return d.Mode == QueryModeFilter && d.SelectedField != ""
}

// SelectField picks field the way the current mode means: Filter selects
// it for its values, Group toggles grouping by it, Sort cycles asc, desc,
// off (for the records, or for the groups).
func (d *QueryDraft) SelectField(field string) {
	switch {
	case d.Mode == QueryModeGroup:
		d.toggleGroup(field)
	case d.SortingGroups():
		if field == d.Query.GroupBy {
			d.Query.GroupSortDir = nextSortDir(d.Query.GroupSortDir)
		}
	case d.Mode == QueryModeSort:
		dir := ""
		if d.Query.SortBy == field {
			dir = d.Query.SortDir
			if dir == "" {
				dir = "asc"
			}
		}
		d.Query.SortDir = nextSortDir(dir)
		d.Query.SortBy = field
		if d.Query.SortDir == "" {
			d.Query.SortBy = ""
		}
	default:
		d.SelectedField = field
	}
}

// nextSortDir cycles off -> asc -> desc -> off.
func nextSortDir(dir string) string {
	switch dir {
	case "":
		return "asc"
	case "asc":
		return "desc"
	}
	return ""
}

func (d *QueryDraft) toggleGroup(field string) {
	if d.Query.GroupBy == field {
		d.ClearGroup()
		return
	}
	d.Query.GroupBy, d.Query.GroupSortDir = field, ""
}

// ActiveValues are the values the filter's clauses on field hold.
func (d QueryDraft) ActiveValues(field string) map[string]bool {
	out := map[string]bool{}
	for _, tok := range d.Query.Tokens {
		if tok.Kind == ExprTokenClause && tok.Field == field {
			for _, v := range tok.Values {
				out[v] = true
			}
		}
	}
	return out
}

// ToggleValue toggles value of the selected field: off when a clause holds
// it, into the trailing clause on the field (any(A, C, E)), or as a new
// clause joined by the pending operator.
func (d *QueryDraft) ToggleValue(value string) {
	field := d.SelectedField
	if field == "" || value == "" {
		return
	}
	if d.removeValue(field, value) {
		return
	}
	if tok := d.trailingClause(); tok != nil && tok.Field == field && !d.InvertNext {
		tok.Values = append(tok.Values, value)
		return
	}
	d.appendOperand(ExprToken{Kind: ExprTokenClause, Field: field, Values: []string{value}, All: d.QuantAll, Invert: d.InvertNext})
	d.InvertNext = false
}

func (d *QueryDraft) removeValue(field, value string) bool {
	for i := range d.Query.Tokens {
		tok := &d.Query.Tokens[i]
		if tok.Kind != ExprTokenClause || tok.Field != field {
			continue
		}
		if j := slices.Index(tok.Values, value); j >= 0 {
			tok.Values = slices.Delete(slices.Clone(tok.Values), j, j+1)
			if len(tok.Values) == 0 {
				d.Query.Tokens = slices.Delete(d.Query.Tokens, i, i+1)
				d.normalize()
			}
			return true
		}
	}
	return false
}

// CanCombine reports whether AND / OR mean anything yet: there is a clause
// to combine with.
func (d QueryDraft) CanCombine() bool {
	return slices.ContainsFunc(d.Query.Tokens, func(t ExprToken) bool { return t.Kind == ExprTokenClause })
}

// SetOperator makes op (AND or OR) the join of the next operand, replacing
// a trailing operator. It does nothing while there is nothing to combine.
func (d *QueryDraft) SetOperator(op string) {
	if !d.CanCombine() || (op != ExprOpAnd && op != ExprOpOr) {
		return
	}
	d.PendingOp = op
	last := d.last()
	switch {
	case last == nil:
	case isBinaryOp(*last):
		last.Op = op
	case last.Kind == ExprTokenClause || last.Kind == ExprTokenRParen:
		d.Query.Tokens = append(d.Query.Tokens, ExprToken{Kind: ExprTokenOp, Op: op})
	}
}

// ToggleInvert negates (or stops negating) the next clause.
func (d *QueryDraft) ToggleInvert() { d.InvertNext = !d.InvertNext }

// SetQuantifier picks any or all: for a group just opened, how its members
// join (any( is OR, all( is AND); otherwise for the next clause's values,
// and for the trailing clause on the selected field.
func (d *QueryDraft) SetQuantifier(all bool) {
	if last := d.last(); last != nil && last.Kind == ExprTokenLParen {
		last.Op = ExprOpOr
		if all {
			last.Op = ExprOpAnd
		}
		return
	}
	d.QuantAll = all
	if tok := d.trailingClause(); tok != nil && tok.Field == d.SelectedField {
		tok.All = all
	}
}

// OpenParens counts the groups opened and not yet closed.
func (d QueryDraft) OpenParens() int {
	n := 0
	for _, tok := range d.Query.Tokens {
		switch tok.Kind {
		case ExprTokenLParen:
			n++
		case ExprTokenRParen:
			n = max(0, n-1)
		}
	}
	return n
}

// CanCloseParen reports whether a group is open to close.
func (d QueryDraft) CanCloseParen() bool { return d.OpenParens() > 0 }

// OpenParen opens a group, joined to what precedes it by the pending
// operator.
func (d *QueryDraft) OpenParen() {
	d.appendOperand(ExprToken{Kind: ExprTokenLParen})
}

// CloseParen closes the innermost open group; an empty group is dropped.
func (d *QueryDraft) CloseParen() {
	if !d.CanCloseParen() {
		return
	}
	for last := d.last(); last != nil && isBinaryOp(*last); last = d.last() {
		d.Query.Tokens = d.Query.Tokens[:len(d.Query.Tokens)-1]
	}
	if last := d.last(); last != nil && last.Kind == ExprTokenLParen {
		d.Query.Tokens = d.Query.Tokens[:len(d.Query.Tokens)-1]
		return
	}
	d.Query.Tokens = append(d.Query.Tokens, ExprToken{Kind: ExprTokenRParen})
}

// ClearFilter drops every clause, operator and group.
func (d *QueryDraft) ClearFilter() {
	d.Query.Tokens = nil
	d.InvertNext = false
}

// ClearGroup stops grouping, and so ordering groups.
func (d *QueryDraft) ClearGroup() {
	d.Query.GroupBy, d.Query.GroupSortDir = "", ""
	d.SortTarget = SortTargetRecords
}

// ClearSort restores the default order of records and groups.
func (d *QueryDraft) ClearSort() {
	d.Query.SortBy, d.Query.SortDir, d.Query.GroupSortDir = "", "", ""
}

// RemoveClause removes the i-th clause (from 0) and whatever it leaves
// dangling.
func (d *QueryDraft) RemoveClause(i int) {
	seen := 0
	for at, tok := range d.Query.Tokens {
		if tok.Kind != ExprTokenClause {
			continue
		}
		if seen == i {
			d.Query.Tokens = slices.Delete(d.Query.Tokens, at, at+1)
			d.normalize()
			return
		}
		seen++
	}
}

// Reset clears filter, group and sort and the editing state, keeping the
// mode and selected field.
func (d *QueryDraft) Reset() {
	d.Query = QuerySpec{}
	d.QuantAll, d.InvertNext = false, false
	d.PendingOp, d.SortTarget = ExprOpAnd, SortTargetRecords
}

// Compile compiles the drafted query: the same predicate the preview, an
// applied table and a replayed preset ask.
func (d QueryDraft) Compile(s Schema) CompiledQuery { return d.Query.Compile(s) }

func (d *QueryDraft) last() *ExprToken {
	if len(d.Query.Tokens) == 0 {
		return nil
	}
	return &d.Query.Tokens[len(d.Query.Tokens)-1]
}

func (d *QueryDraft) trailingClause() *ExprToken {
	if last := d.last(); last != nil && last.Kind == ExprTokenClause {
		return last
	}
	return nil
}

// join is the operator the next operand is joined by: the innermost open
// group's quantifier, else the pending operator.
func (d QueryDraft) join() string {
	depth, joins := 0, []string{}
	for _, tok := range d.Query.Tokens {
		switch tok.Kind {
		case ExprTokenLParen:
			joins = append(joins[:depth], tok.Op)
			depth++
		case ExprTokenRParen:
			depth = max(0, depth-1)
		}
	}
	if depth > 0 && joins[depth-1] != "" {
		return joins[depth-1]
	}
	if d.PendingOp == ExprOpOr {
		return ExprOpOr
	}
	return ExprOpAnd
}

// appendOperand appends tok, joined by an operator when it follows one.
func (d *QueryDraft) appendOperand(tok ExprToken) {
	if last := d.last(); last != nil && (last.Kind == ExprTokenClause || last.Kind == ExprTokenRParen) {
		d.Query.Tokens = append(d.Query.Tokens, ExprToken{Kind: ExprTokenOp, Op: d.join()})
	}
	d.Query.Tokens = append(d.Query.Tokens, tok)
}

func isBinaryOp(tok ExprToken) bool {
	return tok.Kind == ExprTokenOp && (tok.Op == ExprOpAnd || tok.Op == ExprOpOr)
}

// normalize drops what a removal leaves dangling: operators with no
// operand on one side, and empty groups.
func (d *QueryDraft) normalize() {
	for changed := true; changed; {
		changed = false
		toks := d.Query.Tokens
		for i := range toks {
			if d.dangling(i) {
				d.Query.Tokens = slices.Delete(toks, i, i+1)
				changed = true
				break
			}
			if toks[i].Kind == ExprTokenLParen && i+1 < len(toks) && toks[i+1].Kind == ExprTokenRParen {
				d.Query.Tokens = slices.Delete(toks, i, i+2)
				changed = true
				break
			}
		}
	}
}

// dangling reports whether token i is an operator missing an operand.
func (d QueryDraft) dangling(i int) bool {
	toks := d.Query.Tokens
	tok := toks[i]
	if tok.Kind != ExprTokenOp {
		return false
	}
	next := i + 1
	if next == len(toks) || toks[next].Kind == ExprTokenRParen || isBinaryOp(toks[next]) {
		return true
	}
	if !isBinaryOp(tok) {
		return false
	}
	return i == 0 || toks[i-1].Kind == ExprTokenLParen || toks[i-1].Kind == ExprTokenOp
}
