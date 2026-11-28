package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
)

// MailNotice is the daemon notice of one recorded mailbox message.
type MailNotice = mcpui.MailNotice

// OpMailNotify hands a recorded mailbox message to the daemon.
const OpMailNotify = "mail_notify"

// mailEventBodyRunes bounds the agent response copied into a notifyPane message.
const mailEventBodyRunes = 4000

// pendingWake is the coalesced wake notice held for one recipient pane.
type pendingWake struct {
	count int
	last  MailNotice
}

// wakeQueue holds wake notices for panes that are mid-turn, so a wake is
// never typed into an agent's active turn.
type wakeQueue struct {
	mu      sync.Mutex
	pending map[string]*pendingWake
}

func newWakeQueue() *wakeQueue {
	return &wakeQueue{pending: map[string]*pendingWake{}}
}

func (q *wakeQueue) hold(n MailNotice) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	w := q.pending[n.ToPane]
	if w == nil {
		w = &pendingWake{}
		q.pending[n.ToPane] = w
	}
	w.count++
	w.last = n
}

func (q *wakeQueue) take(pane string) *pendingWake {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	w := q.pending[pane]
	delete(q.pending, pane)
	return w
}

func (q *wakeQueue) forget(pane string) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.pending, pane)
}

// held reports the number of messages waiting to wake pane.
func (q *wakeQueue) held(pane string) int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if w := q.pending[pane]; w != nil {
		return w.count
	}
	return 0
}

// wakeText is the one-line notice pasted into a recipient pane.
func wakeText(w *pendingWake) string {
	from := w.last.FromPane
	if from == "" {
		from = "mtm"
	}
	head := fmt.Sprintf("[mtm mail] %s from %s: %s", w.last.MsgKind, from, w.last.Subject)
	if w.count > 1 {
		head = fmt.Sprintf("[mtm mail] %d new messages; latest %s from %s: %s", w.count, w.last.MsgKind, from, w.last.Subject)
	}
	return head + " — read with `mtm msg inbox` (MCP agent.inbox), then `mtm msg ack <id>`."
}

// wireMailbox connects the wake queue and notifyPane delivery to s.
func (s *Server) wireMailbox() {
	s.wakes = newWakeQueue()
	s.callbacks.notify = s.notifyPaneOfEvent
}

// forgetPaneHooks drops a closed pane's in-memory rules and held wakes.
func (s *Server) forgetPaneHooks(paneID string) {
	s.callbacks.forgetPane(paneID)
	s.wakes.forget(paneID)
}

// onAgentEdge receives telemetry edges: every edge goes to the callback bus;
// agent.done also feeds the queue watcher and releases held wake notices.
func (s *Server) onAgentEdge(ev AgentDoneEvent) {
	s.callbacks.publish(ev)
	if ev.Event != EventAgentDone {
		return
	}
	s.queueWatch.publish(ev)
	s.flushWake(ev.PaneID)
}

// handleMailRequest serves OpMailNotify.
func (s *Server) handleMailRequest(req Request) Response {
	if req.Mail == nil {
		return Response{OK: false, Error: "mail_notify: mail is required"}
	}
	if err := s.handleMailNotify(*req.Mail); err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	return Response{OK: true}
}

// handleMailNotify emits mail.received (and agent.blocked for a blocked
// message) and wakes the recipient now when idle, or holds the wake until
// its next agent.done.
func (s *Server) handleMailNotify(n MailNotice) error {
	if strings.TrimSpace(n.ToPane) == "" {
		return errors.New("mail_notify: toPane is required")
	}
	s.callbacks.publish(mailEvent(EventMailReceived, n.ToPane, n, s))
	if n.MsgKind == data.MsgKindBlocked && n.FromPane != "" {
		s.callbacks.publish(mailEvent(EventAgentBlocked, n.FromPane, n, s))
	}
	s.wakes.hold(n)
	s.flushWake(n.ToPane)
	return nil
}

func mailEvent(event, pane string, n MailNotice, s *Server) AgentDoneEvent {
	return AgentDoneEvent{
		Event:     event,
		PaneID:    pane,
		LaneID:    n.Lane,
		MessageID: n.MessageID,
		FromPane:  n.FromPane,
		ToPane:    n.ToPane,
		MsgKind:   n.MsgKind,
		Subject:   n.Subject,
		Thread:    n.Thread,
		Summary:   n.Subject,
		At:        s.clock(),
	}
}

// paneIdleForWake reports whether pane is live, at rest (idle or done) and
// has no approval waiting, so a pasted notice cannot collide with a turn.
func (s *Server) paneIdleForWake(pane string) bool {
	if s.host == nil || pane == "" {
		return false
	}
	if f, ok := s.host.Snapshot(pane); !ok || f.Exited {
		return false
	}
	tel, pending := s.telemetry.snapshot(pane)
	pt := tel[pane]
	return pt != nil && isRestState(pt.State) && len(pending) == 0
}

// flushWake pastes pane's held wake notice when the pane is idle.
func (s *Server) flushWake(pane string) {
	if s.wakes == nil || !s.paneIdleForWake(pane) {
		return
	}
	w := s.wakes.take(pane)
	if w == nil {
		return
	}
	if err := s.host.Write(pane, bracketedPastePrompt(wakeText(w))); err != nil {
		s.wakes.hold(w.last)
	}
}

// notifyPaneOfEvent delivers a notifyPane hook: it records a mailbox message
// from the event's pane to target and wakes target when idle. Mail events
// already addressed to target are skipped so nothing is delivered twice.
func (s *Server) notifyPaneOfEvent(_ context.Context, target string, ev AgentDoneEvent) error {
	if ev.Event == EventMailReceived || ev.ToPane == target || ev.PaneID == target {
		return nil
	}
	store := s.cfg.Store
	if store == nil {
		return errors.New("notifyPane: daemon has no store")
	}
	rec, err := store.SendMessage(data.MessageInput{
		From:    ev.PaneID,
		To:      target,
		Lane:    ev.LaneID,
		Kind:    eventMsgKind(ev.Event),
		Subject: eventSubject(ev),
		Body:    eventBody(ev),
	}, daemonActor)
	if err != nil {
		return fmt.Errorf("notifyPane: %w", err)
	}
	return s.handleMailNotify(mcpui.NoticeFromMessage(*rec))
}

func eventMsgKind(event string) string {
	switch event {
	case EventAgentDone:
		return data.MsgKindReport
	case EventAgentBlocked, EventAgentNeedsInput:
		return data.MsgKindBlocked
	}
	return data.MsgKindNote
}

func eventSubject(ev AgentDoneEvent) string {
	subject := ev.Event + " " + ev.PaneID
	if s := strings.TrimSpace(ev.Subject); s != "" {
		return subject + ": " + s
	}
	if s := strings.TrimSpace(ev.Summary); s != "" {
		return subject + ": " + s
	}
	return subject
}

func eventBody(ev AgentDoneEvent) string {
	parts := []string{}
	if ev.Summary != "" {
		parts = append(parts, ev.Summary)
	}
	if resp := []rune(strings.TrimSpace(ev.Response)); len(resp) > 0 {
		if len(resp) > mailEventBodyRunes {
			resp = append(resp[:mailEventBodyRunes], '…')
		}
		parts = append(parts, string(resp))
	}
	if ev.MessageID != "" {
		parts = append(parts, "message: "+data.KindMessage+":"+ev.MessageID)
	}
	return strings.Join(parts, "\n\n")
}

// NotifyMail implements mcpui.MailNotifyRuntime in-process.
func (r *serverPaneRuntime) NotifyMail(_ context.Context, n MailNotice) error {
	return r.s.handleMailNotify(n)
}

// NotifyMail implements mcpui.MailNotifyRuntime over the daemon socket.
func (r *clientPaneRuntime) NotifyMail(ctx context.Context, n MailNotice) error {
	c, err := r.client(ctx)
	if err != nil {
		return err
	}
	return c.NotifyMail(ctx, n)
}

// NotifyMail hands a recorded mailbox message to the daemon.
func (c *Client) NotifyMail(ctx context.Context, n MailNotice) error {
	_, err := c.call(ctx, Request{Op: OpMailNotify, Mail: &n}, 0)
	return err
}
