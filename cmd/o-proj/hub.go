package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// The hub is one directory of links to every project o-proj makes or
// adopts (origin o-proj in the store) (~/hub/o/NAME -> DIR), so the owner's hub repository indexes
// them. Links are absolute: the targets are the absolute trees in mtm's
// store, readlink then prints the same path the store holds, and a link
// keeps working wherever the hub directory itself is reached from.

// NoHub is the --hub value that skips the link.
const NoHub = "none"

// DefaultHub is $O_PROJ_HUB, else ~/hub/o.
func (t *Tool) DefaultHub() string {
	if v := lookup(t.Env, "O_PROJ_HUB"); v != "" {
		return v
	}
	return filepath.Join(t.Home, "hub", "o")
}

// expand resolves a leading ~/ against the tool's home.
func (t *Tool) expand(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok && t.Home != "" {
		return filepath.Join(t.Home, rest)
	}
	return p
}

// tilde abbreviates the home directory to ~ for messages.
func (t *Tool) tilde(p string) string {
	if t.Home != "" && within(p, t.Home) {
		if rel, err := filepath.Rel(t.Home, p); err == nil {
			if rel == "." {
				return "~"
			}
			return "~/" + rel
		}
	}
	return p
}

// Link outcomes; a link that already points at the directory counts as linked.
const (
	linked = "linked"
	kept   = "kept"
)

// link makes hub/name -> dir, creating hub when missing. Anything already
// at hub/name that is not a link to dir is kept as it is.
func (t *Tool) link(hub, name, dir string) (string, error) {
	hub, err := filepath.Abs(t.expand(hub))
	if err != nil {
		return "", err
	}
	path := filepath.Join(hub, name)
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			t.say("keep: %s exists (not a link)", t.tilde(path))
			return kept, nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if target == dir || samePath(target, dir) {
			t.say("link: %s -> %s (exists)", t.tilde(path), dir)
			return linked, nil
		}
		t.say("keep: %s exists -> %s", t.tilde(path), target)
		return kept, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(hub, 0o755); err != nil {
		return "", err
	}
	if err := os.Symlink(dir, path); err != nil {
		return "", err
	}
	t.say("link: %s -> %s", t.tilde(path), dir)
	return linked, nil
}

// LinkCounts is link's summary; Skipped counts the projects --all leaves
// out because o-proj did not make them.
type LinkCounts struct {
	Linked  int `json:"linked"`
	Kept    int `json:"kept"`
	Missing int `json:"missing"`
	Skipped int `json:"skipped"`
}

// projectTree is the tree a project's hub link points at: its default
// tree, else its first.
func projectTree(p work.Project) string {
	if p.DefaultTree != "" {
		return p.DefaultTree
	}
	if len(p.Trees) > 0 {
		return p.Trees[0].Path
	}
	return ""
}

// LinkProjects links one registered project (name), whatever made it, or
// every project o-proj made (all) into hub, one line per project, then the
// summary. A project whose tree is not on disk is reported as missing and
// not linked.
func (t *Tool) LinkProjects(data, hub, name string, all bool) (LinkCounts, error) {
	var n LinkCounts
	if all == (name != "") {
		return n, fmt.Errorf("usage: %s link NAME | --all [--hub DIR]", t.Prog)
	}
	if hub == NoHub {
		return n, errors.New("link needs a hub directory, not --hub none")
	}
	projects, err := t.projects(data)
	if err != nil {
		return n, err
	}
	found := false
	for _, p := range projects {
		if !all && p.Name != name {
			continue
		}
		found = true
		if all && p.Origin != Origin {
			n.Skipped++
			continue
		}
		dir := projectTree(p)
		if dir == "" {
			t.say("missing: %s has no tree", p.Name)
			n.Missing++
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			t.say("missing: %s tree %s does not exist", p.Name, dir)
			n.Missing++
			continue
		}
		outcome, err := t.link(hub, p.Name, dir)
		if err != nil {
			return n, err
		}
		if outcome == kept {
			n.Kept++
		} else {
			n.Linked++
		}
	}
	if !found && !all {
		return n, fmt.Errorf("no registered project %q; see %s list", name, t.Prog)
	}
	t.say("linked %d, kept %d, missing %d, skipped %d (not made by %s)", n.Linked, n.Kept, n.Missing, n.Skipped, Origin)
	return n, nil
}

// Link is the inline command: link [NAME] [--all] [--hub DIR].
func (t *Tool) Link(data string, args []string) error {
	name, hub, all, err := t.hubArgs("link", data, args)
	if err != nil {
		return err
	}
	_, err = t.LinkProjects(data, hub, name, all)
	return err
}

// hubArgs parses link's and unlink's arguments: [NAME] [--all] [--hub DIR]
// [-data PATH]; data is updated in place through the flag.
func (t *Tool) hubArgs(verb, data string, args []string) (name, hub string, all bool, err error) {
	flags := flag.NewFlagSet(t.Prog+" "+verb, flag.ContinueOnError)
	flags.SetOutput(t.Stderr)
	allFlag := flags.Bool("all", false, verb+" every project the verb covers")
	hubFlag := flags.String("hub", t.DefaultHub(), "the hub directory the links are in ($O_PROJ_HUB, else ~/hub/o)")
	flags.StringVar(&data, "data", data, "SQLite database path (mtm's)")
	words, err := parse(flags, args)
	if err != nil {
		return "", "", false, err
	}
	if len(words) > 1 {
		return "", "", false, fmt.Errorf("usage: %s %s NAME | --all [--hub DIR]", t.Prog, verb)
	}
	if len(words) == 1 {
		name = words[0]
	}
	return name, *hubFlag, *allFlag, nil
}

// UnlinkCounts is unlink's summary.
type UnlinkCounts struct {
	Unlinked int `json:"unlinked"`
	Kept     int `json:"kept"`
}

// UnlinkProjects removes the hub link of one registered project (name) or
// of every project o-proj did not make (all). Only a symlink to the
// project's own tree is removed; any other link or a file is kept and
// reported, and a name with nothing in the hub is passed over silently.
// Hub entries that name no registered project are never looked at.
func (t *Tool) UnlinkProjects(data, hub, name string, all bool) (UnlinkCounts, error) {
	var n UnlinkCounts
	if all == (name != "") {
		return n, fmt.Errorf("usage: %s unlink NAME | --all [--hub DIR]", t.Prog)
	}
	if hub == NoHub {
		return n, errors.New("unlink needs a hub directory, not --hub none")
	}
	hub, err := filepath.Abs(t.expand(hub))
	if err != nil {
		return n, err
	}
	projects, err := t.projects(data)
	if err != nil {
		return n, err
	}
	found := false
	for _, p := range projects {
		if all && p.Origin == Origin || !all && p.Name != name {
			continue
		}
		found = true
		path := filepath.Join(hub, p.Name)
		fi, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return n, err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.say("keep: %s exists (not a link)", t.tilde(path))
			n.Kept++
			continue
		}
		target, err := os.Readlink(path)
		if err != nil {
			return n, err
		}
		dir := projectTree(p)
		if dir == "" || target != dir && !samePath(target, dir) {
			t.say("keep: %s -> %s (not %s's tree)", t.tilde(path), target, p.Name)
			n.Kept++
			continue
		}
		if err := os.Remove(path); err != nil {
			return n, err
		}
		t.say("unlink: %s -> %s", t.tilde(path), target)
		n.Unlinked++
	}
	if !found && !all {
		return n, fmt.Errorf("no registered project %q; see %s list", name, t.Prog)
	}
	t.say("unlinked %d, kept %d", n.Unlinked, n.Kept)
	return n, nil
}

// Unlink is the inline command: unlink [NAME] [--all] [--hub DIR].
func (t *Tool) Unlink(data string, args []string) error {
	name, hub, all, err := t.hubArgs("unlink", data, args)
	if err != nil {
		return err
	}
	_, err = t.UnlinkProjects(data, hub, name, all)
	return err
}

// Adopt records a registered project as made by o-proj (for projects o-proj
// registered before it recorded provenance). It writes only the store row:
// no git, no files, no link.
func (t *Tool) Adopt(data string, args []string) error {
	flags := flag.NewFlagSet(t.Prog+" adopt", flag.ContinueOnError)
	flags.SetOutput(t.Stderr)
	flags.StringVar(&data, "data", data, "SQLite database path (mtm's)")
	words, err := parse(flags, args)
	if err != nil {
		return err
	}
	if len(words) != 1 {
		return fmt.Errorf("usage: %s adopt NAME", t.Prog)
	}
	db, err := store.Open(data)
	if err != nil {
		return err
	}
	defer db.Close()
	w, err := work.Open(db)
	if err != nil {
		return err
	}
	projects, err := w.Projects()
	if err != nil {
		return err
	}
	for _, p := range projects {
		if p.Name != words[0] {
			continue
		}
		if p.Origin == Origin {
			t.say("adopt: %s (%s) already has origin %s", p.Name, p.ID, Origin)
			return nil
		}
		p.Origin = Origin
		if err := w.PutProject(p); err != nil {
			return err
		}
		t.say("adopt: %s (%s) origin %s", p.Name, p.ID, Origin)
		return nil
	}
	return fmt.Errorf("no registered project %q; see %s list", words[0], t.Prog)
}
