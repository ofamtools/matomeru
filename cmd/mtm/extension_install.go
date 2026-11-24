package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"ofam.dev/matomeru/app"
	"ofam.dev/oden"
)

func mtmExtensionCatalogCommand(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "dir", Type: "string", Repeatable: true, Description: "Directory to search before PATH"},
		{Name: "json", Type: "bool", Description: "Output JSON"},
	}
	return oden.Command{
		Name:       "catalog",
		Summary:    "list the published OFAM tool catalog with installed status",
		Usage:      "[--dir DIR] [--json]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			if len(p.Positional()) != 0 {
				return errors.New("usage: mtm extension catalog [--dir DIR] [--json]")
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()
			items, err := app.CatalogExtensions(c.Context, store, p.Strings("dir"))
			if err != nil {
				return err
			}
			if p.Bool("json") || p.Format() == "json" {
				return printJSON(c.Stdout, items)
			}
			return printExtensionCatalogTable(c.Stdout, items)
		},
	}
}

func printExtensionCatalogTable(w io.Writer, items []app.ExtCatalogItem) error {
	tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCATEGORY\tSTATUS\tVERSION\tPACKAGE")
	for _, it := range items {
		ver := it.Version
		if ver == "" {
			ver = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", it.Name, it.Category, it.Status, ver, it.Package)
	}
	return tw.Flush()
}

func mtmExtensionInstallCommand(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "all", Type: "bool", Description: "Install all missing core OFAM tools"},
		{Name: "optional", Type: "bool", Description: "With --all, also install optional OFAM tools"},
		{Name: "goprivate", Type: "string", Default: "ofam.dev", Description: "GOPRIVATE value passed to go install"},
		{Name: "dir", Type: "string", Repeatable: true, Description: "Directory to search before PATH when rediscovering"},
		paramFormatJSON,
		{Name: "json", Type: "bool", Description: "Output JSON"},
	}
	return oden.Command{
		Name:       "install",
		Summary:    "download and install OFAM extensions via go install",
		Usage:      "[<name> | --all] [--optional] [--goprivate VAL] [--format json|table]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			names := p.Positional()
			all := p.Bool("all")
			if all && len(names) > 0 {
				return errors.New("usage: mtm extension install [<name> | --all] [--optional] [--goprivate VAL] [--format json|table]")
			}
			if !all && len(names) == 0 {
				return errors.New("usage: mtm extension install [<name> | --all] [--optional] [--goprivate VAL] [--format json|table]")
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()

			results, installErr := app.InstallExtensions(c.Context, store, app.ExtInstallOptions{
				Names:     names,
				All:       all,
				Optional:  p.Bool("optional"),
				GoPrivate: p.String("goprivate"),
				Dirs:      p.Strings("dir"),
				Actor:     cliActor,
			})
			format := recordFormat(p)
			if format == "table" {
				if werr := printExtensionInstallTable(c.Stdout, results); werr != nil {
					return werr
				}
			} else {
				if results == nil {
					results = []app.ExtInstallResult{}
				}
				if werr := printJSON(c.Stdout, results); werr != nil {
					return werr
				}
			}
			return installErr
		},
	}
}

func printExtensionInstallTable(w io.Writer, results []app.ExtInstallResult) error {
	if len(results) == 0 {
		_, err := fmt.Fprintln(w, "all requested OFAM extensions are already installed")
		return err
	}
	tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tEXIT\tVERSION\tPACKAGE")
	for _, r := range results {
		ver := r.Version
		if ver == "" {
			ver = "-"
		}
		if r.Error != "" && ver == "-" {
			ver = strings.ReplaceAll(r.Error, "\n", " ")
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", r.Name, r.Status, r.ExitCode, ver, r.Package)
	}
	return tw.Flush()
}
