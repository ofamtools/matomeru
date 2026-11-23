package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"ofam.dev/matomeru/harness"
	"ofam.dev/oden"
)

const oaiProbeTimeout = 6 * time.Second

func mtmHarnessCommand() oden.Command {
	return oden.Command{
		Name:     "harness",
		Aliases:  []string{"harnesses"},
		Summary:  "inspect available agent harnesses and launch commands",
		Category: "Configuration",
		Children: []oden.Command{
			{
				Name:    "list",
				Summary: "list available harnesses merged from o-ai and harnesses.json",
				Usage:   "[--json]",
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(nil)
					if err != nil {
						return err
					}
					if len(p.Positional()) != 0 {
						return errors.New("usage: mtm harness list [--json]")
					}
					cfgPath := filepath.Join(configDir(), "harnesses.json")
					cfg, err := harness.LoadFile(cfgPath)
					if err != nil {
						return err
					}
					installed := probeOAIHarnesses(c.Context)
					entries := harness.MergeInstalled(installed, cfg)
					if p.Format() == "json" {
						return printJSON(c.Stdout, entries)
					}
					tw := tabwriter.NewWriter(c.Stdout, 2, 8, 2, ' ', 0)
					fmt.Fprintln(tw, "ID\tLABEL\tSOURCE\tCOMMAND")
					for _, e := range entries {
						argv, _ := harness.BuildLaunchCommand(e, "", "")
						if len(e.MCPFlag) > 0 {
							argv = append(argv, e.MCPFlag...)
						}
						label := e.Label
						if label == "" {
							label = e.Name
						}
						fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.ID, label, e.Source, strings.Join(argv, " "))
					}
					return tw.Flush()
				},
			},
		},
	}
}

// probeOAIHarnesses queries o-ai for installed ai.harness inventory on a short
// best-effort timeout. Missing o-ai, non-zero exits, timeouts, or parse errors
// return nil so `mtm harness list` never fails when o-ai is unavailable.
func probeOAIHarnesses(ctx context.Context) []harness.InstalledHarness {
	bin, err := exec.LookPath("o-ai")
	if err != nil {
		return nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, oaiProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, bin, "query", "--kind", "ai.harness").Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	installed, err := harness.ParseOAIJSON(out)
	if err != nil {
		return nil
	}
	return installed
}
