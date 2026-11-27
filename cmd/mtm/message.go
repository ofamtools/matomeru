package main

import (
	"errors"
	"io"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/oden"
)

// messageInputParams are the flags shared by `mtm msg send` and `mtm msg reply`.
var messageInputParams = []oden.Parameter{
	{Name: "from", Type: "string", Description: "Sending pane ID (default: $MTM_PANE_ID)"},
	{Name: "lane", Type: "string", Description: "Lane ID the message is about"},
	{Name: "thread", Type: "string", Description: "Thread key to continue (empty starts a thread)"},
	{Name: "kind", Type: "string", Description: "Message kind: " + strings.Join(data.MsgKinds, ", ") + " (default note)"},
	{Name: "subject", Type: "string", Description: "One-line subject (derived from the body when omitted)"},
	{Name: "body", Type: "string", Description: "Message body; - reads stdin (or pass the remaining positional words)"},
	{Name: "artifact", Type: "string", Repeatable: true, Description: "Path, URL or kind:id reference (repeat for more)"},
}

func messageInputFromParams(p oden.Parsed, stdin io.Reader, words []string) (data.MessageInput, error) {
	body := p.String("body")
	if body == "" && len(words) > 0 {
		body = strings.Join(words, " ")
	}
	if body == "-" {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			return data.MessageInput{}, err
		}
		body = string(raw)
	}
	return data.MessageInput{
		From:      strings.TrimSpace(p.String("from")),
		Lane:      strings.TrimSpace(p.String("lane")),
		Thread:    strings.TrimSpace(p.String("thread")),
		Kind:      strings.TrimSpace(p.String("kind")),
		Subject:   strings.TrimSpace(p.String("subject")),
		Body:      body,
		Artifacts: p.Strings("artifact"),
	}, nil
}

// mtmMessageCommands are the mailbox verbs of the message noun (`mtm msg ...`).
func mtmMessageCommands(g globalOpts) []oden.Command {
	sendParams := append([]oden.Parameter{
		{Name: "to", Type: "string", Description: "Recipient pane ID (or the first positional argument)"},
	}, messageInputParams...)
	inboxParams := []oden.Parameter{
		{Name: "pane", Type: "string", Description: "Recipient pane ID (default: $MTM_PANE_ID)"},
		{Name: "all", Type: "bool", Description: "Include acknowledged messages"},
		{Name: "thread", Type: "string", Description: "Only messages of this thread"},
		{Name: "limit", Type: "int", Default: "0", Description: "Keep the newest N messages (0 = all)"},
		{Name: "ack", Type: "bool", Description: "Mark the listed messages read"},
	}
	replyParams := append([]oden.Parameter{
		{Name: "to", Type: "string", Description: "Override recipient (default: the original sender)"},
	}, messageInputParams...)
	return []oden.Command{
		{
			Name:       "send",
			Summary:    "send a mailbox message to a pane; the daemon wakes it only once it is idle",
			Usage:      "<to-pane> [body...] [--kind request|report|blocked|review_request|approved|rejected|note] [--lane ID] [--subject S]",
			Parameters: sendParams,
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(sendParams)
				if err != nil {
					return err
				}
				pos := p.Positional()
				to := strings.TrimSpace(p.String("to"))
				if to == "" && len(pos) > 0 {
					to, pos = pos[0], pos[1:]
				}
				if to == "" {
					return errors.New("usage: mtm msg send <to-pane> [body...] [--kind KIND]")
				}
				in, err := messageInputFromParams(p, c.Stdin, pos)
				if err != nil {
					return err
				}
				in.To = to
				return withMailTools(g, func(m *mcpui.MailTools) (any, error) {
					return m.Send(c.Context, cliActor, mcpui.AgentSendArgs{MessageInput: in})
				}, c.Stdout)
			},
		},
		{
			Name:       "inbox",
			Summary:    "list unread mailbox messages for a pane (default $MTM_PANE_ID)",
			Usage:      "[--pane ID] [--all] [--thread T] [--limit N] [--ack]",
			Parameters: inboxParams,
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(inboxParams)
				if err != nil {
					return err
				}
				pane := strings.TrimSpace(p.String("pane"))
				if pos := p.Positional(); pane == "" && len(pos) == 1 {
					pane = pos[0]
				}
				return withMailTools(g, func(m *mcpui.MailTools) (any, error) {
					return m.Inbox(cliActor, mcpui.AgentInboxArgs{
						Pane: pane, All: p.Bool("all"), Thread: p.String("thread"), Limit: p.Int("limit"), Ack: p.Bool("ack"),
					})
				}, c.Stdout)
			},
		},
		{
			Name:    "ack",
			Summary: "mark mailbox messages read",
			Usage:   "<message-id>...",
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(nil)
				if err != nil {
					return err
				}
				ids := p.Positional()
				if len(ids) == 0 {
					return errors.New("usage: mtm msg ack <message-id>...")
				}
				return withMailTools(g, func(m *mcpui.MailTools) (any, error) {
					return m.Ack(cliActor, mcpui.AgentAckArgs{IDs: ids})
				}, c.Stdout)
			},
		},
		{
			Name:       "reply",
			Summary:    "reply to a mailbox message on its thread (goes to the original sender)",
			Usage:      "<message-id> [body...] [--kind KIND] [--to PANE]",
			Parameters: replyParams,
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(replyParams)
				if err != nil {
					return err
				}
				pos := p.Positional()
				if len(pos) == 0 {
					return errors.New("usage: mtm msg reply <message-id> [body...] [--kind KIND]")
				}
				in, err := messageInputFromParams(p, c.Stdin, pos[1:])
				if err != nil {
					return err
				}
				in.To = strings.TrimSpace(p.String("to"))
				return withMailTools(g, func(m *mcpui.MailTools) (any, error) {
					return m.Reply(c.Context, cliActor, mcpui.AgentReplyArgs{ID: pos[0], MessageInput: in})
				}, c.Stdout)
			},
		},
	}
}

// withMailTools opens the store, runs fn over the live-daemon mailbox tools
// and prints its result as JSON.
func withMailTools(g globalOpts, fn func(*mcpui.MailTools) (any, error), w io.Writer) error {
	store, err := openStoreWithSeed(g)
	if err != nil {
		return err
	}
	defer store.Close()
	res, err := fn(newCLIToolbox(g, store).Mail)
	if err != nil {
		return err
	}
	return printJSON(w, res)
}
