package main

import _ "embed"

// defaultRows are mtm's default binding rows kept as data
// (bindings/rows.json): today the key redirects between a pane row and the
// rows drawn under it.
//
//go:embed bindings/rows.json
var defaultRows []byte

// defaultRowsSource names the file in binding defaults.
const defaultRowsSource = "mtm/bindings/rows.json"

// defineDefaultRows loads the embedded rows once every verb of mtm's own is
// registered (New calls it after the pages and declarations). The file is
// compiled in, so a bad row is a build defect: it panics, as
// MustLoadDefaultBindings does, and a test that builds the app catches it.
func (a *App) defineDefaultRows() { a.r.MustLoadDefaultBindings(defaultRowsSource, defaultRows) }
