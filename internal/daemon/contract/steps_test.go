//go:build tuioscontract

package contract

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ofam.dev/matomeru/internal/daemon"
)

func (d *daemonUnder) shellSteps(sess, w1 string) {
	d.step("verb", "send-text", func() error {
		if err := d.typeLine(sess, w1, "echo hello-$((40+2))"); err != nil {
			return err
		}
		return d.screen(sess, w1, "hello-42", 10*time.Second)
	})
	d.step("verb", "send-text/paste", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		if err := d.c.SendText(ctx, daemon.SendTextParams{Session: sess, Window: w1, Text: "echo pasted-$((1+1))", Paste: true, Submit: true}); err != nil {
			return err
		}
		return d.screen(sess, w1, "pasted-2", 10*time.Second)
	})
	d.step("verb", "send-keys", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		if err := d.c.SendText(ctx, daemon.SendTextParams{Session: sess, Window: w1, Text: "echo keys-$((3+4))"}); err != nil {
			return err
		}
		if err := d.c.SendKeys(ctx, daemon.SendKeysParams{Session: sess, Window: w1, Keys: "Enter"}); err != nil {
			return err
		}
		if err := d.screen(sess, w1, "keys-7", 10*time.Second); err != nil {
			return err
		}
		return d.c.SendKeys(ctx, daemon.SendKeysParams{Session: sess, Window: w1, Keys: "ctrl+c"})
	})
	palette := make([]string, 16)
	for i := range palette {
		palette[i] = fmt.Sprintf("#%02x%02x%02x", i*10, i*10, i*10)
	}
	one, three := 1, 3
	variants := []struct {
		id string
		p  daemon.CaptureParams
	}{
		{"plain", daemon.CaptureParams{}},
		{"source-visible", daemon.CaptureParams{Source: "visible"}},
		{"source-recent", daemon.CaptureParams{Source: "recent"}},
		{"scrollback", daemon.CaptureParams{Scrollback: true}},
		{"ansi", daemon.CaptureParams{ANSI: true}},
		{"styled", daemon.CaptureParams{Styled: true}},
		{"styled-resolved-palette", daemon.CaptureParams{Styled: true, Resolved: true, Palette: palette}},
		{"lines", daemon.CaptureParams{Lines: 5}},
		{"start-end", daemon.CaptureParams{Start: &one, End: &three}},
	}
	for _, v := range variants {
		d.step("verb", "capture-pane/"+v.id, func() error {
			v.p.Session, v.p.Window = sess, w1
			ctx, cancel := d.ctx()
			defer cancel()
			got, err := d.c.Capture(ctx, v.p)
			if err != nil {
				return err
			}
			if got.BootID == "" {
				return fmt.Errorf("capture-pane result has no boot_id: %+v", got)
			}
			if v.id == "start-end" && strings.Count(strings.TrimRight(got.Content, "\n"), "\n") > 2 {
				return fmt.Errorf("start=1 end=3 returned %q", got.Content)
			}
			if v.id == "lines" && strings.Count(strings.TrimRight(got.Content, "\n"), "\n") > 4 {
				return fmt.Errorf("lines=5 returned %q", got.Content)
			}
			if got.Content == "" {
				return fmt.Errorf("empty content")
			}
			return nil
		})
	}
	// P4: the raw call, exactly what turns_model.go:134 sends, per parameter.
	d.step("verb", "capture-pane/unwrap-raw", func() error {
		var out daemon.CaptureResult
		return d.call("capture-pane", daemon.CaptureParams{Session: sess, Window: w1, Source: "recent", Unwrap: true}, &out)
	})
	d.step("verb", "capture-pane/trim-raw", func() error {
		var out daemon.CaptureResult
		return d.call("capture-pane", daemon.CaptureParams{Session: sess, Window: w1, Source: "recent", Trim: true}, &out)
	})
	d.step("verb", "capture-pane/unwrap-fallback", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		c := daemon.ForServer(d.server) // a fresh client, so no param is already marked unsupported
		got, err := c.Capture(ctx, daemon.CaptureParams{Session: sess, Window: w1, Source: "recent", Unwrap: true})
		if err != nil {
			return err
		}
		if !strings.Contains(got.Content, "hello-42") {
			return fmt.Errorf("fallback capture lost content: %q", tail(got.Content))
		}
		return nil
	})
	d.step("verb", "capture-pane/turns_model(unwrap+trim)", func() error {
		// cmd/mtm/turns_model.go:134 sends Unwrap and Trim together.
		ctx, cancel := d.ctx()
		defer cancel()
		c := daemon.ForServer(d.server)
		start, end := 1, 20
		_, err := c.Capture(ctx, daemon.CaptureParams{Session: sess, Window: w1, Source: "recent", Start: &start, End: &end, Unwrap: true, Trim: true})
		return err
	})
}

func (d *daemonUnder) windowSteps(sess, w1 string) {
	mark := d.events.mark()
	d.step("verb", "set-window/name", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		if err := d.c.RenameWindow(ctx, sess, w1, "w1b"); err != nil {
			return err
		}
		panes, err := d.c.Panes(ctx, sess)
		if err != nil {
			return err
		}
		for _, p := range panes {
			if p.ID == w1 && p.Name == "w1b" {
				return nil
			}
		}
		return fmt.Errorf("rename not visible in %+v", panes)
	})
	d.step("event", "event/window-retitled", func() error {
		_, err := d.events.wait("window-retitled", mark, func(e daemon.Event) bool { return e.Window == w1 }, 10*time.Second)
		return err
	})
	d.step("event", "event/window-retitled(osc-title)", func() error {
		// The shell's title only shows (and retitles) once the name is cleared.
		ctx, cancel := d.ctx()
		defer cancel()
		if err := d.c.RenameWindow(ctx, sess, w1, ""); err != nil {
			return err
		}
		m := d.events.mark()
		if err := d.typeLine(sess, w1, `printf '\033]0;ct-title\007'`); err != nil {
			return err
		}
		_, err := d.events.wait("window-retitled", m, func(e daemon.Event) bool { return e.Window == w1 }, 10*time.Second)
		return err
	})
	d.step("verb", "resize", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		if err := daemon.Resize(ctx, d.c, sess, w1, 90, 30); err != nil {
			return err
		}
		// Resize discards the reply; the raw call shows what the daemon answers.
		// A detached session keeps its nominal size, so list-windows is not checked.
		var out struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		}
		if err := d.call("resize", map[string]any{"session": sess, "window": w1, "width": 90, "height": 30}, &out); err != nil {
			return err
		}
		if out.Width != 90 || out.Height != 30 {
			return fmt.Errorf("resize reply %+v", out)
		}
		return nil
	})
	d.step("event", "event/window-minimized+restored", func() error {
		m := d.events.mark()
		if err := d.call("set-window", map[string]any{"session": sess, "window": w1, "minimized": true}, nil); err != nil {
			return err
		}
		if _, err := d.events.wait("window-minimized", m, func(e daemon.Event) bool { return e.Window == w1 }, 10*time.Second); err != nil {
			return err
		}
		if err := d.call("set-window", map[string]any{"session": sess, "window": w1, "minimized": false}, nil); err != nil {
			return err
		}
		_, err := d.events.wait("window-restored", m, func(e daemon.Event) bool { return e.Window == w1 }, 10*time.Second)
		return err
	})
	d.step("event", "event/window-moved", func() error {
		m := d.events.mark()
		if err := d.call("move-window", map[string]any{"session": sess, "window": w1, "workspace": 2}, nil); err != nil {
			return err
		}
		if _, err := d.events.wait("window-moved", m, func(e daemon.Event) bool { return e.Window == w1 }, 10*time.Second); err != nil {
			return err
		}
		return d.call("move-window", map[string]any{"session": sess, "window": w1, "workspace": 1}, nil)
	})
	d.step("event", "event/output", func() error {
		m := d.events.mark()
		if err := d.typeLine(sess, w1, "echo out-evt"); err != nil {
			return err
		}
		_, err := d.events.wait("output", m, func(e daemon.Event) bool { return e.Window == w1 }, 10*time.Second)
		return err
	})
}

func (d *daemonUnder) commandSteps(sess, w1 string) {
	m := d.events.mark()
	// A shell that marks commands with OSC 133 (prompt, command line, start, finish).
	line := `printf '\033]133;A\007$ \033]133;B\007ctcmd-xyz\n\033]133;C\007ctout\n\033]133;D;3\007'`
	d.step("event", "event/command-started", func() error {
		if err := d.typeLine(sess, w1, line); err != nil {
			return err
		}
		_, err := d.events.wait("command-started", m, func(e daemon.Event) bool { return e.Window == w1 }, 10*time.Second)
		return err
	})
	d.step("event", "event/command-finished", func() error {
		_, err := d.events.wait("command-finished", m, func(e daemon.Event) bool { return e.Window == w1 }, 10*time.Second)
		return err
	})
	d.step("verb", "command-history", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		deadline := time.Now().Add(5 * time.Second)
		for {
			got, err := d.c.History(ctx, daemon.HistoryParams{Session: sess, Window: w1, Limit: 10, Query: "ctcmd"})
			if err != nil {
				return err
			}
			for _, c := range got {
				if strings.Contains(c.Cmdline, "ctcmd-xyz") {
					if c.Exit == nil || *c.Exit != 3 || c.Window != w1 {
						return fmt.Errorf("history entry %+v", c)
					}
					return nil
				}
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("no ctcmd-xyz in %+v", got)
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	d.step("verb", "capture-pane/last-command-output", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		got, err := d.c.Capture(ctx, daemon.CaptureParams{Session: sess, Window: w1, Source: "last-command-output"})
		if err != nil {
			return err
		}
		if !strings.Contains(got.Content, "ctout") || got.ExitCode == nil || *got.ExitCode != 3 {
			return fmt.Errorf("last-command-output %+v", got)
		}
		return nil
	})
	d.step("verb", "wait-for/window-output", func() error {
		go func() {
			time.Sleep(700 * time.Millisecond)
			d.typeLine(sess, w1, "echo wf-$((5*5))")
		}()
		return d.waitFor(daemon.WaitForParams{Condition: "window-output", Session: sess, Window: w1, Pattern: "wf-25", Timeout: 10000})
	})
	d.step("error", "error/timeout(wait-for)", func() error {
		return wantCode(d.waitFor(daemon.WaitForParams{Condition: "window-output", Session: sess, Window: w1, Pattern: "never-appears-zz", Timeout: 700}), "timeout")
	})
	d.step("verb", "wait-for/session-exists", func() error {
		return d.waitFor(daemon.WaitForParams{Condition: "session-exists", Session: sess, Timeout: 3000})
	})
	d.step("verb", "wait-for/window-idle", func() error {
		return d.waitFor(daemon.WaitForParams{Condition: "window-idle", Session: sess, Window: w1, Timeout: 10000})
	})
	d.step("verb", "wait-for/command-finished", func() error {
		go func() {
			time.Sleep(700 * time.Millisecond)
			d.typeLine(sess, w1, `printf '\033]133;A\007$ \033]133;B\007wfcmd\n\033]133;C\007\033]133;D;0\007'`)
		}()
		return d.waitFor(daemon.WaitForParams{Condition: "command-finished", Session: sess, Window: w1, Timeout: 10000})
	})
}

func (d *daemonUnder) waitFor(p daemon.WaitForParams) error {
	ctx, cancel := d.ctx()
	defer cancel()
	return d.c.WaitFor(ctx, p)
}

type frameResult struct {
	f   daemon.Frame
	err error
}

func pumpFrames(s *daemon.PaneStream) <-chan frameResult {
	ch := make(chan frameResult, 256)
	go func() {
		defer close(ch)
		for {
			f, err := s.ReadFrame()
			ch <- frameResult{f, err}
			if err != nil {
				return
			}
		}
	}()
	return ch
}

// collect reads frames until done reports true or timeout.
func collect(ch <-chan frameResult, seen map[byte]int, text *strings.Builder, timeout time.Duration, done func() bool) error {
	deadline := time.After(timeout)
	for !done() {
		select {
		case r, ok := <-ch:
			if !ok {
				return fmt.Errorf("stream closed")
			}
			if r.err != nil {
				return r.err
			}
			seen[r.f.Type]++
			if r.f.Type == 'S' || r.f.Type == 'O' {
				text.Write(r.f.Data)
			}
		case <-deadline:
			return fmt.Errorf("timed out; frames seen %v", seen)
		}
	}
	return nil
}

func (d *daemonUnder) streamSteps(sess, w1 string) {
	ctx, cancel := d.ctx()
	r, err := d.c.NewWindow(ctx, daemon.NewWindowParams{Session: sess, Name: "w2", Focus: false, Command: []string{"/bin/sh"}})
	cancel()
	if err != nil {
		d.t.Errorf("stream window: %v", err)
		return
	}
	w2 := r.WindowID
	seen := map[byte]int{}
	var text strings.Builder
	var stream *daemon.PaneStream
	var ch <-chan frameResult
	var lastSeq uint64
	var boot string
	d.step("stream", "stream-pane/open+S", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		s, err := d.c.StreamPane(context.Background(), daemon.StreamParams{Session: sess, Window: w2, LeaseCols: 100, LeaseRows: 30,
			Viewer: map[string]any{"bg": "#101010", "fg": "#e0e0e0"}})
		_ = ctx
		if err != nil {
			return err
		}
		stream, ch = s, pumpFrames(s)
		if s.Info.Mode == "" || s.Info.BootID == "" || s.Info.Window != w2 || s.Info.Session != sess {
			return fmt.Errorf("stream info %+v", s.Info)
		}
		boot = s.Info.BootID
		return collect(ch, seen, &text, 10*time.Second, func() bool { return seen['S'] > 0 })
	})
	if stream == nil {
		return
	}
	defer stream.Close()
	d.step("stream", "stream-pane/I-input+O", func() error {
		if err := stream.Input([]byte("echo streamed-$((6*7))\n")); err != nil {
			return err
		}
		return collect(ch, seen, &text, 10*time.Second, func() bool { return strings.Contains(text.String(), "streamed-42") && seen['O'] > 0 })
	})
	d.step("stream", "stream-pane/L-lease+R", func() error {
		if err := stream.Lease(90, 25); err != nil {
			return err
		}
		ctx, cancel := d.ctx()
		defer cancel()
		if err := daemon.Resize(ctx, d.c, sess, w2, 70, 20); err != nil {
			return err
		}
		return collect(ch, seen, &text, 10*time.Second, func() bool { return seen['R'] > 0 })
	})
	d.step("stream", "stream-pane/E-error-frame", func() error {
		return d.rawErrorFrame(sess, w2, seen)
	})
	d.step("stream", "stream-pane/resume", func() error {
		_ = lastSeq
		s2, err := d.c.StreamPane(context.Background(), daemon.StreamParams{Session: sess, Window: w2, FromSeq: 1, BootID: boot})
		if err != nil {
			return err
		}
		defer s2.Close()
		if s2.Info.BootID != boot {
			return fmt.Errorf("boot_id changed: %q vs %q", s2.Info.BootID, boot)
		}
		if s2.Info.Mode != "resume" && s2.Info.Mode != "snapshot" {
			return fmt.Errorf("mode %q", s2.Info.Mode)
		}
		return nil
	})
	d.step("verb", "close-window", func() error {
		m := d.events.mark()
		// cmd/mtm/panes.go:1202
		if err := d.call("close-window", map[string]any{"session": sess, "window": w2}, nil); err != nil {
			return err
		}
		if _, err := d.events.wait("window-closed", m, func(e daemon.Event) bool { return e.Window == w2 }, 10*time.Second); err != nil {
			return err
		}
		return nil
	})
	d.step("stream", "stream-pane/X-exit-frame", func() error {
		return collect(ch, seen, &text, 10*time.Second, func() bool { return seen['X'] > 0 })
	})
	d.step("error", "error/window_not_found", func() error {
		return wantCode(d.call("get-window", map[string]any{"session": sess, "window": w2}, nil), "window_not_found")
	})
}

// rawErrorFrame opens a stream by hand and sends a malformed lease frame:
// the one place the suite writes protocol bytes itself, because mtm's
// client never sends a malformed frame.
func (d *daemonUnder) rawErrorFrame(sess, win string, seen map[byte]int) error {
	conn, err := net.Dial("unix", d.c.Socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	req, _ := json.Marshal(map[string]any{"id": "e1", "verb": "stream-pane", "params": map[string]any{"session": sess, "window": win}})
	conn.Write(append(req, '\n'))
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	// reply line
	var line []byte
	one := make([]byte, 1)
	for {
		if _, err := conn.Read(one); err != nil {
			return err
		}
		if one[0] == '\n' {
			break
		}
		line = append(line, one[0])
	}
	frame := append([]byte{'L', 0, 0, 0, 1}, 0x07) // lease payload must be 4 bytes
	conn.Write(frame)
	for {
		var h [5]byte
		if err := readFull(conn, h[:]); err != nil {
			return fmt.Errorf("no E frame: %v", err)
		}
		body := make([]byte, binary.BigEndian.Uint32(h[1:]))
		if err := readFull(conn, body); err != nil {
			return err
		}
		if h[0] == 'E' {
			var e daemon.Error
			if json.Unmarshal(body, &e) != nil || e.Code == "" {
				return fmt.Errorf("E frame payload %q", body)
			}
			seen['E']++
			return nil
		}
	}
}

func readFull(c net.Conn, b []byte) error {
	for n := 0; n < len(b); {
		m, err := c.Read(b[n:])
		if err != nil {
			return err
		}
		n += m
	}
	return nil
}

func (d *daemonUnder) agentSteps(sess, w1 string) {
	var agentWin string
	d.step("verb", "start-agent", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		res, err := d.c.StartAgent(ctx, daemon.StartAgentParams{Session: sess, Agent: "fakeagent", Name: "agent1", Cwd: filepath.Join(d.root, "work"),
			ReadyTimeout: 3000, Focus: false, Grants: []string{"none"}})
		agentWin = res.WindowID
		if err != nil {
			return err
		}
		if res.WindowID == "" || res.Session != sess {
			return fmt.Errorf("start-agent result %+v", res)
		}
		return nil
	})
	if agentWin == "" {
		d.t.Errorf("no agent window; agent verbs skipped")
		return
	}
	d.step("event", "event/agent-state+set-agent-state", func() error {
		m := d.events.mark()
		// cmd/mtm/mcp_ops.go:155
		var reply map[string]any
		if err := d.call("set-agent-state", map[string]any{"window": agentWin, "state": "working", "message": "ct", "source": "report", "session": sess}, &reply); err != nil {
			return err
		}
		_, err := d.events.wait("agent-state", m, func(e daemon.Event) bool { return e.Window == agentWin }, 10*time.Second)
		return err
	})
	d.step("verb", "list-agents", func() error {
		var out struct {
			Agents []struct {
				Window string `json:"window_id"`
				State  string `json:"state"`
			} `json:"agents"`
		}
		if err := d.call("list-agents", map[string]any{"all": true, "all_sessions": true}, &out); err != nil {
			return err
		}
		for _, a := range out.Agents {
			if a.Window == agentWin {
				return nil
			}
		}
		return fmt.Errorf("agent window %s missing from %+v", agentWin, out)
	})
	d.step("event", "event/agent-activity+agent-activity", func() error {
		m := d.events.mark()
		act := map[string]any{"event": "prompt", "text": "ct prompt"}
		if err := d.call("report-agent-activity", map[string]any{"session": sess, "window": agentWin, "activity": act}, nil); err != nil {
			return err
		}
		ev, err := d.events.wait("agent-activity", m, func(e daemon.Event) bool { return e.Window == agentWin }, 10*time.Second)
		if err != nil {
			return err
		}
		// cmd/mtm/journal.go:170 reads the entry from the event's data.
		if _, ok := ev.Data["entry"].(map[string]any); !ok {
			return fmt.Errorf("agent-activity event has no entry object: %v", ev.Data)
		}
		ctx, cancel := d.ctx()
		defer cancel()
		got, err := d.c.Activity(ctx, daemon.ActivityParams{Session: sess, Window: agentWin, Limit: 10, Recap: true})
		if err != nil {
			return err
		}
		if len(got.Entries) == 0 || got.Entries[0].Kind != "prompt" || got.LastSeq == 0 || !got.Untrusted {
			return fmt.Errorf("activity %+v", got)
		}
		return nil
	})
	d.step("verb", "agent-activity/since_seq", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		got, err := d.c.Activity(ctx, daemon.ActivityParams{Session: sess, Window: agentWin, SinceSeq: 1 << 40})
		if err != nil {
			return err
		}
		if len(got.Entries) != 0 {
			return fmt.Errorf("entries after a huge since_seq: %+v", got.Entries)
		}
		return nil
	})
	var nonce string
	d.step("verb", "attach-presence", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		p, err := d.c.Presence(ctx, sess)
		if err != nil {
			return err
		}
		defer p.Close()
		if p.Nonce == "" || p.ClientID == "" || p.Session != sess {
			return fmt.Errorf("presence %+v", p)
		}
		// The held presence lives as long as the context it was opened with.
		n, err := d.c.HumanNonce(context.Background())
		if err != nil || n == "" {
			return fmt.Errorf("HumanNonce %q %v", n, err)
		}
		nonce = n
		return nil
	})
	d.step("verb", "queue-prompt", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		r, err := d.c.QueuePrompt(ctx, daemon.QueuePromptParams{Session: sess, Window: agentWin, Text: "queued one", From: daemon.FromHuman, HumanNonce: nonce})
		if err != nil {
			return err
		}
		if r.ID == "" || r.Queued < 1 || r.Position < 0 {
			return fmt.Errorf("queue-prompt %+v", r)
		}
		return nil
	})
	d.step("error", "error/queue_full", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		var last error
		for i := 0; i < daemon.QueueMax+3; i++ {
			_, last = d.c.QueuePrompt(ctx, daemon.QueuePromptParams{Session: sess, Window: agentWin, Text: fmt.Sprintf("fill %d", i), From: daemon.FromHuman, HumanNonce: nonce})
			if last != nil {
				if i+1 != daemon.QueueMax {
					// one message is already queued; the refusal must come at QueueMax+1 messages in total
					return fmt.Errorf("queue refused at message %d (+1 earlier), mtm assumes QueueMax=%d: %v", i+1, daemon.QueueMax, last)
				}
				return wantCode(last, daemon.CodeQueueFull)
			}
		}
		return fmt.Errorf("no queue_full after %d messages", daemon.QueueMax+3)
	})
	d.step("verb", "agent-transcript", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		_, err := d.c.Transcript(ctx, daemon.TranscriptParams{Session: sess, Window: agentWin, HumanNonce: nonce, Limit: 10})
		if err == nil {
			return nil
		}
		// An agent with no transcript is a typed refusal, not a protocol error.
		switch errCode(err) {
		case "no_transcript", "unsupported_harness":
			return nil
		}
		return err
	})
	d.step("error", "error/not_human(agent-transcript)", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		_, err := d.c.Transcript(ctx, daemon.TranscriptParams{Session: sess, Window: agentWin, HumanNonce: "wrong-nonce", Limit: 10})
		return wantCode(err, "not_human")
	})
}

func (d *daemonUnder) presenceSteps(sess, w1 string) {
	paneEnv := filepath.Join(d.root, "pane-env.txt")
	var paneEnvText string
	d.step("env", "env/pane-TUIOS_*+event/window-exit", func() error {
		m := d.events.mark()
		ctx, cancel := d.ctx()
		defer cancel()
		r, err := d.c.NewWindow(ctx, daemon.NewWindowParams{Session: sess, Name: "envpane", Focus: false,
			Command: []string{"/bin/sh", "-c", "env > " + paneEnv + "; sleep 1; exit 3"}})
		if err != nil {
			return err
		}
		ev, err := d.events.wait("window-exit", m, func(e daemon.Event) bool { return e.Window == r.WindowID }, 15*time.Second)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(paneEnv)
		if err != nil {
			return err
		}
		var missing []string
		for _, k := range []string{"TUIOS_SOCKET", "TUIOS_SERVER", "TUIOS_SESSION", "TUIOS_PANE_ID", "TUIOS_WINDOW_ID"} {
			if !strings.Contains("\n"+string(raw), "\n"+k+"=") {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("pane env lacks %v", missing)
		}
		if !strings.Contains(string(raw), "TUIOS_SOCKET="+d.c.Socket) {
			return fmt.Errorf("TUIOS_SOCKET is not the daemon socket %s", d.c.Socket)
		}
		_ = ev
		paneEnvText = string(raw)
		return nil
	})
	d.step("env", "env/TUIOS_WINDOW_NAME", func() error {
		if !strings.Contains("\n"+paneEnvText, "\nTUIOS_WINDOW_NAME=") {
			return fmt.Errorf("a pane's environment has no TUIOS_WINDOW_NAME (mtm reads it at cmd/mtm/capture.go:128 and internal/setup/omp.ts:68)")
		}
		return nil
	})
	d.step("error", "error/forbidden(presence-from-pane)", func() error {
		out := filepath.Join(d.root, "inpane.json")
		ctx, cancel := d.ctx()
		defer cancel()
		_, err := d.c.NewWindow(ctx, daemon.NewWindowParams{Session: sess, Name: "inpane", Focus: false,
			Command: []string{"/usr/bin/env", envInPane + "=" + out, os.Args[0]}})
		if err != nil {
			return err
		}
		deadline := time.Now().Add(60 * time.Second)
		for {
			raw, err := os.ReadFile(out)
			if err == nil && len(raw) > 0 {
				var res map[string]string
				if json.Unmarshal(raw, &res) != nil {
					return fmt.Errorf("bad result %q", raw)
				}
				if res["code"] != "forbidden" {
					return fmt.Errorf("presence from inside a pane: code %q (%s)", res["code"], res["error"])
				}
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("the in-pane client wrote no result")
			}
			time.Sleep(200 * time.Millisecond)
		}
	})
}

func (d *daemonUnder) endSteps(sess, w1, bootID string) {
	d.step("event", "subscribe/replay(after_seq,boot_id)", func() error {
		orig := d.events.snapshot()
		var seqs []uint64
		for _, e := range orig {
			// output and agent-activity are not retained for replay; the daemon
			// says so with a gap marker (reason not_retained), checked below.
			if e.Seq != 0 && e.Type != "output" && e.Type != "agent-activity" && e.Type != "subscribed" {
				seqs = append(seqs, e.Seq)
			}
		}
		if len(seqs) < 6 {
			return fmt.Errorf("only %d sequenced events to replay", len(seqs))
		}
		pivot := seqs[len(seqs)/2]
		last := seqs[len(seqs)-1]
		l := d.subscribe(allEventTypes, pivot, bootID)
		defer l.cancel()
		deadline := time.Now().Add(10 * time.Second)
		for {
			got := map[uint64]bool{}
			for _, e := range l.snapshot() {
				got[e.Seq] = true
			}
			var missing []uint64
			for _, s := range seqs {
				if s > pivot && s <= last && !got[s] {
					missing = append(missing, s)
				}
			}
			if got[last] && len(missing) == 0 {
				for s := range got {
					if s != 0 && s <= pivot {
						return fmt.Errorf("replay delivered seq %d at or before after_seq %d", s, pivot)
					}
				}
				for _, e := range l.snapshot() {
					if e.Type == "gap" && e.Data["reason"] == "not_retained" {
						return nil
					}
				}
				return fmt.Errorf("replay skipped output/agent-activity without a gap marker with reason not_retained")
			}
			if time.Now().After(deadline) {
				var types, lost []string
				for _, e := range l.snapshot() {
					types = append(types, fmt.Sprintf("%s#%d %v", e.Type, e.Seq, e.Data))
				}
				for _, e := range orig {
					for _, m := range missing {
						if e.Seq == m {
							lost = append(lost, fmt.Sprintf("%s#%d", e.Type, e.Seq))
						}
					}
				}
				return fmt.Errorf("replay after %d missing %v of %d; got %v (err %v)", pivot, lost, len(seqs), types, l.err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	d.step("event", "subscribe/boot_changed-gap", func() error {
		l := d.subscribe(allEventTypes, 3, "not-the-boot-id")
		defer l.cancel()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			for _, e := range l.snapshot() {
				if e.Type == "gap" || strings.Contains(fmt.Sprint(e.Data), "boot_changed") {
					return nil
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		var types []string
		for _, e := range l.snapshot() {
			types = append(types, e.Type+fmt.Sprint(e.Data))
		}
		return fmt.Errorf("no boot_changed gap marker; got %v", types)
	})
	d.step("verb", "open-host-connection", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		err := d.c.Call(daemon.WithHost(ctx, "no-such-host"), "list-sessions", nil, nil)
		switch errCode(err) {
		case "unknown_host", "host_unreachable", "host_refused":
			return nil
		}
		return fmt.Errorf("open-host-connection to an unknown host: %v", err)
	})
	d.dumpShapes(sess, w1)
	d.step("verb", "rename-session", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		if err := d.c.RenameSession(ctx, sess, sess+"r"); err != nil {
			return err
		}
		list, err := d.c.Sessions(ctx)
		if err != nil || len(list) != 1 || list[0].Name != sess+"r" {
			return fmt.Errorf("sessions after rename: %+v %v", list, err)
		}
		return nil
	})
	d.step("verb", "kill-session", func() error {
		m := d.events.mark()
		if err := d.call("kill-session", map[string]any{"session": sess + "r"}, nil); err != nil { // cmd/mtm/panes.go:1032
			return err
		}
		if _, err := d.events.wait("session-closed", m, nil, 10*time.Second); err != nil {
			return err
		}
		ctx, cancel := d.ctx()
		defer cancel()
		list, err := d.c.Sessions(ctx)
		if err != nil || len(list) != 0 {
			return fmt.Errorf("sessions after kill: %+v %v", list, err)
		}
		return nil
	})
	// Every event type must have been seen at least once.
	seen := map[string]bool{}
	for _, e := range d.events.snapshot() {
		seen[e.Type] = true
	}
	for _, typ := range allEventTypes {
		d.step("event", "event-seen/"+typ, func() error {
			if !seen[typ] {
				return fmt.Errorf("event %s never delivered", typ)
			}
			return nil
		})
	}
}

// dumpShapes writes, when MTM_CONTRACT_REPORT is set, the top-level reply
// keys of the read verbs and the data keys of every event type seen, so two
// versions can be compared beyond what the registry declares.
func (d *daemonUnder) dumpShapes(sess, w1 string) {
	dir := os.Getenv(envReport)
	if dir == "" {
		return
	}
	os.MkdirAll(dir, 0o755)
	reads := map[string]map[string]any{
		"hello": {"client": "mtm"}, "list-sessions": nil, "list-windows": {"session": sess},
		"list-workspaces": {"session": sess}, "session-info": {"session": sess},
		"get-window": {"session": sess, "window": w1}, "capture-pane": {"session": sess, "window": w1},
		"list-agents": {"all": true, "all_sessions": true}, "agent-activity": {"session": sess, "window": w1},
		"command-history": {"session": sess, "window": w1},
	}
	shapes := map[string][]string{}
	for verb, params := range reads {
		var out map[string]any
		if err := d.call(verb, params, &out); err == nil {
			shapes["reply:"+verb] = jsonKeys(out)
		} else {
			shapes["reply:"+verb] = []string{"ERROR " + err.Error()}
		}
	}
	for _, e := range d.events.snapshot() {
		k := "event:" + e.Type
		if _, ok := shapes[k]; !ok {
			keys := jsonKeys(e.Data)
			if e.Seq != 0 {
				keys = append(keys, "(seq)")
			}
			if e.Session != "" {
				keys = append(keys, "(session)")
			}
			if e.Window != "" {
				keys = append(keys, "(window)")
			}
			if e.BootID != "" {
				keys = append(keys, "(boot_id)")
			}
			if e.At != 0 {
				keys = append(keys, "(time)")
			}
			shapes[k] = keys
		}
	}
	raw, _ := json.MarshalIndent(shapes, "", " ")
	os.WriteFile(filepath.Join(dir, "shapes-"+d.version+".json"), raw, 0o644)
}
