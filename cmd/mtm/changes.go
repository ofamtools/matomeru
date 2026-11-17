package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// The owner's changes (run plan 5.4a, decision 54) are kept in mtm's
// database, the changes table (work.ChangesBackend): mtm gives ochazuke's
// overlay that backend under overlay.store's value mtm-db, its default
// here. A mounted tool's thing is kept under its extension and catalog
// noun (changeNames), so extensions.noun-names never orphans a change.
// Once, at the start, the rows of the ext_rows table and of overlay.json
// beside settings.json move into it (foldChanges: a backup first, the old
// table and file renamed); mtm --migrate-dry-run prints what the move
// would do. The projects of tool rows are mtm's part of the table, read by
// the scope code whatever overlay.store says. A tool run on its own keeps
// its own file.

// changeNames maps mtm's nouns to the changes table's identity of a thing:
// a mounted extension's noun is its extension and catalog noun (the
// mounted extensions, else the registry as it would mount now).
type changeNames struct{ a *App }

func (n changeNames) Mounted(ext, noun string) string { return n.a.extHostNoun(ext, noun) }

func (n changeNames) Identity(noun string) (string, string) {
	for _, x := range n.a.exts {
		if catalog := x.catalogNoun(noun); catalog != "" {
			return x.name, catalog
		}
	}
	return "", noun
}

// useChangesTable makes the changes table the overlay's backend
// (overlay.store's default mtm-db); a failure is a notice, and the owner's
// changes go where overlay.store's other values say.
func (a *App) useChangesTable() {
	if err := a.r.SetOverlayBackend(work.ChangesBackendName, a.work.ChangesBackend(changeNames{a})); err != nil {
		a.r.UI().Notify("your changes: the changes table: " + err.Error())
	}
}

// foldChanges moves ext_rows and overlay.json into the changes table once
// (work.FoldChanges), as the start makes the changes table the overlay's
// backend and before anything reads the overlay (the rows are read again
// after it all the same): ext_rows always (the projects of tool rows are mtm's, whatever
// overlay.store says), overlay.json while overlay.store is mtm-db (each of
// its nouns under its identity from the registry and the naming setting).
// What it did, and each value it did not take, is a notice; a failure is a
// notice too, and the old table and file stay as they were.
func (a *App) foldChanges() {
	path := ""
	if a.r.Settings().String(ochazuke.OverlayStoreSetting) == work.ChangesBackendName {
		path = a.r.Config().Path(ochazuke.OverlayFile)
	}
	_, entries, _ := a.extRegistry().OrderedSlim(false)
	fold, err := a.work.FoldChanges(registryNames(entries, a.r.Settings().String(settingExtNounNames)), path)
	if err != nil {
		a.r.UI().Notify("your changes: moving them into the changes table: " + err.Error())
		return
	}
	if len(fold.Moved) == 0 && len(fold.Renamed) == 0 {
		return
	}
	a.r.ReloadOverlay()
	text := fmt.Sprintf("your changes: moved %d into the changes table (backup %s)", len(fold.Moved), filepath.Base(fold.Backup))
	if len(fold.Notes) > 0 {
		text += "; " + strings.Join(fold.Notes, "; ")
	}
	a.r.UI().Notify(text)
}

// registryNames are the changes table's names as the registry entries
// mount the extensions under the naming value (extensions.noun-names).
func registryNames(entries map[string]extEntry, value string) work.NounNames {
	mounted := map[[2]string]string{}
	identity := map[string][2]string{}
	for name, e := range entries {
		ns, _ := extNamespace(name, e.Bin, e.Catalog)
		naming, _ := extNamingOf(value, ns)
		for _, n := range e.Catalog.Nouns {
			local := naming.local(ns, n.Name)
			mounted[[2]string{name, n.Name}] = local
			identity[local] = [2]string{name, n.Name}
		}
	}
	return tableNames{mounted, identity}
}

type tableNames struct {
	mounted  map[[2]string]string
	identity map[string][2]string
}

func (t tableNames) Mounted(ext, noun string) string {
	if local, ok := t.mounted[[2]string{ext, noun}]; ok {
		return local
	}
	return work.OwnNames{}.Mounted(ext, noun)
}

func (t tableNames) Identity(noun string) (string, string) {
	if id, ok := t.identity[noun]; ok {
		return id[0], id[1]
	}
	return "", noun
}

// changesDryRun prints what the move into the changes table would do for
// the database at path, with the registry beside it and the config in
// configDir as mtm would find them (its overlay.store and naming
// settings), writing nothing.
func changesDryRun(path, configDir string, stdout io.Writer) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	db, err := store.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	settings := map[string]any{}
	if data, err := os.ReadFile(filepath.Join(configDir, "settings.json")); err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	value, _ := settings[settingExtNounNames].(string)
	overlay := ""
	if where, _ := settings[ochazuke.OverlayStoreSetting].(string); configDir != "" && (where == "" || where == work.ChangesBackendName) {
		overlay = filepath.Join(configDir, ochazuke.OverlayFile)
	}
	_, entries, _ := extRegistry{Dir: filepath.Join(filepath.Dir(path), extensionsDir)}.Ordered()
	fold, err := work.PlanChangesFold(db, registryNames(entries, value), overlay)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, fold.String())
	return err
}
