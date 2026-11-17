package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"

	ocli "ofam.dev/ochazuke/cli"
)

// mtm's own pages declared as data (spine S9, run plan 5.6): pages/ID.json,
// one ochazuke cli.CatalogPage each, mounted over mtm's own nouns with no
// Go code of the page's: an index list draws its noun's store (a tree by
// the noun's declared parent when it says "tree"), a record-form detail
// the selected thing's props and its relations. pane-index is the first.
// A page is hidden like any other (page.hide); its key is a bindings row.
//
//go:embed pages/*.json
var pageFiles embed.FS

// mountDataPages mounts every page file in name order. A file that does
// not read or mount is reported on stderr and the rest mount (record
// first).
func (a *App) mountDataPages() {
	names, err := fs.Glob(pageFiles, "pages/*.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mtm: pages: "+err.Error())
		return
	}
	slices.Sort(names)
	for _, name := range names {
		if err := a.mountDataPage(name); err != nil {
			fmt.Fprintf(os.Stderr, "mtm: page %s: %v\n", path.Base(name), err)
		}
	}
}

func (a *App) mountDataPage(name string) error {
	data, err := pageFiles.ReadFile(name)
	if err != nil {
		return err
	}
	var p ocli.CatalogPage
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	_, err = ocli.MountPage(a.m, ocli.CatalogDoc{}, p, ocli.PageConfig{})
	return err
}
