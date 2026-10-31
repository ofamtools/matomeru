package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

const sidesFile = "sides.json"

type sideRow struct {
	ocli.CatalogSide
	Hint   string `json:"hint,omitempty"`
	Source string `json:"source"`
	Bin    string `json:"bin,omitempty"`
}

func builtinSides() []sideRow {
	return []sideRow{
		{CatalogSide: ocli.CatalogSide{Name: "vim", Title: "side vim", Key: "v", Launch: "vim-session"}, Hint: "vim", Source: "built-in"},
		{CatalogSide: ocli.CatalogSide{Name: "term", Title: "side terminal", Key: "t", Command: []string{"{shell}"}, Launch: "command"}, Hint: "term", Source: "built-in"},
	}
}

// sidePatch follows harnesses.json: omitted fields keep the earlier row's
// values; explicit empty fields replace them (notably key: "").
type sidePatch struct {
	Name     string    `json:"name"`
	Title    *string   `json:"title"`
	Hint     *string   `json:"hint"`
	Key      *string   `json:"key"`
	Command  *[]string `json:"command"`
	Launch   *string   `json:"launch"`
	Disabled bool      `json:"disabled"`
}

func loadSidePatches(path string) ([]sidePatch, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var rows []sidePatch
	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if rows == nil {
		return nil, fmt.Errorf("%s: expected a JSON array", path)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("%s: expected one JSON array", path)
	}
	return rows, nil
}

func mergeSideRows(exts []*extension, patches []sidePatch) ([]sideRow, []string, error) {
	rows := builtinSides()
	notices := []string{}
	put := func(row sideRow) {
		for i, old := range rows {
			if old.Name == row.Name {
				rows[i] = row
				return
			}
		}
		rows = append(rows, row)
	}
	for _, x := range exts {
		if err := x.doc.ValidateSides(); err != nil {
			notices = append(notices, x.name+": "+err.Error())
			continue
		}
		for _, row := range x.doc.Sides {
			if err := validateSideRow(row); err != nil {
				notices = append(notices, x.name+": "+err.Error())
				continue
			}
			if slices.ContainsFunc(builtinSides(), func(b sideRow) bool { return b.Name == row.Name }) {
				notices = append(notices, fmt.Sprintf("%s: side %s skipped: built-in name", x.name, row.Name))
				continue
			}
			put(sideRow{CatalogSide: row, Source: "extension:" + x.name, Bin: x.bin})
		}
	}
	for _, patch := range patches {
		// Validate the name even when disabling a row.
		if err := (ocli.CatalogDoc{Sides: []ocli.CatalogSide{{Name: patch.Name, Launch: "disabled"}}}).ValidateSides(); err != nil {
			return nil, notices, err
		}
		if patch.Disabled {
			rows = slices.DeleteFunc(rows, func(row sideRow) bool { return row.Name == patch.Name })
			continue
		}
		row := sideRow{CatalogSide: ocli.CatalogSide{Name: patch.Name}, Source: "user"}
		for _, old := range rows {
			if old.Name == patch.Name {
				row = old
				break
			}
		}
		row.Source = "user"
		if patch.Title != nil {
			row.Title = *patch.Title
		}
		if patch.Hint != nil {
			row.Hint = *patch.Hint
		}
		if patch.Key != nil {
			row.Key = *patch.Key
		}
		if patch.Command != nil {
			row.Command = slices.Clone(*patch.Command)
		}
		if patch.Launch != nil {
			row.Launch = *patch.Launch
		}
		put(row)
	}
	for i := range rows {
		if rows[i].Launch == "" {
			rows[i].Launch = "command"
		}
		if rows[i].Title == "" {
			rows[i].Title = rows[i].Name
		}
		if err := validateSideRow(rows[i].CatalogSide); err != nil {
			return nil, notices, err
		}
	}
	return rows, notices, nil
}

func (a *App) sideDefinition(name string) (sideRow, bool) {
	for _, row := range a.sideRows {
		if row.Name == name {
			return row, true
		}
	}
	return sideRow{}, false
}

func (a *App) sideTitle(name string) string {
	if row, ok := a.sideDefinition(name); ok {
		return row.Title
	}
	return "side " + name // removed extension: its companion is still reachable
}

func (a *App) sideHint(name string) string {
	if row, ok := a.sideDefinition(name); ok && row.Hint != "" {
		return row.Hint
	}
	return name
}

func (a *App) reloadSides() []string {
	patches, err := loadSidePatches(filepath.Join(a.configDir, sidesFile))
	if err != nil {
		return []string{err.Error()}
	}
	rows, notices, err := mergeSideRows(a.exts, patches)
	if err != nil {
		return append(notices, err.Error())
	}
	before := a.sideRows
	a.sideRows = rows
	a.setSideChoices()
	if err := a.bindSideKeys(); err != nil {
		a.sideRows = before
		a.setSideChoices()
		return append(notices, err.Error())
	}
	return notices
}

func (a *App) refreshSides() {
	for _, msg := range a.reloadSides() {
		a.r.UI().Notify(sidesFile + ": " + msg)
	}
}

func (a *App) setSideChoices() {
	var names []string
	for _, row := range a.sideRows {
		names = append(names, row.Name)
	}
	for _, key := range []string{"pane.side", "terminal.side", "side.show", "side.restart"} {
		op, ok := a.r.Operations[a.r.CanonicalKey(key)]
		if !ok {
			continue
		}
		param := "kind"
		if strings.HasPrefix(key, "side.") {
			param = "name"
		}
		p := op.Parameters[param]
		p.Enum = slices.Clone(names)
		if key == "pane.side" {
			p.Enum = append(p.Enum, "list")
		}
		op.Parameters[param] = p
		a.r.Operations[a.r.CanonicalKey(key)] = op
	}
}

func (a *App) listSideRows(ochazuke.Ctx) ochazuke.Result {
	return ochazuke.Done(slices.Clone(a.sideRows))
}

func (a *App) showSideRow(ctx ochazuke.Ctx) ochazuke.Result {
	ctx.Args = ochazuke.Arguments{"kind": ochazuke.StringArg(ctx.Args, "name", ""), "pane": ochazuke.StringArg(ctx.Args, "pane", "")}
	return a.sidePane(ctx)
}

func (a *App) restartSideRow(ctx ochazuke.Ctx) ochazuke.Result {
	name := ochazuke.StringArg(ctx.Args, "name", "")
	if name == "" {
		name = a.attached.SideFront
	}
	ctx.Args = ochazuke.Arguments{"kind": name, "pane": ochazuke.StringArg(ctx.Args, "pane", ""), "restart": true}
	return a.sidePane(ctx)
}

func (a *App) sideOpening(parent string) bool {
	for key := range a.sideStarting {
		if strings.HasPrefix(key, parent+"/") {
			return true
		}
	}
	return false
}

func (a *App) sideLeaderWords() []struct{ key, text string } {
	var words []struct{ key, text string }
	if n := a.r.Noun("terminal"); n != nil {
		for _, row := range a.sideRows {
			for _, b := range n.Bindings["leader_pending"] {
				if !b.Unbind && b.Request.Key() == a.r.CanonicalKey("terminal.side") && ochazuke.StringArg(b.Request.Args, "kind", "") == row.Name {
					words = append(words, struct{ key, text string }{b.Keys, row.Title + ": open it, focus it, or back to the pane"})
				}
			}
		}
	}
	return append(words, leaderWords...)
}

const sidesBindingsSource = "mtm/sides"
const sidesLeaderSource = "mtm/sides-leader"

// bindSideKeys replaces one named default source below bindings.json.
// Collision checks use defaults so a user override never hides a clash or
// prevents the row it overrides from being regenerated.
func (a *App) bindSideKeys() error {
	if a.r.Noun("terminal") == nil {
		return nil
	}
	occupied := map[string]bool{}
	for _, b := range a.r.DefaultBindings() {
		if b.Noun == a.r.CanonicalNoun("terminal") && b.Mode == "leader_pending" && b.Source != sidesBindingsSource && b.Keys != "" {
			occupied[b.Keys] = true
		}
	}
	var rows []ochazuke.Binding
	for _, row := range a.sideRows {
		if row.Key == "" {
			continue
		}
		if occupied[row.Key] {
			a.r.UI().Notify(fmt.Sprintf("side %s: leader key %s skipped: already bound", row.Name, row.Key))
			continue
		}
		occupied[row.Key] = true
		binding := ochazuke.Do("terminal.side", ochazuke.Arguments{"kind": row.Name}).Hinted(a.sideHint(row.Name))
		binding.Noun, binding.Mode, binding.Keys = "terminal", "leader_pending", row.Key
		binding.Help = row.Title + ": open it, focus it, or back to the pane"
		rows = append(rows, binding)
	}
	if err := a.r.SetDefaultBindings(sidesBindingsSource, rows); err != nil {
		return err
	}
	return a.updateSideLeaderHint()
}

// updateSideLeaderHint changes only the dynamic side portion; the rest is
// the existing leader summary. The modal and keyboard help read the rows.
func (a *App) updateSideLeaderHint() error {
	n := a.r.Noun("terminal")
	if n == nil {
		return nil
	}
	var parts []string
	for _, b := range n.Bindings["leader_pending"] {
		if b.Request.Key() != a.r.CanonicalKey("terminal.side") || b.Unbind {
			continue
		}
		name := ochazuke.StringArg(b.Request.Args, "kind", "")
		parts = append(parts, b.Keys+" "+a.sideHint(name))
	}
	parts = append(parts, "d close/detach · z zoom · o other · R layout · w sides · j/k panes · p pick · s starred · ` starred · W waiting · * star · [ copy · c context · f hints · N note · n task · T todos · S all · : palette · , settings · ' pages · ? keys")
	for _, b := range a.r.DefaultBindings() {
		if b.Noun == a.r.CanonicalNoun("terminal") && b.Mode == "terminal" && (b.Keys == "{terminal-leader}" || b.Keys == a.r.TerminalLeader()) {
			row := b.Binding
			row.Hint = "leader: " + strings.Join(parts, " · ")
			return a.r.SetDefaultBindings(sidesLeaderSource, []ochazuke.Binding{row})
		}
	}
	return nil
}

// sideNames keeps the chooser in declaration order, with removed catalog
// rows last in stable name order; visibility history is separate.
func (a *App) sideNames(s sideState) []string {
	var names, unknown []string
	for _, row := range a.sideRows {
		if s.Windows[row.Name] != "" {
			names = append(names, row.Name)
		}
	}
	for name, id := range s.Windows {
		if id != "" && !slices.Contains(names, name) {
			unknown = append(unknown, name)
		}
	}
	slices.Sort(unknown)
	return append(names, unknown...)
}

func (a *App) sideKey(name string) string {
	if n := a.r.Noun("terminal"); n != nil {
		for _, b := range n.Bindings["leader_pending"] {
			if !b.Unbind && b.Request.Key() == a.r.CanonicalKey("terminal.side") && ochazuke.StringArg(b.Request.Args, "kind", "") == name {
				return b.Keys
			}
		}
	}
	return ""
}

func validateSideRow(row ocli.CatalogSide) error {
	if err := (ocli.CatalogDoc{Sides: []ocli.CatalogSide{row}}).ValidateSides(); err != nil {
		return err
	}
	if row.Name == "list" {
		return errors.New("side name list is reserved for pane.side kind=list")
	}
	parts := strings.Split(strings.ToLower(row.Key), "+")
	key := parts[len(parts)-1]
	if strings.HasPrefix(key, "f") {
		if n, err := strconv.Atoi(key[1:]); err == nil && n >= 1 && n <= 12 {
			return fmt.Errorf("side %s: function keys are not bound", row.Name)
		}
	}
	return nil
}
