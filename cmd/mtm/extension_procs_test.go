package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// TestExtProcsLimit: with a limit of N, more than N calls run at most N at
// once and every one completes; 0 runs them all at once; a waiting change
// goes before the waiting reads; the wait does not use up a call's deadline.
func TestExtProcsLimit(t *testing.T) {
	t.Parallel()
	burst := func(limit, calls int) (peak, done int) {
		p := newExtProcs(limit)
		var mu sync.Mutex
		now := 0
		var wg sync.WaitGroup
		for range calls {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := p.call(context.Background(), false, func(context.Context) error {
					mu.Lock()
					now++
					peak = max(peak, now)
					mu.Unlock()
					time.Sleep(20 * time.Millisecond)
					mu.Lock()
					now--
					done++
					mu.Unlock()
					return nil
				})
				if err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		return peak, done
	}
	if peak, done := burst(3, 12); peak > 3 || done != 12 {
		t.Fatalf("limit 3: peak %d, done %d of 12", peak, done)
	}
	if peak, done := burst(0, 12); peak != 12 || done != 12 {
		t.Fatalf("limit 0: peak %d, done %d of 12 (want all at once)", peak, done)
	}

	// Order: one slot held; two reads then a change wait; the change runs first.
	p := newExtProcs(1)
	release, err := p.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	var names []string
	queue := func(name string, write bool) {
		names = append(names, name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = p.call(context.Background(), write, func(context.Context) error {
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
				return nil
			})
		}()
		waitFor(t, 5*time.Second, name+" queued", func() bool {
			p.mu.Lock()
			defer p.mu.Unlock()
			return len(p.queue) == len(names)
		})
	}
	queue("read1", false)
	queue("read2", false)
	queue("change", true)
	// The deadline: a 100ms call that waits 200ms still gets its 100ms.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var late error
	wg.Add(1)
	go func() {
		defer wg.Done()
		late = p.call(ctx, false, func(c context.Context) error { return c.Err() })
	}()
	time.Sleep(200 * time.Millisecond)
	release()
	wg.Wait()
	if got := strings.Join(order, ","); got != "change,read1,read2" {
		t.Fatalf("order %s, want change,read1,read2", got)
	}
	if late != nil {
		t.Fatalf("a call that waited past its caller's deadline ran with %v", late)
	}
}

// TestExtProcsStop: a long-running tool (and its own child) started through
// the gate ends when its job is cancelled and when mtm closes; so does a
// watch process.
func TestExtProcsStop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := filepath.Join(dir, "longtool")
	// The tool starts a child of its own, writes both pids, and waits.
	script := "#!/bin/sh\nsleep 300 &\necho \"$$ $!\" > \"$PIDFILE\"\nwait\n"
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	start := func(name string, run func(env []string)) []int {
		pidfile := filepath.Join(dir, name+".pid")
		go run([]string{"PIDFILE=" + pidfile})
		var pids []int
		waitFor(t, 5*time.Second, name+" started", func() bool {
			raw, err := os.ReadFile(pidfile)
			fields := strings.Fields(string(raw))
			if err != nil || len(fields) != 2 {
				return false
			}
			pids = nil
			for _, f := range fields {
				n, _ := strconv.Atoi(f)
				pids = append(pids, n)
			}
			return true
		})
		return pids
	}
	gone := func(what string, pids []int) {
		t.Helper()
		for _, pid := range pids {
			waitFor(t, 5*time.Second, what+": pid "+strconv.Itoa(pid)+" ends", func() bool { return syscall.Kill(pid, 0) == syscall.ESRCH })
		}
	}
	mounted := func(p *extProcs, ctx context.Context, env []string) {
		res := p.result(ocli.RunMounted(ocli.CatalogVerb{Name: "thing list"}, ocli.ProxyCommand{
			Bin: tool, Args: []string{"thing", "list"}, Env: append(os.Environ(), env...), Timeout: time.Minute, WaitDelay: extWaitDelay,
		}), false, nil)
		_, _ = res.Await(ctx)
	}

	// A cancelled job: its mounted verb's process group ends.
	p := newExtProcs(2)
	ctx, cancel := context.WithCancel(context.Background())
	pids := start("cancel", func(env []string) { mounted(p, ctx, env) })
	cancel()
	gone("cancelled job", pids)

	// mtm closes: a mounted verb, an exec read and a watch all end.
	p = newExtProcs(2)
	verb := start("close-verb", func(env []string) { mounted(p, context.Background(), env) })
	read := start("close-read", func(env []string) { _, _ = p.exec(context.Background(), false, tool, env, "thing", "list") })
	watch := start("close-watch", func(env []string) {
		_, err := p.watch(func(life context.Context) (func(), error) {
			return execExtWatch(life, tool, env, "", "thing", ochazuke.WatchQuery{}, func(ochazuke.WatchEvent) {})
		})
		if err != nil {
			t.Error(err)
		}
	})
	p.close(extCloseWait)
	gone("closed mtm, mounted verb", verb)
	gone("closed mtm, exec read", read)
	gone("closed mtm, watch", watch)
}
