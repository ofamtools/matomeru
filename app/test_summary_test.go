package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/ui"
)

func TestTestSummaryRunOutput(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	passingEvent := `{"Action":"pass","Package":"pkg","Test":"TestPass"}` + "\n"
	output := strings.Repeat(passingEvent, 5) + `{"Action":"skip","Package":"pkg","Test":"TestSkip"}
{"Action":"output","Package":"pkg","Test":"TestFail","Output":"check failed\n"}
{"Action":"fail","Package":"pkg","Test":"TestFail"}
{"Action":"fail","Package":"pkg"}`
	rec, _, err := store.Create(data.CreateInput{Kind: data.KindRun, Fields: map[string]any{
		"title": "test suite", "output": output, "runStatus": "failed", "outputTruncated": true,
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 140, 40)
	d.Keys("2")
	root := a.View(a.State())
	strip := root.Find(runTestSummaryID + ".strip")
	if strip == nil {
		t.Fatalf("Runs panel lacks the compact summary:\n%s", d.Frame())
	}
	segments, ok := ui.DecodeInlineRuns(strip.Props["runs"])
	want := []string{"success", "muted", "error"}
	if !ok || len(segments) != len(want) {
		t.Fatalf("compact strip segments = %+v", segments)
	}
	for i, tone := range want {
		if segments[i].Tone != tone {
			t.Errorf("compact strip tone %d = %q, want %q", i, segments[i].Tone, tone)
		}
	}
	theme := ui.DefaultDarkTheme()
	theme.Tones["success"] = ui.ToneStyle{Fg: "2"}
	theme.Tones["muted"] = ui.ToneStyle{Fg: "8"}
	theme.Tones["error"] = ui.ToneStyle{Fg: "1"}
	narrow, _ := ui.Render(*strip, theme, "", 4, 2)
	lines := strings.Split(ansiRe.ReplaceAllString(narrow.ANSI, ""), "\n")
	if len(lines) != 2 || strings.TrimRight(lines[0], " ") != "...." || strings.TrimRight(lines[1], " ") != ".s✗" ||
		!strings.Contains(narrow.ANSI, "\x1b[32m....") || !strings.Contains(narrow.ANSI, "\x1b[90ms") || !strings.Contains(narrow.ANSI, "\x1b[31m✗") {
		t.Fatalf("narrow strip did not wrap or retain its tones: %q", narrow.ANSI)
	}
	if frame := d.Frame(); !strings.Contains(frame, ".s✗") || !strings.Contains(frame, "check failed") || !strings.Contains(frame, "partial test results") {
		t.Fatalf("Runs panel did not show the symbol strip, failure details, and truncation label:\n%s", frame)
	}
	settings, err := config.LoadSettings([]byte(`{"runs.compactTestOutput":false}`), DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.Settings = settings
	a.applySettings()
	root = a.View(a.State())
	rawFrame, _ := ui.Render(root, a.Shell.Theme, a.State().ActiveFrame().FocusID, 140, 40)
	plain := ansiRe.ReplaceAllString(rawFrame.ANSI, "")
	if root.Find(runTestSummaryID) != nil || !strings.Contains(plain, `"Action":"pass"`) {
		t.Fatalf("setting off did not restore raw output:\n%s", plain)
	}
	stored, err := store.Get(rec.Ref())
	if err != nil || stored.Text("output") != output {
		t.Fatalf("Run output changed after formatting, error=%v", err)
	}
}
