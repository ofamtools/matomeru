package app

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/matomeru/harness"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/toast"
)

const (
	harnessesFileName  = "harnesses.json"
	daemonRPCTimeout   = 2 * time.Second
	oaiDiscoverTimeout = 6 * time.Second
)

// daemonSyncActor writes automatic daemon reconciliation/reset updates to the
// store without entering the owner's undo stack.
var daemonSyncActor = core.Actor{Kind: "system", ID: "mtm.daemon", Label: "daemon"}

// --- 1. Local daemon integration + daemon.reset ----------------------------------

func (a *App) daemonClient() *daemon.Client {
	if a.Daemon != nil {
		return a.Daemon
	}
	if ha, ok := a.Host.(*daemon.HostAdapter); ok {
		return ha.Client()
	}
	return nil
}

func (a *App) daemonMCPBase() string {
	a.live.telemetryMu.RLock()
	base := a.live.mcpURL
	a.live.telemetryMu.RUnlock()
	if base != "" {
		return base
	}
	client := a.daemonClient()
	if client == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(a.live.bgCtx, daemonRPCTimeout)
	defer cancel()
	st, err := client.Status(ctx)
	if err != nil || st == nil || st.MCPURL == "" {
		return ""
	}
	a.live.telemetryMu.Lock()
	a.live.mcpURL = st.MCPURL
	a.live.telemetryMu.Unlock()
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
	ctx, cancel := context.WithTimeout(a.live.bgCtx, daemonRPCTimeout)
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
			if a.Host != nil {
				if f, ok := a.Host.Snapshot(rec.ID); ok {
					a.live.frames[rec.ID] = f
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
		if a.Host != nil {
			if f, ok := a.Host.Snapshot(info.ID); ok {
				a.live.frames[info.ID] = f
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
	if a.Settings.Bool(SetConfirmDaemonReset, true) {
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
	return len(a.live.frames)
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
	if ha, ok := a.Host.(*daemon.HostAdapter); ok {
		ha.ClearCache()
	}
	a.live.frames = map[string]ptyhost.Frame{}
	a.live.shown = ""
	if a.Host != nil {
		a.Term = ptyhost.NewController(a.Host)
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
	a.activePage = map[string]string{}
	a.scratch = ""
	if s.ActiveFrame().Mode == ModeTerminal {
		s = withMode(s, ModeNormal)
	}
	if topModal(s) == modalScratch {
		s = closeModal(s, modalScratch)
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
	a.wantReload = true
	a.say("reloading Matomeru client...")
	return core.Next(s, core.QuitEffect{})
}

// --- 2. Harness catalog + async o-ai discovery -----------------------------------

func defaultInstalledHarnesses() []harness.InstalledHarness {
	defs := harness.DefaultConfigs()
	out := make([]harness.InstalledHarness, 0, len(defs))
	for id := range defs {
		if id == "antigravity" {
			if _, hasAgy := defs["agy"]; hasAgy {
				continue
			}
		}
		out = append(out, harness.InstalledHarness{ID: id, Installed: true})
	}
	return out
}

func (a *App) loadHarnesses(configDir string) {
	var cfg harness.FileConfig
	if configDir != "" {
		loaded, err := harness.LoadFile(filepath.Join(configDir, harnessesFileName))
		if err != nil {
			a.warn("%s: %v", harnessesFileName, err)
		} else {
			cfg = loaded
		}
	}
	entries := harness.MergeInstalled(defaultInstalledHarnesses(), cfg)
	a.harnessMu.Lock()
	a.harnessCfg = cfg
	a.harnessEntries = entries
	a.harnessMu.Unlock()
}

// Harnesses returns a copy of the current merged harness catalog entries.
func (a *App) Harnesses() []harness.Entry {
	a.harnessMu.RLock()
	defer a.harnessMu.RUnlock()
	return slices.Clone(a.harnessEntries)
}

// SetInstalledHarnesses merges installed inventory from o-ai with harnesses.json
// and updates the app's harness catalog.
func (a *App) SetInstalledHarnesses(installed []harness.InstalledHarness) []harness.Entry {
	a.harnessMu.Lock()
	merged := harness.MergeInstalled(installed, a.harnessCfg)
	a.harnessEntries = merged
	a.harnessMu.Unlock()
	if a.form != nil && a.form.kind == data.KindPane {
		a.applyHarnessChoices(a.form)
	}
	a.gen++
	return slices.Clone(merged)
}

func (a *App) lookupHarness(name string) (harness.Entry, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return harness.Entry{}, false
	}
	alias := harness.AliasID(want)
	a.harnessMu.RLock()
	defer a.harnessMu.RUnlock()
	for _, e := range a.harnessEntries {
		if strings.EqualFold(e.ID, want) || strings.EqualFold(e.Name, want) {
			return e, true
		}
	}
	if alias != "" {
		for _, e := range a.harnessEntries {
			if strings.EqualFold(e.ID, alias) || strings.EqualFold(e.Name, alias) {
				return e, true
			}
		}
	}
	return harness.Entry{}, false
}

func (a *App) harnessChoices(current string) []string {
	entries := a.Harnesses()
	choices := make([]string, 0, len(entries)+1)
	for _, e := range entries {
		if e.ID != "" && !slices.Contains(choices, e.ID) {
			choices = append(choices, e.ID)
		}
	}
	if cur := strings.TrimSpace(current); cur != "" && !slices.Contains(choices, cur) {
		choices = append(choices, cur)
	}
	return choices
}

func (a *App) defaultDirForTask(taskRef core.Ref) string {
	if a.Store == nil || taskRef.Kind != data.KindTask || taskRef.ID == "" {
		return ""
	}
	cur, err := a.Store.Get(taskRef)
	if err != nil || cur == nil {
		return ""
	}
	seen := map[string]bool{cur.ID: true}
	for range 32 {
		if dir := strings.TrimSpace(cur.Text("defaultDir")); dir != "" {
			return dir
		}
		parentRef, hasParent := cur.RefField("parent")
		if !hasParent || parentRef.Kind != data.KindTask || parentRef.ID == "" || seen[parentRef.ID] {
			break
		}
		parent, err := a.Store.Get(parentRef)
		if err != nil || parent == nil {
			break
		}
		seen[parent.ID] = true
		cur = parent
	}
	return ""
}

func (a *App) applyHarnessChoices(fs *formState) {
	if fs == nil || fs.fm == nil || fs.kind != data.KindPane {
		return
	}
	wasDirty := fs.fm.Dirty
	current := data.Text(fs.fm.Values["harness"])
	choices := a.harnessChoices(current)
	if len(choices) > 0 {
		for i := range fs.fm.Specs {
			if fs.fm.Specs[i].Name != "harness" {
				continue
			}
			fs.fm.Specs[i].Enum = choices
			if fs.ref == nil && strings.TrimSpace(current) == "" {
				fs.fm.Specs[i].Default = choices[0]
				fs.fm.SetValue("harness", choices[0])
			}
			break
		}
	}
	// Pre-fill cwd on New Pane forms from the selected task/project's defaultDir,
	// falling back to os.Getwd() so the user always sees the launch directory.
	if fs.ref == nil && strings.TrimSpace(data.Text(fs.fm.Values["cwd"])) == "" {
		dir := ""
		if taskRef, ok := data.AsRef(fs.fm.Values["task"]); ok {
			dir = a.defaultDirForTask(taskRef)
		}
		if dir == "" {
			if wd, err := os.Getwd(); err == nil {
				dir = wd
			}
		}
		if dir != "" {
			fs.fm.SetValue("cwd", dir)
		}
	}
	fs.fm.Dirty = wasDirty
}

func findOAIBinary(searchDirs []string, storeBinary string) string {
	if storeBinary != "" {
		if info, err := os.Stat(storeBinary); err == nil && !info.IsDir() {
			return storeBinary
		}
	}
	for _, dir := range searchDirs {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, "o-ai")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("o-ai"); err == nil {
		return p
	}
	return ""
}

func installedFromRecords(items []data.Record) []harness.InstalledHarness {
	out := make([]harness.InstalledHarness, 0, len(items))
	for _, rec := range items {
		id := rec.ID
		if v := rec.Text("id"); v != "" {
			id = v
		}
		out = append(out, harness.InstalledHarness{
			ID:            id,
			Installed:     rec.Bool("installed"),
			Configured:    rec.Bool("configured"),
			Executable:    rec.Text("executable"),
			ProcessStatus: rec.Text("processStatus"),
			CatalogStatus: rec.Text("catalogStatus"),
		})
	}
	return out
}

// startHarnessDiscovery runs one async background goroutine (tracked by
// a.live.bg, never blocking the UI loop) that discovers installed harnesses
// from cached ai.harness records or `o-ai query --kind ai.harness` and merges
// them with harnesses.json via harness.MergeInstalled.
func (a *App) startHarnessDiscovery() {
	dirs := append(slices.Clone(a.opts.ExtensionDirs), a.Settings.Strings("extensionDirs", nil)...)
	for i, d := range dirs {
		dirs[i] = expandHome(d)
	}
	storeBinary := ""
	if a.Store != nil {
		if rec, err := a.Store.GetByField(data.KindExtension, "name", "o-ai"); err == nil && rec != nil {
			storeBinary = rec.Text("binary")
		}
	}
	a.harnessMu.RLock()
	cfg := a.harnessCfg
	a.harnessMu.RUnlock()
	cache := a.readCache

	a.background(func(ctx context.Context) func(core.State) core.State {
		var installed []harness.InstalledHarness
		if cache != nil {
			if snap, ok := cache.Get("ai.harness"); ok && len(snap.Items) > 0 {
				installed = installedFromRecords(snap.Items)
			}
		}
		if len(installed) == 0 {
			bin := findOAIBinary(dirs, storeBinary)
			if bin == "" {
				return nil
			}
			pctx, cancel := context.WithTimeout(ctx, oaiDiscoverTimeout)
			defer cancel()
			out, err := exec.CommandContext(pctx, bin, "query", "--kind", "ai.harness").Output()
			if err != nil {
				return nil
			}
			parsed, err := harness.ParseOAIJSON(out)
			if err != nil || len(parsed) == 0 {
				return nil
			}
			installed = parsed
		}
		merged := harness.MergeInstalled(installed, cfg)
		return func(s core.State) core.State {
			a.harnessMu.Lock()
			a.harnessEntries = merged
			a.harnessMu.Unlock()
			if a.form != nil && a.form.kind == data.KindPane {
				a.applyHarnessChoices(a.form)
			}
			a.gen++
			return s
		}
	})
}

func shellJoin(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	parts := make([]string, len(argv))
	for i, arg := range argv {
		if arg == "" || strings.ContainsAny(arg, " \t\n\r'\"$`\\;&|<>()*?[]{}~#!") {
			parts[i] = handlers.Quote(arg)
		} else {
			parts[i] = arg
		}
	}
	return strings.Join(parts, " ")
}

// buildPaneLaunch resolves the command and extra environment variables for
// launching pane. If pane's harness matches a catalog entry, BuildLaunchCommand
// supplies the launch command (when pane.command is empty) and entry.Env is
// merged into the returned env map.
func (a *App) buildPaneLaunch(pane data.Record) (string, map[string]string) {
	cmd := pane.Text("command")
	env := map[string]string{}
	if entry, ok := a.lookupHarness(pane.Text("harness")); ok {
		argv, hEnv := harness.BuildLaunchCommand(entry, a.paneMCPURL(pane.ID), pane.ID)
		maps.Copy(env, hEnv)
		if cmd == "" && len(argv) > 0 {
			cmd = shellJoin(argv)
		}
	}
	return cmd, env
}

// --- 3. Live hook telemetry + blocking approvals ---------------------------------

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
	a.live.telemetryMu.Lock()
	if fp == a.live.telemetryFP && a.live.telemetry != nil {
		a.live.telemetryMu.Unlock()
		return false
	}
	a.live.telemetry = res
	a.live.telemetryFP = fp
	seq := a.live.telemetrySeq.Add(1)
	if markDrawn {
		a.live.telemetryDrawn = seq
	}
	a.live.telemetryMu.Unlock()
	return true
}

// pollTelemetry fetches daemon hook telemetry and updates the cache only when
// the visible state changed.
func (a *App) pollTelemetry() bool {
	client := a.daemonClient()
	if client == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(a.live.bgCtx, daemonRPCTimeout)
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
	a.live.telemetryMu.RLock()
	defer a.live.telemetryMu.RUnlock()
	if a.live.telemetry == nil {
		return nil
	}
	return a.live.telemetry.Telemetry[paneID]
}

func (a *App) pendingApprovalsFor(paneID string) []*daemon.PendingApproval {
	a.live.telemetryMu.RLock()
	defer a.live.telemetryMu.RUnlock()
	if a.live.telemetry == nil {
		return nil
	}
	var out []*daemon.PendingApproval
	for _, p := range a.live.telemetry.PendingApprovals {
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

// --- 4. Schedule pause / resume --------------------------------------------------

func (a *App) schedulePause(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindSchedule {
		a.say("no schedule here to pause")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("pause schedule", err)
		return core.Next(s)
	}
	if rec.Bool("paused") {
		a.say("%s is already paused", a.title(ref))
		return core.Next(s)
	}
	_, cs, err := a.Store.Patch(data.PatchInput{
		Ref:     ref,
		Fields:  map[string]any{"paused": true},
		Summary: "pause schedule",
	}, User)
	a.wrote("paused "+a.title(ref), cs, err)
	if err == nil {
		a.pushToast("Paused "+a.title(ref), toast.Info)
	}
	return core.Next(s)
}

func (a *App) scheduleResume(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindSchedule {
		a.say("no schedule here to resume")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("resume schedule", err)
		return core.Next(s)
	}
	fields := map[string]any{"paused": false}
	if _, hasEnabled := rec.Value("enabled"); hasEnabled && !rec.Bool("enabled") {
		fields["enabled"] = true
	}
	switch strings.ToLower(strings.TrimSpace(rec.Text("status"))) {
	case "paused", "disabled", "stopped":
		fields["status"] = "active"
	}
	if !rec.Bool("paused") && len(fields) == 1 {
		a.say("%s is already active", a.title(ref))
		return core.Next(s)
	}
	_, cs, err := a.Store.Patch(data.PatchInput{
		Ref:     ref,
		Fields:  fields,
		Summary: "resume schedule",
	}, User)
	a.wrote("resumed "+a.title(ref), cs, err)
	if err == nil {
		a.pushToast("Resumed "+a.title(ref), toast.Ok)
	}
	return core.Next(s)
}
