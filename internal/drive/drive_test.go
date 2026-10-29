package drive

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCheckAndAvailable(t *testing.T) {
	for _, v := range []string{"", Terminal, Exec, API} {
		if err := Check(v); err != nil {
			t.Fatalf("Check(%q): %v", v, err)
		}
	}
	if err := Check("pty"); err == nil || !strings.Contains(err.Error(), "terminal, exec, api") {
		t.Fatalf("Check(pty) = %v", err)
	}
	if err := Available(API); !errors.Is(err, ErrNotAvailable) || !strings.Contains(err.Error(), "not available yet") {
		t.Fatalf("Available(api) = %v", err)
	}
	for _, v := range []string{"", Terminal, Exec} {
		if err := Available(v); err != nil {
			t.Fatalf("Available(%q) = %v", v, err)
		}
	}
	if Of("", Exec) != Exec || Of(Terminal, Exec) != Terminal {
		t.Fatal("Of")
	}
}

func TestStreamLinesAndExitCode(t *testing.T) {
	var lines []string
	code, err := Stream(context.Background(), Command{Argv: []string{"sh", "-c", "echo one; echo two >&2; printf three; exit 3"}}, func(l string) { lines = append(lines, l) })
	if code != 3 || err == nil {
		t.Fatalf("code %d err %v", code, err)
	}
	slices.Sort(lines)
	if !slices.Equal(lines, []string{"one", "three", "two"}) {
		t.Fatalf("lines %q", lines)
	}
	code, err = Stream(context.Background(), Command{Argv: []string{"cat"}, Stdin: "from stdin\n", Env: []string{"X=1"}}, func(l string) { lines = append(lines, l) })
	if code != 0 || err != nil || lines[len(lines)-1] != "from stdin" {
		t.Fatalf("stdin: code %d err %v lines %q", code, err, lines)
	}
}

func TestStreamCancelKillsAndMissingCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	code, err := Stream(ctx, Command{Argv: []string{"sh", "-c", "sleep 30"}}, func(string) {})
	if err == nil || code == 0 || time.Since(start) > 5*time.Second {
		t.Fatalf("cancel: code %d err %v after %s", code, err, time.Since(start))
	}
	if code, err := Stream(context.Background(), Command{Argv: []string{"mtm-no-such-command"}}, func(string) {}); code != -1 || err == nil || !strings.Contains(err.Error(), "not on PATH") {
		t.Fatalf("missing: code %d err %v", code, err)
	}
	if code, err := Stream(context.Background(), Command{Argv: []string{"true"}, Dir: "/no/such/dir"}, func(string) {}); code != -1 || err == nil || !strings.Contains(err.Error(), "folder /no/such/dir: not a directory") {
		t.Fatalf("dir: code %d err %v", code, err)
	}
}
