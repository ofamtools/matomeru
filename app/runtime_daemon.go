package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/harness"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/matomeru/runs"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/toast"
)

// RuntimeController owns live execution runtime state: command runner, daemon
// client, watcher/live frames, harness discovery, toasts, and reload flag.
type RuntimeController struct {
	Runner            *runs.Runner
	Daemon            *daemon.Client
	live              liveState
	harnessMu         sync.RWMutex
	harnessCfg        harness.FileConfig
	harnessEntries    []harness.Entry
	toasts            *toast.Model
	wantReload        bool
	uxNoteSessionPath string
	uxNoteCount       int
}

func newRuntimeController() *RuntimeController {
	return &RuntimeController{}
}

const daemonRPCTimeout = 2 * time.Second

// daemonSyncActor writes automatic daemon reconciliation/reset updates to the
// store without entering the owner's undo stack.
var daemonSyncActor = core.Actor{Kind: "system", ID: "mtm.daemon", Label: "daemon"}

// --- 1. Local daemon integration + daemon.reset ----------------------------------

func (a *App) daemonClient() *daemon.Client {
	if a.rt.Daemon != nil {
		return a.rt.Daemon
	}
	if ha, ok := a.panes.Host.(*daemon.HostAdapter); ok {
		return ha.Client()
	}
	return nil
}

func (a *App) daemonMCPBase() string {
	a.rt.live.telemetryMu.RLock()
	base := a.rt.live.mcpURL
	a.rt.live.telemetryMu.RUnlock()
	if base != "" {
		return base
	}
	client := a.daemonClient()
	if client == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(a.rt.live.bgCtx, daemonRPCTimeout)
	defer cancel()
	st, err := client.Status(ctx)
	if err != nil || st == nil || st.MCPURL == "" {
		return ""
	}
	a.rt.live.telemetryMu.Lock()
	a.rt.live.mcpURL = st.MCPURL
	a.rt.live.telemetryMu.Unlock()
	return st.MCPURL
}

func (a *App) paneMCPURL(paneID string) string {
	base := strings.TrimRight(a.daemonMCPBase(), "/")
	if base == "" || paneID == "" {
		return ""
	}
	return base + "/mcp/" + url.PathEscape(paneID)
}

// reconcileDaemonPanes reconciles store pane records against live panes on the
// local daemon during live startup. Surviving live panes remain active and
// attached; when the daemon has 0 live panes (for example after a reset), stale
// active pane records are archived so startup opens cleanly with zero pre-listed
// panes.
func (a *App) reconcileDaemonPanes() {
	client := a.daemonClient()
	if client == nil || a.Store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(a.rt.live.bgCtx, daemonRPCTimeout)
	defer cancel()
	livePanes, err := client.List(ctx)
	if err != nil {
		return
	}
	liveByID := make(map[string]daemon.PaneInfo, len(livePanes))
	for _, p := range livePanes {
		liveByID[p.ID] = p
	}

	res, err := a.Store.Query(data.Query{Kind: data.KindPane})
	if err != nil {
		return
	}
	seen := make(map[string]bool, len(res.Items))
	for _, rec := range res.Items {
		seen[rec.ID] = true
		if _, alive := liveByID[rec.ID]; alive {
			if a.panes.Host != nil {
				if f, ok := a.panes.Host.Snapshot(rec.ID); ok {
					a.rt.live.frames[rec.ID] = f
				}
			}
			continue
		}
		staleLive := len(liveByID) == 0 ||
			rec.Text("runStatus") == "running" ||
			rec.Text("runStatus") == "waiting" ||
			rec.Text("pid") != ""
		if !staleLive {
			continue
		}
		if rec.Text("runStatus") != "exited" {
			_, _, _ = a.Store.Patch(data.PatchInput{
				Ref:     rec.Ref(),
				Fields:  map[string]any{"runStatus": "exited"},
				Summary: "reconcile exited daemon pane",
			}, daemonSyncActor)
		}
		_, _, _ = a.Store.Archive(rec.Ref(), daemonSyncActor)
	}

	// Ensure surviving live daemon panes that are absent from the store are
	// visible in 1 Panes.
	for _, info := range livePanes {
		if seen[info.ID] {
			continue
		}
		if _, gerr := a.Store.Get(core.Ref{Kind: data.KindPane, ID: info.ID}); gerr == nil {
			continue // exists as archived; leave archived
		}
		title := info.Title
		if title == "" {
			title = info.Cmd
		}
		if title == "" {
			title = info.ID
		}
		status := "running"
		if info.Exited {
			status = "exited"
		}
		fields := map[string]any{
			"title":     title,
			"runStatus": status,
		}
		if info.Cmd != "" {
			fields["command"] = info.Cmd
		}
		if info.Cwd != "" {
			fields["cwd"] = info.Cwd
		}
		if info.PID > 0 {
			fields["pid"] = info.PID
		}
		_, _, _ = a.Store.Create(data.CreateInput{Kind: data.KindPane, ID: info.ID, Fields: fields}, daemonSyncActor)
		if a.panes.Host != nil {
			if f, ok := a.panes.Host.Snapshot(info.ID); ok {
				a.rt.live.frames[info.ID] = f
			}
		}
	}
}

// daemonReset prompts via the shared confirmation modal when confirm.daemonReset
// is enabled, then kills all panes on the local daemon, clears local frame and
// telemetry caches, archives the dropped pane records in the store, and
// reports a confirmation message.
func (a *App) daemonReset(s core.State, _ core.Command) core.StepResult {
	client := a.daemonClient()
	if client == nil {
		a.say("no local daemon is connected")
		return core.Next(s)
	}
	if a.cfg.Settings.Bool(SetConfirmDaemonReset, true) {
		liveCount := a.liveDaemonPaneCount(client)
		return core.Next(a.openConfirm(s, modal.ConfirmOptions{
			Title:        "Reset Daemon",
			Message:      fmt.Sprintf("Drop all %d live daemon panes and reset the daemon? This cannot be undone.", liveCount),
			ConfirmLabel: "Confirm",
			OnConfirm: func() modal.Result {
				a.afterModal(func(st core.State) core.State {
					return a.execDaemonReset(st)
				})
				return modal.Result{}
			},
		}))
	}
	return core.Next(a.execDaemonReset(s))
}

func (a *App) liveDaemonPaneCount(client *daemon.Client) int {
	if client == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), daemonRPCTimeout)
	defer cancel()
	if panes, err := client.List(ctx); err == nil {
		return len(panes)
	}
	return len(a.rt.live.frames)
}

func (a *App) execDaemonReset(s core.State) core.State {
	client := a.daemonClient()
	if client == nil {
		a.say("no local daemon is connected")
		return s
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Reset(ctx); err != nil {
		a.fail("daemon reset", err)
		return s
	}
	if ha, ok := a.panes.Host.(*daemon.HostAdapter); ok {
		ha.ClearCache()
	}
	a.rt.live.frames = map[string]ptyhost.Frame{}
	a.rt.live.shown = ""
	if a.panes.Host != nil {
		a.panes.Term = ptyhost.NewController(a.panes.Host)
	}
	a.setTelemetry(&daemon.TelemetryResult{
		Telemetry:        map[string]*daemon.PaneTelemetry{},
		PendingApprovals: []*daemon.PendingApproval{},
	}, true)

	dropped := 0
	if res, err := a.Store.Query(data.Query{Kind: data.KindPane}); err == nil {
		for _, rec := range res.Items {
			if rec.Text("runStatus") != "exited" {
				_, _, _ = a.Store.Patch(data.PatchInput{
					Ref:     rec.Ref(),
					Fields:  map[string]any{"runStatus": "exited"},
					Summary: "daemon reset",
				}, daemonSyncActor)
			}
			if _, _, aerr := a.Store.Archive(rec.Ref(), daemonSyncActor); aerr == nil {
				dropped++
			}
		}
	}
	a.panes.activePage = map[string]string{}
	if a.activeScratch() != nil {
		s, _ = a.modals.Close(s, "scratch", modal.CloseCancel)
	}
	if s.ActiveFrame().Mode == ModeTerminal {
		s = withMode(s, ModeNormal)
	}
	msg := fmt.Sprintf("daemon reset · %d pane(s) cleared", dropped)
	a.say("%s", msg)
	a.pushToast(msg, toast.Warn)
	return s
}

// daemonUpgrade re-execs the local daemon binary in place without killing any
// live PTY panes, child processes, or subagents.
func (a *App) daemonUpgrade(s core.State, _ core.Command) core.StepResult {
	client := a.daemonClient()
	if client == nil {
		a.say("no local daemon is connected")
		a.pushToast("no local daemon is connected", toast.Warn)
		return core.Next(s)
	}
	bin := ""
	if len(os.Args) > 0 && os.Args[0] != "" {
		if p, err := exec.LookPath(os.Args[0]); err == nil {
			bin = p
		}
	}
	if bin == "" {
		if exe, err := os.Executable(); err == nil {
			bin = exe
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := client.Upgrade(ctx, bin)
	if err != nil {
		a.fail("daemon upgrade", err)
		a.pushToast("daemon upgrade: "+err.Error(), toast.Err)
		return core.Next(s)
	}
	msg := fmt.Sprintf("daemon upgraded · pid %d · %d pane(s) preserved", res.PID, res.PaneCount)
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
	return core.Next(s)
}

// appReload marks the client for in-place binary re-exec and exits Bubble Tea
// cleanly without touching the daemon or any live panes.
func (a *App) appReload(s core.State, _ core.Command) core.StepResult {
	a.rt.wantReload = true
	a.say("reloading Matomeru client...")
	return core.Next(s, core.QuitEffect{})
}

// --- 2. Live hook telemetry + blocking approvals ---------------------------------

func telemetryFingerprint(res *daemon.TelemetryResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	ids := make([]string, 0, len(res.Telemetry))
	for id := range res.Telemetry {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		pt := res.Telemetry[id]
		if pt == nil {
			continue
		}
		fmt.Fprintf(&b, "t:%s|%s|%s|%s|%d|%s\n", id, pt.State, pt.Tool, pt.Summary, pt.SubagentCount, pt.Harness)
	}
	for _, p := range res.PendingApprovals {
		if p == nil {
			continue
		}
		fmt.Fprintf(&b, "a:%s|%s|%s|%s|%s\n", p.RequestID, p.PaneID, p.Tool, p.Summary, p.Harness)
	}
	return b.String()
}

func (a *App) setTelemetry(res *daemon.TelemetryResult, markDrawn bool) bool {
	fp := telemetryFingerprint(res)
	a.rt.live.telemetryMu.Lock()
	if fp == a.rt.live.telemetryFP && a.rt.live.telemetry != nil {
		a.rt.live.telemetryMu.Unlock()
		return false
	}
	a.rt.live.telemetry = res
	a.rt.live.telemetryFP = fp
	seq := a.rt.live.telemetrySeq.Add(1)
	if markDrawn {
		a.rt.live.telemetryDrawn = seq
	}
	a.rt.live.telemetryMu.Unlock()
	return true
}

// pollTelemetry fetches daemon hook telemetry and updates the cache only when
// the visible state changed.
func (a *App) pollTelemetry() bool {
	client := a.daemonClient()
	if client == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(a.rt.live.bgCtx, daemonRPCTimeout)
	defer cancel()
	res, err := client.Telemetry(ctx, "")
	if err != nil {
		return false
	}
	return a.setTelemetry(res, false)
}

func (a *App) refreshTelemetry() {
	client := a.daemonClient()
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), daemonRPCTimeout)
	defer cancel()
	res, err := client.Telemetry(ctx, "")
	if err != nil {
		return
	}
	a.setTelemetry(res, true)
	a.flushAllQueuedPrompts()
}

func (a *App) paneTelemetry(paneID string) *daemon.PaneTelemetry {
	a.rt.live.telemetryMu.RLock()
	defer a.rt.live.telemetryMu.RUnlock()
	if a.rt.live.telemetry == nil {
		return nil
	}
	return a.rt.live.telemetry.Telemetry[paneID]
}

func (a *App) pendingApprovalsFor(paneID string) []*daemon.PendingApproval {
	a.rt.live.telemetryMu.RLock()
	defer a.rt.live.telemetryMu.RUnlock()
	if a.rt.live.telemetry == nil {
		return nil
	}
	var out []*daemon.PendingApproval
	for _, p := range a.rt.live.telemetry.PendingApprovals {
		if p != nil && p.PaneID == paneID {
			out = append(out, p)
		}
	}
	return out
}

func (a *App) paneHasAttention(paneID string) bool {
	if len(a.pendingApprovalsFor(paneID)) > 0 {
		return true
	}
	if pt := a.paneTelemetry(paneID); pt != nil && pt.State == daemon.StateNeedsYou {
		return true
	}
	return false
}

func (a *App) effectiveHookState(paneID string) (string, *daemon.PaneTelemetry, []*daemon.PendingApproval) {
	pt := a.paneTelemetry(paneID)
	pending := a.pendingApprovalsFor(paneID)
	state := ""
	if pt != nil {
		state = pt.State
	}
	if len(pending) > 0 {
		state = daemon.StateNeedsYou
	}
	return state, pt, pending
}

func (a *App) decorateRailRow(row *table.Row, pane data.Record) {
	state, pt, pending := a.effectiveHookState(pane.ID)
	if len(pending) > 0 {
		row.Facts["hasPendingApproval"] = true
	}
	if state == "" && pt == nil {
		return
	}
	if state != "" {
		paneSchema := a.schema(data.KindPane)
		icon, label, tone, rowTone := views.StatusIndicator(paneSchema, "runStatus", state)
		styleOpts := a.styleOptions()
		row.Cells["runStatus"] = label
		row.Facts["hookState"] = state
		row.Facts["statusTone"] = tone
		if styleOpts.StatusIcons && icon != "" {
			if row.CellIcons == nil {
				row.CellIcons = map[string]string{}
			}
			row.CellIcons["runStatus"] = icon
		} else if row.CellIcons != nil {
			delete(row.CellIcons, "runStatus")
		}
		if styleOpts.RowSemanticStyling {
			if rowTone != "" && !pane.IsArchived() {
				row.Tone = rowTone
			}
			if tone != "" {
				if row.CellTones == nil {
					row.CellTones = map[string]string{}
				}
				row.CellTones["runStatus"] = tone
				if styleOpts.StatusIcons && icon != "" {
					if row.IconTones == nil {
						row.IconTones = map[string]string{}
					}
					row.IconTones["runStatus"] = tone
				}
			}
		}
	}
	var extras []string
	if pt != nil && pt.Tool != "" {
		extras = append(extras, pt.Tool)
	}
	if pt != nil && pt.SubagentCount > 0 {
		extras = append(extras, fmt.Sprintf("+%d", pt.SubagentCount))
	}
	if len(extras) > 0 {
		if cur, ok := row.Cells["rail"].(string); ok {
			row.Cells["rail"] = cur + " [" + strings.Join(extras, " ") + "]"
		}
	}
}

func (a *App) paneHeaderStrip(pane data.Record) (ui.ViewNode, bool) {
	state, pt, pending := a.effectiveHookState(pane.ID)
	if state == "" && pt == nil && len(pending) == 0 {
		return ui.ViewNode{}, false
	}
	var parts []string
	if state != "" {
		parts = append(parts, "Hook: "+state)
	} else {
		parts = append(parts, "Hook: active")
	}
	if pt != nil && pt.Tool != "" {
		parts = append(parts, "tool: "+pt.Tool)
	}
	if pt != nil && pt.SubagentCount > 0 {
		label := "subagents"
		if pt.SubagentCount == 1 {
			label = "subagent"
		}
		parts = append(parts, fmt.Sprintf("%d %s", pt.SubagentCount, label))
	}
	if pt != nil && pt.Summary != "" {
		parts = append(parts, pt.Summary)
	}
	_, _, tone, _ := views.StatusIndicator(a.schema(data.KindPane), "runStatus", state)
	return line("site.telemetry", " "+strings.Join(parts, " · "), tone), true
}

func (a *App) paneApprovalBanner(pane data.Record) (ui.ViewNode, bool) {
	pending := a.pendingApprovalsFor(pane.ID)
	if len(pending) == 0 {
		return ui.ViewNode{}, false
	}
	appr := pending[0]
	title := "Permission Request"
	if len(pending) > 1 {
		title = fmt.Sprintf("Permission Request (%d pending)", len(pending))
	}
	tool := appr.Tool
	if tool == "" {
		tool = "tool"
	}
	detail := "Tool: " + tool
	if appr.Summary != "" {
		detail += " · " + appr.Summary
	}
	return ui.ViewNode{
		ID:     "site.approval",
		Kind:   ui.KindPanel,
		Border: true,
		Tone:   "warning",
		Title:  title,
		Box:    layout.Constraints{FixedH: 4},
		Badges: []ui.Badge{{Text: "needs_you", Tone: "warning"}},
		Children: []ui.ViewNode{
			line("site.approval.detail", detail, "warning"),
			line("site.approval.hint", "Y approve (pane.approve) · D deny (pane.deny)", "muted"),
		},
	}, true
}

func (a *App) paneResolveApproval(decision string) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		client := a.daemonClient()
		if client == nil {
			a.say("no local daemon is connected")
			return core.Next(s)
		}
		reqID := c.Arg("requestId")
		var appr *daemon.PendingApproval
		if reqID == "" {
			paneID := ""
			if ref, ok := a.subject(s, c); ok && ref.Kind == data.KindPane {
				paneID = ref.ID
			} else if pane, ok := a.SelectedPane(); ok {
				paneID = pane.ID
			}
			if paneID == "" {
				a.say("no pane is selected")
				return core.Next(s)
			}
			pending := a.pendingApprovalsFor(paneID)
			if len(pending) == 0 {
				a.refreshTelemetry()
				pending = a.pendingApprovalsFor(paneID)
			}
			if len(pending) == 0 {
				a.say("no pending approval for this pane")
				return core.Next(s)
			}
			appr = pending[0]
			reqID = appr.RequestID
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.ResolveApproval(ctx, reqID, decision, c.Arg("reason")); err != nil {
			a.fail(decision+" approval", err)
			return core.Next(s)
		}
		a.refreshTelemetry()
		verb, level := "approved", toast.Ok
		if decision == daemon.DecisionDeny {
			verb, level = "denied", toast.Warn
		}
		if appr != nil && appr.Tool != "" {
			a.say("%s %s (%s)", verb, appr.Tool, reqID)
			a.pushToast(fmt.Sprintf("%s %s", verb, appr.Tool), level)
		} else {
			a.say("%s approval %s", verb, reqID)
			a.pushToast(fmt.Sprintf("%s approval", verb), level)
		}
		return core.Next(s)
	}
}
