package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
)

// ruleCaptureDir, when set, receives text captures of the box's rule.
var ruleCaptureDir = os.Getenv("MTM_RULE_CAPTURES")

// The box's rule shows whether the box has the focus: muted while the
// turns have it, the mode's colour once the box has it, faint against
// bold in mono; the text is the same either way, so nothing moves.
func TestThePromptRuleShowsTheBoxFocus(t *testing.T) {
	t.Parallel()
	for _, theme := range []string{"dark", "light", "mono"} {
		t.Run(theme, func(t *testing.T) {
			app, _ := promptApp(t)
			var s ochazuke.Styles
			setTheme := func() { // a key may restyle the model: set it before each read
				app.m.Appearance.Theme, app.m.Appearance.Dark = theme, theme != "light"
				s = ochazuke.NewStyles(*app.m.Appearance)
			}
			p := app.promptPane()
			if p == nil {
				t.Fatal("no prompt pane")
			}
			rule := func() string { setTheme(); return strings.SplitN(app.promptBoxBlock(*p, 70).Text, "\n", 2)[0] }
			press(t, app, "esc", "esc") // the turns have the focus
			if app.r.Focus == promptID {
				t.Fatalf("esc esc stayed in the box")
			}
			unfocused := rule()
			for mode := range promptModes {
				app.pb.mode = mode
				if got := rule(); got != s.Muted.Render(xansi.Strip(got)) {
					t.Fatalf("mode %d unfocused: %q, want %q", mode, got, s.Muted.Render(xansi.Strip(got)))
				}
			}
			app.pb.mode = promptSend
			press(t, app, "}")
			if app.r.Focus != promptID {
				t.Fatalf("} focused %s", app.r.Focus)
			}
			focused := rule()
			for mode := range promptModes {
				app.pb.mode = mode
				if got := rule(); got != app.promptModeStyle(s).Render(xansi.Strip(got)) {
					t.Fatalf("mode %d focused: %q, want the mode's style", mode, got)
				}
			}
			app.pb.mode = promptSend
			if xansi.Strip(focused) != xansi.Strip(unfocused) {
				t.Fatalf("the rule's text changed with the focus:\n%q\n%q", xansi.Strip(unfocused), xansi.Strip(focused))
			}
			setTheme()
			if theme == "mono" {
				for mode := range promptModes {
					app.pb.mode = mode
					if st := app.promptModeStyle(s); !st.GetBold() {
						t.Fatalf("mono mode %d focused is not bold", mode)
					}
				}
				if !s.Muted.GetFaint() {
					t.Fatal("mono muted is not faint")
				}
			}
			if ruleCaptureDir != "" {
				_ = os.WriteFile(filepath.Join(ruleCaptureDir, "prompt-rule-"+theme+".txt"),
					[]byte("unfocused:\n"+unfocused+"\nfocused:\n"+focused+"\n"), 0o644)
			}
		})
	}
}
