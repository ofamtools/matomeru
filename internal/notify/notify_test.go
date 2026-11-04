package notify

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestSenderUsesTextArgumentsAndPartialSuccess(t *testing.T) {
	t.Parallel()
	title, body := "-e \"quoted\"; $(touch nope)", "body\nwith `code`"
	var commands [][]string
	var terminal string
	s := Sender{Channel: "both", Commands: append([][]string{OSCommand}, []string{"extra", "--message={{body}}", "{{title}}"}),
		Terminal: func(_ context.Context, text string) error { terminal = text; return nil },
		Exec: func(_ context.Context, argv []string) (string, error) {
			commands = append(commands, argv)
			return "offline", errors.New("fake failure")
		},
	}
	sent, err := s.Send(context.Background(), title, body)
	if !sent || err == nil {
		t.Fatalf("partial success %v %v", sent, err)
	}
	want := [][]string{{"/usr/bin/osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", "--", title, body}, {"extra", "--message=" + body, title}}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("argv %#v", commands)
	}
	if terminal != "\x1b]9;mtm: -e \"quoted\"; $(touch nope) · bodywith `code`\x1b\\" {
		t.Fatalf("terminal %q", terminal)
	}
}

func TestTerminalRejectsInjectedControls(t *testing.T) {
	t.Parallel()
	got := Terminal("4;title\x1b\\\a\n\r\t\u009d\u009b", "body\x1b]52;c;attack\a")
	want := "\x1b]9;mtm: 4;title\\ · body]52;c;attack\x1b\\"
	if got != want {
		t.Fatalf("OSC 9 = %q, want %q", got, want)
	}
}

func TestCommandsRejectInvalidArgv(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"null", "[[]]", `[[""]]`, `[["tool","\u0000"]]`, `["shell text"]`, `{}`} {
		if _, err := Commands("notify.commands", text); err == nil {
			t.Fatalf("accepted unsafe argv %s", text)
		}
	}
	commands, err := Commands("notify.commands", `[["tool","{{title}}","{{body}}"]]`)
	if err != nil || !reflect.DeepEqual(commands, [][]string{{"tool", "{{title}}", "{{body}}"}}) {
		t.Fatalf("argv %#v %v", commands, err)
	}
}
