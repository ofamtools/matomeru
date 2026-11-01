package main

import (
	"strings"
	"testing"
)

func TestTerminalDetachHint(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	press(t, a, "enter")
	f := frame(a)
	if !strings.Contains(f, "detach") {
		t.Fatal("terminal has no detach hint")
	}
	press(t, a, "ctrl+a")
	if f := frame(a); !strings.Contains(f, "d close/detach") {
		t.Fatalf("leader has no exit row hint:\n%s", f)
	}
	press(t, a, "d")
	if a.r.Mode.Name == "terminal" {
		t.Fatal("did not detach")
	}
}
