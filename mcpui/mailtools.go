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

// Inter-agent mailbox tool names.
const (
	ToolAgentSend  = "agent.send"
	ToolAgentInbox = "agent.inbox"
	ToolAgentAck   = "agent.ack"
	ToolAgentReply = "agent.reply"
)

// MailNotice tells the daemon that a mailbox message was recorded, so it can
// emit mail.received (and agent.blocked) and wake the recipient when idle.
type MailNotice struct {
	MessageID string `json:"messageId"`
	FromPane  string `json:"fromPane,omitempty"`
	ToPane    string `json:"toPane"`
	Lane      string `json:"lane,omitempty"`
	Thread    string `json:"thread,omitempty"`
	MsgKind   string `json:"msgKind,omitempty"`
	Subject   string `json:"subject,omitempty"`
}

// MailNotifyRuntime is an optional PaneRuntime extension that hands a
// recorded message to the daemon's callback bus and idle-safe wake queue.
type MailNotifyRuntime interface {
	NotifyMail(ctx context.Context, n MailNotice) error
}

// NoticeFromMessage builds the daemon notice of a stored message record.
func NoticeFromMessage(rec data.Record) MailNotice {
	from, _ := rec.RefField("fromPane")
	to, _ := rec.RefField("toPane")
	lane, _ := rec.RefField("lane")
	return MailNotice{
		MessageID: rec.ID,
		FromPane:  from.ID,
		ToPane:    to.ID,
		Lane:      lane.ID,
		Thread:    data.MessageThread(rec),
		MsgKind:   rec.Text("msgKind"),
		Subject:   rec.Text("subject"),
	}
}

// MailTools serves agent.send, agent.inbox, agent.ack and agent.reply: a
// structured mailbox that works the same for every harness, because it never
// types into a pane mid-turn.
type MailTools struct {
	store *data.Store
	// Panes supplies the live runtime used for daemon notices (optional).
	Panes  *PaneTools
	Getenv func(string) string
	Clock  func() time.Time
}

// NewMailTools returns mailbox tools over store.
func NewMailTools(store *data.Store) *MailTools {
	return &MailTools{store: store}
}

// AgentSendArgs are the arguments of agent.send.
type AgentSendArgs struct {
	data.MessageInput
	By string `json:"by,omitempty"`
}

// AgentReplyArgs are the arguments of agent.reply.
type AgentReplyArgs struct {
	ID string `json:"id"`
	data.MessageInput
	By string `json:"by,omitempty"`
}

// AgentInboxArgs are the arguments of agent.inbox.
type AgentInboxArgs struct {
	Pane   string `json:"pane,omitempty"`
	All    bool   `json:"all,omitempty"`
	Thread string `json:"thread,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Ack    bool   `json:"ack,omitempty"`
	By     string `json:"by,omitempty"`
}

// AgentAckArgs are the arguments of agent.ack.
type AgentAckArgs struct {
	ID  string   `json:"id,omitempty"`
	IDs []string `json:"ids,omitempty"`
	By  string   `json:"by,omitempty"`
}

// AgentSendResult is returned by agent.send and agent.reply.
type AgentSendResult struct {
	Message     data.Record `json:"message"`
	Notified    bool        `json:"notified"`
	NotifyError string      `json:"notifyError,omitempty"`
}

// AgentInboxResult is returned by agent.inbox.
type AgentInboxResult struct {
	Pane     string        `json:"pane"`
	Messages []data.Record `json:"messages"`
	Acked    int           `json:"acked,omitempty"`
}

// AgentAckResult is returned by agent.ack.
type AgentAckResult struct {
	Acked []data.Record `json:"acked"`
}

// ToolSpecs returns the MCP tool schemas for the mailbox.
func (m *MailTools) ToolSpecs() []ToolSpec {
	by := text("optional agent identifier recorded on the change set")
	message := props{
		"from":      text("sending pane ID; defaults to MTM_PANE_ID"),
		"to":        text("recipient pane ID"),
		"lane":      text("lane ID the message is about"),
		"thread":    text("thread key to continue (empty starts a new thread)"),
		"msgKind":   props{"type": "string", "enum": data.MsgKinds, "description": "message kind (default note)"},
		"subject":   text("one-line subject; derived from body when omitted"),
		"body":      text("message body (Markdown)"),
		"artifacts": props{"type": "array", "items": props{"type": "string"}, "description": "paths, URLs or kind:id references"},
		"by":        by,
	}
	reply := props{"id": text("message ID being replied to")}
	for k, v := range message {
		if k != "to" {
			reply[k] = v
		}
	}
	reply["to"] = text("override recipient (default: the original sender)")
	return []ToolSpec{
		{
			Name:        ToolAgentSend,
			Description: "Send a structured mailbox message to another pane (request, report, blocked, review_request, approved, rejected, note). The daemon emits mail.received and wakes the recipient only once it is idle, so a busy agent's turn is never interrupted.",
			InputSchema: object([]string{"to"}, message),
		},
		{
			Name:        ToolAgentInbox,
			Description: "List mailbox messages addressed to a pane (default MTM_PANE_ID), unread only unless all is set; ack marks them read.",
			InputSchema: object(nil, props{
				"pane":   text("recipient pane ID; defaults to MTM_PANE_ID"),
				"all":    flag("include acknowledged messages"),
				"thread": text("only messages of this thread"),
				"limit":  count("keep the newest N messages (0 = all)"),
				"ack":    flag("mark the listed messages read"),
				"by":     by,
			}),
		},
		{
			Name:        ToolAgentAck,
			Description: "Mark mailbox messages read (stamps readAt).",
			InputSchema: object(nil, props{
				"id":  text("message ID"),
				"ids": props{"type": "array", "items": props{"type": "string"}, "description": "message IDs"},
				"by":  by,
			}),
		},
		{
			Name:        ToolAgentReply,
			Description: "Reply to a mailbox message: goes to the original sender on the same thread and lane.",
			InputSchema: object([]string{"id"}, reply),
		},
	}
}

// HasTool reports whether name is a mailbox tool.
func (m *MailTools) HasTool(name string) bool {
	switch name {
	case ToolAgentSend, ToolAgentInbox, ToolAgentAck, ToolAgentReply:
		return true
	}
	return false
}

// Call dispatches one mailbox tool call.
func (m *MailTools) Call(ctx context.Context, actor core.Actor, name string, raw json.RawMessage) (any, error) {
	switch name {
	case ToolAgentSend:
		var args AgentSendArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return m.Send(ctx, actor, args)
	case ToolAgentReply:
		var args AgentReplyArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return m.Reply(ctx, actor, args)
	case ToolAgentInbox:
		var args AgentInboxArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return m.Inbox(actor, args)
	case ToolAgentAck:
		var args AgentAckArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return m.Ack(actor, args)
	}
	return nil, fmt.Errorf("unknown mailbox tool %q", name)
}

func (m *MailTools) env(name string) string {
	if m.Getenv != nil {
		return m.Getenv(name)
	}
	return os.Getenv(name)
}

func (m *MailTools) now() time.Time {
	if m.Clock != nil {
		return m.Clock()
	}
	return time.Now()
}

func (m *MailTools) ready() error {
	if m == nil || m.store == nil {
		return errors.New("mailbox: no store")
	}
	return nil
}

// Send records a message and notifies the daemon. A notice failure (no
// daemon running) never loses the message: it is reported, not raised.
func (m *MailTools) Send(ctx context.Context, actor core.Actor, args AgentSendArgs) (AgentSendResult, error) {
	if err := m.ready(); err != nil {
		return AgentSendResult{}, err
	}
	if strings.TrimSpace(args.From) == "" {
		args.From = m.env(EnvPaneID)
	}
	rec, err := m.store.SendMessage(args.MessageInput, agentActor(actor, args.By))
	if err != nil {
		return AgentSendResult{}, fmt.Errorf("agent.send: %w", err)
	}
	return m.notify(ctx, *rec), nil
}

// Reply records a reply to args.ID and notifies the daemon.
func (m *MailTools) Reply(ctx context.Context, actor core.Actor, args AgentReplyArgs) (AgentSendResult, error) {
	if err := m.ready(); err != nil {
		return AgentSendResult{}, err
	}
	if strings.TrimSpace(args.From) == "" {
		args.From = m.env(EnvPaneID)
	}
	rec, err := m.store.ReplyMessage(args.ID, args.MessageInput, agentActor(actor, args.By))
	if err != nil {
		return AgentSendResult{}, fmt.Errorf("agent.reply: %w", err)
	}
	return m.notify(ctx, *rec), nil
}

func (m *MailTools) notify(ctx context.Context, rec data.Record) AgentSendResult {
	res := AgentSendResult{Message: rec}
	var rt PaneRuntime
	if m.Panes != nil {
		rt = m.Panes.Runtime
	}
	n, ok := rt.(MailNotifyRuntime)
	if !ok {
		res.NotifyError = "no live daemon runtime; message recorded only"
		return res
	}
	if err := n.NotifyMail(ctx, NoticeFromMessage(rec)); err != nil {
		res.NotifyError = err.Error()
		return res
	}
	res.Notified = true
	return res
}

// Inbox lists a pane's messages, optionally acknowledging them.
func (m *MailTools) Inbox(actor core.Actor, args AgentInboxArgs) (AgentInboxResult, error) {
	if err := m.ready(); err != nil {
		return AgentInboxResult{}, err
	}
	pane := strings.TrimSpace(args.Pane)
	if pane == "" {
		pane = m.env(EnvPaneID)
	}
	msgs, err := m.store.Inbox(data.InboxQuery{Pane: pane, IncludeAll: args.All, Thread: args.Thread, Limit: args.Limit})
	if err != nil {
		return AgentInboxResult{}, fmt.Errorf("agent.inbox: %w", err)
	}
	res := AgentInboxResult{Pane: pane, Messages: msgs}
	if res.Messages == nil {
		res.Messages = []data.Record{}
	}
	if !args.Ack {
		return res, nil
	}
	ids := make([]string, 0, len(msgs))
	for _, rec := range msgs {
		if rec.Text("readAt") == "" {
			ids = append(ids, rec.ID)
		}
	}
	acked, err := m.store.AckMessages(ids, m.now(), agentActor(actor, args.By))
	res.Acked = len(acked)
	return res, err
}

// Ack marks messages read.
func (m *MailTools) Ack(actor core.Actor, args AgentAckArgs) (AgentAckResult, error) {
	if err := m.ready(); err != nil {
		return AgentAckResult{}, err
	}
	ids := args.IDs
	if id := strings.TrimSpace(args.ID); id != "" {
		ids = append([]string{id}, ids...)
	}
	if len(ids) == 0 {
		return AgentAckResult{}, errors.New("agent.ack: id or ids is required")
	}
	acked, err := m.store.AckMessages(ids, m.now(), agentActor(actor, args.By))
	if acked == nil {
		acked = []data.Record{}
	}
	return AgentAckResult{Acked: acked}, err
}
