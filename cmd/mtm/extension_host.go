package main

import (
	"encoding/json"
	"io"
	"os"
	"slices"
	"strings"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

const (
	settingExtHostCall   = "extensions.host-call"
	settingExtHostRights = "extensions.host-call-rights"
	settingExtHostDepth  = "extensions.host-call-depth"
)

var extHostSettings = []ochazuke.Setting{
	{Name: settingExtHostCall, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection, Type: ochazuke.SettingBoolean, Default: true, Description: "Give extensions a host command for peer discovery and calls"},
	{Name: settingExtHostRights, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection, Type: ochazuke.SettingString, Default: "agent", Enum: []string{"agent", "caller"}, Protected: true, Description: "Host callbacks run as an extension agent; caller explicitly inherits the launching actor's rights"},
	{Name: settingExtHostDepth, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection, Type: ochazuke.SettingInteger, Default: 4, Description: "Maximum nested extension host callback depth"},
}

// hostCommand is the exact argv also advertised by Shell.MCPCommand, including
// the data/server flags. JSON preserves spaces without a shell interpretation.
func (a *App) hostCommand() []string {
	cmd := a.mcpServerCommand(io.Discard)
	return append([]string{cmd.Executable}, cmd.Args[:len(cmd.Args)-2]...)
}

// extensionExecEnv always removes inherited host access first. Catalog and
// discovery are pure reads, and host-call=off must revoke inherited access too.
// Depth/chain are deliberately untouched so nested calls cannot reset the limit.
func extensionExecEnv(overrides []string, catalog bool) []string {
	withoutHost := func(s string) bool { return strings.HasPrefix(s, "OCHAZUKE_HOST=") }
	env := slices.DeleteFunc(os.Environ(), withoutHost)
	if catalog {
		overrides = slices.DeleteFunc(slices.Clone(overrides), withoutHost)
	}
	return append(env, overrides...)
}

func (a *App) extensionPeers(c ochazuke.Ctx) ochazuke.Result {
	peers := []ocli.HostPeer{}
	name := ochazuke.StringArg(c.Args, "name", "")
	for _, x := range a.exts {
		if name != "" && name != x.name {
			continue
		}
		peer := ocli.HostPeer{Name: x.name, Namespace: extensionNamespace(x), Version: x.doc.Version, Nouns: []ocli.HostPeerNoun{}}
		for _, n := range extPlan(x.doc) {
			local, mounted := x.nouns[n.Name]
			if !mounted {
				continue
			}
			noun := ocli.HostPeerNoun{Noun: local, Local: n.Name, About: n.About, Verbs: []ocli.HostPeerVerb{}}
			for _, v := range n.Verbs {
				op, ok := a.r.Operation(local + "." + v.Name)
				if !ok {
					continue
				}
				noun.Verbs = append(noun.Verbs, ocli.HostPeerVerb{Verb: v.Name, Description: op.Description, Params: op.Parameters, Agent: a.r.AgentRight(op, false).Right})
			}
			peer.Nouns = append(peer.Nouns, noun)
		}
		peers = append(peers, peer)
	}
	return ochazuke.Done(peers)
}

func (a *App) hostEnv() []string {
	actor, _ := json.Marshal(a.r.Actor())
	env := []string{"OCHAZUKE_ACTOR=" + string(actor)}
	if a.r.Settings().Bool(settingExtHostCall) {
		argv, _ := json.Marshal(a.hostCommand())
		env = append(env, "OCHAZUKE_HOST="+string(argv))
	}
	return env
}
