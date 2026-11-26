package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"ofam.dev/matomeru/data"
	"ofam.dev/oden"
)

func mtmTaskLineageCommands(g globalOpts) []oden.Command {
	content := []oden.Parameter{
		{Name: "title", Type: "string", Required: true, Description: "Title of the new task"},
		{Name: "body", Type: "string", Description: "Markdown body of the new task, or - to read stdin"},
	}
	return []oden.Command{
		mtmTaskLineageCommand(g, data.TaskTypeRewrite, "create a task linked to the task it rewrites", "<source> --title TITLE [--body BODY]", content, 1),
		mtmTaskLineageCommand(g, data.TaskTypeSplit, "create tasks linked to the task they split", "<source> --parts JSON", []oden.Parameter{
			{Name: "parts", Type: "string", Required: true, Description: "JSON array of objects with title and optional body, or - to read stdin"},
		}, 1),
		mtmTaskLineageCommand(g, data.TaskTypeJoin, "create a task linked to all joined tasks", "<source> <source>... --title TITLE [--body BODY]", content, 2),
	}
}

func mtmTaskLineageCommand(g globalOpts, operation, summary, usage string, params []oden.Parameter, sources int) oden.Command {
	fullUsage := "usage: mtm task " + operation + " " + usage
	return oden.Command{
		Name: operation, Summary: summary, Usage: usage, Parameters: params,
		Handle: func(c *oden.Ctx) error {
			parsed, err := c.Parse(params)
			if err != nil {
				return err
			}
			positional := parsed.Positional()
			if len(positional) < sources || (sources == 1 && len(positional) > sources) {
				return errors.New(fullUsage)
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()
			ids := make([]string, 0, len(positional))
			for _, query := range positional {
				id, err := resolveRecordID(c.Context, store, data.KindTask, query, c.Stdin, fullUsage)
				if err != nil {
					return err
				}
				ids = append(ids, id)
			}
			if operation == data.TaskTypeSplit {
				raw, _, err := resolveBody(parsed.String("parts"), true, c.Stdin)
				if err != nil {
					return err
				}
				var parts []data.TaskSplitPart
				if err := json.Unmarshal([]byte(raw), &parts); err != nil {
					return fmt.Errorf("--parts: %w", err)
				}
				records, err := store.SplitTask(ids[0], parts, cliActor)
				if err != nil {
					return err
				}
				return printJSON(c.Stdout, records)
			}
			body, _, err := resolveBody(parsed.String("body"), true, c.Stdin)
			if err != nil {
				return err
			}
			title := parsed.String("title")
			var record *data.Record
			if operation == data.TaskTypeRewrite {
				record, err = store.RewriteTask(ids[0], title, body, cliActor)
			} else {
				record, err = store.JoinTasks(ids, title, body, cliActor)
			}
			if err != nil {
				return err
			}
			return printJSON(c.Stdout, record)
		},
	}
}
