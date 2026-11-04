package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// presenceServer answers attach-presence with a nonce (keeping the
// connection) and agent-transcript only with that nonce.
func presenceServer(t *testing.T, socket string, presences *atomic.Int32) {
	t.Helper()
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadBytes('\n')
					if err != nil {
						return
					}
					var req struct {
						ID     string         `json:"id"`
						Verb   string         `json:"verb"`
						Params map[string]any `json:"params"`
					}
					if json.Unmarshal(line, &req) != nil {
						return
					}
					reply := map[string]any{"id": req.ID}
					switch {
					case req.Verb == "attach-presence":
						presences.Add(1)
						reply["result"] = map[string]any{"human_nonce": "n1", "client_id": "c1"}
					case req.Verb == "agent-transcript" && req.Params["human_nonce"] == "n1":
						reply["result"] = map[string]any{"entries": []any{}}
					default:
						reply["error"] = map[string]any{"code": "not_human", "message": "for the person"}
					}
					out, _ := json.Marshal(reply)
					if _, err := c.Write(append(out, '\n')); err != nil {
						return
					}
				}
			}(c)
		}
	}()
}

func TestHumanNonceOpensOnePresenceAndReadsTranscripts(t *testing.T) {
	dir := runtimeDir(t)
	socket := filepath.Join(dir, "p.sock")
	var presences atomic.Int32
	presenceServer(t, socket, &presences)
	c := New(socket)
	ctx := context.Background()
	if _, err := c.Transcript(ctx, TranscriptParams{Window: "w"}); err == nil {
		t.Fatal("a read without the nonce passed")
	}
	for range 2 {
		nonce, err := c.HumanNonce(ctx)
		if err != nil || nonce != "n1" {
			t.Fatalf("nonce %q %v", nonce, err)
		}
		if _, err := c.Transcript(ctx, TranscriptParams{Window: "w", HumanNonce: nonce}); err != nil {
			t.Fatal(err)
		}
	}
	if presences.Load() != 1 {
		t.Fatalf("opened %d presences", presences.Load())
	}
	c.DropPresence()
	if _, err := c.HumanNonce(ctx); err != nil || presences.Load() != 2 {
		t.Fatalf("renew: %v, %d presences", err, presences.Load())
	}
	c.DropPresence()
}

// An older daemon refuses capture-pane's unwrap by name: Capture asks
// again once without it, and sends it no more on that client.
func TestCaptureDropsUnwrapAnOlderDaemonRefuses(t *testing.T) {
	dir := runtimeDir(t)
	socket := filepath.Join(dir, "c.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	var withUnwrap, without atomic.Int32
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadBytes('\n')
				if err != nil {
					return
				}
				var req struct {
					ID     string         `json:"id"`
					Params map[string]any `json:"params"`
				}
				if json.Unmarshal(line, &req) != nil {
					return
				}
				reply := map[string]any{"id": req.ID}
				if _, ok := req.Params["unwrap"]; ok {
					withUnwrap.Add(1)
					reply["error"] = map[string]any{"code": "invalid_params", "message": "verb capture-pane has no parameter unwrap"}
				} else {
					without.Add(1)
					reply["result"] = map[string]any{"content": "a\nb\n"}
				}
				out, _ := json.Marshal(reply)
				c.Write(append(out, '\n'))
			}(c)
		}
	}()
	c := New(socket)
	for range 2 {
		got, err := c.Capture(context.Background(), CaptureParams{Window: "w", Unwrap: true, Trim: true})
		if err != nil || got.Content != "a\nb\n" {
			t.Fatalf("capture %+v %v", got, err)
		}
	}
	if withUnwrap.Load() != 1 || without.Load() != 2 {
		t.Fatalf("refused %d, answered %d", withUnwrap.Load(), without.Load())
	}
	// Another refusal is not swallowed.
	if paramRejected(&Error{Code: "invalid_params", Message: "bad start"}, "unwrap") {
		t.Fatal("any invalid_params counted")
	}
}
