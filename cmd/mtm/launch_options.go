package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/harness"
)

// The launch options a form offers (model, effort, fast, permission-mode)
// are finite and depend on the harness and the model. They come from a
// snapshot mtm keeps beside its database, so opening or typing in a form
// never runs a process. The snapshot is what o-ai's model.list reported at
// the last check; a harness it has no rows for offers its registry values.

const (
	launchOptionsFile       = "launch-options.json"
	launchOptionsCheckJob   = "launch-options-check"
	launchOptionsTimeout    = 2 * time.Minute
	launchOptionsFirstDelay = time.Minute
	settingLaunchPickers    = "launch.option-pickers"
	settingLaunchCheckEvery = "launch.options-check-interval"
	launchModelsSource      = "harness.models"
	launchChoicesSource     = "harness.choices"
)

var launchPickersSetting = ochazuke.Setting{Name: settingLaunchPickers, Type: ochazuke.SettingBoolean, Default: true,
	Description: "Launch forms offer model, effort, fast and permission-mode as pickers of the values the harness and model take; off makes them free text"}

var launchCheckEverySetting = ochazuke.Setting{Name: settingLaunchCheckEvery, Type: ochazuke.SettingDuration, Default: 24 * time.Hour,
	Description: "How often mtm asks o-ai, in the background, which models and settings each harness takes; 0 never (harness.refresh still runs on demand). A change applies at the next check or start"}

// launchModel is one model a harness reported, with the values each setting
// takes on it: a setting named with no values is one the model does not
// support.
type launchModel struct {
	ID       string              `json:"id"`
	Name     string              `json:"name,omitempty"`
	Settings map[string][]string `json:"settings,omitempty"`
}

// launchOptions is the snapshot: Checked is the last check that succeeded,
// Tried the last one made.
type launchOptions struct {
	Checked   time.Time                `json:"checked"`
	Tried     time.Time                `json:"tried"`
	Harnesses map[string][]launchModel `json:"harnesses,omitempty"`
}

// model is the snapshot's row for a harness's model.
func (o *launchOptions) model(name, id string) (launchModel, bool) {
	if o == nil || id == "" {
		return launchModel{}, false
	}
	i := slices.IndexFunc(o.Harnesses[name], func(m launchModel) bool { return m.ID == id })
	if i < 0 {
		return launchModel{}, false
	}
	return o.Harnesses[name][i], true
}

// parseLaunchOptions reads o-ai's model rows (JSON Lines of {data: ...}), up
// to the first line that is not one.
func parseLaunchOptions(out []byte) map[string][]launchModel {
	type row struct {
		Data struct {
			Harness  string `json:"harness"`
			CLISlug  string `json:"cliSlug"`
			Slug     string `json:"slug"`
			Name     string `json:"name"`
			Thinking []struct {
				Slug string `json:"slug"`
			} `json:"thinking"`
		} `json:"data"`
	}
	found := map[string][]launchModel{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var r row
		if dec.Decode(&r) != nil {
			return found
		}
		id := cmp.Or(r.Data.CLISlug, r.Data.Slug)
		if r.Data.Harness == "" || id == "" || slices.ContainsFunc(found[r.Data.Harness], func(m launchModel) bool { return m.ID == id }) {
			continue
		}
		levels := make([]string, 0, len(r.Data.Thinking))
		for _, t := range r.Data.Thinking {
			levels = append(levels, t.Slug)
		}
		found[r.Data.Harness] = append(found[r.Data.Harness], launchModel{ID: id, Name: r.Data.Name, Settings: map[string][]string{harness.SettingEffort: levels}})
	}
}

func (a *App) launchOptionsPath() string { return filepath.Join(a.dataDir(), launchOptionsFile) }

// loadLaunchOptions reads the snapshot; none, or an unreadable one, is the
// registry's values alone.
func (a *App) loadLaunchOptions() {
	var o launchOptions
	data, err := os.ReadFile(a.launchOptionsPath())
	if err != nil || json.Unmarshal(data, &o) != nil {
		o = launchOptions{}
	}
	a.setLaunchOptions(&o)
}

// setLaunchOptions makes o the snapshot, and its models ones a launch takes.
func (a *App) setLaunchOptions(o *launchOptions) {
	a.launchOpts = o
	a.shareLaunchModels()
}

// shareLaunchModels tells the harness registry the snapshot's models.
func (a *App) shareLaunchModels() {
	listed := map[string][]string{}
	if a.launchOpts != nil {
		for name, models := range a.launchOpts.Harnesses {
			for _, m := range models {
				listed[name] = append(listed[name], m.ID)
			}
		}
	}
	a.harnesses.SetListedModels(listed)
}

// writeLaunchOptions replaces the snapshot file in one step.
func writeLaunchOptions(path string, o *launchOptions) error {
	data, err := json.Marshal(o)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// launchValues are the values a form offers for a setting of h with model
// selected: the model's own when the snapshot says, limited to what the
// harness's settings row can apply; else the registry's.
func (a *App) launchValues(h harness.Harness, name, model string) []string {
	registry := h.Choices(name)
	m, ok := a.launchOpts.model(h.Name, model)
	values, reported := m.Settings[name]
	if !ok || !reported {
		return registry
	}
	if len(registry) > 0 {
		return slices.DeleteFunc(slices.Clone(values), func(v string) bool { return !slices.Contains(registry, v) })
	}
	if s := h.Settings[name]; len(s.Flags) == 0 && s.Command == "" {
		return nil
	}
	return values
}

// modelIDs are the models a form offers for h: the registry's, then the
// snapshot's. A harness with no model flag takes none.
func (a *App) modelIDs(h harness.Harness) []string {
	if h.ModelFlag == "" {
		return nil
	}
	out := slices.Clone(h.Models)
	if a.launchOpts != nil {
		for _, m := range a.launchOpts.Harnesses[h.Name] {
			if !slices.Contains(out, m.ID) {
				out = append(out, m.ID)
			}
		}
	}
	return out
}

// choiceHarnesses are the harnesses a choices source covers: the named one,
// else every harness.
func (a *App) choiceHarnesses(c ochazuke.Ctx) ([]harness.Harness, error) {
	name := ochazuke.StringArg(c.Args, "harness", "")
	if name == "" {
		return a.harnesses.List(), nil
	}
	h, err := a.harnesses.Lookup(name)
	if err != nil {
		return nil, err
	}
	return []harness.Harness{h}, nil
}

// listLaunchChoices is harness.choices: the values a setting takes for the
// selected harness and model, each once. A harness mtm does not know has none.
func (a *App) listLaunchChoices(c ochazuke.Ctx) ochazuke.Result {
	harnesses, _ := a.choiceHarnesses(c)
	var out []ochazuke.Choice
	seen := map[string]bool{}
	for _, h := range harnesses {
		for _, v := range a.launchValues(h, ochazuke.StringArg(c.Args, "setting", ""), ochazuke.StringArg(c.Args, "model", "")) {
			if !seen[v] {
				seen[v] = true
				out = append(out, ochazuke.Choice{ID: v, Label: v})
			}
		}
	}
	return ochazuke.Done(out)
}

// launchOptionRows reads o-ai's model rows through its mounted model.list.
func (a *App) launchOptionRows(fresh bool) func(context.Context) ([]byte, error) {
	if a.launchRows != nil {
		return a.launchRows
	}
	x, noun := a.mountedNoun("ai.model")
	if x == nil {
		return func(context.Context) ([]byte, error) { return nil, nil } // no rows: the check keeps the snapshot
	}
	words := []string{noun, "list", "--format", "jsonl"}
	if fresh {
		words = append(words, "--fresh")
	}
	bin, env, procs := x.bin, a.extEnv(x.name), a.procs
	return func(ctx context.Context) ([]byte, error) { return procs.exec(ctx, false, bin, env, words...) }
}

// launchOptionsChecked is what a check found: the models per harness, or
// why the last snapshot stays.
type launchOptionsChecked struct {
	Models map[string]int `json:"models,omitempty"`
	Kept   string         `json:"kept,omitempty"`
	next   *launchOptions
}

// checkLaunchOptions asks o-ai off the loop and replaces the snapshot. A
// check that finds nothing keeps the last snapshot and notes when it tried.
func (a *App) checkLaunchOptions(fresh bool) ochazuke.Result {
	rows, path, now := a.launchOptionRows(fresh), a.launchOptionsPath(), time.Now()
	tried := launchOptions{}
	if a.launchOpts != nil {
		tried = *a.launchOpts
	}
	tried.Tried = now
	a.launchOpts = &tried
	return ochazuke.Pending(func(ctx context.Context) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, launchOptionsTimeout)
		defer cancel()
		out, err := rows(ctx)
		found := parseLaunchOptions(out)
		if err != nil || len(found) == 0 {
			_ = writeLaunchOptions(path, &tried)
			kept := "o-ai reported no models"
			if err != nil {
				kept = err.Error()
			}
			return launchOptionsChecked{Kept: kept}, nil
		}
		next := &launchOptions{Checked: now, Tried: now, Harnesses: found}
		if err := writeLaunchOptions(path, next); err != nil {
			return launchOptionsChecked{Kept: err.Error()}, nil
		}
		models := map[string]int{}
		for name, rows := range found {
			models[name] = len(rows)
		}
		return launchOptionsChecked{Models: models, next: next}, nil
	}).Replacing(launchOptionsCheckJob).Then(func(_ ochazuke.Ctx, v any, _ error) {
		if checked, ok := v.(launchOptionsChecked); ok && checked.next != nil {
			a.setLaunchOptions(checked.next)
		}
	})
}

// checkOptionsVerb is harness.refresh.
func (a *App) checkOptionsVerb(c ochazuke.Ctx) ochazuke.Result {
	fresh, _ := c.Args["fresh"].(bool)
	return a.checkLaunchOptions(fresh)
}

// launchOptionsCheckMsg is the spot check's timer.
type launchOptionsCheckMsg struct{}

// launchCheckWait is how long after now the next spot check is due.
func launchCheckWait(o *launchOptions, now time.Time, every time.Duration) time.Duration {
	if o == nil || o.Tried.IsZero() {
		return launchOptionsFirstDelay
	}
	return max(o.Tried.Add(every).Sub(now), launchOptionsFirstDelay)
}

// startLaunchOptionChecks applies the picker setting the session opened
// with and arms the spot check.
func (a *App) startLaunchOptionChecks() tea.Cmd {
	a.applyLaunchPickers()
	return a.armLaunchOptionCheck()
}

// armLaunchOptionCheck sets the timer for the next spot check.
func (a *App) armLaunchOptionCheck() tea.Cmd {
	every := a.r.Settings().Duration(settingLaunchCheckEvery)
	if a.cli || every <= 0 {
		return nil
	}
	return tea.Tick(launchCheckWait(a.launchOpts, time.Now(), every), func(time.Time) tea.Msg { return launchOptionsCheckMsg{} })
}

// launchOptionsUpdate runs a due spot check, silently, and arms the next.
func (a *App) launchOptionsUpdate(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(launchOptionsCheckMsg); !ok {
		return nil
	}
	every := a.r.Settings().Duration(settingLaunchCheckEvery)
	if every <= 0 {
		return nil
	}
	ctx := ochazuke.Ctx{Runtime: a.r, Context: a.r.Context(), Instance: a.r.Instances["app"]}
	if _, err := a.r.Launch("app", "harness.refresh", a.checkLaunchOptions(false), ctx); err != nil {
		return a.armLaunchOptionCheck()
	}
	return tea.Batch(a.r.PendingCommands(), a.armLaunchOptionCheck())
}

// launchModelParam is a launch's model: a picker of the selected harness's
// models. Any model is still taken from the CLI, MCP and saved rows.
func launchModelParam(description string) ochazuke.Parameter {
	return ochazuke.Parameter{Type: "string", Source: launchModelsSource, SourceArgs: map[string]string{"harness": "harness"}, ClearStale: true, Description: description}
}

// launchSettingParam is a launch setting: a picker of the values it takes
// for the selected harness and model.
func launchSettingParam(name, description string) ochazuke.Parameter {
	return ochazuke.Parameter{Type: "string", Source: launchChoicesSource, SourceFixed: map[string]any{"setting": name},
		SourceArgs: map[string]string{"harness": "harness", "model": "model"}, ClearStale: true, Description: description}
}

// applyLaunchPickers makes every parameter whose choices follow the harness
// a picker, or free text with launch.option-pickers off, so a new form over
// them follows.
func (a *App) applyLaunchPickers() {
	on := a.r.Settings().Bool(settingLaunchPickers)
	apply := func(p *ochazuke.Parameter) bool {
		if _, follows := p.SourceArgs["harness"]; !follows {
			return false
		}
		p.Free, p.ClearStale = !on, on
		return true
	}
	for _, op := range a.r.Operations.List() {
		params, changed := maps.Clone(op.Parameters), false
		for name, p := range params {
			if apply(&p) {
				params[name], changed = p, true
			}
		}
		if changed {
			next := *op
			next.Parameters = params
			a.r.Operations.Set(next)
		}
	}
	for _, n := range a.r.Nouns {
		for i := range n.Props {
			apply(&n.Props[i].Parameter)
		}
	}
}

// defineLaunchOptions describes the harness verbs the launch forms read,
// reads the snapshot and follows the picker setting.
func (a *App) defineLaunchOptions() {
	a.parameters("harness.choices", map[string]ochazuke.Parameter{
		"setting": {Type: "string", Required: true, Enum: harness.LaunchSettingNames, Description: "The setting whose values to list"},
		"harness": {Type: "string", Description: "Only this harness's values; empty: every harness"},
		"model":   {Type: "string", Description: "The selected model, for values that depend on it"},
	})
	a.parameters("harness.refresh", map[string]ochazuke.Parameter{"fresh": {Type: "boolean", Description: "Have o-ai collect now rather than answer from its cache"}})
	a.loadLaunchOptions()
	a.r.Settings().Observe(settingLaunchPickers, func(string, any) { a.applyLaunchPickers() })
}

// withLaunchOptionsUpdate adds the spot check's update to cmd, the update
// loop's periodic work.
func (a *App) withLaunchOptionsUpdate(msg tea.Msg, cmd tea.Cmd) tea.Cmd {
	return tea.Batch(cmd, a.launchOptionsUpdate(msg))
}
