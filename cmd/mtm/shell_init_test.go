package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke"
)

var osc133 = regexp.MustCompile(`\x1b\]133;([ABCD])(?:;(\d+))?\x07`)

// shellInitFinished reads a transcript the way the daemon reads marks (A prompt,
// B end of prompt, C command starts, D;n command ends): a command finishes at a
// D that follows a C. It returns the exit statuses of finished commands, and the
// mark letters in order.
func shellInitFinished(t *testing.T, out string) (exits []int, marks string) {
	t.Helper()
	started := false
	for _, m := range osc133.FindAllStringSubmatch(out, -1) {
		marks += m[1]
		switch m[1] {
		case "C":
			started = true
		case "D":
			if started {
				n, err := strconv.Atoi(m[2])
				if err != nil {
					t.Fatalf("D without a status: %q", m[0])
				}
				exits = append(exits, n)
			}
			started = false
		}
	}
	return exits, marks
}

func TestShellInitSnippets(t *testing.T) {
	t.Parallel()
	for _, shell := range []string{"zsh", "bash"} {
		var out, errOut bytes.Buffer
		if code, handled := shellInitShell([]string{"shell-init", shell}, &out, &errOut); !handled || code != 0 {
			t.Fatalf("%s: code %d handled %v: %s", shell, code, handled, errOut.String())
		}
		s := out.String()
		for _, mark := range []string{`133;A`, `133;B`, `133;C`, `133;D;`} {
			if !strings.Contains(s, mark) {
				t.Errorf("%s snippet has no %q", shell, mark)
			}
		}
		if !strings.Contains(s, "MTM_ENV == 1") || strings.Contains(s, "TUIOS") {
			t.Errorf("%s snippet must be guarded by MTM_ENV only", shell)
		}
	}
	var out, errOut bytes.Buffer
	if code, _ := shellInitShell([]string{"shell-init", "fish"}, &out, &errOut); code == 0 || out.Len() != 0 {
		t.Errorf("fish: code %d, stdout %q", code, out.String())
	}
}

// TestShellInitMarks runs a scripted interactive shell with the snippet and
// checks the marks it sends: once per command, D with the exit status only
// after a command ran, nothing without MTM_ENV, and sourcing twice sends the
// same marks as sourcing once.
func TestShellInitMarks(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		shell string
		bins  []string
		args  []string
	}{
		{"zsh", []string{"zsh"}, []string{"-f", "-i"}},
		{"bash", []string{"/opt/homebrew/bin/bash", "/usr/local/bin/bash", "bash"}, []string{"--norc", "--noprofile", "-i"}},
	} {
		t.Run(c.shell, func(t *testing.T) {
			t.Parallel()
			bin := ""
			for _, b := range c.bins {
				if p, err := exec.LookPath(b); err == nil {
					bin = p
					break
				}
			}
			if bin == "" {
				t.Skipf("no %s", c.shell)
			}
			if c.shell == "bash" {
				if exec.Command(bin, "-c", `exit $(( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 4) ? 0 : 1 ))`).Run() != nil {
					t.Skip("bash older than 4.4")
				}
			}
			dir := t.TempDir()
			var snippet bytes.Buffer
			shellInitShell([]string{"shell-init", c.shell}, &snippet, nil)
			file := filepath.Join(dir, "init."+c.shell)
			if err := os.WriteFile(file, snippet.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			session := func(env, load string) string {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, bin, c.args...)
				cmd.Dir = dir
				cmd.Env = []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH"), "TERM=dumb", "ZDOTDIR=" + dir, env}
				cmd.Stdin = strings.NewReader(load + "\ntrue\nfalse\n\nexit\n")
				out, _ := cmd.CombinedOutput()
				return string(out)
			}
			once := session("MTM_ENV=1", fmt.Sprintf("source %q", file))
			twice := session("MTM_ENV=1", fmt.Sprintf("source %q; source %q", file, file))
			exits, marks := shellInitFinished(t, once)
			if n := len(exits); n < 2 || !reflect.DeepEqual(exits[n-2:], []int{0, 1}) {
				t.Errorf("finished exits %v, want ...[0 1]\n%q", exits, once)
			}
			// readline may redraw a prompt, so B can repeat; every prompt still ends with one.
			if strings.Count(marks, "A") > strings.Count(marks, "B") || strings.Count(marks, "D") > strings.Count(marks, "C")+1 {
				t.Errorf("marks %q: every A needs a B and D only follows a command", marks)
			}
			if _, marks2 := shellInitFinished(t, twice); marks2 != marks {
				t.Errorf("sourcing twice changed the marks:\nonce  %q\ntwice %q", marks, marks2)
			}
			if got := osc133.FindString(session("MTM_ENV=0", fmt.Sprintf("source %q", file))); got != "" {
				t.Errorf("marks without MTM_ENV=1: %q", got)
			}
		})
	}
}

// shell.init is the CLI's shell-init as a verb (palette, MCP, bindings):
// it answers the same snippet for each shell and refuses another.
func TestShellInitVerb(t *testing.T) {
	t.Parallel()
	a := open(t)
	for name, want := range shellInitSnippets {
		v, err := a.r.Execute(ochazuke.Request{Noun: "shell", Verb: "init", Args: ochazuke.Arguments{"name": name}})
		if err != nil || v != want {
			t.Fatalf("shell.init %s: err %v, same snippet %v", name, err, v == want)
		}
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "shell", Verb: "init", Args: ochazuke.Arguments{"name": "fish"}}); err == nil {
		t.Fatal("shell.init fish answered")
	}
}
