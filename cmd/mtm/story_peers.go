//go:build monogatari

package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/monogatari"
	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

func buildPeerRows(env monogatari.Env) (monogatari.Built, error) {
	return buildPeerFixture(env, false, "")
}
func buildMissingPeerRows(env monogatari.Env) (monogatari.Built, error) {
	return buildPeerFixture(env, true, "")
}

func buildMountedResults(env monogatari.Env) (monogatari.Built, error) {
	return buildPeerFixture(env, true, `{"schema":1,"name":"o-alpha","nouns":[{"name":"item","verbs":[{"name":"list","kind":"application","rows":"item"},{"name":"output","kind":"application","result":"text"},{"name":"history","kind":"application","result":"rows"}]}],"bindings":[{"noun":"item","mode":"normal","keys":"enter","request":{"noun":"item","verb":"output"}},{"noun":"item","mode":"normal","keys":"H","request":{"noun":"item","verb":"history"}}]}`)
}

func buildNamedDeleteRows(env monogatari.Env) (monogatari.Built, error) {
	return buildPeerFixture(env, false, `{"schema":1,"name":"o-alpha","nouns":[{"name":"item","verbs":[{"name":"list","kind":"application","rows":"item"},{"name":"delete","kind":"application","confirm":"Remove machine {{name}}?","params":{"name":{"type":"string"}}}]}],"bindings":[{"noun":"item","mode":"normal","keys":"d d","request":{"noun":"item","verb":"delete"},"from":{"name":"row:name"}}]}`)
}

func buildPeerFixture(env monogatari.Env, missing bool, alphaCatalog string) (built monogatari.Built, err error) {
	built, err = buildSeeded(env)
	if err != nil {
		return built, err
	}
	defer func() {
		if err != nil {
			built.Close()
		}
	}()
	a := built.Program.(*App)
	catalogs := map[string]string{
		"alpha": `{"schema":1,"name":"o-alpha","nouns":[{"name":"item","verbs":[{"name":"list","kind":"application","rows":"item"},{"name":"delete","kind":"application","confirm":"Delete {{label}}?","params":{"force":{"type":"boolean"}}}]}],"bindings":[{"noun":"item","mode":"normal","keys":"d D","request":{"noun":"item","verb":"delete","args":{"force":true}}}],"peerBindings":[{"noun":"alpha.item","mode":"normal","keys":"g s","request":{"noun":"beta.doc","verb":"send"},"from":{"target":"row:doc","pane":"row:pane"}}]}`,
		"beta":  `{"schema":1,"name":"o-beta","nouns":[{"name":"doc","verbs":[{"name":"send","kind":"application","params":{"pane":{"type":"string"}}}]}]}`,
	}
	if alphaCatalog != "" {
		catalogs["alpha"] = alphaCatalog
	}
	for _, name := range []string{"alpha", "beta"} {
		if missing && name == "beta" {
			continue
		}
		var doc ocli.CatalogDoc
		if err = json.Unmarshal([]byte(catalogs[name]), &doc); err != nil {
			return built, err
		}
		bin := filepath.Join(a.dataDir(), "o-"+name)
		script := "#!/bin/sh\ncase \"$1 $2\" in\n\"catalog \") cat <<'CATALOG'\n" + catalogs[name] + "\nCATALOG\n;;\n" +
			`"item list") echo '{"id":"i1","label":"Source row","name":"sandbox","doc":"doc-7","pane":"pane-9"}' ;;
"doc send") printf '%s\n' "$*" ;;
"item output") printf '%s\n' '"hello-out\nexit 0"' ;;
"item history") printf '%s\n' '[{"name":"build","status":"ok"},{"name":"test","status":"failed"}]' ;;
*) echo '[]' ;;
esac
`
		if err = os.WriteFile(bin, []byte(script), 0o755); err != nil {
			return built, err
		}
		if err = a.extRegistry().write(name, extEntry{Bin: bin, Catalog: doc}); err != nil {
			return built, err
		}
	}
	a.syncExtensions()
	return built, nil
}

// buildDetailNotes is mounted-results with two context items and a note about
// each thing that has one: the first context item and the alpha row i1.
func buildDetailNotes(env monogatari.Env) (monogatari.Built, error) {
	built, err := buildMountedResults(env)
	if err != nil {
		return built, err
	}
	a := built.Program.(*App)
	fail := func(err error) (monogatari.Built, error) {
		built.Close()
		return monogatari.Built{}, err
	}
	var about []string
	for i, it := range []contextitems.Item{
		{Kind: "link", Text: "https://example.com/with-note", Window: "w1"},
		{Kind: "link", Text: "https://example.com/no-note", Window: "w1"},
	} {
		added, _, err := a.contexts.Merge([]contextitems.Item{it}, contextitems.Owner{}, int64(i+1))
		if err != nil || len(added) == 0 {
			return fail(cmp.Or(err, errors.New("context item not added")))
		}
		about = append(about, ochazuke.AnyRef(contextNoun, added[0].ID))
	}
	about = append(about[:1], ochazuke.AnyRef("alpha.item", "i1"))
	for i, ref := range about {
		note := work.Note{ID: fmt.Sprintf("detail-note-%d", i), Text: []string{"Ask for the spec link", "Source row needs a rerun"}[i], About: ref, Created: int64(i + 20), Updated: int64(i + 20)}
		if err := a.work.PutNote(note); err != nil {
			return fail(err)
		}
	}
	return built, nil
}
