package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

func aboutSection(t *testing.T, secs []ochazuke.AboutSection, title string) map[string]ochazuke.AboutRow {
	t.Helper()
	for _, s := range secs {
		if s.Title == title {
			out := map[string]ochazuke.AboutRow{}
			for _, row := range s.Rows {
				out[row.Label] = row
			}
			return out
		}
	}
	t.Fatalf("no About section %q", title)
	return nil
}

// The About page's mtm sections: the --data in use, the config directory,
// the daemon's version from its hello (a fake daemon), and each registered
// extension's bin and catalog version.
func TestAboutSectionsFromAFakeDaemon(t *testing.T) {
	t.Parallel()
	sock := serveDaemon(t, func(verb string) (any, bool) {
		if verb == "hello" {
			return map[string]any{"type": "hello", "daemon_version": "v9.9.9", "protocol": 3, "pid": 77, "sessions": 2}, true
		}
		return nil, false
	})
	data := seeded(t)
	app, err := New(Options{Data: data, Socket: sock, lookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	reg := filepath.Join(filepath.Dir(data), "extensions")
	if err := os.MkdirAll(reg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reg, "notes.json"), []byte(`{"bin": "/opt/bin/mtm-notes", "catalog": {"schema": 1, "name": "notes", "version": "v1.4.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := app.r.Execute(ochazuke.Request{Noun: "ochazuke.about", Verb: "show"})
	if err != nil {
		t.Fatal(err)
	}
	secs := got.([]ochazuke.AboutSection)
	paths := aboutSection(t, secs, "Matomeru")
	if paths["database"].Value != data || paths["database"].Kind != ochazuke.AboutPath || paths["data directory"].Value != filepath.Dir(data) {
		t.Fatalf("paths %#v", paths)
	}
	if paths["config directory"].Value != filepath.Dir(data) {
		t.Fatalf("config dir %#v (a non-default --data keeps its config beside it)", paths["config directory"])
	}
	if paths["harnesses"].Value != filepath.Join(filepath.Dir(data), "harnesses.json") || !strings.Contains(paths["harnesses"].Note, "built-in") {
		t.Fatalf("harnesses %#v", paths["harnesses"])
	}
	daemon := aboutSection(t, secs, "Daemon")
	if daemon["socket"].Value != sock || daemon["version"].Value != "v9.9.9" || daemon["version"].Note != "protocol 3, pid 77, 2 sessions" {
		t.Fatalf("daemon %#v", daemon)
	}
	exts := aboutSection(t, secs, "Extensions")
	if exts["notes"].Value != "/opt/bin/mtm-notes" || exts["notes"].Note != "catalog v1.4.0" || exts["notes"].Kind != ochazuke.AboutPath {
		t.Fatalf("extensions %#v", exts)
	}
	text, err := app.r.Execute(ochazuke.Request{Noun: "ochazuke.about", Verb: "copy"})
	if err != nil || !strings.Contains(text.(string), "Daemon\n") || !strings.Contains(text.(string), data) {
		t.Fatalf("copy %v %v", text, err)
	}
}

// A daemon that does not answer reads "not connected"; with nothing
// registered the Extensions section says none.
func TestAboutDaemonNotConnected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	app, err := New(Options{Data: filepath.Join(dir, "mtm.db"), Socket: filepath.Join(dir, "dead.sock"), lookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	secs := app.r.About()
	if v := aboutSection(t, secs, "Daemon")["version"]; v.Value != "not connected" || v.Note == "" {
		t.Fatalf("daemon version %#v", v)
	}
	if v := aboutSection(t, secs, "Extensions")["registered"]; v.Value != "none" {
		t.Fatalf("extensions %#v", v)
	}
}
