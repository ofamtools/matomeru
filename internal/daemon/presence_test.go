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
