package main

import "ofam.dev/ochazuke"

// scopeDocLists places a scoped collection beneath its owner record. The
// framework's FromFocus fills then see the same owner on an empty list as on
// a project row, without a noun-specific create handler or inferred CLI args.
func (a *App) scopeDocLists() {
	for _, inst := range a.r.Instances {
		list, ok := inst.Component.(*ochazuke.Collection)
		if !ok || list.From() == "" {
			continue
		}
		noun := a.r.Noun(list.From())
		for _, prop := range noun.Props {
			if prop.Role != ochazuke.RoleOwner || !prop.FromFocus || prop.Noun == "" {
				continue
			}
			id := inst.ID + "/owner-scope"
			scope := a.r.Instances[id]
			if a.pin == "" || a.all {
				if scope != nil && inst.Parent == id {
					inst.Parent = scope.Parent
				}
				continue
			}
			if scope == nil {
				scope = a.r.Instance(id, ochazuke.Kind(prop.Noun), ochazuke.Parent(inst.Parent))
				scope.Unfocusable = true
			}
			for _, key := range a.r.Noun(prop.Noun).Props {
				if key.Key {
					scope.Data = ochazuke.Thing{key.Name: a.pin}
					break
				}
			}
			scope.Label = a.ownerLabel(a.pin)
			inst.Parent = id
		}
	}
}
