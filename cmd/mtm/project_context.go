package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
	"ofam.dev/oden"
)

var cliContextKinds = []string{"url", "pr", "file", "doc", "gdoc", "sheet"}

type contextAddOfficialOutput struct {
	ProjectID    string       `json:"projectId"`
	Context      *data.Record `json:"context"`
	Deduplicated bool         `json:"deduplicated,omitempty"`
}

type contextProjectOutput struct {
	ProjectID string                 `json:"projectId"`
	Official  []data.Record          `json:"official"`
	All       []data.Record          `json:"all,omitempty"`
	Worktrees []data.ProjectWorktree `json:"worktrees"`
}

func mtmContextProjectCommands(g globalOpts) []oden.Command {
	return []oden.Command{
		mtmContextOfficialSubcmd(g),
		mtmContextPinSubcmd(g, true),
		mtmContextPinSubcmd(g, false),
		mtmContextAddOfficialSubcmd(g),
		mtmContextProjectSubcmd(g),
	}
}

func resolveCLIProjectID(ctx context.Context, store *data.Store, explicit string, stdin io.Reader, use string) (string, error) {
	if raw := strings.TrimSpace(explicit); raw != "" {
		id, err := resolveRecordID(ctx, store, data.KindTask, raw, stdin, use)
		if err != nil {
			return "", err
		}
		return store.RootProjectID(id), nil
	}
	if envTask := strings.TrimSpace(os.Getenv(mcpui.EnvTaskID)); envTask != "" {
		return store.RootProjectID(envTask), nil
	}
	if envPane := strings.TrimSpace(os.Getenv(mcpui.EnvPaneID)); envPane != "" {
		if pane, err := store.Get(core.Ref{Kind: data.KindPane, ID: envPane}); err == nil && pane != nil {
			if tRef, ok := pane.RefField("task"); ok && tRef.Kind == data.KindTask && strings.TrimSpace(tRef.ID) != "" {
				return store.RootProjectID(tRef.ID), nil
			}
		}
	}
	return "", fmt.Errorf("project is required (pass --project ID or set %s / %s)\n%s", mcpui.EnvTaskID, mcpui.EnvPaneID, use)
}

func mtmContextOfficialSubcmd(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "off", Type: "bool", Description: "Demote the context item from official project context"},
		{Name: "notes", Type: "string", Description: "Why this context item matters to agents and humans"},
	}
	return oden.Command{
		Name:       "official",
		Summary:    "promote or demote a context item as official curated project context",
		Usage:      "<id> [--off] [--notes TEXT] [--json]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			const use = "usage: mtm context official <id> [--off] [--notes TEXT] [--json]"
			pos := p.Positional()
			if len(pos) > 1 {
				return errors.New(use)
			}
			query := ""
			if len(pos) == 1 {
				query = pos[0]
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()

			id, err := resolveRecordID(c.Context, store, data.KindContext, query, c.Stdin, use)
			if err != nil {
				return err
			}
			var notesPtr *string
			if p.Has("notes") {
				n := p.String("notes")
				notesPtr = &n
			}
			rec, err := store.SetContextOfficial(core.Ref{Kind: data.KindContext, ID: id}, !p.Bool("off"), nil, notesPtr, cliActor)
			if err != nil {
				return err
			}
			if p.Format() == "json" {
				return printJSON(c.Stdout, rec)
			}
			_, werr := fmt.Fprintf(c.Stdout, "%s  %s  official=%v  pinned=%v\n",
				rec.ID, rec.Text("title"), rec.Bool("official"), rec.Bool("pinned"))
			return werr
		},
	}
}

func mtmContextPinSubcmd(g globalOpts, pinned bool) oden.Command {
	name := "pin"
	summary := "pin a context item for quick access on the Project Page"
	if !pinned {
		name = "unpin"
		summary = "unpin a context item from the Project Page"
	}
	usage := fmt.Sprintf("<id> [--json]")
	return oden.Command{
		Name:    name,
		Summary: summary,
		Usage:   usage,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(nil)
			if err != nil {
				return err
			}
			use := fmt.Sprintf("usage: mtm context %s <id> [--json]", name)
			pos := p.Positional()
			if len(pos) > 1 {
				return errors.New(use)
			}
			query := ""
			if len(pos) == 1 {
				query = pos[0]
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()

			id, err := resolveRecordID(c.Context, store, data.KindContext, query, c.Stdin, use)
			if err != nil {
				return err
			}
			rec, _, err := store.Patch(data.PatchInput{
				Ref:     core.Ref{Kind: data.KindContext, ID: id},
				Fields:  map[string]any{"pinned": pinned},
				Summary: fmt.Sprintf("%s context:%s", name, id),
			}, cliActor)
			if err != nil {
				return err
			}
			if p.Format() == "json" {
				return printJSON(c.Stdout, rec)
			}
			_, werr := fmt.Fprintf(c.Stdout, "%s  %s  pinned=%v\n", rec.ID, rec.Text("title"), rec.Bool("pinned"))
			return werr
		},
	}
}

func mtmContextAddOfficialSubcmd(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "project", Type: "string", Description: "Project or task ID/title (defaults to $MTM_TASK_ID or $MTM_PANE_ID's task)"},
		{Name: "title", Type: "string", Description: "Display title (derived from value when omitted)"},
		{Name: "kind", Type: "string", Enum: cliContextKinds, Description: "Context kind (url, pr, file, doc, gdoc, sheet)"},
		{Name: "notes", Type: "string", Description: "Why this context item matters to agents and humans"},
	}
	return oden.Command{
		Name:       "add-official",
		Summary:    "add or promote an official curated context item on a project",
		Usage:      "<value> --project <id> [--title TITLE] [--kind KIND] [--notes TEXT] [--json]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			const use = "usage: mtm context add-official <value> --project <id> [--title TITLE] [--kind KIND] [--notes TEXT] [--json]"
			pos := p.Positional()
			if len(pos) != 1 || strings.TrimSpace(pos[0]) == "" {
				return errors.New(use)
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()

			projectID, err := resolveCLIProjectID(c.Context, store, p.String("project"), c.Stdin, use)
			if err != nil {
				return err
			}
			rec, deduped, err := store.AddOfficialProjectContext(data.OfficialContextInput{
				ProjectID:   projectID,
				Title:       p.String("title"),
				Value:       pos[0],
				ContextKind: p.String("kind"),
				Notes:       p.String("notes"),
				Pinned:      true,
			}, cliActor)
			if err != nil {
				return err
			}
			if p.Format() == "json" {
				return printJSON(c.Stdout, contextAddOfficialOutput{
					ProjectID:    projectID,
					Context:      &rec,
					Deduplicated: deduped,
				})
			}
			suffix := ""
			if deduped {
				suffix = " (deduplicated)"
			}
			_, werr := fmt.Fprintf(c.Stdout, "%s  [%s] %s  official=%v  pinned=%v%s\n",
				rec.ID, rec.Text("contextKind"), rec.Text("title"), rec.Bool("official"), rec.Bool("pinned"), suffix)
			return werr
		},
	}
}

func mtmContextProjectSubcmd(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "project", Type: "string", Description: "Project or task ID/title (defaults to $MTM_TASK_ID or $MTM_PANE_ID's task)"},
		{Name: "all", Type: "bool", Description: "Show all project-scoped context items instead of official items only"},
	}
	return oden.Command{
		Name:       "project",
		Summary:    "list a project's official curated context items and worktrees",
		Usage:      "[--project <id>] [--all] [--json]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			const use = "usage: mtm context project [--project <id>] [--all] [--json]"
			if len(p.Positional()) != 0 {
				return errors.New(use)
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()

			projectID, err := resolveCLIProjectID(c.Context, store, p.String("project"), c.Stdin, use)
			if err != nil {
				return err
			}
			official, err := store.OfficialProjectContext(projectID)
			if err != nil {
				return err
			}
			if official == nil {
				official = []data.Record{}
			}
			worktrees, err := store.ProjectWorktrees(projectID)
			if err != nil {
				return err
			}
			if worktrees == nil {
				worktrees = []data.ProjectWorktree{}
			}
			out := contextProjectOutput{
				ProjectID: projectID,
				Official:  official,
				Worktrees: worktrees,
			}
			showAll := p.Bool("all")
			if showAll {
				all, err := store.ProjectContextAll(projectID)
				if err != nil {
					return err
				}
				if all == nil {
					all = []data.Record{}
				}
				out.All = all
			}
			if p.Format() == "json" {
				return printJSON(c.Stdout, out)
			}
			return printProjectContextTable(c.Stdout, out, showAll)
		},
	}
}

func printProjectContextTable(w io.Writer, out contextProjectOutput, showAll bool) error {
	rows := out.Official
	if showAll {
		rows = out.All
	}
	if len(rows) == 0 {
		if showAll {
			fmt.Fprintf(w, "no context items found for project %s\n", out.ProjectID)
		} else {
			fmt.Fprintf(w, "no official context items found for project %s\n", out.ProjectID)
		}
	} else {
		tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tKIND\tOFFICIAL\tPINNED\tTITLE\tVALUE")
		for _, rec := range rows {
			fmt.Fprintf(tw, "%s\t%s\t%v\t%v\t%s\t%s\n",
				rec.ID,
				rec.Text("contextKind"),
				rec.Bool("official"),
				rec.Bool("pinned"),
				strings.ReplaceAll(rec.Text("title"), "\n", " "),
				strings.ReplaceAll(rec.Text("value"), "\n", " "),
			)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if len(out.Worktrees) > 0 {
		fmt.Fprintf(w, "\nWorktrees (%d):\n", len(out.Worktrees))
		for _, wt := range out.Worktrees {
			extra := ""
			if wt.Branch != "" {
				extra += " [" + wt.Branch + "]"
			}
			if wt.Repo != "" {
				extra += " (" + wt.Repo + ")"
			}
			fmt.Fprintf(w, "  - %s%s (%s)\n", wt.Path, extra, wt.Source)
		}
	}
	return nil
}
