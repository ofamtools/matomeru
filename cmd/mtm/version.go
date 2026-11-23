package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/oden"
)

type versionDaemonInfo struct {
	Running bool   `json:"running"`
	PID     int    `json:"pid,omitempty"`
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Dirty   bool   `json:"dirty,omitempty"`
	BuiltAt string `json:"builtAt,omitempty"`
	Uptime  string `json:"uptime,omitempty"`
	Socket  string `json:"socket,omitempty"`
	Match   bool   `json:"match"`
}

type versionOutput struct {
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Commit          string            `json:"commit,omitempty"`
	ShortSHA        string            `json:"shortSha,omitempty"`
	Dirty           bool              `json:"dirty,omitempty"`
	BuiltAt         string            `json:"builtAt,omitempty"`
	GoVersion       string            `json:"goVersion"`
	OchazukeVersion string            `json:"ochazukeVersion,omitempty"`
	OdenVersion     string            `json:"odenVersion,omitempty"`
	Binary          string            `json:"binary,omitempty"`
	Daemon          versionDaemonInfo `json:"daemon"`
}

// mtmVersionCommand keeps `--version` and `-v` compact and machine-friendly
// ("mtm <version>") for scripts and agents, while `mtm version` prints full
// build metadata (commit SHA, dirty flag, build time, binary path) alongside
// the running daemon's version/commit and mismatch status.
func mtmVersionCommand(flagMode bool) oden.Command {
	params := []oden.Parameter{
		{Name: "socket", Type: "string", Description: "Unix domain socket path for live daemon comparison"},
	}
	return oden.Command{
		Name:       "version",
		Aliases:    []string{"--version", "-v"},
		Summary:    "print binary build version, commit SHA, and running daemon comparison",
		Usage:      "[--socket PATH] [--json]",
		Category:   "Info",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			if len(p.Positional()) != 0 {
				return errors.New("usage: mtm version [--socket PATH] [--json]")
			}
			bi := daemon.CurrentBuildInfo()
			// Flag invocations (--version / -v) stay compact one-liners for
			// scripts, extensions, and agents unless --json was requested.
			if flagMode && p.Format() != "json" {
				_, werr := fmt.Fprintf(c.Stdout, "mtm %s\n", bi.Version)
				return werr
			}

			sock := daemon.ResolveSocketPath(p.String("socket"))
			ctx, cancel := context.WithTimeout(c.Context, 500*time.Millisecond)
			st, derr := daemon.NewClient(sock).Status(ctx)
			cancel()

			dInfo := versionDaemonInfo{Socket: sock}
			if derr == nil && st != nil {
				dInfo.Running = true
				dInfo.PID = st.PID
				dInfo.Version = st.Version
				dInfo.Commit = st.Commit
				dInfo.Dirty = st.Dirty
				dInfo.BuiltAt = st.BuiltAt
				dInfo.Uptime = st.Uptime
				dInfo.Match = bi.SameBuild(st)
			}

			out := versionOutput{
				Name:            "mtm",
				Version:         bi.Version,
				Commit:          bi.Commit,
				ShortSHA:        bi.ShortSHA,
				Dirty:           bi.Dirty,
				BuiltAt:         bi.BuiltAt,
				GoVersion:       bi.GoVersion,
				OchazukeVersion: bi.OchazukeVersion,
				OdenVersion:     bi.OdenVersion,
				Binary:          bi.Binary,
				Daemon:          dInfo,
			}
			if p.Format() == "json" {
				return printJSON(c.Stdout, out)
			}

			commitLabel := bi.ShortSHA
			if commitLabel == "" {
				commitLabel = "unknown"
			} else if bi.Commit != "" && bi.Commit != bi.ShortSHA {
				commitLabel = bi.ShortSHA + " (" + bi.Commit + ")"
			}
			if bi.Dirty {
				commitLabel += " [dirty]"
			}
			fmt.Fprintf(c.Stdout, "mtm %s\n", bi.Version)
			fmt.Fprintf(c.Stdout, "commit:    %s\n", commitLabel)
			if bi.OchazukeVersion != "" {
				fmt.Fprintf(c.Stdout, "ochazuke:  %s\n", bi.OchazukeVersion)
			}
			if bi.OdenVersion != "" {
				fmt.Fprintf(c.Stdout, "oden:      %s\n", bi.OdenVersion)
			}
			if bi.BuiltAt != "" {
				fmt.Fprintf(c.Stdout, "built:     %s\n", bi.BuiltAt)
			}
			fmt.Fprintf(c.Stdout, "go:        %s\n", bi.GoVersion)
			if bi.Binary != "" {
				fmt.Fprintf(c.Stdout, "binary:    %s\n", bi.Binary)
			}
			if !dInfo.Running {
				fmt.Fprintf(c.Stdout, "daemon:    not running (%s)\n", sock)
				return nil
			}
			dVer := dInfo.Version
			if dVer == "" {
				dVer = "unknown (pre-v1.2.1)"
			}
			dCommit := dInfo.Commit
			if len(dCommit) >= 7 {
				dCommit = dCommit[:7]
			}
			if dCommit == "" {
				dCommit = "unknown"
			}
			if dInfo.Dirty {
				dCommit += " [dirty]"
			}
			fmt.Fprintf(c.Stdout, "daemon:    running pid=%d version=%s commit=%s uptime=%s\n", dInfo.PID, dVer, dCommit, dInfo.Uptime)
			if dInfo.Match {
				fmt.Fprintf(c.Stdout, "status:    MATCH (running daemon matches this binary)\n")
			} else {
				hint := "mtm daemon upgrade"
				if dInfo.Version == "" {
					hint = "mtm daemon stop  # old daemon predates live upgrade; restart once"
				}
				fmt.Fprintf(c.Stdout, "status:    MISMATCH (running daemon differs from this binary; run: %s)\n", hint)
			}
			return nil
		},
	}
}
