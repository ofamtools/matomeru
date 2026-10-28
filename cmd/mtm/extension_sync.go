package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// A refreshed or re-registered extension takes effect in the running TUI
// without a restart. The registry files are the truth: a sync compares
// them, in tab order, with what is mounted, and from the first extension
// that differs (a changed entry, a new or removed file) unmounts the rest
// and mounts them again from their files, so the tabs keep registration
// order (a page added later is always the last tab). Unmounting removes
// the pages, mtm's nouns of the extension and their verbs and rows; the
// page that was open is opened again when it is still there, its rows
// reloaded, the focus restored when the instance survived.
//
// A sync runs after register or refresh from this mtm, and whenever a page
// is opened (a refresh or register from the CLI, another process, shows
// then); it waits while a modal is open.

// entryJSON is a registry entry as compared between syncs.
// The bin's stamp is not compared: it changes no mounted thing.
func entryJSON(e extEntry) []byte {
	e.BinStamp = nil
	data, _ := json.Marshal(e)
	return data
}

// notifyExtErrs shows each registry error once.
func (a *App) notifyExtErrs(errs []error) {
	if a.extErrsSeen == nil {
		a.extErrsSeen = map[string]bool{}
	}
	for _, err := range errs {
		if msg := "extension: " + err.Error(); !a.extErrsSeen[msg] {
			a.extErrsSeen[msg] = true
			a.r.UI().Notify(msg)
		}
	}
}

// unmountExtension removes an extension's pages, nouns, verbs and rows; it
// stays in a.exts (the caller drops it). When its page is open, the page
// before its first tab (else Work) is opened first.
func (a *App) unmountExtension(x *extension) error {
	var errs []error
	if cur, _ := a.extViewOf(a.r.Page); cur == x && len(x.views) > 0 {
		fallback := "work"
		for _, p := range a.r.PageList() {
			if p.ID == x.views[0].Page() {
				break
			}
			fallback = p.ID
		}
		errs = append(errs, a.r.OpenPage(fallback))
	}
	for _, v := range x.views {
		if a.r.Instances[v.Page()] != nil {
			errs = append(errs, a.r.RemoveInstance(v.Page()))
		}
	}
	x.views = nil
	for _, local := range x.nouns {
		delete(a.r.Nouns, local)
		for key := range a.r.Operations {
			if strings.HasPrefix(key, local+".") {
				delete(a.r.Operations, key)
			}
		}
		delete(a.owners, local)
	}
	return errors.Join(errs...)
}

// syncExtensions mounts the registry as it is now (see above).
func (a *App) syncExtensions() {
	names, entries, errs := a.extRegistry().Ordered()
	a.notifyExtErrs(errs)
	i := 0
	for i < len(a.exts) && i < len(names) && a.exts[i].name == names[i] && bytes.Equal(a.exts[i].entry, entryJSON(entries[names[i]])) {
		i++
	}
	if i == len(a.exts) && i == len(names) {
		return
	}
	page, focus := a.r.Page, a.r.Focus
	var uerrs []error
	for _, x := range a.exts[i:] {
		uerrs = append(uerrs, a.unmountExtension(x))
	}
	a.exts = a.exts[:i]
	for _, name := range names[i:] {
		a.mountExtension(name, entries[name])
	}
	if a.r.Page != page && a.r.PageOf(page) != nil {
		if err := a.r.OpenPage(page); err != nil {
			uerrs = append(uerrs, err)
		} else if a.r.Instances[focus] != nil {
			uerrs = append(uerrs, a.r.SetFocus(focus))
		}
		if x, _ := a.extViewOf(page); x != nil {
			a.reloadExtension(x)
		}
		a.lastPage = a.r.Page
	}
	if err := errors.Join(uerrs...); err != nil {
		a.r.UI().Error(err.Error())
	}
}
