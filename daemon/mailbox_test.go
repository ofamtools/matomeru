package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
)

// openCapturePane opens a raw `cat` pane whose input lands in a file.
func openCapturePane(t *testing.T, client *Client, id string) string {
	t.Helper()
	dir := t.TempDir()
	ready, capture := filepath.Join(dir, "ready"), filepath.Join(dir, "capture")
	cmd := "stty raw -echo && touch '" + ready + "' && exec cat > '" + capture + "'"
	if err := client.Open(context.Background(), OpenRequest{ID: id, Cmd: cmd}); err != nil {
		t.Fatalf("open %s: %v", id, err)
	}
	waitFor(t, 3*time.Second, id+" ready", func() bool { _, err := os.Stat(ready); return err == nil })
	return capture
}

func fileText(path string) string {
	raw, _ := os.ReadFile(path)
	return string(raw)
}

func setState(t *testing.T, client *Client, pane, state string) {
	t.Helper()
	if _, err := client.HookEvent(context.Background(), HookEvent{PaneID: pane, Harness: "claude", Kind: HookKindState, State: state}); err != nil {
		t.Fatalf("state %s %s: %v", pane, state, err)
	}
}

func TestMailWakeHeldUntilRecipientIdleAndNotifyPaneMails(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv, client := startTestServer(t, Config{Store: store, DisableMCP: true})
	l, stop := srv.callbacks.listen("orch")
	defer stop()
	capture := openCapturePane(t, client, "orch")
	ctx := context.Background()

	// A busy recipient gets the mail.received event but no keystrokes.
	setState(t, client, "orch", StateRunning)
	msg, err := store.SendMessage(data.MessageInput{From: "w0", To: "orch", Kind: data.MsgKindRequest, Subject: "look at this"}, daemonActor)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.NotifyMail(ctx, mcpui.NoticeFromMessage(*msg)); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-l.ch:
		if ev.Event != EventMailReceived || ev.MessageID != msg.ID || ev.FromPane != "w0" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no mail.received event")
	}
	time.Sleep(100 * time.Millisecond)
	if got := fileText(capture); got != "" {
		t.Fatalf("wake typed into an active turn: %q", got)
	}

	// Its agent.done releases the held wake.
	setState(t, client, "orch", StateDone)
	waitFor(t, 3*time.Second, "wake paste", func() bool { return strings.Contains(fileText(capture), "look at this") })

	// notifyPane: the worker's agent.done becomes a mailbox report and, the
	// recipient being idle, an immediate wake.
	if err := NewClientPaneRuntime(srv.SocketPath(), "").(mcpui.PaneCallbackRuntime).RegisterOnDone(ctx, "w1", mcpui.PaneOnDone{NotifyPane: "orch"}); err != nil {
		t.Fatal(err)
	}
	setState(t, client, "w1", StateRunning)
	setState(t, client, "w1", StateDone)
	waitFor(t, 3*time.Second, "notifyPane wake", func() bool { return strings.Contains(fileText(capture), "agent.done w1") })
	inbox, err := store.Inbox(data.InboxQuery{Pane: "orch"})
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 2 || inbox[1].Text("msgKind") != data.MsgKindReport {
		t.Fatalf("inbox = %+v", inbox)
	}
	if from, _ := inbox[1].RefField("fromPane"); from.ID != "w1" {
		t.Fatalf("report from = %v", from)
	}
}

func TestHookRuleOnMatchesEventLists(t *testing.T) {
	cases := []struct {
		on, event string
		want      bool
	}{
		{"", EventAgentDone, true},
		{"", EventAgentNeedsInput, false},
		{"*", EventMailReceived, true},
		{OnDoneNotifyEvents, EventAgentBlocked, true},
		{OnDoneNotifyEvents, EventMailReceived, false},
	}
	for _, c := range cases {
		r := HookRule{ID: "r", On: c.on, Run: HookRun{NotifyPane: "x"}}
		if got := r.Matches(AgentDoneEvent{Event: c.event}); got != c.want {
			t.Errorf("On %q event %q = %v, want %v", c.on, c.event, got, c.want)
		}
	}
}
