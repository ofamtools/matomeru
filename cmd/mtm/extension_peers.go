package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

const (
	settingExtPeerKeys   = "extensions.peer-keys"
	settingExtPeerStatus = "extensions.peer-status"
)

var extPeerSettings = []ochazuke.Setting{
	{Name: settingExtPeerKeys, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection, Type: ochazuke.SettingString, Default: "any", Enum: []string{"any", "own-rows", "off"}, Description: "Catalog peer keys may bind any noun, only their own rows, or none"},
	{Name: settingExtPeerStatus, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection, Type: ochazuke.SettingBoolean, Default: true, Description: "Show a peer verb's result on the current extension page too"},
}

// extBindingPolicy is shared by ordinary and soft catalog rows. Peer calls
// happen inside ReloadPeerBindings, below the previous peer and owner layers.
func (a *App) extBindingPolicy(row ochazuke.Binding, home map[string]string, crud map[string][]string) string {
	if row.Instance != "" {
		return "instance-only row"
	}
	if row.Keys != "" && slices.Contains(crud[row.Noun], strings.Fields(row.Keys)[0]) {
		return "mtm CRUD key takes precedence"
	}
	list := home[row.Noun]
	if list == "" {
		list = a.owners[row.Noun]
	}
	if list != "" && a.keyTaken(list, row) {
		return "mtm binds the key on the list path"
	}
	return ""
}

func extensionNamespace(x *extension) string {
	ns, err := ocli.ExtensionNamespace(x.bin)
	if err != nil {
		return x.name
	}
	return ns
}

// resolvePeerNoun maps the stable binary namespace through the complete mount
// registry; the mount's naming policy handles renamed and legacy registrations.
func (a *App) resolvePeerNoun(noun string) (string, string) {
	ns, tail, dotted := strings.Cut(noun, ".")
	if !dotted {
		return noun, ""
	}
	var found *extension
	for _, x := range a.exts {
		if extensionNamespace(x) != ns {
			continue
		}
		if found != nil {
			return noun, "extension namespace " + ns + " is registered more than once"
		}
		found = x
	}
	if found == nil {
		return noun, ""
	} // library diagnoses an absent peer
	if local, ok := found.nouns[tail]; ok {
		return local, ""
	}
	return noun, "no noun " + noun
}

func (a *App) reloadPeerBindings() {
	var rows []ochazuke.PeerBinding
	var peers []ochazuke.BindingPeer
	home := map[string]string{}
	crud := map[string][]string{}
	for _, x := range a.exts {
		peers = append(peers, ochazuke.BindingPeer{Name: x.name, Namespace: extensionNamespace(x), Version: x.doc.Version, Nouns: slices.Sorted(maps.Values(x.nouns))})
		maps.Copy(home, x.bindingLists)
		maps.Copy(crud, x.bindingCRUD)
	}
	for _, x := range a.exts {
		for _, row := range x.doc.PeerBindings {
			decl := ochazuke.PeerBinding{Extension: x.name, Row: row}
			var why string
			decl.Row.Noun, decl.Problem = a.resolvePeerNoun(row.Noun)
			decl.Row.Request.Noun, why = a.resolvePeerNoun(row.Request.Noun)
			if decl.Problem == "" {
				decl.Problem = why
			}
			decl.Row.When = "" // remote guards are not host guards
			rows = append(rows, decl)
		}
	}
	a.r.ReloadPeerBindings(rows, peers, func(p ochazuke.PeerBinding) string {
		switch a.r.Settings().String(settingExtPeerKeys) {
		case "off":
			return "extensions.peer-keys is off"
		case "own-rows":
			x := a.extension(p.Extension)
			if x == nil || !slices.Contains(slices.Collect(maps.Values(x.nouns)), p.Row.Noun) {
				return "extensions.peer-keys allows only the declaring extension's rows"
			}
		}
		return a.extBindingPolicy(p.Row, home, crud)
	})
}

func (a *App) peerRows(ochazuke.Ctx) ochazuke.Result { return ochazuke.Done(a.r.PeerBindings()) }

func (a *App) peerRowFacts(name string) string {
	if a.r.Settings().String(ochazuke.PeerNoticesSetting) == "off" {
		return ""
	}
	var b strings.Builder
	for _, p := range a.r.PeerBindings() {
		if p.Extension == name && !p.Loaded {
			fmt.Fprintf(&b, "\n  %s on %s: %s", p.Row.Keys+p.Row.Pattern, p.Row.Noun, p.Reason)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n\ndropped peer rows" + b.String()
}
