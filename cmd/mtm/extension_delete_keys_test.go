package main

import (
	"encoding/json"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

func TestExtensionDeleteSequenceKeepsConfirmedVariants(t *testing.T) {
	t.Parallel()
	var doc ocli.CatalogDoc
	if err := json.Unmarshal([]byte(crudCatalogNamed("new", "edit", "delete")), &doc); err != nil {
		t.Fatal(err)
	}
	for i := range doc.Nouns[0].Verbs {
		v := &doc.Nouns[0].Verbs[i]
		if v.Name == "delete" {
			v.Confirm = "Delete {{label}}?"
			v.Params = map[string]ochazuke.Parameter{"force": {Type: "boolean"}}
		}
	}
	doc.Bindings = append(doc.Bindings, ochazuke.Binding{Noun: "thing", Mode: "normal", Keys: "d D", Request: ochazuke.Request{Noun: "thing", Verb: "delete", Args: ochazuke.Arguments{"force": true}}})
	raw, _ := json.Marshal(doc)
	a, log := crudApp(t, string(raw))
	before := len(calls(t, log))
	pressJob(t, a, "d")
	pressJob(t, a, "D")
	if a.r.TopModal() == "" || !strings.Contains(frame(a), "Delete one?") {
		t.Fatalf("no confirmation: %s", frame(a))
	}
	if len(calls(t, log)) != before {
		t.Fatal("delete executed before consent")
	}
	press(t, a, "n")
	if len(calls(t, log)) != before {
		t.Fatal("decline executed delete")
	}
	pressJob(t, a, "d")
	pressJob(t, a, "D")
	pressJob(t, a, "y")
	if got := since(t, log, before); len(got) != 1 || got[0] != `thing delete --target t1 --json {"force":true} --yes` {
		t.Fatalf("force variant: %v", got)
	}
}
