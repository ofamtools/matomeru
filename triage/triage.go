// Package triage sorts signals with rules. A signal is a record of
// something that happened elsewhere; a triage.rule is an expression over a
// signal's fields and an action for the signals it holds for. Rules are
// records like any other: the owner writes them, edits them and turns them
// off, and every action a rule takes is an undoable change set.
//
// triage imports only the standard library, the Data API and Ochazuke's
// core and expr. It has no UI.
package triage

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/expr"
)

// DefaultSnooze is how long the snooze action hides a signal when the rule's
// actionValue names no duration.
const DefaultSnooze = 24 * time.Hour

// Now is the clock the snooze action reads.
var Now = time.Now

// Applied is one action a rule took on a signal.
type Applied struct {
	Signal core.Ref `json:"signal"`
	Rule   core.Ref `json:"rule"`
	Action string   `json:"action"`
	Value  string   `json:"value,omitempty"`
}

// Result is what an evaluation did.
type Result struct {
	// Evaluated is how many signals were looked at; Matched is how many of
	// them at least one rule held for.
	Evaluated int       `json:"evaluated"`
	Matched   int       `json:"matched"`
	Applied   []Applied `json:"applied"`
	// Problems are rules that could not run (an expression that does not
	// parse, an action that failed). The other rules still ran.
	Problems []string `json:"problems,omitempty"`
}

func (r *Result) add(other Result) {
	r.Evaluated += other.Evaluated
	r.Matched += other.Matched
	r.Applied = append(r.Applied, other.Applied...)
	for _, p := range other.Problems {
		if !slices.Contains(r.Problems, p) {
			r.Problems = append(r.Problems, p)
		}
	}
}

// Rule is an enabled triage.rule with its expression compiled.
type Rule struct {
	Record data.Record
	When   expr.Expr
}

func intOf(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

func enabled(rec data.Record) bool {
	v, has := rec.Value("enabled")
	return !has || v == nil || rec.Bool("enabled")
}

// Rules returns the enabled rules, the highest priority first (rules of one
// priority in the order they were made), and one problem for each rule whose
// expression does not parse or is empty.
func Rules(store *data.Store) ([]Rule, []string, error) {
	res, err := store.Query(data.Query{Kind: data.KindTriageRule})
	if err != nil {
		return nil, nil, err
	}
	recs := slices.DeleteFunc(res.Items, func(r data.Record) bool { return !enabled(r) })
	slices.SortStableFunc(recs, func(a, b data.Record) int {
		pa, _ := a.Value("priority")
		pb, _ := b.Value("priority")
		return intOf(pb) - intOf(pa)
	})
	var rules []Rule
	var problems []string
	for _, rec := range recs {
		src := strings.TrimSpace(rec.Text("when"))
		if src == "" {
			problems = append(problems, fmt.Sprintf("rule %q has no when expression", rec.Text("title")))
			continue
		}
		when, err := expr.Parse(src)
		if err != nil {
			problems = append(problems, fmt.Sprintf("rule %q: %v", rec.Text("title"), err))
			continue
		}
		rules = append(rules, Rule{Record: rec, When: when})
	}
	return rules, problems, nil
}

// Env is what a rule's expression reads: the signal's fields by name, with
// its id, kind, star and archive status beside them.
func Env(sig data.Record) map[string]any {
	env := sig.MergedFields()
	env["id"], env["kind"] = sig.ID, sig.Kind
	env[data.ColStarStatus], env[data.ColArchiveStatus] = sig.StarStatus, sig.ArchiveStatus
	if ref, ok := sig.RefField("task"); ok {
		env["task"] = ref.ID
	}
	return env
}

// apply carries out one rule's action on a signal.
func apply(store *data.Store, sig data.Record, rule data.Record, actor core.Actor) (Applied, error) {
	ref := sig.Ref()
	action, value := rule.Text("action"), strings.TrimSpace(rule.Text("actionValue"))
	if action == "" {
		action = data.ActionMarkNoise
	}
	done := Applied{Signal: ref, Rule: rule.Ref(), Action: action, Value: value}
	state := func(to string) error {
		_, _, err := store.Patch(data.PatchInput{Ref: ref, Fields: map[string]any{"triageState": to},
			Summary: fmt.Sprintf("triage: %s by rule %q", to, rule.Text("title"))}, actor)
		return err
	}
	var err error
	switch action {
	case data.ActionMarkNoise:
		if err = state(data.TriageNoise); err == nil {
			_, _, err = store.Archive(ref, actor)
		}
	case data.ActionMarkActionable:
		err = state(data.TriageActionable)
	case data.ActionSnooze:
		d := DefaultSnooze
		if value != "" {
			if d, err = time.ParseDuration(value); err != nil {
				return done, fmt.Errorf("snooze %q is not a duration (24h, 90m)", value)
			}
		}
		_, _, err = store.Snooze(ref, Now().Add(d), actor)
	case data.ActionTag:
		if value == "" {
			return done, errors.New("the tag action needs an actionValue: the tag")
		}
		_, err = store.Tag(ref, value, actor)
	case data.ActionRelateTask:
		if value == "" {
			return done, errors.New("the relate_task action needs an actionValue: the task's ID")
		}
		id := strings.TrimPrefix(value, data.KindTask+":")
		_, _, err = store.Patch(data.PatchInput{Ref: ref, Fields: map[string]any{"task": core.Ref{Kind: data.KindTask, ID: id}},
			Summary: fmt.Sprintf("triage: task %s by rule %q", id, rule.Text("title"))}, actor)
	default:
		return done, fmt.Errorf("unknown action %q", action)
	}
	return done, err
}

func evaluate(store *data.Store, rules []Rule, sig data.Record, actor core.Actor) Result {
	res := Result{Evaluated: 1, Applied: []Applied{}}
	env := Env(sig)
	for _, rule := range rules {
		if !rule.When.Eval(env) {
			continue
		}
		done, err := apply(store, sig, rule.Record, actor)
		if err != nil {
			res.Problems = append(res.Problems, fmt.Sprintf("rule %q on %s: %v", rule.Record.Text("title"), data.RefText(sig.Ref()), err))
			continue
		}
		res.Applied = append(res.Applied, done)
	}
	if len(res.Applied) > 0 {
		res.Matched = 1
	}
	return res
}

// Evaluate runs every enabled rule, the highest priority first, against one
// signal and applies the action of each rule that holds: mark_noise sets
// triageState to noise and archives the signal, mark_actionable sets it to
// actionable, snooze hides it for actionValue (a duration; DefaultSnooze
// when empty), tag adds actionValue as a tag and relate_task sets its task.
// actor is who the changes are written as.
func Evaluate(store *data.Store, rec data.Record, actor core.Actor) (Result, error) {
	if rec.Kind != data.KindSignal {
		return Result{}, fmt.Errorf("triage: %s is not a signal", data.RefText(rec.Ref()))
	}
	rules, problems, err := Rules(store)
	if err != nil {
		return Result{}, err
	}
	res := evaluate(store, rules, rec, actor)
	res.Problems = append(problems, res.Problems...)
	return res, nil
}

// ApplyAll evaluates every signal still unreviewed (and not archived), so a
// rule made a moment ago also triages what arrived before it.
func ApplyAll(store *data.Store, actor core.Actor) (Result, error) {
	rules, problems, err := Rules(store)
	if err != nil {
		return Result{}, err
	}
	sigs, err := store.Query(data.Query{Kind: data.KindSignal})
	if err != nil {
		return Result{}, err
	}
	total := Result{Applied: []Applied{}, Problems: problems}
	for _, sig := range sigs.Items {
		if state := sig.Text("triageState"); state != "" && state != data.TriageUnreviewed {
			continue
		}
		total.add(evaluate(store, rules, sig, actor))
	}
	return total, nil
}

// Ingest records a signal and evaluates it at once. Its triageState starts
// as unreviewed and its occurredAt as now, unless the fields say otherwise.
func Ingest(store *data.Store, fields map[string]any, actor core.Actor) (*data.Record, Result, error) {
	in := map[string]any{}
	for k, v := range fields {
		if s, isText := v.(string); v != nil && !(isText && s == "") {
			in[k] = v
		}
	}
	if _, has := in["occurredAt"]; !has {
		in["occurredAt"] = Now().UTC().Format(time.RFC3339)
	}
	if _, has := in["title"]; !has {
		in["title"] = strings.TrimSpace(data.Text(in["source"]) + " " + data.Text(in["eventKind"]))
	}
	if in["title"] == "" {
		return nil, Result{}, errors.New("triage: a signal needs a title, a source or an eventKind")
	}
	rec, _, err := store.Create(data.CreateInput{Kind: data.KindSignal, Fields: in}, actor)
	if err != nil {
		return nil, Result{}, err
	}
	res, err := Evaluate(store, *rec, actor)
	if err != nil {
		return rec, res, err
	}
	if fresh, gerr := store.Get(rec.Ref()); gerr == nil {
		rec = fresh
	}
	return rec, res, nil
}

// draftFields are the fields a drafted rule tests, the most telling first.
var draftFields = []string{"source", "author", "eventKind", "container"}

// DraftRuleFromSignal builds the fields of a triage.rule that holds for
// signals like sig: a when expression over its source and author (then its
// eventKind and container, when those are missing), marking them as noise.
// Nothing is written: the caller shows the draft and creates the rule.
func DraftRuleFromSignal(sig data.Record) map[string]any {
	var terms, words []string
	for _, name := range draftFields {
		v := strings.TrimSpace(sig.Text(name))
		if v == "" {
			continue
		}
		terms = append(terms, name+" == "+strconv.Quote(v))
		words = append(words, v)
		if len(terms) == 2 {
			break
		}
	}
	if len(terms) == 0 {
		if title := strings.TrimSpace(sig.Text("title")); title != "" {
			terms, words = []string{"title == " + strconv.Quote(title)}, []string{title}
		}
	}
	title := "Noise"
	if len(words) > 0 {
		title = "Noise: " + strings.Join(words, " ")
	}
	return map[string]any{
		"title":       title,
		"when":        strings.Join(terms, " && "),
		"action":      data.ActionMarkNoise,
		"actionValue": "",
		"priority":    0,
		"enabled":     true,
	}
}
