package setup

import (
	"bytes"
	"context"
	_ "embed"
	"path/filepath"
	"slices"
	"sync"

	osetup "ofam.dev/ochazuke/setup"
)

// notes.json declares mtm's agent-instruction notes as data for ochazuke's
// setup engine (its block kind): the "One MCP server: mtm" section of the
// owner's instructions, between mtm markers, in Claude Code's and Codex's user
// instructions. The rest of either file is untouched, and both items share one
// marker pair so two paths that resolve to one file hold one note. The hook
// items above stay mtm's own; the notes ride the library's engine, so an
// item added to notes.json is offered by setup list with no code.
//
//go:embed notes.json
var notesJSON []byte

var noteItems = sync.OnceValues(func() ([]osetup.Item, error) {
	return osetup.Decode(bytes.NewReader(notesJSON))
})

func init() {
	items, err := noteItems()
	if err != nil {
		panic("setup: notes.json: " + err.Error())
	}
	for _, it := range items {
		harness := "claude-code"
		if it.ID == "codex-agents" {
			harness = "codex"
		}
		defs = append(defs, Definition{ID: it.ID, Name: it.Name, Harness: harness, Description: it.Description})
	}
}

// isNote reports whether id is one of the notes.json items.
func isNote(id string) bool {
	items, _ := noteItems()
	return slices.ContainsFunc(items, func(it osetup.Item) bool { return it.ID == id })
}

func (o Options) engine() (*osetup.Setup, error) {
	items, err := noteItems()
	if err != nil {
		return nil, err
	}
	opts := osetup.Options{Tool: "mtm", Home: o.Home, Bin: o.Bin, Data: o.Data, Skip: o.Skip}
	if o.Skip == nil {
		skip, err := skipDefaults(items, opts)
		if err != nil {
			return nil, err
		}
		opts.Skip = skip
	}
	return osetup.New(items, opts)
}

// skipDefaults: co-located instruction files commonly import each other, so
// of identical blocks in one resolved directory only the last is offered. It
// is a default: a non-empty setup.skip replaces it. (The same function is in
// o-lane, o-questions and o-cmds; upstream todo: one copy in ochazuke's cli.)
func skipDefaults(items []osetup.Item, opts osetup.Options) ([]string, error) {
	var blocks []osetup.Item
	for _, item := range items {
		if item.Kind == osetup.Block && item.Follow {
			blocks = append(blocks, item)
		}
	}
	engine, err := osetup.New(blocks, opts)
	if err != nil {
		return nil, err
	}
	rows, err := engine.List(context.Background())
	if err != nil {
		return nil, err
	}
	type block struct{ dir, begin, end, content string }
	seen := map[block]bool{}
	skip := []string{}
	for n := len(rows) - 1; n >= 0; n-- {
		row := rows[n]
		path, err := filepath.EvalSymlinks(row.Path)
		if err != nil {
			continue
		}
		key := block{filepath.Dir(path), row.Begin, row.End, row.Content}
		if seen[key] {
			skip = append(skip, row.ID)
		}
		seen[key] = true
	}
	return skip, nil
}

// notePlan is a note's state and what installing writes (the whole file).
func (o Options) notePlan(id string) (Plan, error) {
	def, err := definition(id)
	if err != nil {
		return Plan{}, err
	}
	engine, err := o.engine()
	if err != nil {
		return Plan{}, err
	}
	plan, err := engine.Plan(context.Background(), osetup.Selection{IDs: []string{id}})
	if err != nil {
		return Plan{Item: Item{Definition: def, Status: "conflict"}}, err
	}
	p := Plan{Item: Item{Definition: def, Status: "missing"}}
	for _, st := range plan.Items {
		p.Path, p.Status = st.Path, noteStatus(st)
	}
	for _, w := range plan.Writes {
		if w.Path == p.Path {
			p.Content = w.Content
		}
	}
	return p, nil
}

// noteStatus keeps mtm's words (missing, installed, outdated, conflict) and
// says the engine's others (n/a, skipped, external) as they are, with the
// reason where there is one.
func noteStatus(st osetup.Status) string {
	s := string(st.State)
	if st.Reason != "" && st.State != osetup.Installed && st.State != osetup.Missing {
		s += " (" + st.Reason + ")"
	}
	return s
}

func (o Options) noteApply(id string, remove bool) (Item, error) {
	def, err := definition(id)
	if err != nil {
		return Item{}, err
	}
	engine, err := o.engine()
	if err != nil {
		return Item{Definition: def}, err
	}
	sel := osetup.Selection{IDs: []string{id}}
	var rows []osetup.Status
	if remove {
		rows, err = engine.Uninstall(context.Background(), sel)
	} else {
		rows, err = engine.Install(context.Background(), sel)
	}
	it := Item{Definition: def, Status: "missing"}
	for _, st := range rows {
		it.Path, it.Status = st.Path, noteStatus(st)
	}
	return it, err
}
