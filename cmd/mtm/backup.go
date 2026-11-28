package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"ofam.dev/matomeru/internal/backup"
	"ofam.dev/oden"
)

const backupUsage = `usage: mtm backup [--out FILE] [--dir DIR] [--bin-dir DIR] [--only NAME ...] [--bins-only] [--json]
       mtm backup inspect <zip>
       mtm backup list [--dir DIR] [--json]`

// sqliteDriver is the database/sql driver the data package registers; backup
// uses it to snapshot a live database.
const sqliteDriver = "sqlite"

func mtmBackupCommand(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "out", Type: "string", Description: "The zip to write (default: ofam-backup-YYYYMMDD-HHMMSS.zip in --dir)"},
		{Name: "dir", Type: "string", Description: "Directory the zip is written to, and that list reads (default: the home directory)"},
		{Name: "bin-dir", Type: "string", Description: "Directory of the installed binaries (default: ~/bin)"},
		{Name: "only", Type: "string", Repeatable: true, Description: "Back up only this binary; repeat for several (default: mtm, o, mtm-*, o-*)"},
		{Name: "bins-only", Type: "bool", Description: "Skip the config, database, drafts and attachments"},
		{Name: "json", Type: "bool", Description: "Output JSON"},
	}
	return oden.Command{
		Name:       "backup",
		Summary:    "zip the installed binaries, config and data with a MANIFEST.md of versions and commits",
		Usage:      "[inspect <zip> | list] [--out FILE] [--dir DIR] [--bin-dir DIR] [--only NAME] [--bins-only] [--json]",
		Details:    "Run it before an install or update replaces anything. MANIFEST.md, at the root of the zip, lists the version, commit SHA, Go version, size and SHA-256 of every binary, every config and data file, and how to restore.",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			asJSON := p.Bool("json") || p.Format() == "json"
			pos := p.Positional()
			switch {
			case len(pos) == 0:
				return runBackupCreate(g, p, c.Stdout, asJSON)
			case pos[0] == "inspect" && len(pos) == 2:
				manifest, err := backup.InspectZip(pos[1])
				if err != nil {
					return err
				}
				_, err = io.WriteString(c.Stdout, manifest)
				return err
			case pos[0] == "list" && len(pos) == 1:
				return runBackupList(p.String("dir"), c.Stdout, asJSON)
			}
			return errors.New(backupUsage)
		},
	}
}

func runBackupCreate(g globalOpts, p oden.Parsed, w io.Writer, asJSON bool) error {
	opts := backup.Options{
		BinDir:    p.String("bin-dir"),
		ConfigDir: configDir(),
		OutPath:   p.String("out"),
		OutDir:    p.String("dir"),
		BinsOnly:  p.Bool("bins-only"),
		Only:      p.Strings("only"),
		DBDriver:  sqliteDriver,
	}
	if !opts.BinsOnly {
		db := g.db
		if db == "" {
			var err error
			if db, err = defaultDB(); err != nil {
				return err
			}
		}
		if db == ":memory:" || strings.HasPrefix(db, "file:") {
			return fmt.Errorf("mtm backup: %q is not a database file; pass --bins-only to back up the binaries alone", db)
		}
		opts.DBPath = db
	}
	rep, err := backup.Create(opts)
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(w, rep)
	}
	return printBackupReport(w, rep)
}

func printBackupReport(w io.Writer, rep backup.Report) error {
	tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "BINARY\tVERSION\tCOMMIT\tGO\tSIZE")
	for _, b := range rep.Binaries {
		commit := b.CommitSHA
		if len(commit) > 12 {
			commit = commit[:12]
		}
		if b.Dirty {
			commit += " (dirty)"
		}
		goVersion := b.GoVersion
		if goVersion == "" {
			goVersion = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", b.Name, b.Version, commit, goVersion, backup.HumanSize(b.Size))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, s := range rep.Skipped {
		fmt.Fprintf(w, "skipped  %s\n", s)
	}
	_, err := fmt.Fprintf(w, "\n%d binaries and %d config/data files backed up\n%s (%s)\nmtm backup inspect %s    # versions, commits, hashes and restore steps\n",
		len(rep.Binaries), len(rep.Files), rep.ZipPath, backup.HumanSize(rep.ZipSize), rep.ZipPath)
	return err
}

func runBackupList(dir string, w io.Writer, asJSON bool) error {
	list, err := backup.List(dir)
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(w, list)
	}
	if len(list) == 0 {
		_, err := fmt.Fprintln(w, "no backups: run `mtm backup` to make one")
		return err
	}
	tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "MODIFIED (UTC)\tSIZE\tPATH")
	for _, a := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", a.ModTime.Format("2006-01-02 15:04:05"), backup.HumanSize(a.Size), a.Path)
	}
	return tw.Flush()
}
