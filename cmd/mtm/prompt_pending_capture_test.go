package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestActivityPromptCapturesKeysBeforePreviewCompletes(t *testing.T) {
	t.Parallel()
	for _, queue := range []bool{false, true} {
		for _, tab := range []int{tabActivity, tabTranscript} {
			for _, text := range []string{"hello", "dd"} {
				t.Run(fmt.Sprintf("queue=%t/%s/i%s", queue, tabNames[tab], text), func(t *testing.T) {
					t.Parallel()
					a, f, row := externalQueuePromptApp(t, false, tab)
					if !queue {
						row = "pane:matomeru/w1"
						if err := a.r.SetFocus(row); err != nil {
							t.Fatal(err)
						}
						settle(a, follow(a))
					}
					a.preview = preview{}
					var commands []tea.Cmd
					for _, key := range "i" + text {
						_, cmd := a.Update(keyMsg(string(key)))
						commands = append(commands, cmd)
						fr := frame(a)
						if a.r.FocusID() != promptID || a.r.BaseMode() != "insert" || !strings.Contains(fr, "[send]") || strings.Contains(fr, "COPY") || len(a.r.Modals()) != 0 {
							t.Fatalf("key %c before capture completed: %s\n%s", key, where(a), fr)
						}
					}
					if got := string(a.pb.input.Text); got != text {
						t.Fatalf("undrained burst wrote %q, want %q", got, text)
					}
					for _, cmd := range commands {
						settle(a, cmd)
					}
					pressSettle(t, a, "enter")
					if !slices.Equal(f.prompts, []string{"submit w1 " + text}) || len(f.killed) != 0 {
						t.Fatalf("sent %q, killed %q", f.prompts, f.killed)
					}
					press(t, a, "esc", "esc")
					if a.r.FocusID() != row {
						t.Fatalf("Esc returned to %s, want %s", where(a), row)
					}
				})
			}
		}
	}
}

func TestActivityPromptCaptureFailureKeepsDraftAndReportsError(t *testing.T) {
	t.Parallel()
	a, f, row := externalQueuePromptApp(t, false, tabActivity)
	a.preview = preview{}
	f.err = errors.New("capture unavailable")
	_, cmd := a.Update(keyMsg("i"))
	typeText(t, a, "draft survives")
	frame(a)
	settle(a, cmd)
	fr := frame(a)
	if a.r.FocusID() != promptID || a.r.BaseMode() != "insert" || string(a.pb.input.Text) != "draft survives" || !strings.Contains(fr, "[send]") {
		t.Fatalf("capture failure lost prompt: %s\n%s", where(a), fr)
	}
	if !strings.Contains(a.m.Status, "capture unavailable") {
		t.Fatalf("capture failure feedback = %q", a.m.Status)
	}
	press(t, a, "esc", "esc")
	if a.r.FocusID() != row {
		t.Fatalf("Esc returned to %s, want %s", where(a), row)
	}
}
