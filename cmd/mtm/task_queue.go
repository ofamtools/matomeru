package main

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
	"ofam.dev/oden"
)

var (
	cliQueueStatuses = []string{
		data.AgentStatusQueued,
		data.AgentStatusWorking,
		data.AgentStatusNeedsYou,
		data.AgentStatusDone,
		"open",
		"all",
	}
	cliFlagStatuses = []string{
		data.AgentStatusQueued,
		data.AgentStatusWorking,
		data.AgentStatusNeedsYou,
		data.AgentStatusDone,
		data.AgentStatusNone,
	}
)

type taskQueueOutput struct {
	Items []data.AgentQueueItem `json:"items"`
	Count int                   `json:"count"`
}

type taskThreadOutput struct {
	Task        *data.Record      `json:"task"`
	Notes       []data.Record     `json:"notes"`
	Attachments []data.Attachment `json:"attachments"`
	Context     []data.Record     `json:"context"`
}

type taskDispatchOutput struct {
	Prompt    string                `json:"prompt"`
	Count     int                   `json:"count"`
	Tasks     []data.AgentQueueItem `json:"tasks"`
	DryRun    bool                  `json:"dryRun"`
	Delivered bool                  `json:"delivered"`
	PaneID    string                `json:"paneId,omitempty"`
}

func mtmTaskQueueCommands(g globalOpts) []oden.Command {
	return []oden.Command{
		mtmTaskQueueSubcmd(g),
		mtmTaskFlagSubcmd(g),
		mtmTaskNoteSubcmd(g),
		mtmTaskThreadSubcmd(g),
		mtmTaskDispatchSubcmd(g),
	}
}

func mtmTaskQueueSubcmd(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "project", Type: "string", Description: "Filter to tasks under this project/parent task ID or title"},
		{Name: "status", Type: "string", Default: data.AgentStatusQueued, Enum: cliQueueStatuses, Description: "Agent status filter"},
	}
	return oden.Command{
		Name:       "queue",
		Summary:    "list agent-flagged tasks in pickup order",
		Usage:      "[--project ID] [--status queued|working|needs_you|done|open|all] [--json]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			const use = "usage: mtm task queue [--project ID] [--status queued|working|needs_you|done|open|all] [--json]"
			if len(p.Positional()) != 0 {
				return errors.New(use)
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()

			projectID := ""
			if proj := strings.TrimSpace(p.String("project")); proj != "" {
				projectID, err = resolveRecordID(c.Context, store, data.KindTask, proj, c.Stdin, use)
				if err != nil {
					return err
				}
			}
			status := strings.TrimSpace(p.String("status"))
			if status == "" {
				status = data.AgentStatusQueued
			}
			items, err := store.QueryAgentQueue(data.AgentQueueFilter{
				ProjectID:      projectID,
				Status:         status,
				IncludeSubtree: projectID != "",
				IncludeNotes:   true,
				IncludeContext: true,
			})
			if err != nil {
				return err
			}
			if items == nil {
				items = []data.AgentQueueItem{}
			}
			if p.Format() == "json" {
				return printJSON(c.Stdout, taskQueueOutput{Items: items, Count: len(items)})
			}
			if len(items) == 0 {
				_, werr := fmt.Fprintf(c.Stdout, "no tasks in agent queue (status: %s)\n", status)
				return werr
			}
			return printTaskQueueTable(c.Stdout, items)
		},
	}
}

func printTaskQueueTable(w io.Writer, items []data.AgentQueueItem) error {
	tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPRIORITY\tAGENT\tTITLE\tPROJECT\tNOTES\tATTACHMENTS")
	for _, item := range items {
		prio := strings.TrimSpace(item.Task.Text("priority"))
		if prio == "" {
			prio = "-"
		}
		agentSt := strings.TrimSpace(item.Task.Text("agentStatus"))
		if agentSt == "" && item.Task.Bool("forAgent") {
			agentSt = data.AgentStatusQueued
		}
		proj := item.ProjectTitle
		if len(item.AncestorPath) > 0 {
			proj = strings.Join(item.AncestorPath, " / ")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\n",
			item.Task.ID, prio, agentSt, strings.ReplaceAll(item.Task.Text("title"), "\n", " "),
			proj, len(item.Notes), len(item.Attachments))
	}
	return tw.Flush()
}

func mtmTaskFlagSubcmd(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "off", Type: "bool", Description: "Unflag the task from the agent queue"},
		{Name: "status", Type: "string", Enum: cliFlagStatuses, Description: "Agent workflow status"},
		{Name: "assignee", Type: "string", Description: "Agent harness or worker assigned to this task"},
	}
	return oden.Command{
		Name:       "flag",
		Summary:    "flag or unflag a task for agent pickup",
		Usage:      "<id> [--off] [--status queued|working|needs_you|done|none] [--assignee NAME] [--json]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			const use = "usage: mtm task flag <id> [--off] [--status queued|working|needs_you|done|none] [--assignee NAME] [--json]"
			pos := p.Positional()
			if len(pos) > 1 {
				return errors.New(use)
			}
			query := ""
			if len(pos) == 1 {
				query = pos[0]
			}
			off := p.Bool("off")
			status := strings.TrimSpace(p.String("status"))
			if off && status != "" && status != data.AgentStatusNone {
				return fmt.Errorf("--off cannot be combined with --status %s", status)
			}
			forAgent := !off && status != data.AgentStatusNone
			if off {
				status = data.AgentStatusNone
			}

			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()

			id, err := resolveRecordID(c.Context, store, data.KindTask, query, c.Stdin, use)
			if err != nil {
				return err
			}
			rec, err := store.SetTaskAgentFlag(core.Ref{Kind: data.KindTask, ID: id}, forAgent, status, p.String("assignee"), cliActor)
			if err != nil {
				return err
			}
			if p.Format() == "json" {
				return printJSON(c.Stdout, rec)
			}
			_, werr := fmt.Fprintf(c.Stdout, "%s  %s  forAgent=%v  agentStatus=%s\n",
				rec.ID, rec.Text("title"), rec.Bool("forAgent"), rec.Text("agentStatus"))
			return werr
		},
	}
}

func mtmTaskNoteSubcmd(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "title", Type: "string", Description: "Optional note title (derived from first body line when omitted)"},
		{Name: "body", Type: "string", Description: "Note body text (or - to read stdin)"},
		{Name: "attach", Type: "string", Repeatable: true, Description: "Attach a file path or URL (repeatable)"},
		{Name: "author-kind", Type: "string", Default: "owner", Enum: []string{"owner", "agent", "system"}, Description: "Author kind"},
		{Name: "author-label", Type: "string", Description: "Author display label"},
	}
	return oden.Command{
		Name:       "note",
		Summary:    "append an inline note to a task's thread",
		Usage:      "<id> [--title TITLE] [--body TEXT|- | TEXT] [--attach PATH]... [--author-kind owner|agent|system] [--author-label LABEL] [--json]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			const use = "usage: mtm task note <id> [--title TITLE] [--body TEXT|- | TEXT] [--attach PATH]... [--author-kind owner|agent|system] [--author-label LABEL] [--json]"
			pos := p.Positional()
			var query, body string
			if p.Has("body") {
				if len(pos) > 1 {
					return errors.New(use)
				}
				if len(pos) == 1 {
					query = pos[0]
				}
				body, _, err = resolveBody(p.String("body"), true, c.Stdin)
				if err != nil {
					return err
				}
			} else {
				switch {
				case len(pos) >= 2:
					query = pos[0]
					body = strings.Join(pos[1:], " ")
				case len(pos) == 1:
					query = pos[0]
				}
			}

			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()

			id, err := resolveRecordID(c.Context, store, data.KindTask, query, c.Stdin, use)
			if err != nil {
				return err
			}
			attachRaw := p.Strings("attach")
			attachments := make([]data.Attachment, 0, len(attachRaw))
			for _, raw := range attachRaw {
				if att, ok := attachmentFromCLIArg(raw); ok {
					attachments = append(attachments, att)
				}
			}
			note, err := store.AppendTaskNote(data.AppendTaskNoteInput{
				TaskID:      id,
				Title:       p.String("title"),
				Body:        body,
				AuthorKind:  p.String("author-kind"),
				AuthorLabel: p.String("author-label"),
				PaneID:      strings.TrimSpace(os.Getenv(mcpui.EnvPaneID)),
				Attachments: attachments,
			}, cliActor)
			if err != nil {
				return err
			}
			if p.Format() == "json" {
				return printJSON(c.Stdout, note)
			}
			_, werr := fmt.Fprintf(c.Stdout, "%s  [%s] %s\n", note.ID, note.Text("authorKind"), note.Text("title"))
			return werr
		},
	}
}

func attachmentFromCLIArg(raw string) (data.Attachment, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return data.Attachment{}, false
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return data.Attachment{
			Kind:   "url",
			Path:   s,
			Label:  s,
			Source: "picker",
		}, true
	}
	abs := s
	if resolved, err := filepath.Abs(s); err == nil {
		abs = resolved
	}
	ext := strings.ToLower(filepath.Ext(abs))
	mimeType := mime.TypeByExtension(ext)
	kind := "file"
	if strings.HasPrefix(mimeType, "image/") || isImageExt(ext) {
		kind = "image"
	}
	return data.Attachment{
		Kind:   kind,
		Path:   abs,
		Label:  filepath.Base(abs),
		Mime:   mimeType,
		Source: "picker",
	}, true
}

func isImageExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".ico", ".tiff", ".heic":
		return true
	}
	return false
}

func mtmTaskThreadSubcmd(g globalOpts) oden.Command {
	return oden.Command{
		Name:    "thread",
		Summary: "show a task with its chronological note thread, attachments, and context",
		Usage:   "<id> [--json]",
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(nil)
			if err != nil {
				return err
			}
			const use = "usage: mtm task thread <id> [--json]"
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

			id, err := resolveRecordID(c.Context, store, data.KindTask, query, c.Stdin, use)
			if err != nil {
				return err
			}
			res, err := mcpui.NewTaskTools(store).Thread(mcpui.TaskThreadArgs{Task: id})
			if err != nil {
				return err
			}
			out := taskThreadOutput{
				Task:        res.Task,
				Notes:       res.Notes,
				Attachments: res.Attachments,
				Context:     res.Context,
			}
			if p.Format() == "json" {
				return printJSON(c.Stdout, out)
			}
			return printTaskThread(c.Stdout, out)
		},
	}
}

func printTaskThread(w io.Writer, out taskThreadOutput) error {
	agentSt := strings.TrimSpace(out.Task.Text("agentStatus"))
	if agentSt == "" {
		agentSt = data.AgentStatusNone
	}
	fmt.Fprintf(w, "%s (%s)  forAgent=%v  agentStatus=%s\n",
		out.Task.Text("title"), out.Task.ID, out.Task.Bool("forAgent"), agentSt)
	if desc := strings.TrimSpace(out.Task.Text("description")); desc != "" {
		fmt.Fprintf(w, "\n%s\n", desc)
	}
	fmt.Fprintf(w, "\nNotes (%d):\n", len(out.Notes))
	if len(out.Notes) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, n := range out.Notes {
		author := strings.TrimSpace(n.Text("authorKind"))
		if author == "" {
			author = "owner"
		}
		if label := strings.TrimSpace(n.Text("authorLabel")); label != "" {
			author += ":" + label
		}
		body := strings.TrimSpace(n.Text("body"))
		title := strings.TrimSpace(n.Text("title"))
		switch {
		case body != "" && title != "" && !strings.HasPrefix(body, title):
			fmt.Fprintf(w, "  - [%s] %s: %s\n", author, title, body)
		case body != "":
			fmt.Fprintf(w, "  - [%s] %s\n", author, body)
		default:
			fmt.Fprintf(w, "  - [%s] %s\n", author, title)
		}
	}
	if len(out.Attachments) > 0 {
		fmt.Fprintf(w, "\nAttachments (%d):\n", len(out.Attachments))
		for _, a := range out.Attachments {
			fmt.Fprintf(w, "  - [%s] %s\n", a.Kind, a.Path)
		}
	}
	if len(out.Context) > 0 {
		fmt.Fprintf(w, "\nContext (%d):\n", len(out.Context))
		for _, cr := range out.Context {
			fmt.Fprintf(w, "  - %s: %s\n", cr.Text("title"), cr.Text("value"))
		}
	}
	return nil
}

func mtmTaskDispatchSubcmd(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "project", Type: "string", Description: "Filter queued tasks to this project/parent task ID or title"},
		{Name: "status", Type: "string", Default: data.AgentStatusQueued, Enum: cliQueueStatuses, Description: "Agent status filter"},
		{Name: "pane", Type: "string", Description: "Target live daemon pane ID (defaults to sole live pane)"},
		{Name: "prompt", Type: "string", Description: "Extra instructions prepended to the generated prompt"},
		{Name: "socket", Type: "string", Description: "Override daemon Unix socket path"},
		{Name: "dry-run", Type: "bool", Description: "Render the prompt without writing to a pane"},
	}
	return oden.Command{
		Name:       "dispatch",
		Summary:    "format queued agent tasks and deliver them to a live daemon pane",
		Usage:      "[--project ID] [--pane ID] [--prompt EXTRA] [--socket PATH] [--dry-run] [--json]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			const use = "usage: mtm task dispatch [--project ID] [--pane ID] [--prompt EXTRA] [--socket PATH] [--dry-run] [--json]"
			if len(p.Positional()) != 0 {
				return errors.New(use)
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()

			projectID := ""
			if proj := strings.TrimSpace(p.String("project")); proj != "" {
				projectID, err = resolveRecordID(c.Context, store, data.KindTask, proj, c.Stdin, use)
				if err != nil {
					return err
				}
			}
			status := strings.TrimSpace(p.String("status"))
			if status == "" {
				status = data.AgentStatusQueued
			}
			items, err := store.QueryAgentQueue(data.AgentQueueFilter{
				ProjectID:      projectID,
				Status:         status,
				IncludeSubtree: projectID != "",
				IncludeNotes:   true,
				IncludeContext: true,
			})
			if err != nil {
				return err
			}
			if items == nil {
				items = []data.AgentQueueItem{}
			}
			prompt := data.FormatAgentQueuePrompt(items, p.String("prompt"))
			dryRun := p.Bool("dry-run")
			paneID := strings.TrimSpace(p.String("pane"))

			out := taskDispatchOutput{
				Prompt: prompt,
				Count:  len(items),
				Tasks:  items,
				DryRun: dryRun,
				PaneID: paneID,
			}

			if !dryRun && len(items) > 0 {
				sock := daemon.ResolveSocketPath(p.String("socket"))
				client := daemon.NewClient(sock)
				if paneID == "" {
					panes, err := client.List(c.Context)
					if err != nil {
						return fmt.Errorf("list live daemon panes: %w", err)
					}
					var live []string
					for _, pi := range panes {
						if !pi.Exited && pi.ID != "" {
							live = append(live, pi.ID)
						}
					}
					switch len(live) {
					case 0:
						return errors.New("no live daemon panes found (pass --pane ID or --dry-run)")
					case 1:
						paneID = live[0]
					default:
						return fmt.Errorf("multiple live daemon panes (%s); pass --pane ID", strings.Join(live, ", "))
					}
				}
				payload := "\x1b[200~" + prompt + "\x1b[201~\r"
				if err := client.Write(c.Context, paneID, []byte(payload)); err != nil {
					return fmt.Errorf("deliver prompt to pane %s: %w", paneID, err)
				}
				out.PaneID = paneID
				out.Delivered = true
			}

			if p.Format() == "json" {
				return printJSON(c.Stdout, out)
			}
			if dryRun {
				_, werr := io.WriteString(c.Stdout, prompt)
				return werr
			}
			if out.Delivered {
				_, werr := fmt.Fprintf(c.Stdout, "dispatched %d task(s) to pane %s\n", out.Count, out.PaneID)
				return werr
			}
			_, werr := fmt.Fprintln(c.Stdout, "no queued tasks to dispatch")
			return werr
		},
	}
}

func mtmSchedulePresetCommand(g globalOpts) oden.Command {
	agentQueueParams := []oden.Parameter{
		{Name: "title", Type: "string", Default: "Agent queue pickup", Description: "Schedule title"},
		{Name: "project", Type: "string", Description: "Optional project/task ID or title to scope queue pickup"},
		{Name: "cron", Type: "string", Default: "*/15 * * * *", Description: "Cron expression (5-field or @hourly/@daily/@every)"},
		{Name: "pane", Type: "string", Description: "Optional target pane ID"},
		{Name: "prompt", Type: "string", Description: "Optional extra prompt instructions"},
		{Name: "harness", Type: "string", Description: "Optional agent harness name"},
	}
	return oden.Command{
		Name:    "preset",
		Summary: "create a schedule from a built-in preset",
		Children: []oden.Command{
			{
				Name:       "agent-queue",
				Summary:    "create a recurring agent_queue schedule to drain queued tasks",
				Usage:      `[--title "..."] [--project ID] [--cron "*/15 * * * *"] [--pane ID] [--prompt "..."] [--harness NAME] [--json]`,
				Parameters: agentQueueParams,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(agentQueueParams)
					if err != nil {
						return err
					}
					const use = `usage: mtm schedule preset agent-queue [--title "..."] [--project ID] [--cron "*/15 * * * *"] [--pane ID] [--prompt "..."] [--json]`
					if len(p.Positional()) != 0 {
						return errors.New(use)
					}
					cronSpec := strings.TrimSpace(p.String("cron"))
					if cronSpec == "" {
						cronSpec = "*/15 * * * *"
					}
					expr, err := daemon.ParseCron(cronSpec)
					if err != nil {
						return fmt.Errorf("invalid --cron %q: %w", cronSpec, err)
					}

					store, err := openStoreWithSeed(g)
					if err != nil {
						return err
					}
					defer store.Close()

					title := strings.TrimSpace(p.String("title"))
					if title == "" {
						title = "Agent queue pickup"
					}
					fields := map[string]any{
						"title":        title,
						"scheduleKind": "agent_queue",
						"cron":         cronSpec,
						"paused":       false,
					}
					if next := expr.Next(time.Now().UTC()); !next.IsZero() {
						fields["nextDueAt"] = next.UTC().Format(data.TimeFormat)
					}
					if proj := strings.TrimSpace(p.String("project")); proj != "" {
						projectID, rerr := resolveRecordID(c.Context, store, data.KindTask, proj, c.Stdin, use)
						if rerr != nil {
							return rerr
						}
						fields["task"] = core.Ref{Kind: data.KindTask, ID: projectID}
					}
					if pane := strings.TrimSpace(p.String("pane")); pane != "" {
						paneID := pane
						if resolved, rerr := resolveRecordID(c.Context, store, data.KindPane, pane, c.Stdin, use); rerr == nil {
							paneID = resolved
						} else if !errors.Is(rerr, data.ErrNotFound) {
							return rerr
						}
						fields["pane"] = core.Ref{Kind: data.KindPane, ID: paneID}
					}
					if prompt := strings.TrimSpace(p.String("prompt")); prompt != "" {
						fields["prompt"] = prompt
					}
					if harness := strings.TrimSpace(p.String("harness")); harness != "" {
						fields["harness"] = harness
					}

					rec, _, err := store.Create(data.CreateInput{Kind: data.KindSchedule, Fields: fields}, cliActor)
					if err != nil {
						return err
					}
					if p.Format() == "json" {
						return printJSON(c.Stdout, rec)
					}
					_, werr := fmt.Fprintf(c.Stdout, "%s  %s  cron=%q  nextDueAt=%s\n",
						rec.ID, rec.Text("title"), rec.Text("cron"), rec.Text("nextDueAt"))
					return werr
				},
			},
		},
	}
}
