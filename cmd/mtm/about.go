package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/unixsuperhero/ochazuke"
)

// aboutHelloTimeout bounds the daemon handshake the About page's Daemon
// section makes when it opens or refreshes.
const aboutHelloTimeout = 500 * time.Millisecond

// defineAbout adds mtm's sections to ochazuke's About page
// (ochazuke.about.open, about.show, about.copy): where mtm keeps its data
// and config, which daemon it talks to and its version, and the registered
// extensions. Each runs when the page opens or refreshes, never at startup.
func (a *App) defineAbout() {
	a.r.AddAboutSection("Matomeru", a.aboutPaths)
	a.r.AddAboutSection("Daemon", a.aboutDaemon)
	a.r.AddAboutSection("Extensions", a.aboutExtensions)
}

// aboutPaths are mtm's files: the data directory and database (--data),
// the config directory, settings.json, the harness file and the
// extension registry.
func (a *App) aboutPaths() []ochazuke.AboutRow {
	path := func(label, p, note string) ochazuke.AboutRow {
		return ochazuke.AboutRow{Label: label, Value: p, Kind: ochazuke.AboutPath, Note: note}
	}
	missing := func(p, note string) string {
		if _, err := os.Stat(p); err != nil {
			return note
		}
		return ""
	}
	rows := []ochazuke.AboutRow{
		path("data directory", a.dataDir(), ""),
		path("database", a.dataPath, ""),
		path("config directory", a.configDir, missing(a.configDir, "not created yet")),
		path("settings", a.r.Config().Path(ochazuke.SettingsFile), missing(a.r.Config().Path(ochazuke.SettingsFile), "none: every setting at its default")),
	}
	if h := a.opts.Harnesses; h != "" {
		rows = append(rows, path("harnesses", h, missing(h, "none: the built-in harnesses only")))
	}
	rows = append(rows, path("extensions", a.extRegistry().Dir, missing(a.extRegistry().Dir, "none registered")))
	return rows
}

// aboutDaemon is the daemon mtm talks to: its server, its socket and,
// when it answers the hello handshake, its version.
func (a *App) aboutDaemon() []ochazuke.AboutRow {
	if a.daemon == nil {
		return []ochazuke.AboutRow{{Label: "version", Value: "not connected", Note: "no daemon client"}}
	}
	server := a.daemon.Server
	switch {
	case server != "":
	case a.opts.Socket != "":
		server = "none (--socket)"
	default:
		server = DefaultDaemon
	}
	rows := []ochazuke.AboutRow{
		{Label: "server", Value: server},
		{Label: "socket", Value: a.daemon.Socket, Kind: ochazuke.AboutPath},
	}
	ctx, cancel := context.WithTimeout(context.Background(), aboutHelloTimeout)
	defer cancel()
	hello, err := a.daemon.Hello(ctx)
	if err != nil {
		return append(rows, ochazuke.AboutRow{Label: "version", Value: "not connected", Note: err.Error()})
	}
	version := hello.DaemonVersion
	if version == "" {
		version = "unknown"
	}
	return append(rows, ochazuke.AboutRow{Label: "version", Value: version,
		Note: fmt.Sprintf("protocol %d, pid %d, %d sessions", hello.Protocol, hello.PID, hello.Sessions)})
}

// aboutExtensions is a row per registered extension: its bin and the
// version its catalog declares.
func (a *App) aboutExtensions() []ochazuke.AboutRow {
	reg := a.extRegistry()
	names, err := reg.Names()
	if err != nil {
		return []ochazuke.AboutRow{{Label: "registry", Value: "unreadable", Note: err.Error()}}
	}
	if len(names) == 0 {
		return []ochazuke.AboutRow{{Label: "registered", Value: "none"}}
	}
	var rows []ochazuke.AboutRow
	for _, name := range names {
		e, err := reg.Read(name)
		if err != nil {
			rows = append(rows, ochazuke.AboutRow{Label: name, Value: "unreadable", Note: err.Error()})
			continue
		}
		note := "catalog " + e.Catalog.Version
		if e.Catalog.Version == "" {
			note = "catalog unversioned"
		}
		bin := e.Bin
		if resolved, err := filepath.EvalSymlinks(bin); err == nil && resolved != bin {
			note += ", links to " + resolved
		}
		rows = append(rows, ochazuke.AboutRow{Label: name, Value: bin, Kind: ochazuke.AboutPath, Note: note})
	}
	return rows
}
