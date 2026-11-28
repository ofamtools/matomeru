package mcpui

import (
	"context"
	"encoding/json"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

type mailRuntime struct {
	PaneRuntime
	notices []MailNotice
}

func (r *mailRuntime) NotifyMail(_ context.Context, n MailNotice) error {
	r.notices = append(r.notices, n)
	return nil
}

func callMail(t *testing.T, box *Toolbox, ctx context.Context, name string, args any, out any) {
	t.Helper()
	raw, _ := json.Marshal(args)
	res, err := box.Call(ctx, core.Actor{Kind: "agent", ID: "test"}, name, raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	b, _ := json.Marshal(res)
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
}

func TestMailboxSendInboxReplyAck(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	box := NewToolbox(store, nil)
	rt := &mailRuntime{}
	box.Panes.Runtime = rt
	worker := WithPaneID(context.Background(), "w1")
	orch := WithPaneID(context.Background(), "orch")

	var sent AgentSendResult
	callMail(t, box, worker, ToolAgentSend, map[string]any{"to": "orch", "msgKind": "review_request", "lane": "lane:L1", "body": "branch pushed\nmore", "artifacts": []string{"daemon/mailbox.go"}}, &sent)
	if !sent.Notified || len(rt.notices) != 1 || rt.notices[0].FromPane != "w1" || rt.notices[0].Lane != "L1" {
		t.Fatalf("send = %+v notices = %+v", sent, rt.notices)
	}
	if sent.Message.Text("subject") != "branch pushed" {
		t.Fatalf("subject = %q", sent.Message.Text("subject"))
	}

	var inbox AgentInboxResult
	callMail(t, box, orch, ToolAgentInbox, map[string]any{}, &inbox)
	if inbox.Pane != "orch" || len(inbox.Messages) != 1 {
		t.Fatalf("inbox = %+v", inbox)
	}

	var reply AgentSendResult
	callMail(t, box, orch, ToolAgentReply, map[string]any{"id": sent.Message.ID, "msgKind": "approved", "body": "ship it"}, &reply)
	if to, _ := reply.Message.RefField("toPane"); to.ID != "w1" || reply.Message.Text("thread") != sent.Message.ID || reply.Message.Text("subject") != "Re: branch pushed" {
		t.Fatalf("reply = %+v", reply.Message)
	}

	var acked AgentAckResult
	callMail(t, box, orch, ToolAgentAck, map[string]any{"id": sent.Message.ID}, &acked)
	callMail(t, box, orch, ToolAgentInbox, map[string]any{}, &inbox)
	if len(acked.Acked) != 1 || len(inbox.Messages) != 0 {
		t.Fatalf("after ack: acked = %d inbox = %d", len(acked.Acked), len(inbox.Messages))
	}
}
