package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
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

// Identity is the first extension, in name order, that mounts noun (two
// may under some naming value; registryNames takes the same one).
func (n changeNames) Identity(noun string) (string, string) {
	ext, catalog := "", ""
	for _, x := range n.a.exts {
		if c := x.catalogNoun(noun); c != "" && (ext == "" || x.name < ext) {
			ext, catalog = x.name, c
		}
	}
	if ext == "" {
		return "", noun
	}
	return ext, catalog
}

// rekeyOwnNouns moves the changes rows a fold kept under a noun of mtm's
// own that an extension now mounts (work.RekeyOwnNouns), once the
// extensions have mounted; what it moved is a notice.
func (a *App) rekeyOwnNouns() {
	moved, notes, err := a.work.RekeyOwnNouns(changeNames{a})
	switch {
	case err != nil:
		a.r.UI().Notify("your changes: " + err.Error())
	case moved > 0:
		text := fmt.Sprintf("your changes: %d moved to the extension that now holds their noun", moved)
		if len(notes) > 0 {
			text += "; " + strings.Join(notes, "; ")
		}
		a.r.UI().Notify(text)
	}
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
	if !a.work.FoldPending(path) {
		return // the common start: nothing to move, no registry read
	}
	_, entries, _ := a.extRegistry().OrderedSlim(false)
	names := registryNames(entries, a.r.Settings().String(settingExtNounNames))
	fold, err := a.work.FoldChanges(names, path)
	fold.Notes = append(names.clashes, fold.Notes...)
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
// Entries are read in name order and the first to mount a name keeps it
// (as changeNames does); a name two entries mount is a clash, named in
// the fold's notice.
func registryNames(entries map[string]extEntry, value string) tableNames {
	t := tableNames{mounted: map[[2]string]string{}, identity: map[string][2]string{}}
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		e := entries[name]
		ns, _ := extNamespace(name, e.Bin, e.Catalog)
		naming, _ := extNamingOf(value, ns)
		for _, n := range e.Catalog.Nouns {
			local := naming.local(ns, n.Name)
			t.mounted[[2]string{name, n.Name}] = local
			if held, ok := t.identity[local]; ok {
				t.clashes = append(t.clashes, fmt.Sprintf("%s is mounted by %s and %s: kept as %s's", local, held[0], name, held[0]))
				continue
			}
			t.identity[local] = [2]string{name, n.Name}
		}
	}
	return t
}

type tableNames struct {
	mounted  map[[2]string]string
	identity map[string][2]string
	clashes  []string
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
	names := registryNames(entries, value)
	fold, err := work.PlanChangesFold(db, names, overlay)
	if err != nil {
		return err
	}
	fold.Notes = append(names.clashes, fold.Notes...)
	_, err = io.WriteString(stdout, fold.String())
	return err
}
