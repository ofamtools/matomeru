package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
)

// handleHookRequest serves hook_register, hook_unregister, hook_list and hook_log.
func (s *Server) handleHookRequest(req Request) Response {
	switch req.Op {
	case OpHookRegister:
		if req.Rule == nil {
			return Response{OK: false, Error: "hook_register: rule is required"}
		}
		if err := s.callbacks.register(*req.Rule); err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true}
	case OpHookUnregister:
		id := strings.TrimSpace(req.ID)
		if id == "" {
			return Response{OK: false, Error: "hook_unregister: id is required"}
		}
		return Response{OK: true, Found: s.callbacks.unregister(id)}
	case OpHookList:
		return Response{OK: true, Rules: s.callbacks.rules()}
	case OpHookLog:
		return Response{OK: true, Deliveries: s.callbacks.log(req.Limit)}
	}
	return Response{OK: false, Error: fmt.Sprintf("unknown op %q", req.Op)}
}

// serveWatchEvents streams bus events (optionally for one pane) until
// the client disconnects. The listener is registered before the ack so a
// client may check current state after the ack without missing an edge.
func (s *Server) serveWatchEvents(conn net.Conn, enc *json.Encoder, paneID string) {
	l, stop := s.callbacks.listen(strings.TrimSpace(paneID))
	defer stop()
	if err := enc.Encode(Response{OK: true}); err != nil {
		return
	}
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		var buf [64]byte
		for {
			if _, err := conn.Read(buf[:]); err != nil {
				return
			}
		}
	}()
	defer func() {
		_ = conn.Close()
		<-clientDone
	}()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-clientDone:
			return
		case ev := <-l.ch:
			if err := enc.Encode(Response{OK: true, PaneID: ev.PaneID, Event: &ev}); err != nil {
				return
			}
		}
	}
}

// enrichAgentDone fills task, lane and project from the pane record and its
// claiming lane (a lane already on the event, such as a message's, is kept). It runs on the callback dispatcher, never under telemetry locks.
func (s *Server) enrichAgentDone(ev *AgentDoneEvent) {
	store := s.cfg.Store
	if store == nil || ev.PaneID == "" {
		return
	}
	pane, err := store.Get(core.Ref{Kind: data.KindPane, ID: ev.PaneID})
	if err != nil || pane == nil {
		return
	}
	if t, ok := pane.RefField("task"); ok && t.ID != "" {
		ev.TaskID = t.ID
		ev.ProjectID = store.RootProjectID(t.ID)
		if ev.ProjectID == "" {
			ev.ProjectID = t.ID
		}
	}
	if ev.LaneID == "" {
		ev.LaneID = claimingLane(store, pane.Ref())
	}
}

// claimingLane returns the ID of the first lane that claims pane.
func claimingLane(store *data.Store, pane core.Ref) string {
	rels, err := store.Related(pane, data.RelClaim, data.Incoming)
	if err != nil {
		return ""
	}
	for _, rel := range rels {
		if rel.From.Kind == data.KindLane && rel.From.ID != "" {
			return rel.From.ID
		}
	}
	return ""
}

// OnDoneNotifyEvents are the events a pane.spawn onDone.notifyPane reports.
const OnDoneNotifyEvents = EventAgentDone + "," + EventAgentNeedsInput + "," + EventAgentBlocked

// OnDoneRules builds the in-memory rules pane.spawn registers for paneID: the
// one-shot command/webhook rule and, with NotifyPane, a standing rule that
// mails NotifyPane on agent.done, agent.needs_input and agent.blocked.
func OnDoneRules(paneID string, onDone mcpui.PaneOnDone) []HookRule {
	var rules []HookRule
	if len(onDone.Command) > 0 || strings.TrimSpace(onDone.Webhook) != "" {
		rules = append(rules, OnDoneRule(paneID, onDone))
	}
	if target := strings.TrimSpace(onDone.NotifyPane); target != "" {
		rules = append(rules, HookRule{
			ID:    "pane-" + paneID + "-notify",
			On:    OnDoneNotifyEvents,
			Match: HookMatch{Pane: paneID},
			Run:   HookRun{NotifyPane: target},
		})
	}
	return rules
}

// OnDoneRule builds the one-shot in-memory rule pane.spawn registers for paneID.
func OnDoneRule(paneID string, onDone mcpui.PaneOnDone) HookRule {
	return HookRule{
		ID:    "pane-" + paneID + "-ondone",
		On:    EventAgentDone,
		Once:  true,
		Match: HookMatch{Pane: paneID},
		Run: HookRun{
			Command:    onDone.Command,
			Webhook:    onDone.Webhook,
			Headers:    onDone.Headers,
			TimeoutSec: onDone.TimeoutSec,
		},
	}
}

// RegisterOnDone implements mcpui.PaneCallbackRuntime in-process.
func (r *serverPaneRuntime) RegisterOnDone(_ context.Context, paneID string, onDone mcpui.PaneOnDone) error {
	for _, rule := range OnDoneRules(paneID, onDone) {
		if err := r.s.callbacks.register(rule); err != nil {
			return err
		}
	}
	return nil
}

// RegisterOnDone implements mcpui.PaneCallbackRuntime over the daemon socket.
func (r *clientPaneRuntime) RegisterOnDone(ctx context.Context, paneID string, onDone mcpui.PaneOnDone) error {
	c, err := r.client(ctx)
	if err != nil {
		return err
	}
	for _, rule := range OnDoneRules(paneID, onDone) {
		if err := c.RegisterHook(ctx, rule); err != nil {
			return err
		}
	}
	return nil
}
