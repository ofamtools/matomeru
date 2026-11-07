package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

const peerTargetCatalog = `{"schema":1,"name":"o-beta","version":"2.0","nouns":[{"name":"doc","verbs":[{"name":"send","kind":"application","params":{"pane":{"type":"string"}}}]}]}`

func addPeerFixture(t *testing.T, a *App, registration string, earlier bool) string {
	t.Helper()
	bin, log := fakeExtension(t, peerTargetCatalog, `"doc send") echo 'peer sent doc-7 pane-9' ;;`)
	renamed := filepath.Join(filepath.Dir(bin), "o-beta")
	if err := os.Rename(bin, renamed); err != nil {
		t.Fatal(err)
	}
	var doc ocli.CatalogDoc
	if err := json.Unmarshal([]byte(peerTargetCatalog), &doc); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	if earlier {
		at = time.Unix(1, 0)
	}
	if err := a.extRegistry().write(registration, extEntry{Bin: renamed, Catalog: doc, RegisteredAt: at}); err != nil {
		t.Fatal(err)
	}
	return log
}

func peerSourceFixture(t *testing.T) *App {
	t.Helper()
	a, _, _ := stateApp(t)
	e, err := a.extRegistry().Read("fake")
	if err != nil {
		t.Fatal(err)
	}
	e.Catalog.PeerBindings = []ochazuke.Binding{{Noun: "fake.item", Mode: "normal", Keys: "g s", Request: ochazuke.Request{Noun: "beta.doc", Verb: "send"}, From: map[string]string{"target": "row:doc", "pane": "row:pane"}}}
	if err := a.extRegistry().write("fake", e); err != nil {
		t.Fatal(err)
	}
	return a
}

func focusPeerSource(t *testing.T, a *App) {
	t.Helper()
	openExtPage(t, a, extPageID("fake", ""))
	id := extPageID("fake", "") + "/item/i1"
	inst := a.r.Instances[id]
	if inst == nil {
		t.Fatal("missing source row")
	}
	// The opaque foreign Data stays unchanged: fields live in mounted Props.
	list := a.r.List(extPageID("fake", "") + "/item")
	items := list.Items()
	items[0].Props = ochazuke.Thing{"doc": "doc-7", "pane": "pane-9"}
	if err := list.SetItems(items); err != nil {
		t.Fatal(err)
	}
	if err := a.r.SetFocus(id); err != nil {
		t.Fatal(err)
	}
}

func TestExtensionPeerRowsAcrossOrderAndNaming(t *testing.T) {
	t.Parallel()
	for _, earlier := range []bool{false, true} {
		for _, legacy := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{true: "earlier", false: "later"}[earlier], map[bool]string{true: "legacy", false: "namespaced"}[legacy]}, "/"), func(t *testing.T) {
				t.Parallel()
				a := peerSourceFixture(t)
				log := addPeerFixture(t, a, "renamed", earlier)
				if legacy {
					if err := a.r.Settings().Set(settingExtNounNames, "legacy"); err != nil {
						t.Fatal(err)
					}
				}
				a.syncExtensions()
				focusPeerSource(t, a)
				for range 2 {
					a.reloadPeerBindings()
					press(t, a, "g")
					pressJob(t, a, "s")
				}
				joined := strings.Join(calls(t, log), "\n")
				if !strings.Contains(joined, `doc send --target doc-7 --json {"pane":"pane-9"} --yes`) {
					t.Fatalf("wrong peer argv:\n%s", joined)
				}
				if !strings.Contains(frame(a), "peer sent doc-7 pane-9") {
					t.Fatalf("no status on source page:\n%s", frame(a))
				}
				if _, err := a.r.Execute(ochazuke.Request{Noun: "extension", Verb: "remove", Args: ochazuke.Arguments{"name": "renamed"}, Confirmed: true}); err != nil {
					t.Fatal(err)
				}
				before := len(calls(t, log))
				press(t, a, "g")
				pressJob(t, a, "s")
				if len(calls(t, log)) != before {
					t.Fatal("removed peer key still ran")
				}
				statuses := a.r.PeerBindings()
				if len(statuses) != 1 || statuses[0].Loaded || !strings.Contains(statuses[0].Reason, "extension beta is not registered") {
					t.Fatalf("removal status %+v", statuses)
				}
			})
		}
	}
}

func TestExtensionPeerPoliciesAndOwnerLayer(t *testing.T) {
	t.Parallel()
	a := peerSourceFixture(t)
	addPeerFixture(t, a, "beta", false)
	a.syncExtensions()
	cfg := a.r.Config().Path(ochazuke.BindingsFile)
	if err := os.WriteFile(cfg, []byte(`[{"noun":"fake.item","mode":"normal","keys":"g s","request":{"noun":"fake.link","verb":"toggle"}}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.r.ReloadUserBindings()
	for range 2 {
		a.reloadPeerBindings()
		if p := a.r.PeerBindings(); len(p) != 1 || !p[0].Loaded {
			t.Fatalf("owner/old layer rejected peer: %+v", p)
		}
	}
	focusPeerSource(t, a)
	rows := a.r.ListBindings(a.r.FocusID())
	if !slices.ContainsFunc(rows, func(b ochazuke.Binding) bool { return b.Keys == "g s" && b.Request.Key() == "fake.link.toggle" }) {
		t.Fatal("owner row lost precedence")
	}
	if err := a.r.Settings().Set(settingExtPeerKeys, "off"); err != nil {
		t.Fatal(err)
	}
	if p := a.r.PeerBindings(); len(p) != 1 || p[0].Loaded || !strings.Contains(p[0].Reason, "off") {
		t.Fatalf("off: %+v", p)
	}
	if !strings.Contains(a.peerRowFacts("fake"), "dropped peer rows") {
		t.Fatal("no dropped-row detail")
	}
	if err := a.r.Settings().Set(ochazuke.PeerNoticesSetting, "off"); err != nil {
		t.Fatal(err)
	}
	if a.peerRowFacts("fake") != "" {
		t.Fatal("off still draws peer detail")
	}
	for _, name := range []string{"fake", "beta"} {
		if _, err := a.r.Execute(ochazuke.Request{Noun: "extension", Verb: "remove", Args: ochazuke.Arguments{"name": name}, Confirmed: true}); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.r.PeerBindings()) != 0 {
		t.Fatal("last removal left stale peer statuses")
	}
}

func TestExtensionPeerOwnRowsAndStatusOff(t *testing.T) {
	t.Parallel()
	a := peerSourceFixture(t)
	log := addPeerFixture(t, a, "beta", false)
	a.syncExtensions()
	if err := a.r.Settings().Set(settingExtPeerStatus, false); err != nil {
		t.Fatal(err)
	}
	focusPeerSource(t, a)
	press(t, a, "g")
	pressJob(t, a, "s")
	if !slices.ContainsFunc(calls(t, log), func(c string) bool { return strings.Contains(c, "doc send --target doc-7") }) {
		t.Fatal("peer did not run")
	}
	if strings.Contains(frame(a), "peer sent doc-7 pane-9") {
		t.Fatal("status off still draws on source page")
	}
	x := a.extension("fake")
	x.doc.PeerBindings[0].Noun = "task"
	if err := a.r.Settings().Set(settingExtPeerKeys, "own-rows"); err != nil {
		t.Fatal(err)
	}
	if p := a.r.PeerBindings(); len(p) != 1 || p[0].Loaded || !strings.Contains(p[0].Reason, "declaring extension") {
		t.Fatalf("own-rows: %+v", p)
	}
	if err := a.r.Settings().Set(settingExtPeerKeys, "any"); err != nil {
		t.Fatal(err)
	}
	if p := a.r.PeerBindings(); len(p) != 1 || !p[0].Loaded {
		t.Fatalf("any: %+v", p)
	}
}
