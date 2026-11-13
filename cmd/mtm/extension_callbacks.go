package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
	"ofam.dev/oden"
)

// A kept tool's calls back to mtm (host.run: cli.Host.Call, HostCommand)
// are answered in this process (extensions.host-calls pipe): one headless
// runtime per mtm process, built on the first callback from the same
// options, never the TUI's runtime and never the one whose call waits on
// the tool. It runs one callback at a time with the callback's environment
// as its own (Options.lookupEnv), as an exec of mtm would have seen it, and
// its own extension calls exec (newExtPool), so a callback never waits on
// a kept process that waits on it. exec, or a command line that is not
// this mtm's host command, runs the command as before.

// hostCallbacks is the callback runtime of one mtm process.
type hostCallbacks struct {
	once sync.Once
	app  *App
	err  error
	// turn holds the one callback that runs; a callback whose context
	// ends while it waits gives up.
	turn chan struct{}
	// env is the running callback's environment (the app's lookupEnv).
	env []string
	// exec is the setting extensions.host-calls exec, read off the loop.
	exec atomic.Bool
}

// callbackChainEnv are the host-call limits a callback's own extension
// calls carry on (Host.Call counts them), as an exec of mtm inherited them.
var callbackChainEnv = []string{"OCHAZUKE_CALLER", "OCHAZUKE_HOST_DEPTH", "OCHAZUKE_HOST_CHAIN"}

// answerHostRun is every pool's OnHostRun (off the loop).
func (a *App) answerHostRun(ctx context.Context, call ocli.ServeCall) ocli.ServeAnswer {
	cb := a.callbacks
	words, ok := a.hostWords(call.Argv)
	if !ok || cb == nil || cb.exec.Load() {
		return execHostCall(ctx, call)
	}
	select {
	case cb.turn <- struct{}{}:
	case <-ctx.Done():
		return ocli.ServeAnswer{Stderr: []byte("mtm: " + ctx.Err().Error() + "\n"), Exit: 1}
	}
	defer func() { <-cb.turn }()
	cb.env = call.Env
	cb.once.Do(func() {
		opts := a.opts
		opts.Headless, opts.callback = true, true
		opts.lookupEnv = func(key string) (string, bool) {
			for i := len(cb.env) - 1; i >= 0; i-- {
				if v, ok := strings.CutPrefix(cb.env[i], key+"="); ok {
					return v, true
				}
			}
			return "", false
		}
		cb.app, cb.err = New(opts)
	})
	if cb.err != nil {
		// Never refuse a callback mtm can still answer: exec it.
		return execHostCall(ctx, call)
	}
	return cb.app.runCallback(ctx, words)
}

// hostWords are the words after this mtm's host command (the executable,
// compared by base name, and its data and server flags), false for a
// command line that is not one.
func (a *App) hostWords(argv []string) ([]string, bool) {
	host := a.hostCommand()
	if len(argv) <= len(host) || filepath.Base(argv[0]) != filepath.Base(host[0]) || !slices.Equal(argv[1:len(host)], host[1:]) {
		return nil, false
	}
	return argv[len(host):], true
}

// runCallback runs one callback's words as `mtm WORDS` would, on the
// callback runtime: stdout, stderr and the exit status.
func (a *App) runCallback(ctx context.Context, words []string) ocli.ServeAnswer {
	var out, errOut bytes.Buffer
	a.cliWords = words
	err := a.commands(words, strings.NewReader(""), &out, &errOut, func() error {
		return errors.New("a host callback has no terminal")
	}).Run(ctx)
	answer := ocli.ServeAnswer{Exit: 0}
	if err != nil {
		var exit daemonExit
		if errors.As(err, &exit) {
			answer.Exit = int(exit)
		} else {
			answer.Exit = oden.Report(&errOut, "mtm", err)
		}
	}
	answer.Stdout, answer.Stderr = out.Bytes(), errOut.Bytes()
	return answer
}

// callbackActor is the actor a callback runs as: the actor its environment
// names, through the calling extension (its rights narrowed to an
// extension agent's unless extensions.host-call-rights is caller), refused
// beyond extensions.host-call-depth. It is the boundary ochazuke's shell
// draws from the process environment (Shell.hostCallBoundary), drawn here
// from the callback's, since this process's environment is not the
// callback's.
func (a *App) callbackActor() (ochazuke.Actor, error) {
	actor, err := ochazuke.ActorFromEnv(a.getenv)
	if err != nil {
		return actor, err
	}
	caller := strings.TrimSpace(a.getenv("OCHAZUKE_CALLER"))
	if caller == "" {
		return actor, nil
	}
	depth := 0
	if raw := a.getenv("OCHAZUKE_HOST_DEPTH"); raw != "" {
		if depth, err = strconv.Atoi(raw); err != nil || depth < 0 {
			return actor, fmt.Errorf("invalid OCHAZUKE_HOST_DEPTH %q", raw)
		}
	}
	var chain []string
	if raw := a.getenv("OCHAZUKE_HOST_CHAIN"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &chain); err != nil {
			return actor, fmt.Errorf("invalid OCHAZUKE_HOST_CHAIN: %w", err)
		}
	}
	if limit := max(0, a.r.Settings().Int(settingExtHostDepth)); depth > limit {
		return actor, fmt.Errorf("host call depth %d > %d: %s", depth, limit, strings.Join(chain, " -> "))
	}
	// Already this caller's extension agent (a nested callback), it stays.
	if via := actor.Via; via == nil || !sameActor(via.ThroughExtension(caller), actor) {
		actor = actor.ThroughExtension(caller)
	}
	if setting, err := a.r.Settings().Get(settingExtHostRights); err == nil && setting.Protected && setting.Value == "caller" {
		actor = *actor.Via
	}
	return actor, nil
}

// sameActor compares two actors by who they are, not where Via points.
func sameActor(a, b ochazuke.Actor) bool {
	return a.Kind == b.Kind && a.Harness == b.Harness && a.Name == b.Name
}

// callbackEnv is the host-call chain a callback's own extension calls
// carry (nothing outside a callback runtime).
func (a *App) callbackEnv() []string {
	if !a.callback {
		return nil
	}
	var env []string
	for _, key := range callbackChainEnv {
		if v := a.getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	return env
}

// execHostCall runs a callback as its own process, as a tool would.
func execHostCall(ctx context.Context, call ocli.ServeCall) ocli.ServeAnswer {
	if len(call.Argv) == 0 {
		return ocli.ServeAnswer{Stderr: []byte("host.run needs argv\n"), Exit: 2}
	}
	cmd := exec.CommandContext(ctx, call.Argv[0], call.Argv[1:]...)
	cmd.Env, cmd.Dir, cmd.WaitDelay = call.Env, call.Dir, extWaitDelay
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	answer := ocli.ServeAnswer{}
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			answer.Exit = exit.ExitCode()
		} else {
			answer.Exit = 1
			errOut.WriteString(err.Error() + "\n")
		}
	}
	answer.Stdout, answer.Stderr = out.Bytes(), errOut.Bytes()
	return answer
}
