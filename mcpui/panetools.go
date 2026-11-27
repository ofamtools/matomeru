package mcpui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// Pane orchestration MCP tool names.
const (
	ToolPaneSpawn    = "pane.spawn"
	ToolPaneSnapshot = "pane.snapshot"
	ToolPaneSend     = "pane.send"
	ToolPaneStatus   = "pane.status"
	ToolPaneClose    = "pane.close"
)

// PaneOpenRequest describes a live PTY session to open in the daemon.
type PaneOpenRequest struct {
	ID   string
	Cmd  string
	Cwd  string
	Cols int
	Rows int
	Env  map[string]string
}

// PaneSnapshot is one read-only screen capture of a live PTY pane.
type PaneSnapshot struct {
	Plain    []string `json:"lines"`
	Cols     int      `json:"cols"`
	Rows     int      `json:"rows"`
	Exited   bool     `json:"exited"`
	ExitCode int      `json:"exitCode,omitempty"`
}

// PaneLiveStatus summarizes live daemon PTY + hook telemetry for one pane.
type PaneLiveStatus struct {
	Live         bool   `json:"live"`
	Exited       bool   `json:"exited,omitempty"`
	ExitCode     int    `json:"exitCode,omitempty"`
	Cols         int    `json:"cols,omitempty"`
	Rows         int    `json:"rows,omitempty"`
	Cmd          string `json:"cmd,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	HookState    string `json:"hookState,omitempty"`
	PendingCount int    `json:"pendingApprovals,omitempty"`
	MCPURL       string `json:"mcpUrl,omitempty"`
}

// PaneRuntime abstracts the live daemon PTY host so mcpui stays free of
// daemon/ptyhost/harness imports while supporting both in-process daemon HTTP
// MCP and CLI/stdio client calls.
type PaneRuntime interface {
	Open(ctx context.Context, req PaneOpenRequest) error
	Write(ctx context.Context, paneID string, data []byte) error
	Snapshot(ctx context.Context, paneID string) (PaneSnapshot, bool, error)
	Status(ctx context.Context, paneID string) (PaneLiveStatus, error)
	Close(ctx context.Context, paneID string) error
}

// PaneOnDone is a one-shot callback fired the first time a spawned pane
// emits agent.done: Command gets the event JSON on stdin, Webhook gets it as
// a POST body.
type PaneOnDone struct {
	Command    []string          `json:"command,omitempty"`
	Webhook    string            `json:"webhook,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	TimeoutSec int               `json:"timeoutSec,omitempty"`
}

// Empty reports whether no callback target is set.
func (o *PaneOnDone) Empty() bool {
	return o == nil || (len(o.Command) == 0 && strings.TrimSpace(o.Webhook) == "")
}

// PaneCallbackRuntime is an optional PaneRuntime extension that registers a
// one-shot agent.done callback for a pane.
type PaneCallbackRuntime interface {
	RegisterOnDone(ctx context.Context, paneID string, onDone PaneOnDone) error
}

// PaneLaunchSpec is the resolved shell command, environment, and prompt
// delivery flag returned by PaneTools.ResolveLaunchWithPrompt.
type PaneLaunchSpec struct {
	Cmd                  string
	Env                  map[string]string
	PromptDeliveredInCmd bool
}

// PaneTools serves pane.spawn, pane.snapshot, pane.send, pane.status, and
// pane.close over a data.Store and optional live PaneRuntime.
type PaneTools struct {
	store                   *data.Store
	Runtime                 PaneRuntime
	ResolveLaunch           func(harnessID, mcpURL, paneID, customCmd string) (string, map[string]string, error)
	ResolveLaunchWithPrompt func(harnessID, mcpURL, paneID, customCmd, prompt string) (PaneLaunchSpec, error)
	MCPURL                  func(paneID string) string
}

// NewPaneTools returns pane orchestration tools over store.
func NewPaneTools(store *data.Store) *PaneTools {
	return &PaneTools{store: store}
}

// PaneSpawnArgs are the arguments of pane.spawn.
type PaneSpawnArgs struct {
	ID        string            `json:"id,omitempty"`
	Title     string            `json:"title,omitempty"`
	Harness   string            `json:"harness,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	Project   string            `json:"project,omitempty"`
	Task      string            `json:"task,omitempty"`
	Lane      string            `json:"lane,omitempty"`
	Directory string            `json:"directory,omitempty"`
	Cmd       string            `json:"cmd,omitempty"`
	Prompt    string            `json:"prompt,omitempty"`
	Cols      int               `json:"cols,omitempty"`
	Rows      int               `json:"rows,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	NoLaunch  bool              `json:"noLaunch,omitempty"`
	OnDone    *PaneOnDone       `json:"onDone,omitempty"`
	By        string            `json:"by,omitempty"`
}

// PaneSpawnResult is returned by pane.spawn.
type PaneSpawnResult struct {
	Pane      data.Record    `json:"pane"`
	LaneClaim *data.Relation `json:"laneClaim,omitempty"`
	DirRel    *data.Relation `json:"directoryRelation,omitempty"`
	Spawned   bool           `json:"spawned"`
	Command   string         `json:"command,omitempty"`
	Cwd       string         `json:"cwd,omitempty"`
	OnDone    bool           `json:"onDoneRegistered,omitempty"`
}

// PaneSnapshotArgs are the arguments of pane.snapshot.
type PaneSnapshotArgs struct {
	Pane  string `json:"pane"`
	Lines int    `json:"lines,omitempty"`
}

// PaneSnapshotResult is returned by pane.snapshot.
type PaneSnapshotResult struct {
	PaneID   string   `json:"paneId"`
	Found    bool     `json:"found"`
	Lines    []string `json:"lines"`
	Cols     int      `json:"cols,omitempty"`
	Rows     int      `json:"rows,omitempty"`
	Exited   bool     `json:"exited,omitempty"`
	ExitCode int      `json:"exitCode,omitempty"`
}

// PaneSendArgs are the arguments of pane.send.
type PaneSendArgs struct {
	Pane  string `json:"pane"`
	Text  string `json:"text"`
	Enter *bool  `json:"enter,omitempty"`
}

// PaneSendResult is returned by pane.send.
type PaneSendResult struct {
	PaneID string `json:"paneId"`
	Sent   int    `json:"sent"`
}

// PaneStatusArgs are the arguments of pane.status.
type PaneStatusArgs struct {
	Pane string `json:"pane"`
}

// PaneStatusResult is returned by pane.status.
type PaneStatusResult struct {
	Pane    *data.Record   `json:"pane,omitempty"`
	Runtime PaneLiveStatus `json:"runtime"`
	Lanes   []data.Record  `json:"lanes,omitempty"`
	Task    *data.Record   `json:"task,omitempty"`
}

// PaneCloseArgs are the arguments of pane.close.
type PaneCloseArgs struct {
	Pane string `json:"pane"`
	By   string `json:"by,omitempty"`
}

// PaneCloseResult is returned by pane.close.
type PaneCloseResult struct {
	PaneID string       `json:"paneId"`
	Closed bool         `json:"closed"`
	Pane   *data.Record `json:"pane,omitempty"`
}

// ToolSpecs returns the MCP tool schemas for pane orchestration.
func (p *PaneTools) ToolSpecs() []ToolSpec {
	by := text("optional agent identifier recorded on the change set")
	return []ToolSpec{
		{
			Name:        ToolPaneSpawn,
			Description: "Create a pane record and spawn a live PTY/agent harness (claude, codex, omp, gemini, zsh, or custom cmd) in the Matomeru daemon, associated with a project/task, lane (via claim relation), and optional directory. Automatically injects MTM_PANE_ID, MTM_TASK_ID, MTM_SOCKET, and MTM_MCP_URL.",
			InputSchema: object(nil, props{
				"id":        text("optional explicit pane ID"),
				"title":     text("pane title; defaults to harness name"),
				"harness":   text("harness ID (e.g. claude, codex, omp, gemini, zsh); defaults to zsh"),
				"cwd":       text("working directory; inherits lane worktree or project defaultDir when omitted"),
				"project":   text("project task ID (root project); sets pane task and inherits defaultDir if task/cwd omitted"),
				"task":      text("task ID to associate with the pane (takes precedence over project when both given)"),
				"lane":      text("lane ID to claim this pane (creates lane --claim--> pane relation; also inherits lane task/worktree if omitted)"),
				"directory": text("optional directory path to relate via directory relation"),
				"cmd":       text("optional explicit shell command overriding harness launch command"),
				"prompt":    text("optional initial prompt/input sent to the live pane right after spawn"),
				"cols":      count("initial terminal columns (default 120)"),
				"rows":      count("initial terminal rows (default 36)"),
				"env":       props{"type": "object", "description": "optional extra environment variables"},
				"noLaunch":  flag("if true, create the pane record and relations without starting a live PTY"),
				"onDone": props{
					"type":        "object",
					"description": "optional one-shot callback fired when this pane first emits agent.done (running/needs_you -> done/idle)",
					"properties": props{
						"command":    props{"type": "array", "items": props{"type": "string"}, "description": "argv to run; the agent.done event JSON arrives on stdin"},
						"webhook":    text("URL that receives the agent.done event JSON as an HTTP POST body"),
						"headers":    props{"type": "object", "description": "extra webhook headers; ${VAR} expands from the daemon environment"},
						"timeoutSec": count("per-attempt timeout in seconds (default 15; one retry on failure)"),
					},
				},
				"by": by,
			}),
		},
		{
			Name:        ToolPaneSnapshot,
			Description: "Capture the current plain-text terminal screen of a live pane in the Matomeru daemon.",
			InputSchema: object([]string{"pane"}, props{
				"pane":  text("pane ID"),
				"lines": count("optional tail line limit (0 = all rows)"),
			}),
		},
		{
			Name:        ToolPaneSend,
			Description: "Send text or keystrokes to a live pane in the Matomeru daemon.",
			InputSchema: object([]string{"pane", "text"}, props{
				"pane":  text("pane ID"),
				"text":  text("text or keystrokes to write to the pane PTY"),
				"enter": flag("append carriage return (\\r) after text; defaults to true"),
			}),
		},
		{
			Name:        ToolPaneStatus,
			Description: "Inspect a pane's record, live daemon PTY status, hook state, pending approvals, linked task, and claiming lanes.",
			InputSchema: object([]string{"pane"}, props{
				"pane": text("pane ID"),
			}),
		},
		{
			Name:        ToolPaneClose,
			Description: "Close a live PTY pane in the Matomeru daemon and mark its record runStatus as exited.",
			InputSchema: object([]string{"pane"}, props{
				"pane": text("pane ID"),
				"by":   by,
			}),
		},
	}
}

// HasTool reports whether name is handled by PaneTools.
func (p *PaneTools) HasTool(name string) bool {
	switch name {
	case ToolPaneSpawn, ToolPaneSnapshot, ToolPaneSend, ToolPaneStatus, ToolPaneClose:
		return true
	}
	return false
}

// Call dispatches one pane.* tool call.
func (p *PaneTools) Call(ctx context.Context, actor core.Actor, name string, raw json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch name {
	case ToolPaneSpawn:
		var args PaneSpawnArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return p.Spawn(ctx, actor, args)
	case ToolPaneSnapshot:
		var args PaneSnapshotArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return p.Snapshot(ctx, args)
	case ToolPaneSend:
		var args PaneSendArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return p.Send(ctx, args)
	case ToolPaneStatus:
		var args PaneStatusArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return p.Status(ctx, args)
	case ToolPaneClose:
		var args PaneCloseArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return p.Close(ctx, actor, args)
	default:
		return nil, fmt.Errorf("unknown pane tool %q", name)
	}
}

// Spawn creates a pane record, wires task/project/lane/directory associations,
// and starts a live PTY session via Runtime (unless NoLaunch is true).
func (p *PaneTools) Spawn(ctx context.Context, actor core.Actor, args PaneSpawnArgs) (PaneSpawnResult, error) {
	if p.store == nil {
		return PaneSpawnResult{}, errors.New("pane.spawn: no store configured")
	}
	act := agentActor(actor, args.By)

	harnessID := strings.TrimSpace(args.Harness)
	if harnessID == "" {
		harnessID = "zsh"
	}
	taskID := strings.TrimSpace(args.Task)
	projectID := strings.TrimSpace(args.Project)
	laneID := strings.TrimSpace(args.Lane)
	cwd := strings.TrimSpace(args.Cwd)
	dirPath := strings.TrimSpace(args.Directory)

	// Inherit task/worktree from lane when provided.
	var laneRec *data.Record
	if laneID != "" {
		rec, err := p.store.Get(core.Ref{Kind: data.KindLane, ID: laneID})
		if err != nil {
			return PaneSpawnResult{}, fmt.Errorf("pane.spawn: lane %q: %w", laneID, err)
		}
		laneRec = rec
		if taskID == "" && projectID == "" {
			if tRef, ok := rec.RefField("task"); ok && tRef.Kind == data.KindTask && tRef.ID != "" {
				taskID = tRef.ID
			}
		}
		if cwd == "" {
			if wt := strings.TrimSpace(rec.Text("worktree")); wt != "" {
				cwd = wt
			}
		}
	}

	effectiveTaskID := taskID
	if effectiveTaskID == "" {
		effectiveTaskID = projectID
	}

	// Inherit cwd from task or its root project's defaultDir / directory relation.
	if cwd == "" && effectiveTaskID != "" {
		cwd = p.resolveProjectCwd(effectiveTaskID)
	}
	if cwd == "" && dirPath != "" {
		cwd = dirPath
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	title := strings.TrimSpace(args.Title)
	if title == "" {
		if laneRec != nil {
			if lt := strings.TrimSpace(laneRec.Text("title")); lt != "" {
				title = harnessID + ": " + lt
			}
		}
		if title == "" {
			title = harnessID
		}
	}

	fields := map[string]any{
		"title":     title,
		"harness":   harnessID,
		"cwd":       cwd,
		"runStatus": "running",
	}
	if effectiveTaskID != "" {
		fields["task"] = core.Ref{Kind: data.KindTask, ID: effectiveTaskID}
	}
	if args.NoLaunch {
		fields["runStatus"] = "idle"
	}

	rec, _, err := p.store.Create(data.CreateInput{
		ID:     strings.TrimSpace(args.ID),
		Kind:   data.KindPane,
		Fields: fields,
	}, act)
	if err != nil {
		return PaneSpawnResult{}, fmt.Errorf("pane.spawn: create pane: %w", err)
	}

	res := PaneSpawnResult{Pane: *rec, Cwd: cwd}

	// Relate directory if specified.
	if dirPath != "" {
		rel, _, err := p.store.Relate(data.RelateInput{
			From: rec.Ref(),
			Kind: data.RelDirectory,
			To:   core.Ref{Kind: "directory", ID: dirPath},
		}, act)
		if err != nil {
			return res, fmt.Errorf("pane.spawn: relate directory: %w", err)
		}
		res.DirRel = rel
	}

	// Relate lane --claim--> pane (and attach task to lane if lane has none yet).
	if laneRec != nil {
		rel, _, err := p.store.Relate(data.RelateInput{
			From: laneRec.Ref(),
			Kind: data.RelClaim,
			To:   rec.Ref(),
		}, act)
		if err != nil {
			return res, fmt.Errorf("pane.spawn: claim pane on lane %s: %w", laneID, err)
		}
		res.LaneClaim = rel
		if effectiveTaskID != "" {
			if _, has := laneRec.RefField("task"); !has {
				_, _, _ = p.store.Patch(data.PatchInput{
					Ref:    laneRec.Ref(),
					Fields: map[string]any{"task": core.Ref{Kind: data.KindTask, ID: effectiveTaskID}},
				}, act)
			}
		}
	}

	if args.NoLaunch {
		return res, nil
	}
	if p.Runtime == nil {
		return res, errors.New("pane.spawn: live daemon runtime not available")
	}

	mcpURL := ""
	if p.MCPURL != nil {
		mcpURL = p.MCPURL(rec.ID)
	}
	cmdStr, launchEnv, promptDeliveredInCmd, err := p.resolveSpawnLaunch(harnessID, mcpURL, rec.ID, strings.TrimSpace(args.Cmd), args.Prompt)
	if err != nil {
		return res, fmt.Errorf("pane.spawn: resolve harness %q: %w", harnessID, err)
	}
	for k, v := range args.Env {
		launchEnv[k] = v
	}
	launchEnv[EnvPaneID] = rec.ID
	if effectiveTaskID != "" {
		launchEnv[EnvTaskID] = effectiveTaskID
	}

	cols, rows := args.Cols, args.Rows
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 36
	}
	if err := p.Runtime.Open(ctx, PaneOpenRequest{
		ID:   rec.ID,
		Cmd:  cmdStr,
		Cwd:  cwd,
		Cols: cols,
		Rows: rows,
		Env:  launchEnv,
	}); err != nil {
		return res, fmt.Errorf("pane.spawn: open PTY: %w", err)
	}
	res.Spawned = true
	res.Command = cmdStr
	if !args.OnDone.Empty() {
		cb, ok := p.Runtime.(PaneCallbackRuntime)
		if !ok {
			return res, errors.New("pane.spawn: onDone: runtime does not support callbacks")
		}
		if err := cb.RegisterOnDone(ctx, rec.ID, *args.OnDone); err != nil {
			return res, fmt.Errorf("pane.spawn: onDone: %w", err)
		}
		res.OnDone = true
	}
	if cmdStr != "" {
		if updated, _, err := p.store.Patch(data.PatchInput{
			Ref:    rec.Ref(),
			Fields: map[string]any{"command": cmdStr},
		}, act); err == nil && updated != nil {
			res.Pane = *updated
		}
	}

	if strings.TrimSpace(args.Prompt) != "" && !promptDeliveredInCmd {
		if err := p.deliverInitialStdinPrompt(ctx, rec.ID, args.Prompt); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (p *PaneTools) resolveSpawnLaunch(harnessID, mcpURL, paneID, customCmd, prompt string) (string, map[string]string, bool, error) {
	launchEnv := map[string]string{}
	if p.ResolveLaunchWithPrompt != nil {
		spec, err := p.ResolveLaunchWithPrompt(harnessID, mcpURL, paneID, customCmd, prompt)
		if err != nil {
			return "", nil, false, err
		}
		cmdStr := customCmd
		if spec.Cmd != "" {
			cmdStr = spec.Cmd
		}
		for k, v := range spec.Env {
			launchEnv[k] = v
		}
		return cmdStr, launchEnv, spec.PromptDeliveredInCmd, nil
	}
	if p.ResolveLaunch != nil {
		resolvedCmd, hEnv, err := p.ResolveLaunch(harnessID, mcpURL, paneID, customCmd)
		if err != nil {
			return "", nil, false, err
		}
		cmdStr := customCmd
		if resolvedCmd != "" {
			cmdStr = resolvedCmd
		}
		for k, v := range hEnv {
			launchEnv[k] = v
		}
		return cmdStr, launchEnv, false, nil
	}
	return customCmd, launchEnv, false, nil
}

func (p *PaneTools) deliverInitialStdinPrompt(ctx context.Context, paneID, prompt string) error {
	// Wait briefly until the PTY shows initial output (shell/prompt ready) or
	// the bounded startup window elapses, then write the prompt.
	const (
		pollEvery = 25 * time.Millisecond
		maxWait   = 150 * time.Millisecond
	)
	deadline := time.Now().Add(maxWait)
	for {
		if snap, found, err := p.Runtime.Snapshot(ctx, paneID); err == nil && found && hasNonBlankLine(snap.Plain) {
			break
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		wait := min(pollEvery, remaining)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	_ = p.Runtime.Write(ctx, paneID, []byte(prompt+"\r"))
	return nil
}

func hasNonBlankLine(lines []string) bool {
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			return true
		}
	}
	return false
}

func (p *PaneTools) resolveProjectCwd(taskID string) string {
	if p.store == nil || taskID == "" {
		return ""
	}
	checkIDs := []string{taskID}
	if rootID := p.store.RootProjectID(taskID); rootID != "" && rootID != taskID {
		checkIDs = append(checkIDs, rootID)
	}
	for _, id := range checkIDs {
		rec, err := p.store.Get(core.Ref{Kind: data.KindTask, ID: id})
		if err != nil || rec == nil {
			continue
		}
		if dir := strings.TrimSpace(rec.Text("defaultDir")); dir != "" {
			return dir
		}
		if rels, err := p.store.Related(rec.Ref(), data.RelDirectory, data.Outgoing); err == nil {
			for _, rel := range rels {
				if strings.TrimSpace(rel.To.ID) != "" {
					return strings.TrimSpace(rel.To.ID)
				}
			}
		}
	}
	return ""
}

// Snapshot reads the live terminal screen lines of pane.
func (p *PaneTools) Snapshot(ctx context.Context, args PaneSnapshotArgs) (PaneSnapshotResult, error) {
	id := strings.TrimSpace(args.Pane)
	if id == "" {
		return PaneSnapshotResult{}, errors.New("pane.snapshot: pane is required")
	}
	if p.Runtime == nil {
		return PaneSnapshotResult{}, errors.New("pane.snapshot: live daemon runtime not available")
	}
	snap, found, err := p.Runtime.Snapshot(ctx, id)
	if err != nil {
		return PaneSnapshotResult{}, err
	}
	lines := snap.Plain
	if args.Lines > 0 && len(lines) > args.Lines {
		lines = lines[len(lines)-args.Lines:]
	}
	return PaneSnapshotResult{
		PaneID:   id,
		Found:    found,
		Lines:    lines,
		Cols:     snap.Cols,
		Rows:     snap.Rows,
		Exited:   snap.Exited,
		ExitCode: snap.ExitCode,
	}, nil
}

// Send writes text (plus optional \r) to a live pane's PTY.
func (p *PaneTools) Send(ctx context.Context, args PaneSendArgs) (PaneSendResult, error) {
	id := strings.TrimSpace(args.Pane)
	if id == "" {
		return PaneSendResult{}, errors.New("pane.send: pane is required")
	}
	if p.Runtime == nil {
		return PaneSendResult{}, errors.New("pane.send: live daemon runtime not available")
	}
	payload := args.Text
	addEnter := args.Enter == nil || *args.Enter
	if addEnter {
		payload += "\r"
	}
	if err := p.Runtime.Write(ctx, id, []byte(payload)); err != nil {
		return PaneSendResult{}, err
	}
	return PaneSendResult{PaneID: id, Sent: len(payload)}, nil
}

// Status returns pane record, live PTY/hook status, linked task, and claiming lanes.
func (p *PaneTools) Status(ctx context.Context, args PaneStatusArgs) (PaneStatusResult, error) {
	id := strings.TrimSpace(args.Pane)
	if id == "" {
		return PaneStatusResult{}, errors.New("pane.status: pane is required")
	}
	res := PaneStatusResult{}
	if p.store != nil {
		if rec, err := p.store.Get(core.Ref{Kind: data.KindPane, ID: id}); err == nil && rec != nil {
			res.Pane = rec
			if tRef, ok := rec.RefField("task"); ok && tRef.Kind == data.KindTask && tRef.ID != "" {
				if tRec, err := p.store.Get(tRef); err == nil && tRec != nil {
					res.Task = tRec
				}
			}
			if rels, err := p.store.Related(rec.Ref(), data.RelClaim, data.Incoming); err == nil {
				for _, rel := range rels {
					if rel.From.Kind == data.KindLane {
						if lRec, err := p.store.Get(rel.From); err == nil && lRec != nil {
							res.Lanes = append(res.Lanes, *lRec)
						}
					}
				}
			}
		}
	}
	if p.Runtime != nil {
		st, err := p.Runtime.Status(ctx, id)
		if err != nil {
			return res, err
		}
		res.Runtime = st
	}
	return res, nil
}

// Close terminates a live PTY pane and marks its record exited.
func (p *PaneTools) Close(ctx context.Context, actor core.Actor, args PaneCloseArgs) (PaneCloseResult, error) {
	id := strings.TrimSpace(args.Pane)
	if id == "" {
		return PaneCloseResult{}, errors.New("pane.close: pane is required")
	}
	if p.Runtime == nil {
		return PaneCloseResult{}, errors.New("pane.close: live daemon runtime not available")
	}
	if err := p.Runtime.Close(ctx, id); err != nil {
		return PaneCloseResult{}, err
	}
	res := PaneCloseResult{PaneID: id, Closed: true}
	if p.store != nil {
		if updated, _, err := p.store.Patch(data.PatchInput{
			Ref:    core.Ref{Kind: data.KindPane, ID: id},
			Fields: map[string]any{"runStatus": "exited"},
		}, agentActor(actor, args.By)); err == nil && updated != nil {
			res.Pane = updated
		}
	}
	return res, nil
}
