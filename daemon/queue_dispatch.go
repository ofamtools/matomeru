package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// ServerQueueDispatcher delivers queued agent tasks to an idle live agent pane
// hosted by a daemon *Server, falling back to an actionable signal + desktop
// notification when no idle live agent pane is available.
type ServerQueueDispatcher struct {
	srv *Server
}

var _ QueueDispatcher = (*ServerQueueDispatcher)(nil)

// NewServerQueueDispatcher constructs a QueueDispatcher backed by srv.
func NewServerQueueDispatcher(srv *Server) *ServerQueueDispatcher {
	return &ServerQueueDispatcher{srv: srv}
}

// DispatchQueue implements QueueDispatcher.
func (d *ServerQueueDispatcher) DispatchQueue(ctx context.Context, sched data.Record, items []data.AgentQueueItem, prompt string) (string, error) {
	if d == nil || d.srv == nil {
		return "", errors.New("daemon: queue dispatcher has no server")
	}
	if len(items) == 0 {
		return "no queued tasks", nil
	}

	targetID, targetHarness, waitReason := d.resolveTargetPane(sched)
	if targetID == "" {
		return d.emitWaitingSignal(ctx, sched, items, waitReason)
	}

	if err := d.srv.host.Write(targetID, bracketedPastePrompt(prompt)); err != nil {
		return "", fmt.Errorf("daemon: write agent queue prompt to pane %s: %w", targetID, err)
	}
	if d.srv.cfg.Store != nil {
		ids := make([]string, 0, len(items))
		for _, it := range items {
			if it.Task.ID != "" {
				ids = append(ids, it.Task.ID)
			}
		}
		_, _ = d.srv.cfg.Store.MarkTasksDispatched(ids, targetID, d.srv.clock(), daemonActor)
	}

	detail := fmt.Sprintf("dispatched %s to pane %s", formatQueuedTaskCount(len(items)), targetID)
	d.recordDispatchAudit(targetID, targetHarness, detail)
	return detail, nil
}

type queuePaneCandidate struct {
	id          string
	harness     string
	projectRank int // 3=exact task match, 2=project subtree match, 1=unbound/general
	stateRank   int // 2=idle/done, 1=empty/unknown
	recentAt    time.Time
}

func (d *ServerQueueDispatcher) resolveTargetPane(sched data.Record) (paneID string, harness string, waitReason string) {
	tel, _ := d.srv.telemetry.snapshot("")
	storePanes := d.loadStorePanes()

	// Priority 1: Explicit sched.RefField("pane").
	if paneRef, ok := sched.RefField("pane"); ok && paneRef.Kind == data.KindPane && strings.TrimSpace(paneRef.ID) != "" {
		id := strings.TrimSpace(paneRef.ID)
		if !d.isPaneLive(id) {
			return "", "", fmt.Sprintf("Target pane %s is not live.", id)
		}
		pt := tel[id]
		rec, hasRec := storePanes[id]
		var recPtr *data.Record
		if hasRec {
			recPtr = &rec
		}
		if !isPaneAvailableForDispatch(pt, recPtr) {
			return "", "", fmt.Sprintf("Target pane %s is busy.", id)
		}
		return id, effectivePaneHarness(pt, recPtr), ""
	}

	// Priority 2: Scan live non-shell agent panes.
	wantHarness := strings.TrimSpace(sched.Text("harness"))
	schedTaskID := ""
	if taskRef, ok := sched.RefField("task"); ok && taskRef.Kind == data.KindTask {
		schedTaskID = strings.TrimSpace(taskRef.ID)
	}
	var tasksByID map[string]data.Record
	if schedTaskID != "" {
		tasksByID = d.loadAllTasks()
	}

	liveIDs, metaStarted := d.candidateLivePaneIDs(storePanes)
	var candidates []queuePaneCandidate
	for _, id := range liveIDs {
		pt := tel[id]
		rec, hasRec := storePanes[id]
		var recPtr *data.Record
		if hasRec {
			recPtr = &rec
		}
		if !isPaneAvailableForDispatch(pt, recPtr) {
			continue
		}
		if recPtr != nil && isNonAgentPaneRole(recPtr.Text("role")) {
			continue
		}
		h := effectivePaneHarness(pt, recPtr)
		if h == "" || isShellOrEditorHarness(h) {
			continue
		}
		if wantHarness != "" && !strings.EqualFold(h, wantHarness) {
			continue
		}

		projectRank := 1
		if schedTaskID != "" {
			paneTaskID := ""
			if recPtr != nil {
				if tref, ok := recPtr.RefField("task"); ok && tref.Kind == data.KindTask {
					paneTaskID = strings.TrimSpace(tref.ID)
				}
			}
			switch {
			case paneTaskID == schedTaskID:
				projectRank = 3
			case paneTaskID != "" && taskWithinSubtree(paneTaskID, schedTaskID, tasksByID):
				projectRank = 2
			case paneTaskID == "":
				projectRank = 1
			default:
				// Pane is explicitly bound to a different project/task; do not hijack it.
				continue
			}
		}

		stateRank := 1
		if pt != nil && (pt.State == StateIdle || pt.State == StateDone) {
			stateRank = 2
		} else if recPtr != nil {
			rs := strings.ToLower(strings.TrimSpace(recPtr.Text("runStatus")))
			if rs == StateIdle || rs == StateDone {
				stateRank = 2
			}
		}

		recent := metaStarted[id]
		if recPtr != nil {
			if pAt := recPtr.Time("promptedAt"); pAt.After(recent) {
				recent = pAt
			}
		}
		if pt != nil && pt.LastEventAt.After(recent) {
			recent = pt.LastEventAt
		}

		candidates = append(candidates, queuePaneCandidate{
			id:          id,
			harness:     h,
			projectRank: projectRank,
			stateRank:   stateRank,
			recentAt:    recent,
		})
	}

	if len(candidates) == 0 {
		return "", "", "No idle live agent pane available."
	}

	slices.SortFunc(candidates, func(a, b queuePaneCandidate) int {
		if a.projectRank != b.projectRank {
			return b.projectRank - a.projectRank
		}
		if a.stateRank != b.stateRank {
			return b.stateRank - a.stateRank
		}
		if c := b.recentAt.Compare(a.recentAt); c != 0 {
			return c
		}
		return strings.Compare(a.id, b.id)
	})
	best := candidates[0]
	return best.id, best.harness, ""
}

func (d *ServerQueueDispatcher) isPaneLive(id string) bool {
	if id == "" || d.srv.host == nil {
		return false
	}
	f, ok := d.srv.host.Snapshot(id)
	return ok && !f.Exited
}

func (d *ServerQueueDispatcher) candidateLivePaneIDs(storePanes map[string]data.Record) ([]string, map[string]time.Time) {
	seen := map[string]bool{}
	started := map[string]time.Time{}

	d.srv.mu.Lock()
	for id, m := range d.srv.panes {
		seen[id] = true
		if m != nil {
			started[id] = m.StartedAt
		}
	}
	d.srv.mu.Unlock()

	for id := range storePanes {
		seen[id] = true
	}

	out := make([]string, 0, len(seen))
	for id := range seen {
		if d.isPaneLive(id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out, started
}

func (d *ServerQueueDispatcher) loadStorePanes() map[string]data.Record {
	out := map[string]data.Record{}
	if d.srv.cfg.Store == nil {
		return out
	}
	res, err := d.srv.cfg.Store.Query(data.Query{Kind: data.KindPane})
	if err != nil {
		return out
	}
	for _, rec := range res.Items {
		out[rec.ID] = rec
	}
	return out
}

func (d *ServerQueueDispatcher) loadAllTasks() map[string]data.Record {
	out := map[string]data.Record{}
	if d.srv.cfg.Store == nil {
		return out
	}
	res, err := d.srv.cfg.Store.Query(data.Query{Kind: data.KindTask, IncludeArchived: true})
	if err != nil {
		return out
	}
	for _, rec := range res.Items {
		out[rec.ID] = rec
	}
	return out
}

func (d *ServerQueueDispatcher) recordDispatchAudit(paneID, harness, detail string) {
	if d.srv.cfg.Store != nil {
		// RecordPaneInput with daemonActor (Kind: "system") sets lastAgentInputAt
		// and lastAgentBy without ever moving the owner's human promptedAt.
		_, _, _ = d.srv.cfg.Store.RecordPaneInput(core.Ref{Kind: data.KindPane, ID: paneID}, daemonActor)
	}
	if d.srv.telemetry != nil {
		d.srv.telemetry.recordEvent(HookEvent{
			PaneID:  paneID,
			Harness: harness,
			Kind:    HookKindActivity,
			Summary: detail,
		}, d.srv.clock())
	}
}

func (d *ServerQueueDispatcher) emitWaitingSignal(ctx context.Context, sched data.Record, items []data.AgentQueueItem, reason string) (string, error) {
	sigTitle := fmt.Sprintf("%s waiting in queue", formatAgentTaskCount(len(items)))
	sigBody := formatAgentQueueSignalBody(reason, items)
	if d.srv.cfg.Store != nil {
		if err := createAgentQueueSignal(d.srv.cfg.Store, sched, sigTitle, sigBody, d.srv.clock()); err != nil {
			return "", fmt.Errorf("daemon: create agent queue waiting signal: %w", err)
		}
	}
	if d.srv.cfg.Notifier != nil {
		_ = d.srv.cfg.Notifier.Notify(ctx, sigTitle, sigBody)
	}
	return fmt.Sprintf("%s; emitted signal", sigTitle), nil
}

func bracketedPastePrompt(prompt string) []byte {
	safe := strings.ReplaceAll(prompt, "\x1b[201~", "")
	return []byte("\x1b[200~" + safe + "\x1b[201~\r")
}

func isPaneAvailableForDispatch(pt *PaneTelemetry, rec *data.Record) bool {
	if pt != nil {
		switch strings.ToLower(strings.TrimSpace(pt.State)) {
		case StateRunning, StateNeedsYou:
			return false
		case StateIdle, StateDone:
			return true
		}
	}
	if rec != nil {
		switch strings.ToLower(strings.TrimSpace(rec.Text("runStatus"))) {
		case StateRunning, StateNeedsYou, "dead", "exited":
			return false
		}
	}
	return true
}

func effectivePaneHarness(pt *PaneTelemetry, rec *data.Record) string {
	if pt != nil {
		if h := strings.TrimSpace(pt.Harness); h != "" {
			return h
		}
	}
	if rec != nil {
		if h := strings.TrimSpace(rec.Text("harness")); h != "" {
			return h
		}
	}
	return ""
}

func isNonAgentPaneRole(role string) bool {
	switch strings.TrimSpace(role) {
	case data.RoleScratchTerm, data.RoleScratchVim, data.RoleSideTerm, data.RoleSideVim:
		return true
	default:
		return false
	}
}

func isShellOrEditorHarness(harness string) bool {
	switch strings.ToLower(strings.TrimSpace(harness)) {
	case "", "shell", "sh", "bash", "zsh", "fish", "dash", "ash", "tcsh", "csh", "nu", "nushell", "pwsh",
		"vim", "nvim", "vi", "nano", "emacs", "editor":
		return true
	default:
		return false
	}
}

func taskWithinSubtree(taskID, rootID string, tasksByID map[string]data.Record) bool {
	if taskID == "" || rootID == "" {
		return false
	}
	if taskID == rootID {
		return true
	}
	visited := map[string]bool{taskID: true}
	cur := taskID
	for cur != "" {
		rec, ok := tasksByID[cur]
		if !ok {
			return false
		}
		parent, ok := rec.RefField("parent")
		if !ok || parent.Kind != data.KindTask || parent.ID == "" || visited[parent.ID] {
			return false
		}
		if parent.ID == rootID {
			return true
		}
		visited[parent.ID] = true
		cur = parent.ID
	}
	return false
}

func formatQueuedTaskCount(n int) string {
	if n == 1 {
		return "1 queued task"
	}
	return fmt.Sprintf("%d queued tasks", n)
}
