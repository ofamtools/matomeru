//go:build monogatari

package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"ofam.dev/monogatari"
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
