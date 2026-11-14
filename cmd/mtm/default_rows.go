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

// loadDefaultRows installs defaultRows; it runs once every noun they name
// is defined, before the user's bindings.json applies.
func (a *App) loadDefaultRows() error {
	return a.r.LoadDefaultBindings(defaultRowsSource, defaultRows)
}
