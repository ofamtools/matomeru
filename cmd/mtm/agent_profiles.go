package main

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	agentcfg "ofam.dev/matomeru/internal/config"
	"ofam.dev/oden"
)

var agentProfilesParams = []oden.Parameter{
	{Name: "role", Type: "string", Description: "Only profiles of this role"},
	{Name: "all", Type: "bool", Description: "Include disabled profiles"},
}

var agentSegmentsParams = []oden.Parameter{
	{Name: "kind", Type: "string", Description: "Only segments of this kind (type or tweak)"},
}

var agentComposeParams = []oden.Parameter{
	{Name: "segment", Type: "string", Description: "Prompt segment ID (default: the catalog default)"},
	{Name: "tag", Type: "string", Repeatable: true, Description: "Tweak or type ID layered on the segment, in order"},
	{Name: "prompt", Type: "string", Description: "Raw prompt (or the positional arguments)"},
}

func loadAgentCatalogs(c *oden.Ctx) agentcfg.AgentCatalogs {
	cats, err := agentcfg.LoadAgentCatalogs(configDir())
	if err != nil {
		fmt.Fprintf(c.Stderr, "warning: %v\n", err)
	}
	return cats
}

// mtmAgentProfilesCommand is `mtm agent profiles`.
func mtmAgentProfilesCommand() oden.Command {
	return oden.Command{
		Name:       "profiles",
		Aliases:    []string{"profile"},
		Summary:    "list standard agent profiles (built-ins + agent_profiles.json)",
		Usage:      "[ID] [--role ROLE] [--all] [--json]",
		Parameters: agentProfilesParams,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(agentProfilesParams)
			if err != nil {
				return err
			}
			cat := loadAgentCatalogs(c).Profiles
			list := cat.List(p.Bool("all"))
			if pos := p.Positional(); len(pos) == 1 {
				one, ok := cat.Get(pos[0])
				if !ok {
					return fmt.Errorf("unknown agent profile %q", pos[0])
				}
				list = []agentcfg.AgentProfile{one}
			}
			if role := p.String("role"); role != "" {
				list = cat.ByRole(role)
			}
			if p.Format() == "json" {
				return printJSON(c.Stdout, list)
			}
			tw := tabwriter.NewWriter(c.Stdout, 2, 8, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tROLE\tHARNESS\tMODEL\tREASONING\tSOURCE")
			for _, pr := range list {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", pr.ID, pr.Role, pr.Harness, pr.Model, pr.ReasoningLevel, profileSource(pr.BuiltIn, pr.ID == cat.Default))
			}
			return tw.Flush()
		},
	}
}

// mtmAgentSegmentsCommand is `mtm agent segments`.
func mtmAgentSegmentsCommand() oden.Command {
	return oden.Command{
		Name:       "segments",
		Aliases:    []string{"segment"},
		Summary:    "list prompt segment types and tweaks (built-ins + prompt_segments.json)",
		Usage:      "[--kind type|tweak] [--json]",
		Parameters: agentSegmentsParams,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(agentSegmentsParams)
			if err != nil {
				return err
			}
			cat := loadAgentCatalogs(c).Segments
			list := cat.List(p.String("kind"))
			if p.Format() == "json" {
				return printJSON(c.Stdout, list)
			}
			tw := tabwriter.NewWriter(c.Stdout, 2, 8, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tKIND\tPROFILE\tREASONING\tBUDGET\tGOAL")
			for _, s := range list {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Kind, s.ProfileID, s.ReasoningLevel, s.BudgetHint, s.Goal)
			}
			return tw.Flush()
		},
	}
}

// mtmAgentComposeCommand is `mtm agent compose`.
func mtmAgentComposeCommand() oden.Command {
	return oden.Command{
		Name:       "compose",
		Summary:    "resolve a prompt segment + tags over a prompt to profile, instructions and final prompt",
		Usage:      "--segment ID [--tag TWEAK]... [--prompt TEXT | TEXT...] [--json]",
		Parameters: agentComposeParams,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(agentComposeParams)
			if err != nil {
				return err
			}
			prompt := p.String("prompt")
			if prompt == "" {
				prompt = strings.Join(p.Positional(), " ")
			}
			d, err := loadAgentCatalogs(c).Compose(p.String("segment"), p.Strings("tag"), prompt)
			if p.Format() == "json" {
				return printJSON(c.Stdout, d)
			}
			printDispatch(c, d)
			if errors.Is(err, agentcfg.ErrUnknownSegment) {
				return err
			}
			return nil
		},
	}
}

func printDispatch(c *oden.Ctx, d agentcfg.ComposedDispatch) {
	fmt.Fprintf(c.Stdout, "segment:   %s %s\n", d.SegmentID, strings.Join(d.Tags, " "))
	fmt.Fprintf(c.Stdout, "profile:   %s (from %s)\n", d.Profile.ID, d.ProfileSource)
	fmt.Fprintf(c.Stdout, "harness:   %s  model: %s  reasoning: %s  budget: %s\n", d.Profile.Harness, d.Profile.Model, d.Profile.ReasoningLevel, d.BudgetHint)
	for _, w := range d.Warnings {
		fmt.Fprintf(c.Stderr, "warning: %s\n", w)
	}
	fmt.Fprintf(c.Stdout, "\n%s\n", d.Prompt)
}

func profileSource(builtIn, isDefault bool) string {
	src := "user"
	if builtIn {
		src = "built-in"
	}
	if isDefault {
		src += ", default"
	}
	return src
}
