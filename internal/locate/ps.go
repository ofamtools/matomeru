package locate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// psTimeout bounds one ps run.
const psTimeout = time.Second

// PS is the System of this machine: ps for starts, terminals and command
// lines, the environment from /proc/<pid>/environ where there is one,
// else ps eww.
type PS struct{}

// Procs runs ps once for the pids and the ttys (ps selects their union).
func (PS) Procs(pids []int, ttys []string) map[int]Proc {
	args := []string{"-ww", "-o", "pid=,tty=,lstart=,command="}
	if len(pids) > 0 {
		args = append(args, "-p", joinPids(pids))
	}
	if len(ttys) > 0 {
		args = append(args, "-t", strings.Join(ttys, ","))
	}
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", args...)
	cmd.Env = append(os.Environ(), "TZ=UTC", "LC_ALL=C")
	b, _ := cmd.Output() // ps exits 1 when a pid is gone; what it printed stands
	return parseProcs(string(b))
}

// parseProcs reads ps's pid, tty, lstart (five words) and command
// columns.
func parseProcs(s string) map[int]Proc {
	out := map[int]Proc{}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 7 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		tty := f[1]
		if tty == "??" || tty == "?" || tty == "-" {
			tty = ""
		}
		tty = strings.TrimPrefix(tty, "/dev/")
		args := f[7:]
		if len(args) > 2 {
			args = args[:2]
		}
		out[pid] = Proc{Start: strings.Join(f[2:7], " "), TTY: tty, Args: args}
	}
	return out
}

// Env reads the names asked of each pid's environment.
func (PS) Env(pids []int, names []string) map[int]map[string]string {
	out := map[int]map[string]string{}
	var rest []int
	for _, pid := range pids {
		if runtime.GOOS == "linux" {
			if b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ")); err == nil {
				out[pid] = pick(strings.Split(string(b), "\x00"), names)
				continue
			}
		}
		rest = append(rest, pid)
	}
	if len(rest) == 0 {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	b, _ := exec.CommandContext(ctx, "ps", "eww", "-o", "pid=,command=", "-p", joinPids(rest)).Output()
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if pid, err := strconv.Atoi(f[0]); err == nil {
			out[pid] = pick(f[1:], names)
		}
	}
	return out
}

// pick are the names' assignments among words (an environment, or a
// command line followed by its environment: the last one wins).
func pick(words, names []string) map[string]string {
	env := map[string]string{}
	for _, w := range words {
		k, v, ok := strings.Cut(w, "=")
		if !ok {
			continue
		}
		for _, n := range names {
			if k == n {
				env[k] = v
			}
		}
	}
	return env
}

func joinPids(pids []int) string {
	s := make([]string, len(pids))
	for i, p := range pids {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, ",")
}
