package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func writeExe(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func pinned(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestPinReusesACopyAndRemovesUnusedOldBuilds(t *testing.T) {
	src := filepath.Join(t.TempDir(), "mtm")
	dir := filepath.Join(t.TempDir(), "daemon-bin")
	writeExe(t, src, "build one")
	used := map[string]bool{}
	inUse := func(p string) bool { return used[p] }
	one, err := Pin(src, dir, "v1", inUse)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := Pin(src, dir, "v1", inUse); again != one {
		t.Fatalf("same build pinned twice: %s then %s", one, again)
	}
	if b, _ := os.ReadFile(one); string(b) != "build one" {
		t.Fatalf("copy = %q", b)
	}
	// Overwriting the source in place does not touch the copy.
	writeExe(t, src, "build two, longer")
	if b, _ := os.ReadFile(one); string(b) != "build one" {
		t.Fatalf("the copy changed with the source: %q", b)
	}
	// An old build a daemon still runs is kept; an unused one goes.
	used[one] = true
	two, err := Pin(src, dir, "v2", inUse)
	if err != nil {
		t.Fatal(err)
	}
	if two == one || len(pinned(t, dir)) != 2 {
		t.Fatalf("pins = %v", pinned(t, dir))
	}
	writeExe(t, src, "build three, longer still")
	used[one] = false
	three, err := Pin(src, dir, "v3", inUse)
	if err != nil {
		t.Fatal(err)
	}
	if got := pinned(t, dir); len(got) != 1 || filepath.Join(dir, got[0]) != three {
		t.Fatalf("pins = %v, want only %s", got, three)
	}
}

func TestPinRacingStartersLeaveOneWholeCopy(t *testing.T) {
	src := filepath.Join(t.TempDir(), "mtm")
	dir := filepath.Join(t.TempDir(), "daemon-bin")
	writeExe(t, src, strings.Repeat("x", 1<<20))
	var wg sync.WaitGroup
	paths := make([]string, 8)
	for i := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := Pin(src, dir, "v1", func(string) bool { return true })
			if err != nil {
				t.Error(err)
			}
			paths[i] = p
		}()
	}
	wg.Wait()
	for _, p := range paths {
		if p != paths[0] {
			t.Fatalf("paths differ: %v", paths)
		}
	}
	if got := pinned(t, dir); len(got) != 1 {
		t.Fatalf("pins = %v", got)
	}
	if st, _ := os.Stat(paths[0]); st.Size() != 1<<20 || st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("copy %v", st)
	}
}

func TestScrubEnv(t *testing.T) {
	got := ScrubEnv([]string{"PATH=/bin", "TUIOS_SOCKET=/x", "TUIOS_SERVER=a", "MTM_SOCKET=/x", "MTM_SERVER=a", "MTM_PANE_ID=w", "TMUX=1", "TMUX_PANE=%1", "STY=s", "CLAUDECODE=1", "HOME=/h", "ZELLIJ=0", "MTM_DATA=/d"})
	if strings.Join(got, ",") != "PATH=/bin,HOME=/h,MTM_DATA=/d" {
		t.Fatalf("env = %v", got)
	}
}

func TestSelfStartRunsThePinnedBinaryWithTheHiddenWord(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	exe := filepath.Join(root, "mtm")
	writeExe(t, exe, "#!/bin/sh\necho \"$0 $*\" > "+out+"\necho \"TUIOS_SERVER=${TUIOS_SERVER-unset} MTM_SERVER=${MTM_SERVER-unset} PATHS=${MTM_DAEMON_PATHS-unset}\" >> "+out+"\n")
	s := SelfStart{Word: "__w", Exe: exe, PinDir: filepath.Join(root, "bin"), Label: "t", InUse: func(string) bool { return true }, Paths: "mtm",
		Environ: func() []string {
			return []string{"PATH=/usr/bin:/bin", "TUIOS_SERVER=enclosing", "MTM_SERVER=enclosing", "MTM_DAEMON_PATHS=tuios"}
		}}
	exited, err := s.Start(context.Background(), "srv")
	if err != nil {
		t.Fatal(err)
	}
	if err := <-exited; err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	pins := pinned(t, filepath.Join(root, "bin"))
	if len(pins) != 1 || !strings.HasPrefix(lines[0], filepath.Join(root, "bin", pins[0])+" __w --server srv start-server") {
		t.Fatalf("ran %q, pins %v", lines[0], pins)
	}
	if lines[1] != "TUIOS_SERVER=unset MTM_SERVER=unset PATHS=mtm" {
		t.Fatalf("the enclosing server leaked or the layout was not handed on: %q", lines[1])
	}
	// Unpinned (daemon.pin-binary off): the binary itself.
	s.PinDir = ""
	exited, _ = s.Start(context.Background(), "srv")
	<-exited
	b, _ = os.ReadFile(out)
	if !strings.HasPrefix(string(b), exe+" __w --server srv start-server") {
		t.Fatalf("ran %q", b)
	}
}

// helloServer answers hello on socket after delay, as a daemon that has
// bound its socket before it serves does not.
func helloServer(t *testing.T, socket string, delay time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	ready := time.Now().Add(delay)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 4096)
				n, _ := c.Read(buf)
				if n == 0 || time.Now().Before(ready) {
					return // bound, not yet serving
				}
				var req struct {
					ID json.RawMessage `json:"id"`
				}
				_ = json.Unmarshal(buf[:n], &req)
				reply, _ := json.Marshal(map[string]any{"id": req.ID, "result": map[string]any{"daemon_version": "x", "protocol": 1, "pid": 7}})
				c.Write(append(reply, '\n'))
			}()
		}
	}()
}

func TestEnsureWaitsForABoundSocketToAnswerHelloWithoutAnotherStart(t *testing.T) {
	runtimeDir(t)
	c := ForServer("racer")
	c.Ready = c.HelloReady
	c.Wait = 5 * time.Second
	var starts atomic.Int32
	c.Start = func(context.Context, string) (<-chan error, error) { starts.Add(1); return nil, nil }
	helloServer(t, c.Socket, 600*time.Millisecond) // another starter's daemon, still coming up
	if err := c.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if starts.Load() != 0 {
		t.Fatalf("started a second daemon (%d starts)", starts.Load())
	}
}

func TestEnsureWithHelloStartsAMissingServerAndWaitsForHello(t *testing.T) {
	runtimeDir(t)
	c := ForServer("late")
	c.Ready = c.HelloReady
	var starts atomic.Int32
	c.Start = func(_ context.Context, name string) (<-chan error, error) {
		starts.Add(1)
		helloServer(t, SocketFor(name), 300*time.Millisecond)
		return nil, nil
	}
	if err := c.Ensure(context.Background()); err != nil || starts.Load() != 1 {
		t.Fatalf("ensure: %v, starts %d", err, starts.Load())
	}
}

func TestEnsureWithHelloGivesUpOnASocketThatNeverAnswers(t *testing.T) {
	runtimeDir(t)
	c := ForServer("dead")
	c.Ready = c.HelloReady
	c.Wait = 400 * time.Millisecond
	listen(t, c.Socket) // accepts and hangs up: no hello
	err := c.Ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("ensure = %v", err)
	}
}
