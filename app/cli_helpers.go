package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/actions"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/runs"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	otea "ofam.dev/ochazuke/tea"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/bindings"
	"ofam.dev/ochazuke/widgets/picker"
)

// BindingIssue is one problem reported by CheckBindings.
type BindingIssue struct {
	Severity string `json:"severity"`
	Mode     string `json:"mode,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Keys     string `json:"keys,omitempty"`
	Command  string `json:"command,omitempty"`
	Message  string `json:"message"`
}

// BindingCheckReport is the result of validating the effective keymap.
type BindingCheckReport struct {
	Total    int            `json:"total"`
	Errors   []BindingIssue `json:"errors"`
	Warnings []BindingIssue `json:"warnings"`
}

// CheckBindings validates the effective bindings (defaults + user overrides
// in configDir) using config.CheckBindings and bindings.AnalyzeBindings.
func CheckBindings(configDir string) (BindingCheckReport, error) {
	specs := CommandSpecs()
	modes := Modes()
	rows := DefaultBindings()
	rep := BindingCheckReport{
		Errors:   []BindingIssue{},
		Warnings: []BindingIssue{},
	}

	if configDir != "" {
		if defs, aerr := actions.LoadFile(filepath.Join(configDir, actions.ActionsFile)); aerr != nil {
			rep.Errors = append(rep.Errors, BindingIssue{
				Severity: "error",
				Message:  fmt.Sprintf("%s: %v", actions.ActionsFile, aerr),
			})
		} else if len(defs) > 0 {
			specs = append(specs, actions.ToCommandSpecs(defs)...)
		}
		b, err := os.ReadFile(filepath.Join(configDir, bindingsFileName))
		if err != nil && !os.IsNotExist(err) {
			return rep, err
		}
		if err == nil {
			user, err := config.LoadBindings(b)
			if err != nil {
				rep.Errors = append(rep.Errors, BindingIssue{
					Severity: "error",
					Message:  fmt.Sprintf("%s: %v", bindingsFileName, err),
				})
				return rep, nil
			}
			for _, d := range config.CheckBindings(modes, user, specs...) {
				issue := BindingIssue{
					Severity: string(d.Severity),
					Message:  fmt.Sprintf("user row %d: %s", d.Index+1, d.Message),
				}
				if d.Index >= 0 && d.Index < len(user) {
					issue.Mode = user[d.Index].Mode
					issue.Scope = user[d.Index].Scope
					issue.Keys = strings.Join(user[d.Index].Keys, " ")
					issue.Command = user[d.Index].Command
				}
				if d.Severity == config.SevError {
					rep.Errors = append(rep.Errors, issue)
				} else {
					rep.Warnings = append(rep.Warnings, issue)
				}
			}
			rows = config.MergeBindings(rows, user)
		}
	}

	rep.Total = len(rows)
	for _, d := range config.CheckBindings(modes, rows, specs...) {
		if d.Severity != config.SevError {
			continue
		}
		issue := BindingIssue{
			Severity: string(d.Severity),
			Message:  d.Message,
		}
		if d.Index >= 0 && d.Index < len(rows) {
			issue.Mode = rows[d.Index].Mode
			issue.Scope = rows[d.Index].Scope
			issue.Keys = strings.Join(rows[d.Index].Keys, " ")
			issue.Command = rows[d.Index].Command
		}
		rep.Errors = append(rep.Errors, issue)
	}

	for i, an := range bindings.AnalyzeBindings(modes, rows, nil, specs...) {
		var row core.Binding
		if i < len(rows) {
			row = rows[i]
		}
		for _, cf := range an.Conflicts {
			issue := BindingIssue{
				Severity: "warning",
				Mode:     row.Mode,
				Scope:    row.Scope,
				Keys:     strings.Join(row.Keys, " "),
				Command:  row.Command,
				Message:  cf.Message,
			}
			rep.Warnings = append(rep.Warnings, issue)
		}
	}
	return rep, nil
}

// ExtDiscoveryResult is the JSON-friendly summary of discovering one extension.
type ExtDiscoveryResult struct {
	Name         string       `json:"name"`
	Binary       string       `json:"binary,omitempty"`
	Status       string       `json:"status"`
	Version      string       `json:"version,omitempty"`
	Summary      string       `json:"summary,omitempty"`
	InstallPkg   string       `json:"installPkg,omitempty"`
	Category     string       `json:"category,omitempty"`
	Capabilities []string     `json:"capabilities,omitempty"`
	Commands     []string     `json:"commands,omitempty"`
	Changed      bool         `json:"changed"`
	Record       *data.Record `json:"record,omitempty"`
}

// DiscoverExtensions probes DefaultTools in dirs and PATH, upserting extension
// records in store, and returns results for tools that were found or already recorded.
func DiscoverExtensions(ctx context.Context, store *data.Store, dirs []string) ([]ExtDiscoveryResult, error) {
	return DiscoverExtensionsWithCatalog(ctx, store, dirs, false)
}

// DiscoverExtensionsWithCatalog probes DefaultTools (and the OFAM catalog when
// includeCatalog is true) in dirs and PATH, upserting extension records in store.
func DiscoverExtensionsWithCatalog(ctx context.Context, store *data.Store, dirs []string, includeCatalog bool) ([]ExtDiscoveryResult, error) {
	mgr := ext.New(ext.Options{IncludeCatalog: includeCatalog})
	results, err := mgr.Discover(ctx, dirs, store)
	out := make([]ExtDiscoveryResult, 0, len(results))
	for _, r := range results {
		if r.Binary == "" && r.Record == nil {
			continue
		}
		out = append(out, ExtDiscoveryResult{
			Name:         r.Name,
			Binary:       r.Binary,
			Status:       r.Status,
			Version:      r.Version,
			Summary:      r.Summary,
			InstallPkg:   r.InstallPkg,
			Category:     r.Category,
			Capabilities: r.Capabilities,
			Commands:     r.Commands,
			Changed:      r.Changed,
			Record:       r.Record,
		})
	}
	return out, err
}

// RunExtension executes an extension command through runs.Runner, waits for it
// to finish, and returns the completed run record.
func RunExtension(ctx context.Context, store *data.Store, extName string, args []string, actor core.Actor) (*data.Record, error) {
	runner := runs.New(store, runs.Options{})
	defer runner.Close()
	mgr := ext.New(ext.Options{Actor: actor})
	rec, err := mgr.RunCommand(ctx, extName, args, runner)
	if err != nil {
		return nil, err
	}
	if done := runner.Done(rec.ID); done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			_ = runner.Cancel(rec.ID)
			<-done
			return nil, ctx.Err()
		}
	}
	return store.Get(rec.Ref())
}

// ErrCLISelectionCancelled is returned when the user cancels the interactive CLI picker.
var ErrCLISelectionCancelled = errors.New("selection cancelled")

type cliPickerModel struct {
	pm        *picker.Model
	w, h      int
	pickedID  string
	cancelled bool
}

func (m *cliPickerModel) Init() tea.Cmd { return nil }

func (m *cliPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch ev := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = ev.Width, ev.Height
		if m.h > 6 {
			m.pm.PageSize = min(m.h-6, 15)
		}
	case tea.KeyPressMsg:
		k := tea.Key(ev)
		name := otea.KeyName(k)
		switch name {
		case "esc", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			if picked, _ := m.pm.HandleCommand(core.Command{ID: picker.CmdConfirm}); picked != nil {
				m.pickedID = picked.ID
				return m, tea.Quit
			}
		case "up", "ctrl+p", "ctrl+k":
			m.pm.HandleCommand(core.Command{ID: "motion.up"})
		case "down", "ctrl+n", "ctrl+j":
			m.pm.HandleCommand(core.Command{ID: "motion.down"})
		case "ctrl+d":
			m.pm.HandleCommand(core.Command{ID: "motion.half.down"})
		case "ctrl+u":
			m.pm.HandleCommand(core.Command{ID: "motion.half.up"})
		case "tab":
			m.pm.HandleCommand(core.Command{ID: picker.CmdModeCycle})
		case "backspace":
			m.pm.BackspaceQuery()
		default:
			if k.Text != "" {
				m.pm.AppendQuery(k.Text)
			}
		}
	}
	return m, nil
}

func (m *cliPickerModel) View() tea.View {
	w, h := m.w, m.h
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 18
	}
	node := m.pm.View("cli.picker")
	frame, _ := ui.Render(node, ui.DefaultDarkTheme(), "cli.picker", w, h)
	v := tea.NewView(frame.ANSI)
	v.AltScreen = true
	return v
}

// PickRecordCLI runs a lightweight Bubble Tea program wrapping Ochazuke's
// widgets/picker on stderr so interactive CLI commands can select a record
// without polluting stdout.
func PickRecordCLI(store *data.Store, kind string, candidates []data.Record, title string) (string, error) {
	schema, ok := store.Registry().Get(kind)
	if !ok {
		schema = data.Schema{Kind: kind, Title: kind, Plural: kind}
	}
	items := make([]picker.Item, len(candidates))
	for i, rec := range candidates {
		ref := rec.Ref()
		short := rec.ID
		if len(short) > 8 {
			short = short[:8]
		}
		items[i] = picker.Item{
			ID:       rec.ID,
			Title:    views.Title(schema, rec),
			Subtitle: short,
			Badges:   views.StatusBadges(schema, rec),
			Ref:      &ref,
			Scope:    kind,
			Keywords: []string{rec.ID},
		}
	}
	pm := picker.New(title, items)
	pm.Placeholder = "type to filter · enter selects · esc cancels"
	pm.PageSize = 12
	m := &cliPickerModel{pm: pm}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	if _, err := p.Run(); err != nil {
		return "", err
	}
	if m.cancelled || m.pickedID == "" {
		return "", ErrCLISelectionCancelled
	}
	return m.pickedID, nil
}
