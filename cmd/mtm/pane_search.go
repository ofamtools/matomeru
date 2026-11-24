package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/panesearch"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/oden"
)

func mtmPaneSearchCommand(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "source", Type: "string", Default: "all", Enum: []string{"all", "scrollback", "chat", "records"}, Description: "Source filter (all, scrollback, chat, records)"},
		{Name: "mode", Type: "string", Default: "substring", Enum: []string{"substring", "fuzzy", "regex"}, Description: "Match mode (substring, fuzzy, regex)"},
		{Name: "archived", Type: "bool", Description: "Include archived panes"},
		{Name: "no-transcripts", Type: "bool", Description: "Skip external session transcript files on disk"},
		{Name: "limit", Type: "int", Default: "0", Description: "Maximum number of panes to return (0 = unlimited)"},
		{Name: "socket", Type: "string", Description: "Override daemon Unix socket path for live scrollback/telemetry"},
		paramFormatJSON,
	}
	return oden.Command{
		Name:       "search",
		Summary:    "search across panes: live scrollback, chat turns, SQLite records, and transcripts",
		Usage:      "[<query>] [--source all|scrollback|chat|records] [--mode substring|fuzzy|regex] [--archived] [--no-transcripts] [--limit N] [--json]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			query := strings.Join(p.Positional(), " ")

			src, ok := panesearch.NormalizeSource(p.String("source"))
			if !ok {
				return fmt.Errorf("--source %s is not all, scrollback, chat or records", p.String("source"))
			}
			mode := traits.MatchMode(p.String("mode"))
			switch mode {
			case "":
				mode = traits.MatchSubstring
			case traits.MatchSubstring, traits.MatchFuzzy, traits.MatchRegex:
			default:
				return fmt.Errorf("--mode %s is not substring, fuzzy or regex", p.String("mode"))
			}

			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()

			buildOpts := panesearch.BuildOptions{
				IncludeArchived:    p.Bool("archived"),
				IncludeTranscripts: !p.Bool("no-transcripts"),
				MaxLogBytes:        panesearch.DefaultMaxLogBytes,
				Cache:              panesearch.NewCache(),
			}
			attachLiveDaemonSearchData(c.Context, p.String("socket"), &buildOpts)

			docs, err := panesearch.BuildDocuments(store, buildOpts)
			if err != nil {
				return err
			}
			res := panesearch.Search(docs, query, panesearch.SearchOptions{
				Source:      src,
				Mode:        mode,
				Limit:       p.Int("limit"),
				MaxSnippets: 5,
			})
			if res.FilterErr != "" {
				return errors.New("invalid regex: " + res.FilterErr)
			}

			if p.Bool("json") || p.String("format") == "json" && p.Has("format") {
				return printJSON(c.Stdout, res)
			}
			if len(res.Results) == 0 {
				fmt.Fprintf(c.Stdout, "no panes matched %q (source: %s)\n", query, res.Source)
				return nil
			}
			for i, r := range res.Results {
				if i > 0 {
					fmt.Fprintln(c.Stdout)
				}
				meta := []string{r.PaneID}
				if r.Project != "" {
					meta = append(meta, r.Project)
				}
				if r.Harness != "" {
					meta = append(meta, r.Harness)
				}
				if r.Archived {
					meta = append(meta, "archived")
				}
				fmt.Fprintf(c.Stdout, "%s  (%s)  score=%d matches=%d\n", r.Title, strings.Join(meta, " · "), r.Score, r.MatchCount)
				for _, sn := range r.Snippets {
					fmt.Fprintf(c.Stdout, "  [%s] %s\n", sn.Badge, sn.Line)
				}
			}
			return nil
		},
	}
}

func attachLiveDaemonSearchData(ctx context.Context, socket string, opts *panesearch.BuildOptions) {
	if opts == nil {
		return
	}
	client := daemon.NewClient(socket)
	client.Timeout = 250 * time.Millisecond
	dctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	panes, err := client.List(dctx)
	if err == nil && len(panes) > 0 {
		opts.Frames = make(map[string]panesearch.LiveFrame, len(panes))
		for _, pi := range panes {
			if fr, found, serr := client.Snapshot(dctx, pi.ID); serr == nil && found {
				opts.Frames[pi.ID] = panesearch.LiveFrame{
					Lines:    fr.History(),
					Revision: fr.Revision,
				}
			}
		}
	}
	if telRes, err := client.Telemetry(dctx, ""); err == nil && telRes != nil {
		opts.Turns = map[string][]panesearch.Turn{}
		opts.TranscriptPaths = map[string]string{}
		opts.SessionIDs = map[string]string{}
		for id, pt := range telRes.Telemetry {
			if pt == nil {
				continue
			}
			if pt.TranscriptPath != "" {
				opts.TranscriptPaths[id] = pt.TranscriptPath
			}
			if pt.SessionID != "" {
				opts.SessionIDs[id] = pt.SessionID
			}
			if len(pt.RecentTurns) > 0 {
				turns := make([]panesearch.Turn, len(pt.RecentTurns))
				for i, ht := range pt.RecentTurns {
					turns[i] = panesearch.Turn{
						Prompt:   ht.Prompt,
						Response: ht.Response,
						Summary:  ht.Summary,
						At:       ht.At,
					}
				}
				opts.Turns[id] = turns
			}
		}
	}
}
