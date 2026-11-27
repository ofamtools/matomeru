package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"ofam.dev/matomeru/app"
	"ofam.dev/matomeru/data"
	"ofam.dev/oden"
)

func mtmRepoCommands(g globalOpts) []oden.Command {
	return []oden.Command{
		mtmRepoSyncSubcmd(g),
	}
}

func mtmRepoSyncSubcmd(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "base-dirs", Type: "bool", Default: "true", Description: "Import git repositories from configured base directories"},
		{Name: "o-proj", Type: "bool", Default: "true", Description: "Import repositories from o-proj list if installed"},
		{Name: "harvest", Type: "bool", Default: "true", Description: "Harvest repositories from existing task/lane repo and directory fields"},
		paramFormatJSON,
	}
	return oden.Command{
		Name:       "sync",
		Summary:    "sync tracked repositories from base directories, o-proj, and existing records",
		Usage:      "[--base-dirs=true|false] [--o-proj=true|false] [--harvest=true|false] [--format json|table]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			if len(p.Positional()) != 0 {
				return errors.New("usage: mtm repo sync [--base-dirs=true|false] [--o-proj=true|false] [--harvest=true|false] [--format json|table]")
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()

			var baseStore *data.BaseDirStore
			if root := strings.TrimSpace(store.FilesRoot()); root != "" {
				baseStore = data.NewBaseDirStore(filepath.Join(root, "basedirs.json"))
			}
			report, err := app.SyncRepos(
				c.Context,
				store,
				baseStore,
				p.Bool("base-dirs"),
				p.Bool("o-proj"),
				p.Bool("harvest"),
				cliActor,
			)
			if err != nil {
				return err
			}
			if recordFormat(p) == "table" {
				return printRepoSyncTable(c.Stdout, report)
			}
			return printJSON(c.Stdout, report)
		},
	}
}

func printRepoSyncTable(w io.Writer, report data.RepoSyncReport) error {
	fmt.Fprintf(w, "synced repos: %d created, %d updated, %d unchanged (%d total)\n",
		report.Created, report.Updated, report.Skipped, len(report.Repos))
	if len(report.Repos) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tBRANCH\tSOURCE\tPATH")
	for _, r := range report.Repos {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			r.ID,
			r.Text("name"),
			r.Text("defaultBranch"),
			r.Text("source"),
			r.Text("path"),
		)
	}
	return tw.Flush()
}
