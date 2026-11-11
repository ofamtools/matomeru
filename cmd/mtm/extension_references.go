package main

import (
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
	if err := a.work.RekeyExtRowOwner(x.name, noun, selector, key); err != nil {
		return err
	}
	a.reloadExtRowOwners()
	return nil
}
