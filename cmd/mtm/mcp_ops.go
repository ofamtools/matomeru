package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// The two operations the MCP tools need that no other verb covers (see
// mcp.go). Both are ordinary CLI verbs too, and both act for a pane named
// by --session and --window, defaulting to the pane they run in
// (TUIOS_SESSION, TUIOS_PANE_ID), like history capture-hook:
//
//   - context-item add keeps items found in a text the caller hands in
//     (context_add, document_report), owned the way the journal owns a
//     turn of that pane (journalOwner): its assignment, else its
//     session's owner.
//   - pane progress reports the pane's agent state to the daemon with a
//     message (report_progress), which list-agents and the pane row read.

// mcpContextSource is the Source.Kind of an item a caller handed in.
const mcpContextSource = "mcp"

// progressStates are the agent states pane progress reports.
var progressStates = []string{"working", "idle", "done", "errored"}

func (a *App) defineMCPOperations() {
	a.r.Define(contextNoun).
		Verb("add", a.addContextText, "Keep the items found in a text (a URL, path, PR, branch, commit, or the whole text as a prompt or command) for a pane's task")
	a.parameters(contextNoun+".add", map[string]ochazuke.Parameter{
		"text":    {Type: "string", Required: true, Description: "The text the items are taken from"},
		"kind":    {Type: "string", Enum: contextitems.Kinds, Description: "Keep only items of this kind; prompt or command keeps the whole text as one; a kind the text yields none of keeps the whole text as that kind"},
		"session": {Type: "string", Description: "The pane's session name (default TUIOS_SESSION)"},
		"window":  {Type: "string", Description: "The pane's window id (default TUIOS_PANE_ID); it owns the items"},
		"cwd":     {Type: "string", Description: "The directory relative paths resolve against"},
	})
	a.r.Define("pane").
		Verb("progress", a.paneProgress, "Report what the pane's agent is doing: its agent state (working unless --state) with a short message")
	a.parameters("pane.progress", map[string]ochazuke.Parameter{
		"message": {Type: "string", Required: true, Description: "What the agent is doing right now"},
		"state":   {Type: "string", Enum: progressStates, Description: "The agent state (default working)"},
		"session": {Type: "string", Description: "The pane's session name (default TUIOS_SESSION)"},
		"window":  {Type: "string", Description: "The pane's window id (default TUIOS_PANE_ID)"},
	})
}

// paneArgs are --session and --window, else the pane this runs in.
func (a *App) paneArgs(args ochazuke.Arguments) (session, window string) {
	return ochazuke.StringArg(args, "session", a.getenv("TUIOS_SESSION")), ochazuke.StringArg(args, "window", a.getenv("TUIOS_PANE_ID"))
}

// addContextText is context-item.add.
func (a *App) addContextText(ctx ochazuke.Ctx) ochazuke.Result {
	body := strings.TrimSpace(ochazuke.StringArg(ctx.Args, "text", ""))
	if body == "" {
		return ochazuke.Failed(errors.New("text is required"))
	}
	kind := ochazuke.StringArg(ctx.Args, "kind", "")
	session, window := a.paneArgs(ctx.Args)
	now := work.Now()
	src := contextitems.Source{Kind: mcpContextSource, Session: session, Window: window, At: now, Cwd: ochazuke.StringArg(ctx.Args, "cwd", "")}
	if kind == "prompt" || kind == "command" {
		src.EntryKind = kind
	}
	items := contextitems.Extract(body, 0, src)
	if kind != "" {
		items = slices.DeleteFunc(items, func(it contextitems.Item) bool { return it.Kind != kind })
		if len(items) == 0 {
			items = []contextitems.Item{{Kind: kind, Text: body, Session: session, Window: window, Created: now, Sources: []contextitems.Source{src}}}
		}
	}
	var owner contextitems.Owner
	if window != "" {
		task, project, err := journalOwner(a.work)(session, window)
		if err != nil {
			return ochazuke.Failed(err)
		}
		owner = contextitems.Owner{ProjectID: project, TaskID: task}
	}
	written, added, err := a.contexts.Merge(items, owner, now)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.reloadContext(); err != nil {
		ctx.Status(err.Error())
	}
	if written == nil {
		written = []contextitems.Item{}
	}
	return ochazuke.Done(extracted{Pane: window, Found: len(written), New: added, Items: written})
}

// agentStateCaller reaches the daemon's set-agent-state verb; the tuios
// pane daemon is one, and tests replace it.
type agentStateCaller interface {
	Call(ctx context.Context, verb string, params, out any) error
}

// paneProgress is pane.progress.
func (a *App) paneProgress(ctx ochazuke.Ctx) ochazuke.Result {
	message := strings.TrimSpace(ochazuke.StringArg(ctx.Args, "message", ""))
	if message == "" {
		return ochazuke.Failed(errors.New("message is required"))
	}
	session, window := a.paneArgs(ctx.Args)
	if window == "" {
		return ochazuke.Failed(errors.New("name the pane: --window WINDOW, or run in a TUIOS pane"))
	}
	caller, ok := a.panesd.(agentStateCaller)
	if !ok {
		return ochazuke.Failed(errors.New("pane progress needs the TUIOS daemon"))
	}
	state := ochazuke.StringArg(ctx.Args, "state", "working")
	params := map[string]any{"window": window, "state": state, "message": message, "source": "report"}
	if session != "" {
		params["session"] = session
	}
	return ochazuke.Pending(func(c context.Context) (any, error) {
		c, cancel := context.WithTimeout(c, daemonTimeout)
		defer cancel()
		var reply map[string]any
		if err := caller.Call(c, "set-agent-state", params, &reply); err != nil {
			return nil, fmt.Errorf("set-agent-state %s: %w", window, err)
		}
		return map[string]any{"session": session, "window": window, "state": state, "message": message, "reply": reply}, nil
	})
}
