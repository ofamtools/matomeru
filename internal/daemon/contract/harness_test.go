//go:build tuioscontract

// Package contract runs mtm's real daemon client (internal/daemon) against
// throwaway tuios daemons of several versions and reports what differs.
//
//	MTM_CONTRACT_TUIOS=/path/tuios-v0.10.0,/path/tuios-v0.12.1 \
//	  go test -tags tuioscontract ./internal/daemon/contract/
//
// build-tuios.sh builds the binaries from tags without touching the tuios
// checkout. With MTM_CONTRACT_TUIOS unset the suite skips.
package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"ofam.dev/matomeru/internal/daemon"
)

const (
	envBinaries = "MTM_CONTRACT_TUIOS"  // comma-separated tuios binaries
	envSeed     = "MTM_CONTRACT_SEED"   // "break" seeds a failure
	envReport   = "MTM_CONTRACT_REPORT" // directory for per-version JSON results
	envInPane   = "MTM_CONTRACT_INPANE" // test binary re-run inside a pane: result file
	callTimeout = 20 * time.Second
)

func seeded() bool { return os.Getenv(envSeed) == "break" }

// result is one checked item of the contract.
type result struct {
	Version string `json:"version"`
	Group   string `json:"group"` // verb, event, frame, error, env, registry
	ID      string `json:"id"`
	Status  string `json:"status"` // pass, known-difference, fail
	Detail  string `json:"detail,omitempty"`
}

// known is an expected difference: the check ID fails on these versions
// for the stated reason. A known check that passes is itself a failure, so
// the list cannot go stale.
type known struct {
	ID       string
	Versions []string
	Why      string
}

var expectedDifferences = []known{
	{
		ID:       "registry/capture-pane",
		Versions: []string{"v0.10.0"},
		Why:      "v0.10.0's capture-pane registry has no unwrap or trim, which mtm's CaptureParams carries",
	},
	{
		ID:       "capture-pane/turns_model(unwrap+trim)",
		Versions: []string{"v0.10.0"},
		Why:      "mtm's call at cmd/mtm/turns_model.go:134 (Unwrap and Trim together) is refused by v0.10.0 naming trim first, and daemon.Client.Capture only falls back when the refusal names unwrap, so the call fails on this daemon (a real mtm gap, not only a version difference)",
	},
	{
		ID:       "env/TUIOS_WINDOW_NAME",
		Versions: []string{"v0.10.0", "v0.12.1"},
		Why:      "no daemon version puts TUIOS_WINDOW_NAME in a pane's environment, though mtm reads it (cmd/mtm/capture.go:128, internal/setup/omp.ts:68); both fall back (empty, window id)",
	},
	{
		ID:       "capture-pane/unwrap-raw",
		Versions: []string{"v0.10.0"},
		Why:      "capture-pane has no parameter unwrap at v0.10.0 (P4: invalid_params, sent from cmd/mtm/turns_model.go:134); the daemon.Client.Capture fallback hides it",
	},
	{
		ID:       "capture-pane/trim-raw",
		Versions: []string{"v0.10.0"},
		Why:      "capture-pane has no parameter trim at v0.10.0; Capture's fallback only retries without unwrap, so a Trim-only call is refused",
	},
}

func knownFor(version, id string) *known {
	for i := range expectedDifferences {
		k := &expectedDifferences[i]
		if k.ID != id {
			continue
		}
		for _, v := range k.Versions {
			if v == version {
				return k
			}
		}
	}
	return nil
}

var (
	resultsMu sync.Mutex
	results   []result
)

func record(r result) {
	resultsMu.Lock()
	results = append(results, r)
	resultsMu.Unlock()
}

// daemonUnder is one running throwaway daemon and the mtm client for it.
type daemonUnder struct {
	t       *testing.T
	version string
	bin     string
	root    string
	server  string
	pid     int
	c       *daemon.Client
	events  *eventLog
}

func versionOf(bin string) string {
	return strings.TrimPrefix(filepath.Base(bin), "tuios-")
}

func binaries(t *testing.T) []string {
	raw := os.Getenv(envBinaries)
	if raw == "" {
		t.Skipf("%s is unset; run internal/daemon/contract/build-tuios.sh and set it", envBinaries)
	}
	var out []string
	for _, b := range strings.Split(raw, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}

// scrubEnv points everything a daemon reads at a temp root and removes
// every TUIOS_/TMUX variable, so nothing reaches the owner's tuios. The
// environment is restored when the test ends.
func scrubEnv(t *testing.T, root, pathDir string) {
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "TUIOS_") || strings.HasPrefix(k, "TMUX") || k == "NO_COLOR" || k == "SSH_AUTH_SOCK" {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "run"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("PATH", pathDir+":/usr/bin:/bin")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("SHELL", "/bin/sh")
	for _, d := range []string{"home", "run", "config", "state", "data", "cache"} {
		os.MkdirAll(filepath.Join(root, d), 0o700)
	}
}

// startDaemon starts bin as server "ct" through mtm's own Ensure and
// StartServer (tuios found on PATH), with a scrubbed environment.
func startDaemon(t *testing.T, bin string) *daemonUnder {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "mtmc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	pathDir := filepath.Join(root, "bin")
	os.MkdirAll(pathDir, 0o755)
	abs, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(abs, filepath.Join(pathDir, "tuios")); err != nil {
		t.Fatal(err)
	}
	// A stand-in agent: start-agent finds it on the daemon's PATH.
	script := "#!/bin/sh\nexec sleep 600\n"
	if err := os.WriteFile(filepath.Join(pathDir, "fakeagent"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	scrubEnv(t, root, pathDir)
	d := &daemonUnder{t: t, version: versionOf(bin), bin: abs, root: root, server: "ct"}
	d.c = daemon.ForServer(d.server)
	if !strings.HasPrefix(d.c.Socket, filepath.Join(root, "run")) {
		t.Fatalf("socket %s is outside the temp root %s", d.c.Socket, root)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	d.c.Wait = 45 * time.Second
	if err := d.c.Ensure(ctx); err != nil {
		t.Fatalf("Ensure (mtm's StartServer -> tuios --server ct start-server): %v", err)
	}
	raw, err := os.ReadFile(d.c.Socket + ".pid")
	if err != nil {
		t.Fatalf("pid file: %v", err)
	}
	d.pid, err = strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || d.pid <= 1 {
		t.Fatalf("pid file %q", raw)
	}
	t.Cleanup(d.stop)
	return d
}

// stop ends the daemon this test started, by the PID its own pid file named.
func (d *daemonUnder) stop() {
	if d.events != nil {
		d.events.cancel()
	}
	_ = syscall.Kill(d.pid, syscall.SIGTERM)
	for i := 0; i < 100; i++ {
		if syscall.Kill(d.pid, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(d.pid, syscall.SIGKILL)
}

func (d *daemonUnder) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), callTimeout)
}

// call is mtm's Client.Call. In seeded mode it adds a parameter no daemon
// knows, to prove the suite fails when mtm sends something a daemon refuses.
func (d *daemonUnder) call(verb string, params, out any) error {
	ctx, cancel := d.ctx()
	defer cancel()
	if seeded() {
		if m, ok := params.(map[string]any); ok {
			params = withExtra(m)
		}
	}
	return d.c.Call(ctx, verb, params, out)
}

func withExtra(m map[string]any) map[string]any {
	n := map[string]any{"mtm_seed_unknown": true}
	for k, v := range m {
		n[k] = v
	}
	return n
}

// step runs fn as a named check and records it. A check listed in
// expectedDifferences must fail; any other must pass.
func (d *daemonUnder) step(group, id string, fn func() error) {
	d.t.Helper()
	err := fn()
	k := knownFor(d.version, id)
	switch {
	case err == nil && k == nil:
		record(result{d.version, group, id, "pass", ""})
	case err == nil && k != nil:
		record(result{d.version, group, id, "fail", "expected difference no longer present: " + k.Why})
		d.t.Errorf("[%s] %s/%s: expected difference is gone (%s)", d.version, group, id, k.Why)
	case k != nil:
		record(result{d.version, group, id, "known-difference", err.Error() + " | " + k.Why})
		d.t.Logf("[%s] %s/%s KNOWN DIFFERENCE: %v", d.version, group, id, err)
	default:
		record(result{d.version, group, id, "fail", err.Error()})
		d.t.Errorf("[%s] %s/%s: %v", d.version, group, id, err)
	}
}

// errCode is the daemon error code of err, or "".
func errCode(err error) string {
	var remote *daemon.Error
	if errors.As(err, &remote) {
		return remote.Code
	}
	return ""
}

func wantCode(err error, code string) error {
	if err == nil {
		return fmt.Errorf("want error code %s, got success", code)
	}
	if got := errCode(err); got != code {
		return fmt.Errorf("want error code %s, got %q (%v)", code, got, err)
	}
	return nil
}

func jsonKeys(v map[string]any) []string {
	var out []string
	for k := range v {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// eventLog collects everything one real Client.Subscribe delivers.
type eventLog struct {
	mu     sync.Mutex
	events []daemon.Event
	err    error
	cancel context.CancelFunc
}

func (l *eventLog) snapshot() []daemon.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]daemon.Event(nil), l.events...)
}

func (l *eventLog) mark() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}

// wait returns the first event of type typ at index from or later that
// pred accepts, polling until timeout.
func (l *eventLog) wait(typ string, from int, pred func(daemon.Event) bool, timeout time.Duration) (daemon.Event, error) {
	deadline := time.Now().Add(timeout)
	for {
		evs := l.snapshot()
		for i := from; i < len(evs); i++ {
			if evs[i].Type == typ && (pred == nil || pred(evs[i])) {
				return evs[i], nil
			}
		}
		if time.Now().After(deadline) {
			var seen []string
			for _, e := range evs[min(from, len(evs)):] {
				seen = append(seen, e.Type)
			}
			l.mu.Lock()
			err := l.err
			l.mu.Unlock()
			return daemon.Event{}, fmt.Errorf("no %s event within %s (saw %v, subscribe error %v)", typ, timeout, seen, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

var allEventTypes = []string{
	"window-created", "window-closed", "window-exit", "window-retitled", "window-moved",
	"window-minimized", "window-restored", "agent-state", "session-created", "session-closed",
	"command-started", "command-finished", "agent-activity", "output",
}

// subscribe opens mtm's Client.Subscribe for types and collects events.
func (d *daemonUnder) subscribe(types []string, after uint64, boot string) *eventLog {
	ctx, cancel := context.WithCancel(context.Background())
	l := &eventLog{cancel: cancel}
	ch, fail := d.c.Subscribe(ctx, types, after, boot)
	go func() {
		for ev := range ch {
			l.mu.Lock()
			l.events = append(l.events, ev)
			l.mu.Unlock()
		}
		l.mu.Lock()
		l.err = <-fail
		l.mu.Unlock()
	}()
	return l
}

func writeReport(version string) {
	dir := os.Getenv(envReport)
	if dir == "" {
		return
	}
	os.MkdirAll(dir, 0o755)
	resultsMu.Lock()
	defer resultsMu.Unlock()
	var mine []result
	for _, r := range results {
		if r.Version == version {
			mine = append(mine, r)
		}
	}
	raw, _ := json.MarshalIndent(mine, "", " ")
	os.WriteFile(filepath.Join(dir, "results-"+version+".json"), raw, 0o644)
}

var _ = exec.Command
