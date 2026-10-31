package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ofam.dev/matomeru/internal/daemon"
)

// queueDaemon is a fake tuios for queue-prompt. attach-presence answers
// the nonces in order (none left refuses it, as tuios refuses a process in
// one of its panes); queue-prompt refuses a nonce in stale with not_human,
// a from other than human or a window with not_found, and records the
// params of every call.
type queueDaemon struct {
	mu     sync.Mutex
	nonces []string
	stale  map[string]bool
	calls  []map[string]any
}

func (q *queueDaemon) serve(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mtmq")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "q.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
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
					refuse := func(code, msg string) { reply["error"] = map[string]any{"code": code, "message": msg} }
					q.mu.Lock()
					switch req.Verb {
					case "attach-presence":
						if len(q.nonces) == 0 {
							refuse("forbidden", "a process inside a pane cannot hold a presence")
						} else {
							reply["result"] = map[string]any{"human_nonce": q.nonces[0], "client_id": "c1"}
							q.nonces = q.nonces[1:]
						}
					case "queue-prompt":
						q.calls = append(q.calls, req.Params)
						nonce, _ := req.Params["human_nonce"].(string)
						from, _ := req.Params["from"].(string)
						switch {
						case q.stale[nonce]:
							refuse("not_human", "human_nonce does not belong to a client attached right now")
						case from != "" && from != "human":
							refuse("not_found", "no window "+from)
						case from == "human" && nonce == "":
							refuse("forbidden", "human needs human_nonce")
						default:
							reply["result"] = map[string]any{"id": "q1", "position": 1, "queued": 1}
						}
					default:
						refuse("unknown_verb", req.Verb)
					}
					q.mu.Unlock()
					out, _ := json.Marshal(reply)
					if _, err := c.Write(append(out, '\n')); err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return sock
}

func (q *queueDaemon) sent() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []string
	for _, p := range q.calls {
		from, _ := p["from"].(string)
		nonce, _ := p["human_nonce"].(string)
		out = append(out, from+"/"+nonce)
	}
	return out
}

// The box queues as the person: from human with mtm's presence nonce,
// one presence for every queue, renewed once when tuios refuses it.
func TestPromptQueueIsFromTheHuman(t *testing.T) {
	t.Parallel()
	q := &queueDaemon{nonces: []string{"n1", "n2"}, stale: map[string]bool{}}
	d := tuios{Client: daemon.New(q.serve(t))}
	defer d.DropPresence()
	p := pane{Session: "work", Pane: daemon.Pane{ID: "w1"}}
	ctx := context.Background()
	for range 2 {
		if got, err := d.PromptQueue(ctx, p, "then commit"); err != nil || got.ID != "q1" {
			t.Fatalf("queue: %+v %v", got, err)
		}
	}
	if got := strings.Join(q.sent(), " "); got != "human/n1 human/n1" {
		t.Fatalf("sent %q", got)
	}
	q.mu.Lock()
	call := q.calls[0]
	q.stale["n1"] = true // the daemon restarted
	q.mu.Unlock()
	if call["window"] != "w1" || call["session"] != "work" || call["text"] != "then commit" {
		t.Fatalf("params %v", call)
	}
	if _, err := d.PromptQueue(ctx, p, "more"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(q.sent(), " "); got != "human/n1 human/n1 human/n1 human/n2" {
		t.Fatalf("after a restart sent %q", got)
	}
}

// With no presence (mtm runs in a tuios pane) the prompt is still queued,
// without from, so tuios records mtm's pane as who queued it.
func TestPromptQueueWithoutAPresenceQueuesAsTheCaller(t *testing.T) {
	t.Parallel()
	q := &queueDaemon{stale: map[string]bool{}}
	d := tuios{Client: daemon.New(q.serve(t))}
	if _, err := d.PromptQueue(context.Background(), pane{Session: "work", Pane: daemon.Pane{ID: "w1"}}, "then commit"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(q.sent(), " "); got != "/" {
		t.Fatalf("sent %q", got)
	}
}

// A queue tuios refuses keeps the text in the box and says why.
func TestPromptBoxQueueRefusedKeepsTheText(t *testing.T) {
	t.Parallel()
	app, f := promptApp(t)
	press(t, app, "ctrl+t") // queue
	f.queueErr = &daemon.Error{Code: "not_found", Message: "no window w1"}
	typeText(t, app, "then commit")
	_, cmd := app.Update(keyMsg("enter"))
	settle(app, cmd)
	if string(app.pb.input.Text) != "then commit" || !strings.Contains(app.m.Status, "no window w1") {
		t.Fatalf("refused: %q %q", string(app.pb.input.Text), app.m.Status)
	}
}
