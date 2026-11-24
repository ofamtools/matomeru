package daemon

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxRecentTurns        = 50
	maxTurnFieldTextBytes = 8 * 1024
)

var (
	telemetrySecretAssignRe = regexp.MustCompile(`\b((?:[A-Z][A-Z0-9_]*)?(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|AUTH|CREDENTIAL)[A-Z0-9_]*)=([^\s'"]+)`)
	telemetryBearerRe       = regexp.MustCompile(`(?i)\b(Bearer\s+)[A-Za-z0-9._~+/=-]{8,}`)
)

func sanitizeTurnText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = telemetrySecretAssignRe.ReplaceAllString(s, "${1}=***")
	s = telemetryBearerRe.ReplaceAllString(s, "${1}***")
	if len(s) > maxTurnFieldTextBytes {
		s = strings.TrimSpace(s[:maxTurnFieldTextBytes])
	}
	return s
}

func payloadString(m map[string]any, keys ...string) string {
	if len(m) == 0 {
		return ""
	}
	for _, k := range keys {
		if raw, ok := m[k]; ok {
			if s, ok := raw.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					return trimmed
				}
			}
		}
	}
	return ""
}

func extractTranscriptPath(payload map[string]any) string {
	return payloadString(payload, "transcript_path", "transcriptPath")
}

func extractSessionID(payload map[string]any) string {
	return payloadString(payload, "session_id", "sessionId", "conversationId", "conversation_id", "thread-id", "thread_id")
}

func updateStickyMetadata(pt *PaneTelemetry, payload map[string]any) {
	if tp := extractTranscriptPath(payload); tp != "" {
		pt.TranscriptPath = tp
	}
	if sid := extractSessionID(payload); sid != "" {
		pt.SessionID = sid
	}
}

func appendRecentTurn(pt *PaneTelemetry, ev HookEvent, now time.Time) {
	prompt := sanitizeTurnText(ev.Prompt)
	resp := sanitizeTurnText(ev.Response)
	summary := ""
	if prompt == "" && resp == "" && (ev.Kind == "" || ev.Kind == HookKindState) {
		summary = sanitizeTurnText(ev.Summary)
	}
	if prompt == "" && resp == "" && summary == "" {
		return
	}
	if len(pt.RecentTurns) > 0 {
		last := pt.RecentTurns[len(pt.RecentTurns)-1]
		if last.Prompt == prompt && last.Response == resp && last.Summary == summary {
			return
		}
	}
	pt.RecentTurns = append(pt.RecentTurns, HookTurn{
		Prompt:   prompt,
		Response: resp,
		Summary:  summary,
		At:       now,
	})
	if len(pt.RecentTurns) > maxRecentTurns {
		pt.RecentTurns = slices.Clone(pt.RecentTurns[len(pt.RecentTurns)-maxRecentTurns:])
	}
}

type approvalResult struct {
	Decision string
	Reason   string
}

type pendingEntry struct {
	info *PendingApproval
	ch   chan approvalResult
}

// telemetryStore holds per-pane hook telemetry and blocking approval requests.
type telemetryStore struct {
	mu      sync.Mutex
	panes   map[string]*PaneTelemetry
	pending map[string]*pendingEntry
	seq     atomic.Uint64
}

func newTelemetryStore() *telemetryStore {
	return &telemetryStore{
		panes:   map[string]*PaneTelemetry{},
		pending: map[string]*pendingEntry{},
	}
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	maps.Copy(out, m)
	return out
}

func (ts *telemetryStore) ensurePaneLocked(paneID string) *PaneTelemetry {
	pt := ts.panes[paneID]
	if pt == nil {
		pt = &PaneTelemetry{PaneID: paneID}
		ts.panes[paneID] = pt
	}
	return pt
}

// recordEvent updates PaneTelemetry for non-blocking hook events (state, activity, subagent).
func (ts *telemetryStore) recordEvent(ev HookEvent, now time.Time) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ev.PaneID == "" {
		return
	}
	pt := ts.ensurePaneLocked(ev.PaneID)
	if ev.Harness != "" {
		pt.Harness = ev.Harness
	}
	if ev.Kind != "" {
		pt.LastKind = ev.Kind
	}
	pt.LastEventAt = now
	if ev.State != "" {
		pt.State = ev.State
		if (ev.State == StateIdle || ev.State == StateDone) && ev.Tool == "" {
			pt.Tool = ""
		}
	}
	if ev.Tool != "" {
		pt.Tool = ev.Tool
	}
	if ev.Summary != "" {
		pt.Summary = ev.Summary
	}
	if ev.Payload != nil {
		pt.Payload = cloneMap(ev.Payload)
		updateStickyMetadata(pt, ev.Payload)
	}
	appendRecentTurn(pt, ev, now)
	if ev.Kind == HookKindSubagent && ev.SubagentID != "" {
		switch ev.SubagentAction {
		case SubagentStop:
			delete(pt.ActiveSubagents, ev.SubagentID)
		default:
			if pt.ActiveSubagents == nil {
				pt.ActiveSubagents = map[string]SubagentInfo{}
			}
			pt.ActiveSubagents[ev.SubagentID] = SubagentInfo{
				ID:        ev.SubagentID,
				Summary:   ev.Summary,
				StartedAt: now,
			}
		}
		pt.SubagentCount = len(pt.ActiveSubagents)
	}
}

// waitApproval registers a PendingApproval, sets the pane state to "needs_you",
// and blocks until resolved via resolveApproval, cancelled via ctx, or timed out.
func (ts *telemetryStore) waitApproval(ctx context.Context, ev HookEvent, timeout time.Duration, now time.Time) approvalResult {
	reqID := ev.RequestID
	if reqID == "" {
		reqID = fmt.Sprintf("apr-%d", ts.seq.Add(1))
	}
	ch := make(chan approvalResult, 1)
	entry := &pendingEntry{
		info: &PendingApproval{
			RequestID: reqID,
			PaneID:    ev.PaneID,
			Harness:   ev.Harness,
			Tool:      ev.Tool,
			Summary:   ev.Summary,
			Payload:   cloneMap(ev.Payload),
			CreatedAt: now,
		},
		ch: ch,
	}

	ts.mu.Lock()
	ts.pending[reqID] = entry
	if ev.PaneID != "" {
		pt := ts.ensurePaneLocked(ev.PaneID)
		if ev.Harness != "" {
			pt.Harness = ev.Harness
		}
		pt.State = StateNeedsYou
		pt.LastKind = HookKindApproval
		pt.LastEventAt = now
		if ev.Tool != "" {
			pt.Tool = ev.Tool
		}
		if ev.Summary != "" {
			pt.Summary = ev.Summary
		}
		if ev.Payload != nil {
			pt.Payload = cloneMap(ev.Payload)
			updateStickyMetadata(pt, ev.Payload)
		}
	}
	ts.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case res := <-ch:
		return res
	case <-timer.C:
		ts.removePending(reqID, ev.PaneID)
		return approvalResult{Decision: DecisionDeny, Reason: "approval timed out"}
	case <-ctx.Done():
		ts.removePending(reqID, ev.PaneID)
		return approvalResult{Decision: DecisionDeny, Reason: "daemon shutting down"}
	}
}

func (ts *telemetryStore) removePending(reqID, paneID string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	delete(ts.pending, reqID)
	ts.refreshPaneAfterApprovalLocked(paneID, "")
}

func (ts *telemetryStore) hasPendingForPaneLocked(paneID string) bool {
	if paneID == "" {
		return false
	}
	for _, p := range ts.pending {
		if p.info.PaneID == paneID {
			return true
		}
	}
	return false
}

func (ts *telemetryStore) refreshPaneAfterApprovalLocked(paneID, decision string) {
	if paneID == "" {
		return
	}
	pt := ts.panes[paneID]
	if pt == nil || pt.State != StateNeedsYou {
		return
	}
	if ts.hasPendingForPaneLocked(paneID) {
		return
	}
	switch decision {
	case DecisionAllow:
		pt.State = StateRunning
	default:
		pt.State = StateIdle
	}
}

// resolveApproval unblocks the waiting hook_event call for requestID.
func (ts *telemetryStore) resolveApproval(requestID, decision, reason string) error {
	if requestID == "" {
		return fmt.Errorf("requestId is required")
	}
	if decision != DecisionAllow && decision != DecisionDeny {
		return fmt.Errorf("decision %q must be %q or %q", decision, DecisionAllow, DecisionDeny)
	}
	ts.mu.Lock()
	entry, ok := ts.pending[requestID]
	if !ok {
		ts.mu.Unlock()
		return fmt.Errorf("unknown approval requestId %q", requestID)
	}
	delete(ts.pending, requestID)
	ts.refreshPaneAfterApprovalLocked(entry.info.PaneID, decision)
	ts.mu.Unlock()

	select {
	case entry.ch <- approvalResult{Decision: decision, Reason: reason}:
	default:
	}
	return nil
}

// clearPane cancels any pending approvals for paneID and drops its telemetry.
func (ts *telemetryStore) clearPane(paneID string) {
	if paneID == "" {
		return
	}
	ts.mu.Lock()
	delete(ts.panes, paneID)
	var toCancel []*pendingEntry
	for id, entry := range ts.pending {
		if entry.info.PaneID == paneID {
			toCancel = append(toCancel, entry)
			delete(ts.pending, id)
		}
	}
	ts.mu.Unlock()
	for _, entry := range toCancel {
		select {
		case entry.ch <- approvalResult{Decision: DecisionDeny, Reason: "pane closed"}:
		default:
		}
	}
}

// reset cancels all pending approvals and clears all telemetry state.
func (ts *telemetryStore) reset(reason string) {
	ts.mu.Lock()
	ts.panes = map[string]*PaneTelemetry{}
	all := ts.pending
	ts.pending = map[string]*pendingEntry{}
	ts.mu.Unlock()

	for _, entry := range all {
		select {
		case entry.ch <- approvalResult{Decision: DecisionDeny, Reason: reason}:
		default:
		}
	}
}

// snapshot returns deep copies of telemetry and pending approvals (filtered by paneID if non-empty).
func (ts *telemetryStore) snapshot(paneID string) (map[string]*PaneTelemetry, []*PendingApproval) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	outTel := make(map[string]*PaneTelemetry)
	for id, pt := range ts.panes {
		if paneID != "" && id != paneID {
			continue
		}
		cp := *pt
		cp.Payload = cloneMap(pt.Payload)
		if len(pt.RecentTurns) > 0 {
			cp.RecentTurns = slices.Clone(pt.RecentTurns)
		}
		if len(pt.ActiveSubagents) > 0 {
			cp.ActiveSubagents = make(map[string]SubagentInfo, len(pt.ActiveSubagents))
			maps.Copy(cp.ActiveSubagents, pt.ActiveSubagents)
		}
		outTel[id] = &cp
	}

	outPending := make([]*PendingApproval, 0, len(ts.pending))
	for _, entry := range ts.pending {
		if paneID != "" && entry.info.PaneID != paneID {
			continue
		}
		cp := *entry.info
		cp.Payload = cloneMap(entry.info.Payload)
		outPending = append(outPending, &cp)
	}
	slices.SortFunc(outPending, func(a, b *PendingApproval) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		if a.RequestID < b.RequestID {
			return -1
		}
		if a.RequestID > b.RequestID {
			return 1
		}
		return 0
	})
	return outTel, outPending
}
