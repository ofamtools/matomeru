package main

import (
	"fmt"

	"ofam.dev/ochazuke"
)

func (a *App) rekeyExtensionReferences(x *extension, noun, selector, key string) error {
	if selector == "" || key == "" || selector == key {
		return nil
	}
	all := a.all
	a.all = true // migration covers every owner's references, not just the pin
	defer func() { a.all = all }()
	if err := a.r.RekeyReferences(ochazuke.Ctx{Runtime: a.r}, x.naming.local(x.ns, noun), selector, key); err != nil {
		return err
	}
	return a.rekeyChanges(x.naming.local(x.ns, noun), selector, key)
}

// rekeyChanges moves the owner's changes of a thing to its durable key
// (a legacy selector became a row id): the row under key takes every
// prop it does not hold yet, and the row under old goes. A prop both hold
// with different values is a conflict: both rows are kept and it is
// reported.
func (a *App) rekeyChanges(noun, old, key string) error {
	if old == key {
		return nil
	}
	before := a.r.OverlayRow(noun, old)
	if before == nil {
		return nil
	}
	if after := a.r.OverlayRow(noun, key); after != nil {
		for name, v := range before.Props {
			if have, ok := after.Props[name]; ok && fmt.Sprint(have) != fmt.Sprint(v) {
				return fmt.Errorf("conflicting changes of %s for %s and %s (%s)", noun, old, key, name)
			}
		}
	}
	if _, err := a.r.ChangeOverlay(noun, key, func(row *ochazuke.OverlayRow) bool {
		if row.Props == nil {
			row.Props = map[string]any{}
		}
		for name, v := range before.Props {
			if _, ok := row.Props[name]; !ok {
				row.Props[name] = v
				if ch := before.Changes[name]; ch != nil {
					if row.Changes == nil {
						row.Changes = map[string]*ochazuke.PropChange{}
					}
					row.Changes[name] = ch
				}
			}
		}
		row.Created = row.Created || before.Created
		row.Archived = row.Archived || before.Archived
		return true
	}); err != nil {
		return err
	}
	_, err := a.r.ChangeOverlay(noun, old, func(*ochazuke.OverlayRow) bool { return false })
	return err
}
