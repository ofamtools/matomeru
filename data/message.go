package data

import (
	"errors"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke/core"
)

// KindMessage is one structured inter-agent mailbox message between panes.
const KindMessage = "message"

// Message kinds (msgKind) of a mailbox message.
const (
	MsgKindRequest       = "request"
	MsgKindReport        = "report"
	MsgKindBlocked       = "blocked"
	MsgKindReviewRequest = "review_request"
	MsgKindApproved      = "approved"
	MsgKindRejected      = "rejected"
	MsgKindNote          = "note"
)

// MsgKinds lists the valid msgKind values.
var MsgKinds = []string{MsgKindRequest, MsgKindReport, MsgKindBlocked, MsgKindReviewRequest, MsgKindApproved, MsgKindRejected, MsgKindNote}

const messageSubjectMaxRunes = 72

func messageSchema() Schema {
	return Schema{
		Kind:         KindMessage,
		Title:        "Message",
		Plural:       "Messages",
		SummaryField: "subject",
		Fields: []FieldSpec{
			str("subject", "one-line subject; derived from body when omitted"),
			strFmt("body", "message body", ShowFormatMarkdown),
			{Name: "msgKind", Type: core.ParamString, Enum: MsgKinds, Default: MsgKindNote, Description: "request, report, blocked, review_request, approved, rejected or note"},
			refTo("fromPane", KindPane, "sending pane (empty when sent by a person or script)"),
			refTo("toPane", KindPane, "recipient pane"),
			refTo("lane", KindLane, "lane the message is about"),
			str("thread", "thread key; replies share it (empty = this message starts a thread)"),
			{Name: "artifacts", Type: core.ParamList, ShowFormat: ShowFormatList, Description: "paths, URLs or kind:id references the message points at"},
			str("readAt", "when the recipient acknowledged the message (RFC 3339; empty = unread)"),
		},
		DefaultColumns: []string{"subject", "msgKind", "fromPane", "toPane", "lane", "readAt"},
		Relations:      universalRelations(),
		FactFields:     []string{"msgKind"},
		BadgeRules: []BadgeRule{
			{When: `fields.msgKind == "blocked"`, Text: "blocked", Tone: "error"},
			{When: `fields.msgKind == "review_request"`, Text: "review", Tone: "accent"},
			{When: `fields.msgKind == "approved"`, Text: "approved", Tone: "success"},
			{When: `fields.msgKind == "rejected"`, Text: "rejected", Tone: "error"},
		},
	}
}

// MessageInput describes one mailbox message to send.
type MessageInput struct {
	From      string   `json:"from,omitempty"`
	To        string   `json:"to"`
	Lane      string   `json:"lane,omitempty"`
	Thread    string   `json:"thread,omitempty"`
	Kind      string   `json:"msgKind,omitempty"`
	Subject   string   `json:"subject,omitempty"`
	Body      string   `json:"body,omitempty"`
	Artifacts []string `json:"artifacts,omitempty"`
}

// fields validates in and returns the record fields of the message.
func (in MessageInput) fields() (map[string]any, error) {
	to := strings.TrimSpace(in.To)
	if to == "" {
		return nil, errors.New("message: recipient pane (to) is required")
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = MsgKindNote
	}
	if !slices.Contains(MsgKinds, kind) {
		return nil, errors.New("message: msgKind must be one of " + strings.Join(MsgKinds, ", "))
	}
	subject := strings.TrimSpace(in.Subject)
	if subject == "" {
		subject = MessageSubjectFromBody(in.Body, kind)
	}
	f := map[string]any{
		"subject": subject,
		"body":    in.Body,
		"msgKind": kind,
		"toPane":  core.Ref{Kind: KindPane, ID: to},
		"thread":  strings.TrimSpace(in.Thread),
	}
	optionalRef(f, "fromPane", KindPane, in.From)
	optionalRef(f, "lane", KindLane, strings.TrimPrefix(strings.TrimSpace(in.Lane), KindLane+":"))
	if len(in.Artifacts) > 0 {
		f["artifacts"] = slices.Clone(in.Artifacts)
	}
	return f, nil
}

func optionalRef(f map[string]any, field, kind, id string) {
	if id = strings.TrimSpace(id); id != "" {
		f[field] = core.Ref{Kind: kind, ID: id}
	}
}

// MessageSubjectFromBody derives a one-line subject from body, falling back to the kind.
func MessageSubjectFromBody(body, kind string) string {
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(raw), "#*- "))
		if line == "" {
			continue
		}
		if rs := []rune(line); len(rs) > messageSubjectMaxRunes {
			return strings.TrimSpace(string(rs[:messageSubjectMaxRunes-1])) + "…"
		}
		return line
	}
	return kind
}

// SendMessage records one mailbox message. Unknown panes and lanes are kept
// as plain references: a message is recorded first and never refused for a
// missing association.
func (s *Store) SendMessage(in MessageInput, actor core.Actor) (*Record, error) {
	fields, err := in.fields()
	if err != nil {
		return nil, err
	}
	rec, _, err := s.Create(CreateInput{Kind: KindMessage, Fields: fields}, actor)
	return rec, err
}

// ReplyMessage records a reply to message id: it goes back to the original
// sender on the same thread and lane. in.To overrides the recipient.
func (s *Store) ReplyMessage(id string, in MessageInput, actor core.Actor) (*Record, error) {
	orig, err := s.Get(core.Ref{Kind: KindMessage, ID: strings.TrimSpace(id)})
	if err != nil {
		return nil, err
	}
	if orig == nil {
		return nil, errors.New("message: no message " + id)
	}
	if strings.TrimSpace(in.To) == "" {
		from, _ := orig.RefField("fromPane")
		in.To = from.ID
	}
	in.Thread = MessageThread(*orig)
	if strings.TrimSpace(in.Lane) == "" {
		lane, _ := orig.RefField("lane")
		in.Lane = lane.ID
	}
	if strings.TrimSpace(in.Subject) == "" {
		in.Subject = "Re: " + strings.TrimPrefix(orig.Text("subject"), "Re: ")
	}
	return s.SendMessage(in, actor)
}

// MessageThread returns the message's thread key (its own ID when it starts one).
func MessageThread(rec Record) string {
	if t := strings.TrimSpace(rec.Text("thread")); t != "" {
		return t
	}
	return rec.ID
}

// InboxQuery selects a pane's mailbox messages.
type InboxQuery struct {
	Pane       string
	IncludeAll bool // include acknowledged messages
	Thread     string
	Limit      int
}

// Inbox returns the messages addressed to q.Pane, oldest first; unread only
// unless q.IncludeAll.
func (s *Store) Inbox(q InboxQuery) ([]Record, error) {
	pane := strings.TrimSpace(q.Pane)
	if pane == "" {
		return nil, errors.New("inbox: pane is required")
	}
	res, err := s.Query(Query{
		Kind:        KindMessage,
		Parent:      &core.Ref{Kind: KindPane, ID: pane},
		ParentField: "toPane",
		SortField:   "createdAt",
	})
	if err != nil {
		return nil, err
	}
	out := res.Items[:0]
	for _, rec := range res.Items {
		if keepInboxMessage(rec, q) {
			out = append(out, rec)
		}
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[len(out)-q.Limit:]
	}
	return out, nil
}

func keepInboxMessage(rec Record, q InboxQuery) bool {
	if !q.IncludeAll && rec.Text("readAt") != "" {
		return false
	}
	return q.Thread == "" || MessageThread(rec) == q.Thread
}

// AckMessages stamps readAt on each message id, returning the updated records.
func (s *Store) AckMessages(ids []string, at time.Time, actor core.Actor) ([]Record, error) {
	stamp := at.UTC().Format(TimeFormat)
	out := make([]Record, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		rec, _, err := s.Patch(PatchInput{
			Ref:    core.Ref{Kind: KindMessage, ID: id},
			Fields: map[string]any{fieldReadAt: stamp},
		}, actor)
		if err != nil {
			return out, err
		}
		if rec != nil {
			out = append(out, *rec)
		}
	}
	return out, nil
}

// IsMessageRef reports whether ref addresses a mailbox message.
func IsMessageRef(ref core.Ref) bool { return ref.Kind == KindMessage && ref.ID != "" }
