// Package setup installs and removes mtm's own turn-capture hooks per
// harness, beside the TUIOS integration (which reports agent state and
// activity) and never in place of it (matomeru-old's internal/setup,
// extended from OMP to Claude Code and Codex):
//
//   - omp-activity: the OMP extension matomeru-omp.ts in OMP's extensions
//     directory, an mtm-owned file (its first line is the owner mark);
//   - claude-code-turns: a UserPromptSubmit and a Stop hook in Claude
//     Code's settings.json;
//   - codex-turns: the same two hooks in Codex's hooks.json.
//
// Each hook runs mtm history capture-hook HARNESS (the extension mtm
// history capture-turn) with the whole prompt and the final answer. Only
// mtm's own entries are written or removed: every other key, hook and file
// is kept as it was, and a file mtm does not own is never replaced. The
// paths resolve under Options.Home when it is set (tests and dry runs),
// else under the user's home and the harnesses' own environment
// variables.
package setup

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed omp.ts
var extension []byte

// ompOwner is the extension's first line, the mark of an owned file
// (matomeru-old's, so its installed file counts as mtm's).
const ompOwner = "// MATOMERU_SETUP_ID=omp-activity\n"

// hookMark is in every hook command mtm owns.
const hookMark = " history capture-hook "

// Item ids.
const (
	OMP    = "omp-activity"
	Claude = "claude-code-turns"
	Codex  = "codex-turns"
)

// Definition is one installable item.
type Definition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Harness     string `json:"harness"`
	Description string `json:"description"`
}

// Items are the installable items, in listing order.
func Items() []Definition {
	return []Definition{
		{ID: OMP, Name: "OMP turn capture", Harness: "omp", Description: "An OMP extension that records each request and final answer in mtm's history. Reload OMP after installing."},
		{ID: Claude, Name: "Claude Code turn capture", Harness: "claude-code", Description: "UserPromptSubmit and Stop hooks in Claude Code's settings.json that record each prompt and final answer in mtm's history."},
		{ID: Codex, Name: "Codex turn capture", Harness: "codex", Description: "UserPromptSubmit and Stop hooks in Codex's hooks.json that record each prompt and final answer in mtm's history."},
	}
}

// Options say where the harnesses' files are and what the hooks run.
type Options struct {
	Home string // stands for the home directory; "" is the user's own, with the harnesses' environment variables
	Bin  string // the mtm binary the hooks run (absolute)
	Data string // the database the hooks write (absolute)
}

// Item is one item's state: missing, installed, outdated (owned but not
// what this mtm would write) or conflict (a file mtm does not own).
type Item struct {
	Definition
	Path   string `json:"path"`
	Status string `json:"status"`
}

// Plan is what installing an item writes: the file and its whole content.
type Plan struct {
	Item
	Content string `json:"content"`
}

// hookEvents are the hook events the capture runs on.
var hookEvents = []string{"UserPromptSubmit", "Stop"}

func definition(id string) (Definition, error) {
	for _, d := range Items() {
		if d.ID == id {
			return d, nil
		}
	}
	return Definition{}, fmt.Errorf("unknown setup item %q (one of %s)", id, strings.Join(IDs(), ", "))
}

// IDs are the item ids.
func IDs() []string {
	var out []string
	for _, d := range Items() {
		out = append(out, d.ID)
	}
	return out
}

// path is the file an item writes.
func (o Options) path(id string) (string, error) {
	home := o.Home
	env := func(k string) string {
		if o.Home != "" {
			return ""
		}
		return os.Getenv(k)
	}
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", err
		}
	}
	switch id {
	case OMP:
		agent := env("PI_CODING_AGENT_DIR")
		if agent == "" {
			config := env("PI_CONFIG_DIR")
			if config == "" {
				config = ".omp"
			}
			agent = filepath.Join(home, config, "agent")
		}
		return filepath.Join(agent, "extensions", "matomeru-omp.ts"), nil
	case Claude:
		dir := env("CLAUDE_CONFIG_DIR")
		if dir == "" {
			dir = filepath.Join(home, ".claude")
		}
		return filepath.Join(dir, "settings.json"), nil
	case Codex:
		dir := env("CODEX_HOME")
		if dir == "" {
			dir = filepath.Join(home, ".codex")
		}
		return filepath.Join(dir, "hooks.json"), nil
	}
	return "", fmt.Errorf("unknown setup item %q", id)
}

// quote is a POSIX shell single-quoted word.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// HookCommand is the command a hook of harness runs: only inside a TUIOS
// pane, against that pane's daemon (so it never starts one), its output
// thrown away (a hook's stdout would reach the agent) and always exiting
// 0, so a capture never blocks or disturbs the agent.
func (o Options) HookCommand(harness string) string {
	return `[ -n "$TUIOS_PANE_ID" ] && [ -n "$TUIOS_SOCKET" ] && ` + quote(o.Bin) + " --data " + quote(o.Data) +
		` --socket "$TUIOS_SOCKET"` + hookMark + harness + " >/dev/null 2>&1; exit 0"
}

// extension is the OMP extension with this mtm's binary and database.
func (o Options) extension() []byte {
	config, _ := json.Marshal(struct {
		Bin  string `json:"bin"`
		Data string `json:"data"`
	}{o.Bin, o.Data})
	return bytes.Replace(extension, []byte(`const capture = { bin: "mtm", data: "" };`), append(append([]byte("const capture = "), config...), ';'), 1)
}

// Extension is the embedded OMP extension, for inspection.
func Extension() string { return string(extension) }

// List is every item's state.
func (o Options) List() ([]Item, error) {
	var out []Item
	for _, d := range Items() {
		it, err := o.Status(d.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, nil
}

// Status reads an item's state without writing anything.
func (o Options) Status(id string) (Item, error) {
	p, err := o.Plan(id)
	return p.Item, err
}

// Plan is what Install would write for the item, and its state now.
func (o Options) Plan(id string) (Plan, error) {
	def, err := definition(id)
	if err != nil {
		return Plan{}, err
	}
	path, err := o.path(id)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Item: Item{Definition: def, Path: path, Status: "missing"}}
	have, exists, err := read(path)
	if err != nil {
		return p, err
	}
	if id == OMP {
		want := o.extension()
		p.Content = string(want)
		switch {
		case !exists:
		case bytes.Equal(have, want):
			p.Status = "installed"
		case bytes.HasPrefix(have, []byte(ompOwner)):
			p.Status = "outdated"
		default:
			p.Status = "conflict"
		}
		return p, nil
	}
	want, err := editHooks(have, o, def.Harness, true)
	if err != nil {
		p.Status = "conflict"
		return p, fmt.Errorf("%s: %w", path, err)
	}
	p.Content = string(want)
	owned, current := ownedHooks(have, o.HookCommand(def.Harness))
	switch {
	case owned && current:
		p.Status = "installed"
	case owned:
		p.Status = "outdated"
	}
	return p, nil
}

// Install writes the item (a no-op when it is installed) and returns its
// new state. A file mtm does not own is refused.
func (o Options) Install(id string) (Item, error) {
	p, err := o.Plan(id)
	if err != nil {
		return p.Item, err
	}
	switch p.Status {
	case "installed":
		return p.Item, nil
	case "conflict":
		return p.Item, fmt.Errorf("refusing to overwrite %s: mtm does not own it", p.Path)
	}
	if err := write(p.Path, []byte(p.Content)); err != nil {
		return p.Item, err
	}
	p.Status = "installed"
	return p.Item, nil
}

// Uninstall removes mtm's own file or hooks and nothing else.
func (o Options) Uninstall(id string) (Item, error) {
	p, err := o.Plan(id)
	if err != nil {
		return p.Item, err
	}
	switch p.Status {
	case "missing":
		return p.Item, nil
	case "conflict":
		return p.Item, fmt.Errorf("refusing to remove %s: mtm does not own it", p.Path)
	}
	if id == OMP {
		if err := os.Remove(p.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return p.Item, err
		}
	} else {
		have, _, err := read(p.Path)
		if err != nil {
			return p.Item, err
		}
		out, err := editHooks(have, o, p.Harness, false)
		if err != nil {
			return p.Item, err
		}
		if err := write(p.Path, out); err != nil {
			return p.Item, err
		}
	}
	p.Status = "missing"
	return p.Item, nil
}

// read is a file's content; a missing file is not an error. Anything but a
// regular file is refused.
func read(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, true, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	return data, true, err
}

// write replaces a file through a temporary file in its directory, keeping
// its mode; the first time mtm changes an existing file it keeps the
// original beside it as <file>.mtm.bak.
func write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
		if _, err := os.Lstat(path + ".mtm.bak"); errors.Is(err, os.ErrNotExist) {
			original, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.WriteFile(path+".mtm.bak", original, mode); err != nil {
				return err
			}
		}
	}
	f, err := os.CreateTemp(dir, ".mtm-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
