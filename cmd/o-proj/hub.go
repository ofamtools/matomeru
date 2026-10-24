package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// The hub is one directory of links to every project o-proj makes or
// registers (~/hub/o/NAME -> DIR), so the owner's hub repository indexes
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

// LinkCounts is link's summary.
type LinkCounts struct {
	Linked  int `json:"linked"`
	Kept    int `json:"kept"`
	Missing int `json:"missing"`
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

// LinkProjects links one registered project (name) or all of them into
// hub, one line per project, then the summary. A project whose tree is not
// on disk is reported as missing and not linked.
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
	t.say("linked %d, kept %d, missing %d", n.Linked, n.Kept, n.Missing)
	return n, nil
}

// Link is the inline command: link [NAME] [--all] [--hub DIR].
func (t *Tool) Link(data string, args []string) error {
	flags := flag.NewFlagSet(t.Prog+" link", flag.ContinueOnError)
	flags.SetOutput(t.Stderr)
	all := flags.Bool("all", false, "link every registered project")
	hub := flags.String("hub", t.DefaultHub(), "the hub directory the links go in ($O_PROJ_HUB, else ~/hub/o)")
	flags.StringVar(&data, "data", data, "SQLite database path (mtm's)")
	words, err := parse(flags, args)
	if err != nil {
		return err
	}
	if len(words) > 1 {
		return fmt.Errorf("usage: %s link NAME | --all [--hub DIR]", t.Prog)
	}
	name := ""
	if len(words) == 1 {
		name = words[0]
	}
	_, err = t.LinkProjects(data, *hub, name, *all)
	return err
}
