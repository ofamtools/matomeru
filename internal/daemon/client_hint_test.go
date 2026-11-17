package daemon

import "testing"

func TestErrorShowsHintDetail(t *testing.T) {
	t.Parallel()
	e := &Error{Code: "forbidden", Message: "refused", Hint: map[string]any{"detail": "set agents.pane-scope to all"}}
	if got, want := e.Error(), "forbidden: refused (set agents.pane-scope to all)"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if got := (&Error{Code: "x", Message: "y"}).Error(); got != "x: y" {
		t.Fatalf("no hint: %q", got)
	}
}
