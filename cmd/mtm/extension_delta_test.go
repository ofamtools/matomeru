package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// deltaAnswers are a real ochazuke shell's answers to item state: the
// first call (--since=, every list in full with hashes) and the next one
// from the hashes that answer gave (every list unchanged).
func deltaAnswers(t *testing.T) (full, unchanged []byte, hash string) {
	t.Helper()
	r := ochazuke.NewRuntime()
	r.Define("item").Verb("state", func(ochazuke.Ctx) ochazuke.Result {
		return ochazuke.Done(ocli.Lines{
			ocli.Row{Noun: "item", ID: "s1", Label: "one", Cells: []string{"one", "open"}},
			ocli.Row{Noun: "item", ID: "s2", Label: "two", Cells: []string{"two", "done"}},
		})
	}, "").State()
	dir := filepath.Join(t.TempDir(), "state")
	var out, errOut bytes.Buffer
	sh := ocli.New(r, "o-fake", strings.NewReader(""), &out, &errOut)
	sh.StateCache.Dir = dir
	if err := sh.Run(context.Background(), []string{"item", "state", "--json", "{}", "--since="}); err != nil {
		t.Fatal(err, errOut.String())
	}
	full = bytes.Clone(out.Bytes())
	answer, err := ocli.DecodeState(full)
	if err != nil || !answer.Delta || len(answer.Lists) != 1 {
		t.Fatalf("full answer %+v %v:\n%s", answer, err, full)
	}
	hash = answer.Lists[0].Hash
	out.Reset()
	sh = ocli.New(r, "o-fake", strings.NewReader(""), &out, &errOut)
	sh.StateCache.Dir = dir
	if err := sh.Run(context.Background(), append([]string{"item", "state", "--json", "{}"}, ocli.StateFlags(answer.Cache, map[string]string{"item": hash})...)); err != nil {
		t.Fatal(err, errOut.String())
	}
	unchanged = bytes.Clone(out.Bytes())
	if !strings.Contains(string(unchanged), `"unchanged"`) {
		t.Fatalf("second answer is not unchanged:\n%s", unchanged)
	}
	return full, unchanged, hash
}

// A tool whose catalog declares stateDelta is reloaded through the delta
// protocol: the first load asks for every list in full, a reload sends
// the hashes it holds and an unchanged answer touches no row; an explicit
// extension refresh asks for every list in full again.
func TestExtensionStateReloadsAsDeltas(t *testing.T) {
	t.Parallel()
	full, unchanged, hash := deltaAnswers(t)
	dir := t.TempDir()
	fullFile, unchangedFile := filepath.Join(dir, "full.jsonl"), filepath.Join(dir, "unchanged.jsonl")
	if err := os.WriteFile(fullFile, full, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unchangedFile, unchanged, 0o644); err != nil {
		t.Fatal(err)
	}
	catalog := strings.Replace(strings.Replace(fakeCatalog, "VERSION", "1.0", 1), `"pages":[]`,
		`"state":"item.state","stateDelta":1,"pages":[{"id":"main","title":"Fake Delta","lists":[{"id":"items","noun":"item"}]}]`, 1)
	catalog = strings.Replace(catalog, `{"name":"list","kind":"application","description":"List the items"},`,
		`{"name":"list","kind":"application","description":"List the items"},{"name":"state","kind":"application"},`, 1)
	bin, log := fakeExtension(t, catalog, `"item state") case "$*" in *--since=item=?*) cat '`+unchangedFile+`' ;; *) cat '`+fullFile+`' ;; esac ;;`)
	a := registered(t, seeded(t), "delta", bin)
	openExtPage(t, a, "ext/delta/main")
	list := a.r.List("ext/delta/items")
	if list == nil || list.Len() != 2 {
		t.Fatalf("rows: %+v", list)
	}
	got := workCalls(t, log)
	if last := got[len(got)-1]; !strings.HasSuffix(last, "|item state --json {} --since=item=") {
		t.Fatalf("first load %q", last)
	}
	before := map[string]*ochazuke.Instance{}
	for _, it := range list.Items() {
		before[it.ID] = a.r.Instances[it.ID]
	}
	x := a.extension("delta")
	a.reloadExtension(x)
	settleJobs(t, a, a.r.PendingCommands())
	got = calls(t, log)
	if last := got[len(got)-1]; !strings.Contains(last, "|item state --json {} --since-cache ") || !strings.HasSuffix(last, "--since=item="+hash) {
		t.Fatalf("reload %q, want the held hash %s", last, hash)
	}
	for id, inst := range before {
		if a.r.Instances[id] != inst {
			t.Fatalf("an unchanged reload replaced row %s", id)
		}
	}
	a.refreshExtensionRows(x)
	settleJobs(t, a, a.r.PendingCommands())
	got = calls(t, log)
	var refresh string
	for _, c := range got {
		if strings.Contains(c, "|item state") {
			refresh = c
		}
	}
	if strings.Contains(refresh, "--since=item="+hash) {
		t.Fatalf("refresh sent the held hash: %q", refresh)
	}
	if list.Len() != 2 {
		t.Fatalf("refresh rows: %d", list.Len())
	}
}
