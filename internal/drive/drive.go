// Package drive says how mtm runs a program: in a terminal pane with an
// emulator (terminal), as a plain process whose output streams into a
// job (exec), or through an agent's wire protocol (api, reserved for the
// API pane: accepted and validated, refused when used). A drive is data
// on a harness profile (harnesses.json "drive") and on a command row (a
// schedule's "drive"); a run may override it. The package knows nothing
// of panes or the TUI: Check validates a value, Available says whether
// mtm can run it now, and Stream runs an exec-driven command.
package drive

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"
)

// The drives.
const (
	// Terminal runs the program in a tuios pane (an emulator, keys and a
	// screen): the default for harnesses and shells, and for anything
	// interactive (vim, htop).
	Terminal = "terminal"
	// Exec runs the program as a plain process: its stdout and stderr
	// stream into a job, its exit code is the outcome; no pane, no
	// emulator. The default for command rows that run headless.
	Exec = "exec"
	// API drives an agent through its wire protocol (plans/agent-apis.md).
	// Reserved: a row may name it, a run with it is refused until the API
	// pane lands.
	API = "api"
)

// Values are the drives a row may name.
var Values = []string{Terminal, Exec, API}

// ErrNotAvailable is a drive mtm accepts but cannot run yet.
var ErrNotAvailable = errors.New("not available yet")

// Check reports a value that is no drive; "" (the row's default) is fine.
func Check(v string) error {
	if v == "" || slices.Contains(Values, v) {
		return nil
	}
	return fmt.Errorf("drive %q is not a drive; use %s", v, strings.Join(Values, ", "))
}

// Of is v, else def.
func Of(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Available reports whether mtm runs drive v now: terminal and exec do;
// api is refused with ErrNotAvailable (the API pane is not built).
func Available(v string) error {
	if err := Check(v); err != nil {
		return err
	}
	if v == API {
		return fmt.Errorf("drive api is %w: the API pane (o-agentd) is not built; use drive terminal or exec", ErrNotAvailable)
	}
	return nil
}

// Command is one exec-driven run: the argv, the folder it runs in ("" is
// mtm's), variables added to mtm's environment, and text written to its
// standard input ("" closes it at once).
type Command struct {
	Argv  []string
	Dir   string
	Env   []string
	Stdin string
}

// Stream runs c under nice (when nice is on PATH) in its own process
// group, killed with the group when ctx ends, and calls line for every
// line of its output, stdout and stderr merged in the order they arrive
// (a last line without a newline included). It returns the exit code: 0
// on success, the process's code when it exited non-zero (err is then an
// *exec.ExitError), -1 when it never ran or was killed.
func Stream(ctx context.Context, c Command, line func(string)) (int, error) {
	if len(c.Argv) == 0 || c.Argv[0] == "" {
		return -1, errors.New("the command is empty")
	}
	argv := c.Argv
	if c.Dir != "" {
		if st, err := os.Stat(c.Dir); err != nil || !st.IsDir() {
			return -1, fmt.Errorf("folder %s: not a directory", c.Dir)
		}
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return -1, fmt.Errorf("%s: not on PATH", argv[0])
	}
	if nice, err := exec.LookPath("nice"); err == nil {
		argv = append([]string{nice, "-n", "10"}, argv...)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	r, w := io.Pipe()
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Start(); err != nil {
		w.Close()
		r.Close()
		return -1, err
	}
	read := make(chan struct{})
	go func() {
		defer close(read)
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 64*1024), 1024*1024)
		for s.Scan() {
			line(s.Text())
		}
		// Drain past an over-long line so the process never blocks.
		io.Copy(io.Discard, r)
	}()
	err := cmd.Wait()
	w.Close()
	<-read
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() >= 0 {
		return exit.ExitCode(), err
	}
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	return -1, err
}
