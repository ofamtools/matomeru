//go:build tuioscontract

package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/internal/daemon"
)

func TestMain(m *testing.M) {
	// Re-run inside a daemon pane: the real mtm client asks for a presence,
	// which the daemon must refuse with "forbidden" from inside a pane.
	if out := os.Getenv(envInPane); out != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := daemon.New("").Presence(ctx, "")
		res := map[string]string{"code": errCode(err), "env_socket": os.Getenv("TUIOS_SOCKET")}
		if err != nil {
			res["error"] = err.Error()
		}
		raw, _ := json.Marshal(res)
		os.WriteFile(out, raw, 0o644)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// tags lists the JSON parameter names of one of mtm's parameter structs.
func tags(v any) []string {
	var out []string
	t := reflect.TypeOf(v)
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

// mtmSends is the parameter set mtm sends per verb: the struct tags of the
// real client types, and the literal maps at call sites that live in
// cmd/mtm (package main, which this package cannot import).
func mtmSends() map[string][]string {
	return map[string][]string{
		"hello":                {"client"},
		"list-sessions":        {},
		"list-windows":         {"session"},
		"list-workspaces":      {"session"},
		"session-info":         {"session"},
		"new-session":          {"name", "window", "width", "height"},
		"rename-session":       {"session", "name"},
		"kill-session":         {"session"}, // cmd/mtm/panes.go:1032
		"new-window":           tags(daemon.NewWindowParams{}),
		"close-window":         {"session", "window"}, // cmd/mtm/panes.go:1202
		"get-window":           {"session", "window"}, // cmd/mtm/saved.go:211
		"set-window":           {"session", "window", "name"},
		"resize":               {"session", "window", "width", "height"},
		"capture-pane":         tags(daemon.CaptureParams{}),
		"send-text":            tags(daemon.SendTextParams{}),
		"send-keys":            tags(daemon.SendKeysParams{}),
		"stream-pane":          tags(daemon.StreamParams{}),
		"subscribe":            {"types", "after_seq", "boot_id"},
		"wait-for":             tags(daemon.WaitForParams{}),
		"command-history":      tags(daemon.HistoryParams{}),
		"start-agent":          tags(daemon.StartAgentParams{}),
		"list-agents":          {"all", "all_sessions"},                             // cmd/mtm/panes.go:1018
		"set-agent-state":      {"window", "state", "message", "source", "session"}, // cmd/mtm/mcp_ops.go:155
		"queue-prompt":         tags(daemon.QueuePromptParams{}),
		"agent-activity":       tags(daemon.ActivityParams{}),
		"agent-transcript":     tags(daemon.TranscriptParams{}),
		"attach-presence":      {"session"},
		"open-host-connection": {"host", "ssh_auth_sock"},
	}
}

type verbInfo struct {
	Verb   string `json:"verb"`
	Params []struct {
		Name string `json:"name"`
	} `json:"params"`
	Returns []struct {
		Name string `json:"name"`
	} `json:"returns"`
}

type registrySummary struct {
	Total int                 `json:"total"`
	Mtm   map[string]verbSums `json:"mtm_verbs"`
}
type verbSums struct {
	Params  []string `json:"params"`
	Returns []string `json:"returns"`
}

func TestContract(t *testing.T) {
	bins := binaries(t)
	summaries := map[string]registrySummary{}
	for _, bin := range bins {
		version := versionOf(bin)
		t.Run(version, func(t *testing.T) {
			d := startDaemon(t, bin)
			summaries[version] = d.registry()
			d.run()
			writeReport(version)
		})
	}
	if dir := os.Getenv(envReport); dir != "" {
		raw, _ := json.MarshalIndent(summaries, "", " ")
		os.WriteFile(filepath.Join(dir, "registry.json"), raw, 0o644)
	}
	logRegistryDiff(t, bins, summaries)
}

func logRegistryDiff(t *testing.T, bins []string, s map[string]registrySummary) {
	if len(bins) < 2 {
		return
	}
	a, b := versionOf(bins[0]), versionOf(bins[len(bins)-1])
	t.Logf("registry: %s has %d verbs, %s has %d", a, s[a].Total, b, s[b].Total)
	for verb := range mtmSends() {
		x, y := s[a].Mtm[verb], s[b].Mtm[verb]
		if d := setDiff(x.Params, y.Params); d != "" {
			t.Logf("DIFFERENCE %s params: %s", verb, d)
		}
		if d := setDiff(x.Returns, y.Returns); d != "" {
			t.Logf("DIFFERENCE %s returns: %s", verb, d)
		}
	}
}

func setDiff(a, b []string) string {
	in := func(l []string, v string) bool {
		for _, x := range l {
			if x == v {
				return true
			}
		}
		return false
	}
	var out []string
	for _, v := range a {
		if !in(b, v) {
			out = append(out, "-"+v)
		}
	}
	for _, v := range b {
		if !in(a, v) {
			out = append(out, "+"+v)
		}
	}
	return strings.Join(out, " ")
}

// registry checks, from the daemon's own list-verbs, that every verb mtm
// uses exists and accepts every parameter mtm sends.
func (d *daemonUnder) registry() registrySummary {
	var reg struct {
		Verbs []verbInfo `json:"verbs"`
	}
	if err := d.call("list-verbs", nil, &reg); err != nil {
		d.t.Fatalf("list-verbs: %v", err)
	}
	byName := map[string]verbInfo{}
	for _, v := range reg.Verbs {
		byName[v.Verb] = v
	}
	sum := registrySummary{Total: len(reg.Verbs), Mtm: map[string]verbSums{}}
	for verb, sends := range mtmSends() {
		info, ok := byName[verb]
		d.step("registry", "registry/"+verb, func() error {
			if !ok {
				return fmt.Errorf("verb %s is not in the daemon's registry", verb)
			}
			have := map[string]bool{}
			for _, p := range info.Params {
				have[p.Name] = true
			}
			var missing []string
			for _, p := range sends {
				if !have[p] {
					missing = append(missing, p)
				}
			}
			if len(missing) > 0 {
				return fmt.Errorf("verb %s has no parameter %s that mtm sends", verb, strings.Join(missing, ", "))
			}
			return nil
		})
		var s verbSums
		for _, p := range info.Params {
			s.Params = append(s.Params, p.Name)
		}
		for _, p := range info.Returns {
			s.Returns = append(s.Returns, p.Name)
		}
		sortStrings(s.Params)
		sortStrings(s.Returns)
		sum.Mtm[verb] = s
	}
	return sum
}

// screen polls capture-pane until the pane shows want.
func (d *daemonUnder) screen(session, win, want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string
	for {
		ctx, cancel := d.ctx()
		got, err := d.c.Capture(ctx, daemon.CaptureParams{Session: session, Window: win, Source: "recent"})
		cancel()
		if err == nil {
			last = got.Content
			if strings.Contains(last, want) {
				return nil
			}
		} else {
			last = err.Error()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pane %s never showed %q; last: %q", win, want, tail(last))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func tail(s string) string {
	if len(s) > 300 {
		return s[len(s)-300:]
	}
	return s
}

// type shell types a command line into the pane through the real
// send-text client (submit).
func (d *daemonUnder) typeLine(session, win, text string) error {
	ctx, cancel := d.ctx()
	defer cancel()
	return d.c.SendText(ctx, daemon.SendTextParams{Session: session, Window: win, Text: text, Submit: true})
}

func (d *daemonUnder) run() {
	t := d.t
	const sess = "ct1"
	cwd := filepath.Join(d.root, "work")
	os.MkdirAll(cwd, 0o755)

	// Events first, so nothing is missed.
	d.events = d.subscribe(allEventTypes, 0, "")
	var bootID string
	d.step("event", "subscribe/ack", func() error {
		ev, err := d.events.wait("subscribed", 0, nil, 15*time.Second)
		if err != nil {
			return err
		}
		bootID = ev.BootID
		if v, _ := ev.Data["boot_id"].(string); bootID == "" {
			bootID = v
		}
		return nil
	})

	d.step("verb", "hello", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		h, err := d.c.Hello(ctx)
		if err != nil {
			return err
		}
		if h.PID != d.pid {
			return fmt.Errorf("hello pid %d, pid file %d", h.PID, d.pid)
		}
		want := 1
		if seeded() {
			want = 999
		}
		if h.Protocol != want {
			return fmt.Errorf("hello protocol %d, want %d", h.Protocol, want)
		}
		if h.DaemonVersion == "" {
			return fmt.Errorf("hello daemon_version is empty")
		}
		return nil
	})
	d.step("verb", "list-sessions", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		got, err := d.c.Sessions(ctx)
		if err != nil {
			return err
		}
		if len(got) != 0 {
			return fmt.Errorf("fresh daemon lists %d sessions", len(got))
		}
		return nil
	})
	d.step("error", "error/session_not_found", func() error {
		return wantCode(d.call("session-info", map[string]any{"session": "nope"}, nil), "session_not_found")
	})
	d.step("verb", "new-session+session-info", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		ws, err := daemon.LaunchWorkspace(ctx, d.c, sess, 120, 40)
		if err != nil {
			return err
		}
		if ws != 1 {
			return fmt.Errorf("LaunchWorkspace on a new session = workspace %d, want 1", ws)
		}
		list, err := d.c.Sessions(ctx)
		if err != nil || len(list) != 1 || list[0].Name != sess || list[0].Width != 120 || list[0].Height != 40 {
			return fmt.Errorf("sessions after new-session = %+v, %v", list, err)
		}
		return nil
	})
	d.step("event", "event/session-created", func() error {
		_, err := d.events.wait("session-created", 0, func(e daemon.Event) bool { return e.Session == sess }, 10*time.Second)
		return err
	})
	d.step("error", "error/session_exists", func() error {
		return wantCode(d.call("new-session", map[string]any{"name": sess, "window": false}, nil), "session_exists")
	})

	// A shell window.
	var w1 string
	mark := d.events.mark()
	d.step("verb", "new-window", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		r, err := d.c.NewWindow(ctx, daemon.NewWindowParams{Session: sess, Name: "w1", Cwd: cwd, Focus: false, Command: []string{"/bin/sh"}})
		if err != nil {
			return err
		}
		if r.WindowID == "" || r.PTYID == "" {
			return fmt.Errorf("new-window reply %+v", r)
		}
		w1 = r.WindowID
		return nil
	})
	if w1 == "" {
		t.Fatalf("no window; the rest of the contract needs one")
	}
	d.step("event", "event/window-created", func() error {
		_, err := d.events.wait("window-created", mark, func(e daemon.Event) bool { return e.Window == w1 }, 10*time.Second)
		return err
	})
	d.step("verb", "list-windows", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		panes, err := d.c.Panes(ctx, sess)
		if err != nil {
			return err
		}
		for _, p := range panes {
			if p.ID == w1 {
				if p.Name != "w1" || p.PID == 0 || p.Workspace != 1 || p.Width == 0 || p.Height == 0 {
					return fmt.Errorf("pane %+v", p)
				}
				if filepath.Base(p.Cwd) != "work" {
					return fmt.Errorf("pane cwd %q", p.Cwd)
				}
				return nil
			}
		}
		return fmt.Errorf("window %s missing from %+v", w1, panes)
	})
	d.step("verb", "session-info/list-workspaces(LaunchWorkspace)", func() error {
		ctx, cancel := d.ctx()
		defer cancel()
		ws, err := daemon.LaunchWorkspace(ctx, d.c, sess, 120, 40)
		if err != nil {
			return err
		}
		if ws < 1 {
			return fmt.Errorf("workspace %d", ws)
		}
		return nil
	})
	d.step("verb", "list-workspaces", func() error {
		var out struct {
			Workspaces []struct {
				Workspace   int `json:"workspace"`
				WindowCount int `json:"window_count"`
			} `json:"workspaces"`
		}
		if err := d.call("list-workspaces", map[string]any{"session": sess}, &out); err != nil {
			return err
		}
		if len(out.Workspaces) == 0 || out.Workspaces[0].Workspace != 1 || out.Workspaces[0].WindowCount != 1 {
			return fmt.Errorf("workspaces %+v", out.Workspaces)
		}
		return nil
	})
	d.step("verb", "get-window", func() error {
		var out map[string]any
		if err := d.call("get-window", map[string]any{"session": sess, "window": w1}, &out); err != nil {
			return err
		}
		if fmt.Sprint(out["window_id"]) != w1 && fmt.Sprint(out["id"]) != w1 {
			return fmt.Errorf("get-window reply %v", jsonKeys(out))
		}
		return nil
	})

	d.shellSteps(sess, w1)
	d.windowSteps(sess, w1)
	d.commandSteps(sess, w1)
	d.streamSteps(sess, w1)
	d.agentSteps(sess, w1)
	d.presenceSteps(sess, w1)
	d.endSteps(sess, w1, bootID)
}
