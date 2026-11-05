//go:build monogatari

package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"ofam.dev/monogatari"
	ocli "ofam.dev/ochazuke/cli"
)

func buildPeerRows(env monogatari.Env) (monogatari.Built, error) { return buildPeerFixture(env, false) }
func buildMissingPeerRows(env monogatari.Env) (monogatari.Built, error) {
	return buildPeerFixture(env, true)
}

func buildPeerFixture(env monogatari.Env, missing bool) (built monogatari.Built, err error) {
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
			`"item list") echo '{"id":"i1","label":"Source row","doc":"doc-7","pane":"pane-9"}' ;;
"doc send") printf '%s\n' "$*" ;;
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
