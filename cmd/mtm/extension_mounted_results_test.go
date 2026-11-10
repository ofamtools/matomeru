package main

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// tableResultsSetting is ochazuke's keys.table-results (v0.111.0 and later).
const tableResultsSetting = "keys.table-results"

// A mounted verb's answer is presented by the kind its catalog row declares:
// text and Markdown in their viewer, rows in the results table, anything
// else as the page's status line. The host consults no tool, noun or verb
// name; the library's key presenter does it (keys.text-results,
// keys.table-results), and each setting turns its kind off.
func TestMountedAnswersPresentedByDeclaredKind(t *testing.T) {
	t.Parallel()
	const catalog = `{"schema":1,"name":"o-fake","version":"1","nouns":[{"name":"item","verbs":[` +
		`{"name":"list","kind":"application"},` +
		`{"name":"output","kind":"application","result":"text"},` +
		`{"name":"notes","kind":"application","result":"markdown"},` +
		`{"name":"history","kind":"application","result":"rows"},` +
		`{"name":"ping","kind":"application","result":"JSON value"}]}],` +
		`"bindings":[` +
		`{"noun":"item","mode":"normal","keys":"g o","request":{"noun":"item","verb":"output"}},` +
		`{"noun":"item","mode":"normal","keys":"g n","request":{"noun":"item","verb":"notes"}},` +
		`{"noun":"item","mode":"normal","keys":"g h","request":{"noun":"item","verb":"history"}},` +
		`{"noun":"item","mode":"normal","keys":"g p","request":{"noun":"item","verb":"ping"}}],"pages":[]}`
	const answers = `"item output") printf '%s\n' '"hello-out\nexit 0"' ;;
"item notes") printf '%s\n' '"# Notes\n\nbody text"' ;;
"item history") printf '%s\n' '[{"name":"build","status":"ok"},{"name":"test","status":"failed"}]' ;;
"item ping") printf '%s\n' '{"ok":true}' ;;`
	for _, tc := range []struct {
		name, key, off, modal string
		contains, status      string
	}{
		{name: "text", key: "o", modal: ochazuke.TextViewID, contains: "hello-out"},
		{name: "markdown", key: "n", modal: "markdown-view", contains: "body text"},
		{name: "rows", key: "h", modal: "palette-results", contains: "failed"},
		{name: "status", key: "p", status: `item ping: {"ok":true}`},
		{name: "text off", key: "o", off: ochazuke.KeyTextResultsSetting, status: `item output: "hello-out\nexit 0"`},
		{name: "rows off", key: "h", off: tableResultsSetting, status: `item history: [{"name":"build"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin, _ := fakeExtension(t, catalog, answers)
			if _, declared := ochazuke.NewRuntime().Settings().Value(tableResultsSetting); !declared && strings.HasPrefix(tc.name, "rows") {
				t.Skip("ochazuke without keys.table-results")
			}
			a := registered(t, seeded(t), "fake", bin)
			openExtPage(t, a, "ext/fake")
			if err := a.r.SetFocus("ext/fake/item/i1"); err != nil {
				t.Fatal(err)
			}
			if tc.off != "" {
				if err := a.r.Settings().Set(tc.off, false); err != nil {
					t.Fatal(err)
				}
			}
			pressJob(t, a, "g")
			_, cmd := a.Update(keyMsg(tc.key))
			settleJobs(t, a, cmd)
			if got := a.r.TopModal(); got != tc.modal {
				t.Fatalf("top modal %q, want %q\n%s", got, tc.modal, frame(a))
			}
			f := frame(a)
			if tc.contains != "" && !strings.Contains(f, tc.contains) {
				t.Fatalf("answer %q not shown:\n%s", tc.contains, f)
			}
			if tc.status != "" && !strings.Contains(f, tc.status[:min(len(tc.status), 30)]) {
				t.Fatalf("status %q missing:\n%s", tc.status, f)
			}
			if tc.modal != "" && strings.Contains(f, "item "+map[string]string{"o": "output", "n": "notes", "h": "history"}[tc.key]+": ") {
				t.Fatalf("answer flattened to a status line under its viewer:\n%s", f)
			}
		})
	}
}

// A verb the catalog marks foreground hands the terminal over as a job and
// never runs captured, so its answer is not flattened to a status line:
// o-cmds H (the full-screen history) is such a verb.
func TestMountedForegroundVerbIsNotCaptured(t *testing.T) {
	t.Parallel()
	const catalog = `{"schema":1,"name":"o-fake","version":"1","nouns":[{"name":"item","verbs":[` +
		`{"name":"list","kind":"application"},` +
		`{"name":"history","kind":"application","foreground":true}]}],` +
		`"bindings":[{"noun":"item","mode":"normal","keys":"H","request":{"noun":"item","verb":"history"}}],"pages":[]}`
	bin, log := fakeExtension(t, catalog, "")
	a := registered(t, seeded(t), "fake", bin)
	openExtPage(t, a, "ext/fake")
	if err := a.r.SetFocus("ext/fake/item/i1"); err != nil {
		t.Fatal(err)
	}
	before := len(calls(t, log))
	_, cmd := a.Update(keyMsg("H"))
	if cmd == nil {
		t.Fatal("a foreground verb queued no terminal handoff")
	}
	for _, c := range calls(t, log)[before:] {
		if strings.Contains(c, "item history") {
			t.Fatalf("a foreground verb ran captured: %q", c)
		}
	}
	found := false
	for _, j := range a.r.Jobs() {
		found = found || strings.HasSuffix(j.Verb, "item.history")
	}
	if !found {
		t.Fatalf("no job for the foreground verb: %+v", a.r.Jobs())
	}
}
