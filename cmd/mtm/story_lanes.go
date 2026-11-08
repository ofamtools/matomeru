//go:build monogatari

package main

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"

	"ofam.dev/monogatari"
	ocli "ofam.dev/ochazuke/cli"
)

// buildScopedLanes mounts a real extension page backed by a temp executable.
// Its rows model the project metadata o-lane exposes, including inheritance.
func buildScopedLanes(env monogatari.Env) (built monogatari.Built, err error) {
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
	catalog := `{"schema":1,"name":"o-lane","nouns":[{"name":"lane","verbs":[{"name":"list","kind":"application","rows":"lane"}]}],"pages":[{"id":"lanes","title":"Lanes","lists":[{"id":"lanes/list","noun":"lane","source":"lane.list"}]}]}`
	var doc ocli.CatalogDoc
	if err = json.Unmarshal([]byte(catalog), &doc); err != nil {
		return built, err
	}
	bin := filepath.Join(a.dataDir(), "o-lane")
	script := `#!/bin/sh
case "$1 $2" in
"catalog ") cat <<'CATALOG'
` + catalog + `
CATALOG
;;
"lane list") cat <<'ROWS'
{"id":"parent","label":"Pinned lane","effective_project":"acme-web","project":"acme-web"}
{"id":"child","label":"Inherited lane","parent":"parent","effective_project":"acme-web"}
{"id":"other","label":"Other lane","effective_project":"billing-api"}
{"id":"unknown","label":"Unknown lane","effective_project":"missing"}
{"id":"unassociated","label":"Unassociated lane","effective_project":""}
ROWS
;;
*) exit 2 ;;
esac
`
	if err = os.WriteFile(bin, []byte(script), 0o755); err != nil {
		return built, err
	}
	entry := extEntry{Bin: bin, Catalog: doc}
	if err = a.extRegistry().write("lane", entry); err != nil {
		return built, err
	}
	a.mountExtension("lane", entry)
	return built, nil
}

// Captured from o-lane v0.2.0 (ochazuke v0.105.0, built from main) with `catalog`; only whitespace is compacted.
// Keeping its real page source and host-only row catches contract drift.
//
//go:embed testdata/o-lane-catalog.json
var laneDocCatalog []byte

func buildLaneDocNotes(env monogatari.Env) (built monogatari.Built, err error) {
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
	mdCatalog := `{"schema":1,"name":"o-md","nouns":[{"name":"doc","verbs":[{"name":"send-notes","kind":"application","params":{"pane":{"type":"string"}}}]}]}`
	for _, fixture := range []struct{ name, catalog, body string }{
		{"lane", string(laneDocCatalog), `"lane page-rows") echo '{"noun":"lane","id":"lane-1","label":"Implementation","cells":["Implementation","running"],"data":{"doc":"/docs/review.md","pane":"w1"}}' ;;`},
		{"md", mdCatalog, `"doc send-notes") printf '%s\n' "$*" ;;`},
	} {
		var doc ocli.CatalogDoc
		if err = json.Unmarshal([]byte(fixture.catalog), &doc); err != nil {
			return built, err
		}
		bin := filepath.Join(a.dataDir(), "o-"+fixture.name)
		script := "#!/bin/sh\ncase \"$1 $2\" in\n\"catalog \") cat <<'CATALOG'\n" + fixture.catalog + "\nCATALOG\n;;\n" + fixture.body + "\n*) echo '[]' ;;\nesac\n"
		if err = os.WriteFile(bin, []byte(script), 0o755); err != nil {
			return built, err
		}
		if err = a.extRegistry().write(fixture.name, extEntry{Bin: bin, Catalog: doc}); err != nil {
			return built, err
		}
	}
	a.syncExtensions()
	return built, nil
}
