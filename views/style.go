package views

import (
	"maps"
	"strings"
	"sync"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/expr"
)

// StyleOptions controls declarative visual styling in presenters.
// Both flags default to true and can be opted out via app settings.
type StyleOptions struct {
	RowSemanticStyling bool
	StatusIcons        bool
}

// DefaultStyleOptions enables both semantic row/cell styling and status icons.
func DefaultStyleOptions() StyleOptions {
	return StyleOptions{
		RowSemanticStyling: true,
		StatusIcons:        true,
	}
}

// RecordStyle is the resolved declarative styling for one record.
type RecordStyle struct {
	RowTone   string
	Icon      string
	IconTone  string
	CellTones map[string]string
	CellIcons map[string]string
	IconTones map[string]string
	Archived  bool
	Unread    bool
	Starred   bool
}

type compiledExpr struct {
	e   expr.Expr
	err error
}

var exprCache sync.Map // map[string]compiledExpr

// EvalExpr compiles (with caching) and evaluates a boolean predicate expression
// against env. An empty expression evaluates to true.
func EvalExpr(src string, env map[string]any) (bool, error) {
	trimmed := strings.TrimSpace(src)
	if trimmed == "" {
		return true, nil
	}
	var ce compiledExpr
	if cached, ok := exprCache.Load(trimmed); ok {
		ce = cached.(compiledExpr)
	} else {
		parsed, err := expr.Parse(trimmed)
		ce = compiledExpr{e: parsed, err: err}
		exprCache.Store(trimmed, ce)
	}
	if ce.err != nil {
		return false, ce.err
	}
	return ce.e.Eval(env), nil
}

// RecordFacts returns the facts map exposed to bindings, overlays, and
// expressions for rec: universal status columns, snoozed state, schema status
// columns, and every field listed in schema.FactFields (defaulting missing
// boolean fields to false).
func RecordFacts(schema data.Schema, rec data.Record, now time.Time) map[string]any {
	_, _, hasStatus := schema.PrimaryStatusField()
	facts := map[string]any{
		data.ColArchiveStatus: rec.ArchiveStatus,
		data.ColStarStatus:    rec.StarStatus,
		"snoozed":             rec.IsSnoozed(now),
		"hasStatus":           hasStatus,
	}
	for _, col := range schema.StatusColumns {
		facts[col.Name] = rec.Text(col.Name)
	}
	for _, name := range schema.FactFields {
		if name == "" {
			continue
		}
		f, declared := schema.Field(name)
		if declared && f.Type == core.ParamBool {
			if _, has := rec.Value(name); has {
				facts[name] = rec.Bool(name)
			} else if def, ok := f.Default.(bool); ok {
				facts[name] = def
			} else {
				facts[name] = false
			}
			continue
		}
		if v, has := rec.Value(name); has && v != nil {
			facts[name] = v
		} else if declared && f.Default != nil {
			facts[name] = f.Default
		} else {
			facts[name] = false
		}
	}
	return facts
}

// RecordExprEnv builds the expression evaluation environment for a record.
func RecordExprEnv(schema data.Schema, rec data.Record, now time.Time) map[string]any {
	fields := rec.MergedFields()
	if fields == nil {
		fields = map[string]any{}
	} else {
		fields = maps.Clone(fields)
	}
	for _, f := range schema.Fields {
		if _, exists := fields[f.Name]; !exists && f.Default != nil {
			fields[f.Name] = f.Default
		}
	}
	return map[string]any{
		"id":            rec.ID,
		"kind":          rec.Kind,
		"fields":        fields,
		"facts":         RecordFacts(schema, rec, now),
		"archived":      rec.IsArchived(),
		"starred":       rec.IsStarred(),
		"snoozed":       rec.IsSnoozed(now),
		"unread":        rec.IsUnreadBy(schema.UnreadField),
		"archiveStatus": rec.ArchiveStatus,
		"starStatus":    rec.StarStatus,
	}
}

// StatusIndicator looks up a status column value on schema and returns its
// declarative icon, normalized label, cell/icon tone, and row tone.
func StatusIndicator(schema data.Schema, colName, value string) (icon, label, tone, rowTone string) {
	v := strings.TrimSpace(value)
	col, hasCol := schema.StatusColumn(colName)
	if v == "" && hasCol && col.Default != "" {
		v = col.Default
	}
	if hasCol {
		sv, ok := col.Value(v)
		if !ok {
			sv, ok = col.Value(strings.ToLower(v))
		}
		if ok {
			iconTone := sv.IconTone
			if iconTone == "" {
				iconTone = sv.Tone
			}
			return sv.Icon, sv.Name, iconTone, sv.RowTone
		}
	}
	if v == "" {
		return "·", "unknown", "muted", "row.muted"
	}
	return "·", v, "muted", "row.muted"
}

// ResolveRecordStyle evaluates schema status columns, style rules, archived
// dimming, and unread/star state for rec under opts.
func ResolveRecordStyle(schema data.Schema, rec data.Record, opts StyleOptions) RecordStyle {
	now := time.Now()
	st := RecordStyle{
		Archived: rec.IsArchived(),
		Unread:   rec.IsUnreadBy(schema.UnreadField),
		Starred:  rec.IsStarred(),
	}
	setCellTone := func(col, tone string) {
		if tone == "" {
			return
		}
		if st.CellTones == nil {
			st.CellTones = map[string]string{}
		}
		st.CellTones[col] = tone
	}
	setCellIcon := func(col, icon string) {
		if icon == "" {
			return
		}
		if st.CellIcons == nil {
			st.CellIcons = map[string]string{}
		}
		st.CellIcons[col] = icon
	}
	setIconTone := func(col, tone string) {
		if tone == "" {
			return
		}
		if st.IconTones == nil {
			st.IconTones = map[string]string{}
		}
		st.IconTones[col] = tone
	}

	for _, col := range schema.StatusColumns {
		val := strings.TrimSpace(rec.Text(col.Name))
		if val == "" {
			val = col.Default
		}
		sv, ok := col.Value(val)
		if !ok {
			sv, ok = col.Value(strings.ToLower(val))
		}
		if !ok {
			continue
		}
		iconTone := sv.IconTone
		if iconTone == "" {
			iconTone = sv.Tone
		}
		if opts.RowSemanticStyling {
			setCellTone(col.Name, sv.Tone)
			if col.PrimaryRowStyle && sv.RowTone != "" {
				st.RowTone = sv.RowTone
			}
		}
		if opts.StatusIcons && sv.Icon != "" {
			setCellIcon(col.Name, sv.Icon)
			if opts.RowSemanticStyling {
				setIconTone(col.Name, iconTone)
			}
			if col.PrimaryRowStyle {
				st.Icon = sv.Icon
				if opts.RowSemanticStyling {
					st.IconTone = iconTone
				}
			}
		}
	}

	if len(schema.StyleRules) > 0 && (opts.RowSemanticStyling || opts.StatusIcons) {
		env := RecordExprEnv(schema, rec, now)
		for _, rule := range schema.StyleRules {
			matched, err := EvalExpr(rule.When, env)
			if err != nil || !matched {
				continue
			}
			target := strings.TrimSpace(rule.Target)
			if target == "" || target == "row" {
				if opts.RowSemanticStyling && rule.Tone != "" {
					st.RowTone = rule.Tone
				}
				if opts.StatusIcons && rule.Icon != "" {
					st.Icon = rule.Icon
					if opts.RowSemanticStyling && rule.IconTone != "" {
						st.IconTone = rule.IconTone
					}
				}
				continue
			}
			if opts.RowSemanticStyling && rule.Tone != "" {
				setCellTone(target, rule.Tone)
			}
			if opts.StatusIcons && rule.Icon != "" {
				setCellIcon(target, rule.Icon)
				if opts.RowSemanticStyling {
					it := rule.IconTone
					if it == "" {
						it = rule.Tone
					}
					setIconTone(target, it)
				}
			}
		}
	}

	if st.Archived && opts.RowSemanticStyling {
		st.RowTone = "row.dim"
	}
	return st
}
