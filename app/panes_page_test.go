package app

import (
	"fmt"
	"strings"
	"testing"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

type pasteCaptureHost struct {
	frame   ptyhost.Frame
	written [][]byte
}

func (h *pasteCaptureHost) Open(string, string, string, int, int) error { return nil }
func (h *pasteCaptureHost) Resize(string, int, int) error               { return nil }
func (h *pasteCaptureHost) Close(string) error                          { return nil }
func (h *pasteCaptureHost) CloseAll()                                   {}
func (h *pasteCaptureHost) Snapshot(id string) (ptyhost.Frame, bool) {
	f := h.frame
	f.ID = id
	return f, true
}
func (h *pasteCaptureHost) Write(_ string, data []byte) error {
	h.written = append(h.written, append([]byte(nil), data...))
	return nil
}

func TestNewPaneFormDefaultsToZshFirst(t *testing.T) {
	a, d := demo(t)
	// Direct record.new {kind: "pane"} without excludeChoices keeps zsh first and default.
	d.Send(core.CommandEvent{Command: core.Command{
		ID:     views.CmdNew,
		Args:   map[string]any{"kind": data.KindPane},
		Source: "test",
		Actor:  User,
	}})
	if a.Modal() != "form" {
		t.Fatalf("record.new pane did not open New Pane form: modal=%q", a.Modal())
	}
	rf := a.activeRecordForm()
	if rf == nil || rf.kind != data.KindPane {
		t.Fatalf("expected pane form, got %+v", rf)
	}
	fm := rf.Model()
	var choices []string
	for _, sp := range fm.Specs {
		if sp.Name == "harness" {
			choices = sp.Enum
			break
		}
	}
	if len(choices) == 0 || choices[0] != "zsh" {
		t.Fatalf("expected zsh at front of New Pane harness choices, got %v", choices)
	}
	if got := data.Text(fm.Values["harness"]); got != "zsh" {
		t.Fatalf("expected default harness value zsh, got %q", got)
	}
	d.Keys("esc", "esc")

	// Pressing n in SpacePanes opens "New Agent" with zsh excluded.
	d.Keys("1", "n")
	if a.Modal() != "form" {
		t.Fatalf("n on Panes did not open New Agent form: modal=%q", a.Modal())
	}
	rf = a.activeRecordForm()
	if rf == nil || rf.kind != data.KindPane || rf.Model().Title != "New Agent" {
		t.Fatalf("expected New Agent pane form, got modal=%+v title=%q", rf, rf.Model().Title)
	}
	choices = nil
	for _, sp := range rf.Model().Specs {
		if sp.Name == "harness" {
			choices = sp.Enum
			break
		}
	}
	if len(choices) == 0 {
		t.Fatal("expected non-empty agent harness choices")
	}
	for _, c := range choices {
		if c == "zsh" {
			t.Fatalf("expected zsh excluded from New Agent harness choices, got %v", choices)
		}
	}
	if got := data.Text(rf.Model().Values["harness"]); got == "" || got == "zsh" {
		t.Fatalf("expected non-zsh default harness in New Agent form, got %q", got)
	}
}

func TestLiveTerminalPreservesBracketedPaste(t *testing.T) {
	a, d := demo(t)
	host := &pasteCaptureHost{
		frame: ptyhost.Frame{
			Cols:           80,
			Rows:           24,
			Lines:          []string{"$ "},
			Plain:          []string{"$ "},
			BracketedPaste: true,
			Revision:       1,
		},
	}
	a.panes.Host = host
	a.panes.Term = ptyhost.NewController(host)

	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected selected pane")
	}
	if a.rt.live.frames == nil {
		a.rt.live.frames = map[string]ptyhost.Frame{}
	}
	a.rt.live.frames[pane.ID] = host.frame

	// Enter Screen terminal mode.
	d.Keys("enter", "enter")
	if a.Mode() != ModeTerminal {
		t.Fatalf("expected ModeTerminal, got %q", a.Mode())
	}

	// Send multi-line PasteEvent -> must be wrapped in \x1b[200~...\x1b[201~
	multi := "echo hello\necho world"
	d.Send(core.PasteEvent{Text: multi})
	if len(host.written) != 1 || string(host.written[0]) != "\x1b[200~"+multi+"\x1b[201~" {
		t.Fatalf("expected bracketed paste bytes, got %q", host.written)
	}

	// Disable DEC mode 2004 -> raw bytes.
	host.frame.BracketedPaste = false
	host.written = nil
	d.Send(core.PasteEvent{Text: multi})
	if len(host.written) != 1 || string(host.written[0]) != multi {
		t.Fatalf("expected raw paste when DEC 2004 off, got %q", host.written)
	}
}

func TestUIPageTopDownScrollPaneLockAndOptOut(t *testing.T) {
	a, d := demo(t)
	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected selected pane")
	}

	// Build a faithful multi-section ui.page: Sections 1-6 tall tables + Section 7 bottom Question Card.
	var sections []ui.ViewNode
	for i := 1; i <= 6; i++ {
		sections = append(sections, ui.ViewNode{
			ID:     fmt.Sprintf("sec.%d", i),
			Kind:   ui.KindPanel,
			Title:  fmt.Sprintf("Section %d · Migration Rules", i),
			Border: true,
			Box:    layout.Constraints{FixedH: 8},
			Children: []ui.ViewNode{{
				ID:    fmt.Sprintf("sec.%d.txt", i),
				Kind:  ui.KindText,
				Props: map[string]any{"text": fmt.Sprintf("Top content block for Section %d", i)},
			}},
		})
	}
	sections = append(sections, ui.ViewNode{
		ID:        "q-bottom-card",
		Kind:      ui.KindCard,
		Scope:     mcpui.ScopeQuestion,
		Focusable: true,
		Border:    true,
		Title:     "Section 7 · Approve Cutover?",
		Box:       layout.Constraints{FixedH: 6},
		Props:     map[string]any{"summary": "Bottom interactive question card"},
		Children: []ui.ViewNode{{
			ID:   "q-bottom-form",
			Kind: ui.KindForm,
			Props: map[string]any{
				"fields": []map[string]any{{"name": "choice", "type": "string", "enum": []string{"yes", "no"}, "default": "yes"}},
			},
		}},
	})
	res, err := a.Router.CreatePage(User, mcpui.CreatePageArgs{
		Pane:  pane.ID,
		Slug:  "migration-review",
		Title: "Site Migration Review",
		Root:  ui.ViewNode{ID: "migration.root", Kind: ui.KindVStack, Gap: 1, Children: sections},
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	// Open the page via TOC: must open at line 1 (Section 1 visible), NOT jumping to Section 7!
	s := a.jumpToPage(a.State(), res.ID)
	a.Shell.State = s
	d.Keys("enter")
	if a.Mode() != ModePage {
		t.Fatalf("expected ModePage on opening ui.page, got %q", a.Mode())
	}
	if a.Focus() != views.PageScrollID {
		t.Fatalf("expected focus on %q, got %q", views.PageScrollID, a.Focus())
	}
	if got := a.pageScrollOffset(res.ID); got != 0 {
		t.Fatalf("expected initial scroll offset 0, got %d", got)
	}
	topFrame := d.Frame()
	if !strings.Contains(topFrame, "Section 1") {
		t.Fatalf("opening ui.page did not show Section 1 at top:\n%s", topFrame)
	}

	// Pressing k at the top must NEVER spill focus out of the locked page.
	d.Keys("k", "k", "ctrl+u")
	if a.Mode() != ModePage || a.Focus() != views.PageScrollID || a.pageScrollOffset(res.ID) != 0 {
		t.Fatalf("upward scroll at top spilled out: mode=%q focus=%q off=%d", a.Mode(), a.Focus(), a.pageScrollOffset(res.ID))
	}

	// Scroll down to bottom with G -> Section 7 is visible, still locked in ModePage.
	d.Keys("G")
	bottomOff := a.pageScrollOffset(res.ID)
	if bottomOff <= 0 {
		t.Fatalf("expected positive bottom scroll offset, got %d", bottomOff)
	}
	if !strings.Contains(d.Frame(), "Section 7") {
		t.Fatalf("scrolling to bottom did not reveal Section 7:\n%s", d.Frame())
	}
	// Pressing j past the bottom must NEVER spill focus out.
	d.Keys("j", "j", "ctrl+d")
	if a.Mode() != ModePage {
		t.Fatalf("downward scroll at bottom spilled out of ModePage: got %q", a.Mode())
	}

	// Scroll back up to top with T -> Section 1 visible again!
	d.Keys("T")
	if a.pageScrollOffset(res.ID) != 0 || !strings.Contains(d.Frame(), "Section 1") {
		t.Fatalf("expected T to return to top Section 1: off=%d", a.pageScrollOffset(res.ID))
	}

	// Explicit detach with esc returns to ModeNormal on TOC entry.
	d.Keys("esc")
	if a.Mode() != ModeNormal || a.Focus() != tocPrefix+res.ID {
		t.Fatalf("esc detach mismatch: mode=%q focus=%q want %q", a.Mode(), a.Focus(), tocPrefix+res.ID)
	}

	// Edge focus spill is OFF by default in ModeNormal: pressing k at top of rail stays in rail.
	d.Keys("h", "g", "g", "k", "k")
	if a.Focus() != railTable {
		t.Fatalf("expected no edge spill at top of rail, got focus %q", a.Focus())
	}
}

func TestMinimalHookBarLaneInTOCAndCompactScalableTOC(t *testing.T) {
	a, d := demo(t)
	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected selected pane")
	}

	// 1. Minimal hook indicator: shows ONLY hook fired + event name, strips prompt/summary noise.
	a.setTelemetry(&daemon.TelemetryResult{
		Telemetry: map[string]*daemon.PaneTelemetry{
			pane.ID: {
				PaneID:        pane.ID,
				State:         daemon.StateRunning,
				LastKind:      daemon.HookKindActivity,
				Tool:          "Bash",
				SubagentCount: 2,
				Summary:       "SECRET_PROMPT_DUMP_SHOULD_NOT_APPEAR",
				Payload:       map[string]any{"hook_event_name": "PreToolUse"},
			},
		},
	}, true)
	a.viewMemo.gen++
	d.Send(resize(120, 36))

	frame := d.Frame()
	if !strings.Contains(frame, "Hook: PreToolUse") {
		t.Fatalf("expected minimal hook indicator 'Hook: PreToolUse':\n%s", frame)
	}
	if strings.Contains(frame, "SECRET_PROMPT_DUMP_SHOULD_NOT_APPEAR") || strings.Contains(frame, "tool: Bash") {
		t.Fatalf("minimal hook indicator leaked verbose summary/tool dump:\n%s", frame)
	}

	// Opt-out panes.verboseHookBar=true restores verbose details.
	a.cfg.userSettings[SetVerboseHookBar] = true
	a.reloadSettings()
	a.viewMemo.gen++
	d.Send(resize(120, 36))
	vFrame := d.Frame()
	if !strings.Contains(vFrame, "tool: Bash") || !strings.Contains(vFrame, "SECRET_PROMPT_DUMP_SHOULD_NOT_APPEAR") {
		t.Fatalf("verboseHookBar opt-out did not restore verbose hook bar:\n%s", vFrame)
	}
	a.cfg.userSettings[SetVerboseHookBar] = false
	a.reloadSettings()

	// 2. Lane is in TOC by default (not top bar); creating 16 agent pages renders compactly with collapsible Pages group.
	for i := 1; i <= 16; i++ {
		res, err := a.Router.CreatePage(User, mcpui.CreatePageArgs{
			Pane:  pane.ID,
			Slug:  fmt.Sprintf("agent-page-%02d", i),
			Title: fmt.Sprintf("Agent Report %02d", i),
			Root:  ui.ViewNode{Kind: ui.KindText, Props: map[string]any{"text": "body"}},
		})
		if err != nil {
			t.Fatalf("CreatePage %d: %v", i, err)
		}
		// Mark viewed so they go into the collapsible Pages group.
		if _, err := a.Router.MarkViewed(res.ID, viewer); err != nil {
			t.Fatalf("MarkViewed %d: %v", i, err)
		}
	}
	a.viewMemo.gen++
	d.Send(resize(120, 36))
	tocFrame := d.Frame()
	if !strings.Contains(tocFrame, "◈ 3/6 Matomeru") || !strings.Contains(tocFrame, "▾ Pages (") {
		t.Fatalf("expected lane in TOC and collapsible Pages group:\n%s", tocFrame)
	}
	if strings.Contains(tocFrame, "Lane: Matomeru v2 Step 2") {
		t.Fatalf("top lane bar should be hidden by default when laneProgressBar=false:\n%s", tocFrame)
	}

	// Collapsing the Pages group hides older pages cleanly.
	a.panes.tocCollapsed["pages"] = true
	a.viewMemo.gen++
	d.Send(resize(120, 36))
	collapsedFrame := d.Frame()
	if !strings.Contains(collapsedFrame, "▸ Pages (") || strings.Contains(collapsedFrame, "Agent Report 01") {
		t.Fatalf("collapsing Pages group did not fold older pages:\n%s", collapsedFrame)
	}
}
