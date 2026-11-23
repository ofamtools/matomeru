package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/runs"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/bindings"
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
	Capabilities []string     `json:"capabilities,omitempty"`
	Commands     []string     `json:"commands,omitempty"`
	Changed      bool         `json:"changed"`
	Record       *data.Record `json:"record,omitempty"`
}

// DiscoverExtensions probes DefaultTools in dirs and PATH, upserting extension
// records in store, and returns results for tools that were found or already recorded.
func DiscoverExtensions(ctx context.Context, store *data.Store, dirs []string) ([]ExtDiscoveryResult, error) {
	mgr := ext.New(ext.Options{})
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
