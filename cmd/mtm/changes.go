package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// The owner's changes (run plan 5.4a, decision 54) are kept in mtm's
// database, the changes table (work.ChangesBackend): mtm gives ochazuke's
// overlay that backend under overlay.store's value mtm-db, its default
// here. Once, before the extensions mount, the rows of the ext_rows table
// and of overlay.json beside settings.json move into it (foldChanges: a
// backup first, the old table and file renamed); mtm --migrate-dry-run
// prints what the move would do. A tool run on its own keeps its own file.

// useChangesTable makes the changes table the overlay's backend
// (overlay.store's default mtm-db); a failure is a notice, and the owner's
// changes go where overlay.store's other values say.
func (a *App) useChangesTable() {
	if err := a.r.SetOverlayBackend(work.ChangesBackendName, a.work.ChangesBackend()); err != nil {
		a.r.UI().Notify("your changes: the changes table: " + err.Error())
	}
}

// foldChanges moves ext_rows and overlay.json into the changes table once
// (work.FoldChanges), each extension's rows under the noun it mounts as
// (entries: the registry as it mounts now), while the changes table is
// the overlay's backend; with another overlay.store value the move waits.
// What it did is a notice; a failure is a notice too, and the old table
// and file stay as they were.
func (a *App) foldChanges(entries map[string]extEntry) {
	if a.r.Settings().String(ochazuke.OverlayStoreSetting) != work.ChangesBackendName {
		return
	}
	value := a.r.Settings().String(settingExtNounNames)
	fold, err := a.work.FoldChanges(extNounOf(entries, value), a.r.Config().Path(ochazuke.OverlayFile))
	if err != nil {
		a.r.UI().Notify("your changes: moving them into the changes table: " + err.Error())
		return
	}
	if len(fold.Moved) == 0 && len(fold.Renamed) == 0 {
		return
	}
	a.r.ReloadOverlay()
	a.r.UI().Notify(fmt.Sprintf("your changes: moved %d into the changes table (backup %s)", len(fold.Moved), filepath.Base(fold.Backup)))
}

// extNounOf is mtm's noun of an extension's catalog noun as the registry
// entries mount it under the naming value (extensions.noun-names): an
// extension that is not registered names its noun as its namespace would
// (the extension's name).
func extNounOf(entries map[string]extEntry, value string) work.ExtNounOf {
	return func(ext, noun string) string {
		ns := ext
		if e, ok := entries[ext]; ok {
			ns, _ = extNamespace(ext, e.Bin, e.Catalog)
		}
		naming, _ := extNamingOf(value, ns)
		return naming.local(ns, noun)
	}
}

// changesDryRun prints what the move into the changes table would do for
// the database at path, with the registry and overlay.json beside it as
// mtm would find them (the naming setting's default), writing nothing.
func changesDryRun(path, configDir string, stdout io.Writer) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	db, err := store.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, entries, _ := extRegistry{Dir: filepath.Join(filepath.Dir(path), extensionsDir)}.Ordered()
	overlay := ""
	if configDir != "" {
		overlay = filepath.Join(configDir, ochazuke.OverlayFile)
	}
	fold, err := work.PlanChangesFold(db, extNounOf(entries, extNounNamesNamespaced), overlay)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, fold.String())
	return err
}
