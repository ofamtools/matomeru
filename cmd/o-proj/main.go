// Command o-proj creates and guards projects: a family tool (o-<tool>), so
// o proj ARGS runs it, and so does mtm proj ARGS (mtm discovers o-<sub>
// after its own mtm-<sub>, oden.WithPrefixes). Making a
// project always goes through new, which creates the directory, runs git
// init there and verifies that the directory is its own repository before
// anything is committed (and, last, installs o-inventory's hooks when
// o-inventory is on PATH); check is the guard to run before the first commit
// in any directory. The home directory is itself a git repository, so a
// directory without its own git init commits into it silently.
//
//	o-proj new NAME [--root DIR] [--template T] [--existing] [--no-register] [--github] [--private|--public] [--license mit|none] [--hub DIR|none]
//	o-proj link NAME | --all [--hub DIR]
//	o-proj unlink NAME | --all [--hub DIR]
//	o-proj adopt NAME
//	o-proj check [DIR] [--root DIR]
//	o-proj list
//	o-proj templates
//	o-proj project VERB [ARG...] [--ARG VALUE] [--target ID] [--json '{...}']
//	o-proj catalog
//
// The first seven are the inline commands; project is the same work as an
// ochazuke noun (shell.go), whose verbs print JSON, and catalog describes it
// for a host that mounts this bin.
//
// -data (before the command or after it) is mtm's database, with mtm's
// default. No daemon is started and there is no TUI.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/matomeru/internal/work"
	ocli "github.com/unixsuperhero/ochazuke/cli"
	"github.com/unixsuperhero/oden"
)

// Origin is the provenance new (and --existing, and adopt) records on the
// projects it registers; link --all links only these.
const Origin = "o-proj"

// GitHubOwner is the account new --github creates repositories under.
const GitHubOwner = "unixsuperhero"

// Tool is one invocation's world: streams, the environment its commands run
// with, the home directory the guard refuses, and how commands are run (a
// test swaps run to record gh without calling it).
type Tool struct {
	Stdout, Stderr io.Writer
	Env            []string
	Home           string
	Prog           string // "mtm proj" under mtm, "o proj" under o, else "o-proj"; messages only
	User           string // a template's {{.User}}
	Now            func() time.Time
	Run            func(dir, name string, args ...string) ([]byte, error)
}

func main() {
	home, _ := os.UserHomeDir()
	t := &Tool{Stdout: os.Stdout, Stderr: os.Stderr, Env: os.Environ(), Home: home, Prog: progName(os.Getenv), Now: time.Now}
	if u, err := user.Current(); err == nil {
		t.User = u.Username
	}
	t.Run = t.exec
	os.Exit(oden.Report(os.Stderr, t.Prog, t.Main(os.Args[1:])))
}

// progName is how messages name this command: the word under the parent oden
// app that ran it, else the bin name.
func progName(getenv func(string) string) string {
	if parent := getenv("ODEN_PARENT"); parent != "" {
		return parent + " proj"
	}
	return "o-proj"
}

func (t *Tool) exec(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = t.Env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err != nil {
		err = fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return out.Bytes(), err
}

// DefaultData is mtm's default database (cmd/mtm DefaultData):
// $XDG_DATA_HOME/matomeru/mtm.db, else ~/.local/share/matomeru/mtm.db.
func (t *Tool) DefaultData() string {
	base := lookup(t.Env, "XDG_DATA_HOME")
	if base == "" {
		if t.Home == "" {
			return "mtm.db"
		}
		base = filepath.Join(t.Home, ".local", "share")
	}
	return filepath.Join(base, "matomeru", "mtm.db")
}

// DefaultRoot is where projects live, hiiro's project root: ~/proj.
func (t *Tool) DefaultRoot() string { return filepath.Join(t.Home, "proj") }

func lookup(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], key+"="); ok {
			return v
		}
	}
	return ""
}

// Main parses the global -data flag, then runs the command word with oden.
func (t *Tool) Main(args []string) error {
	flags := flag.NewFlagSet(t.Prog, flag.ContinueOnError)
	flags.SetOutput(t.Stderr)
	data := flags.String("data", t.DefaultData(), "SQLite database path (mtm's)")
	if err := flags.Parse(args); err != nil {
		return &oden.ExitError{Code: 2}
	}
	app := oden.New("o-proj", flags.Args(), oden.WithBuiltins(), oden.WithStdio(os.Stdin, t.Stdout, t.Stderr), oden.WithEnv(t.Env))
	app.Add(
		oden.Command{Name: "new", Summary: "create ROOT/NAME, git init it, verify, commit, register, link from the hub", Run: func(_ context.Context, a []string) error { return t.New(*data, a) }},
		oden.Command{Name: "check", Summary: "refuse a directory whose git toplevel is $HOME or above the project root", Run: func(_ context.Context, a []string) error { return t.Check(a) }},
		oden.Command{Name: "templates", Summary: "the template catalog: name, kind (dir|tar.gz), path; .tmpl files render with Go text/template", Run: func(_ context.Context, a []string) error { return t.ListTemplates(a) }},
		oden.Command{Name: "link", Summary: "link one registered project (NAME) or every one made by o-proj (--all) from the hub, ~/hub/o/NAME -> DIR", Run: func(_ context.Context, a []string) error { return t.Link(*data, a) }},
		oden.Command{Name: "unlink", Summary: "remove the hub link of one registered project (NAME) or of every project not made by o-proj (--all)", Run: func(_ context.Context, a []string) error { return t.Unlink(*data, a) }},
		oden.Command{Name: "adopt", Summary: "record a registered project as made by o-proj without touching its tree", Run: func(_ context.Context, a []string) error { return t.Adopt(*data, a) }},
		oden.Command{Name: "list", Summary: "the projects in mtm's store with their trees (JSON)", Run: func(_ context.Context, a []string) error { return t.List(*data, a) }},
	)
	sh, err := t.shell(*data)
	if err != nil {
		return err
	}
	ocli.Mount(app, sh)
	return app.Run(context.Background())
}

// parse parses flags that may come before or after the positional words.
func parse(flags *flag.FlagSet, args []string) ([]string, error) {
	var words []string
	for {
		if err := flags.Parse(args); err != nil {
			return nil, &oden.ExitError{Code: 2}
		}
		args = flags.Args()
		if len(args) == 0 {
			return words, nil
		}
		words = append(words, args[0])
		args = args[1:]
	}
}

func (t *Tool) say(format string, a ...any) { fmt.Fprintf(t.Stdout, format+"\n", a...) }

// New makes a project: one output line per step, then cd DIR last, so a
// shell wrapper can eval the final line.
func (t *Tool) New(data string, args []string) error {
	flags := flag.NewFlagSet(t.Prog+" new", flag.ContinueOnError)
	flags.SetOutput(t.Stderr)
	root := flags.String("root", t.DefaultRoot(), "directory the project is created in")
	tmplName := flags.String("template", "", "a template from "+t.TemplateDir()+" (a directory or .tar.gz), or a path to one; *.tmpl files render with Go text/template (erb's analogue) and lose the suffix")
	existing := flags.Bool("existing", false, "adopt a directory that already has files")
	noRegister := flags.Bool("no-register", false, "do not add the project to mtm's store")
	github := flags.Bool("github", false, "create github.com/"+GitHubOwner+"/NAME and push")
	private := flags.Bool("private", false, "the GitHub repository is private (the default)")
	public := flags.Bool("public", false, "the GitHub repository is public")
	license := flags.String("license", Licenses[0], "the LICENSE written where absent: "+strings.Join(Licenses, " or "))
	hub := flags.String("hub", t.DefaultHub(), "the hub directory that links NAME -> DIR ($O_PROJ_HUB, else ~/hub/o); none skips the link")
	flags.StringVar(&data, "data", data, "SQLite database path (mtm's)")
	words, err := parse(flags, args)
	if err != nil {
		return err
	}
	if len(words) != 1 {
		return fmt.Errorf("usage: %s new NAME [--root DIR] [--template T] [--existing] [--no-register] [--github] [--private|--public] [--license mit|none] [--hub DIR|none]", t.Prog)
	}
	name := words[0]
	if name == "." || name == ".." || name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, "-") {
		return fmt.Errorf("%q is not a project name: one path segment, not starting with -", name)
	}
	if *private && *public {
		return errors.New("--private and --public are exclusive")
	}
	if err := checkLicense(*license); err != nil {
		return err
	}
	base, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	dir := filepath.Join(base, name)
	var tpl *Template
	if *tmplName != "" {
		found, err := t.FindTemplate(*tmplName)
		if err != nil {
			return err
		}
		tpl = &found
	}

	// mkdir -p
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		t.say("mkdir: created directory '%s'", dir)
	case err != nil:
		return err
	case len(entries) > 0 && !*existing:
		return fmt.Errorf("%s exists and is not empty; pass --existing to adopt it", dir)
	default:
		t.say("mkdir: directory '%s' exists", dir)
	}

	// git init, then verify before anything else touches git
	if _, err := t.Run(dir, "git", "init", "--quiet"); err != nil {
		return err
	}
	t.say("git init: %s", dir)
	top, err := t.verify(dir)
	if err != nil {
		return err
	}
	t.say("verify: git toplevel is %s", top)

	// the template, then the minimal files where the template left them out
	var wrote []string
	if tpl != nil {
		data := TemplateData{Name: name, Dir: dir, Root: base, Date: t.Now().Format("2006-01-02"), User: t.User, Module: "github.com/" + GitHubOwner + "/" + name}
		files, err := Apply(*tpl, dir, data)
		if err != nil {
			return err
		}
		wrote = append(wrote, files...)
		t.say("template: %s (%s) wrote %d files", tpl.Name, tpl.Kind, len(files))
	}
	minimal := []struct{ name, body string }{
		{"AGENTS.md", "# " + name + "\n\nCreated by o-proj.\n"},
		{".gitignore", ".DS_Store\n"},
	}
	if *license != "none" {
		minimal = append(minimal, struct{ name, body string }{"LICENSE", ""})
	}
	for _, f := range minimal {
		path := filepath.Join(dir, f.name)
		if _, err := os.Lstat(path); err == nil {
			t.say("keep: %s exists", path)
			continue
		}
		if f.name == "LICENSE" {
			f.body = licenseText(*license, t.Now().Year(), t.holder(dir))
		}
		if err := os.WriteFile(path, []byte(f.body), 0o644); err != nil {
			return err
		}
		wrote = append(wrote, f.name)
		t.say("write: %s", path)
	}

	// the initial commit holds only the files written here, never whatever
	// an adopted directory already had
	if _, err := t.Run(dir, "git", "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		t.say("commit: skipped, the repository already has commits")
	} else {
		if len(wrote) > 0 {
			if _, err := t.Run(dir, "git", append([]string{"add", "--"}, wrote...)...); err != nil {
				return err
			}
		}
		msg := "chore: new project " + name
		if _, err := t.Run(dir, "git", "commit", "--quiet", "--allow-empty", "-m", msg); err != nil {
			return err
		}
		sha, err := t.Run(dir, "git", "rev-parse", "--short", "HEAD")
		if err != nil {
			return err
		}
		t.say("commit: %s %s", strings.TrimSpace(string(sha)), msg)
	}

	repo := ""
	if *github {
		visibility := "--private"
		if *public {
			visibility = "--public"
		}
		repo = GitHubOwner + "/" + name
		if _, err := t.Run(dir, "gh", "repo", "create", repo, visibility, "--source", dir, "--remote", "origin", "--push"); err != nil {
			return err
		}
		t.say("github: %s (%s)", repo, strings.TrimPrefix(visibility, "--"))
	}

	if *noRegister {
		t.say("register: skipped (--no-register)")
	} else if err := t.register(data, name, dir, repo); err != nil {
		return err
	}
	if *hub == NoHub {
		t.say("link: skipped (--hub none)")
	} else if _, err := t.link(*hub, name, dir); err != nil {
		return err
	}
	t.hook(dir)
	t.say("cd %s", dir)
	return nil
}

// hook installs o-inventory's git hooks in dir, best-effort: a missing
// o-inventory (looked up on the Tool's PATH) or a failed install is a line
// of output, never an error, since the project already exists.
func (t *Tool) hook(dir string) {
	bin := t.lookPath("o-inventory")
	if bin == "" {
		t.say("hook: skipped (o-inventory not on PATH)")
		return
	}
	if _, err := t.Run(dir, bin, "hook", "install", dir); err != nil {
		t.say("hook: failed (%v)", err)
		return
	}
	t.say("hook: installed")
}

// lookPath finds an executable file named name on the PATH in t.Env, ""
// when there is none.
func (t *Tool) lookPath(name string) string {
	for _, d := range filepath.SplitList(lookup(t.Env, "PATH")) {
		if d == "" {
			continue
		}
		path := filepath.Join(d, name)
		if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return path
		}
	}
	return ""
}

// verify is the point of this tool: dir must be its own repository's
// toplevel. When it is not, a commit would land in an enclosing repository
// (the home directory's), so stop before anything is committed.
func (t *Tool) verify(dir string) (string, error) {
	out, err := t.Run(dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	top := strings.TrimSpace(string(out))
	if !samePath(top, dir) {
		return top, fmt.Errorf("ABORT: git toplevel of %s is %s, not the directory itself; committing here would land in %s", dir, top, top)
	}
	return top, nil
}

// samePath compares two paths after resolving symlinks (/tmp is
// /private/tmp on macOS, and git reports the resolved one).
func samePath(a, b string) bool {
	return real(a) == real(b)
}

func real(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (t *Tool) register(data, name, dir, repo string) error {
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
	tree := work.Tree{Kind: "dir", Path: dir}
	for _, p := range projects {
		if p.Name != name {
			continue
		}
		for _, tr := range p.Trees {
			if samePath(tr.Path, dir) {
				t.say("register: project %s (%s) already has tree %s", name, p.ID, dir)
				if p.Origin == Origin {
					return nil
				}
				p.Origin = Origin
				return w.PutProject(p)
			}
		}
		p.Trees = append(p.Trees, tree)
		p.Origin = Origin
		if p.DefaultTree == "" {
			p.DefaultTree = dir
		}
		if p.Repo == "" {
			p.Repo = repo
		}
		if err := w.PutProject(p); err != nil {
			return err
		}
		t.say("register: added tree %s to project %s (%s) in %s", dir, name, p.ID, data)
		return nil
	}
	p := work.Project{ID: work.NewID(), Name: name, Status: "open", Created: work.Now(), Repo: repo, Trees: []work.Tree{tree}, DefaultTree: dir, Origin: Origin}
	if err := w.PutProject(p); err != nil {
		return err
	}
	t.say("register: project %s (%s) in %s", name, p.ID, data)
	return nil
}

// Check is the guard before a first commit: it prints DIR's git toplevel and
// fails when that toplevel is the home directory, or a repository at or
// above the project root (which is never a project's own repository).
func (t *Tool) Check(args []string) error {
	flags := flag.NewFlagSet(t.Prog+" check", flag.ContinueOnError)
	flags.SetOutput(t.Stderr)
	root := flags.String("root", t.DefaultRoot(), "the project root; a repository at or above it is not a project's")
	words, err := parse(flags, args)
	if err != nil {
		return err
	}
	if len(words) > 1 {
		return fmt.Errorf("usage: %s check [DIR] [--root DIR]", t.Prog)
	}
	dir := "."
	if len(words) == 1 {
		dir = words[0]
	}
	_, top, err := t.guard(dir, *root)
	if top != "" {
		t.say("%s", top)
	}
	return err
}

// guard resolves dir and its git toplevel and refuses the toplevel when it
// is the home directory or contains root. top is "" when dir is in no
// repository.
func (t *Tool) guard(dir, root string) (abs, top string, err error) {
	abs, err = filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	out, err := t.Run(abs, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return abs, "", fmt.Errorf("STOP: %s is not in a git repository; create it with %s new NAME (or --existing)", abs, t.Prog)
	}
	top = strings.TrimSpace(string(out))
	switch {
	case t.Home != "" && samePath(top, t.Home):
		return abs, top, fmt.Errorf("STOP: the git toplevel of %s is the home directory %s; a commit here lands in the home repository. Run %s new NAME --existing", abs, top, t.Prog)
	case within(real(root), real(top)):
		return abs, top, fmt.Errorf("STOP: the git toplevel of %s is %s, which contains the project root %s, so it is not this project's repository. Run %s new NAME --existing", abs, top, root, t.Prog)
	}
	return abs, top, nil
}

// ListTemplates prints the catalog, one template per line: name, kind, path.
func (t *Tool) ListTemplates(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("usage: %s templates", t.Prog)
	}
	catalog, err := t.Templates()
	if err != nil {
		return err
	}
	if len(catalog) == 0 {
		fmt.Fprintf(t.Stderr, "no templates in %s\n", t.TemplateDir())
	}
	for _, tpl := range catalog {
		t.say("%s\t%s\t%s", tpl.Name, tpl.Kind, tpl.Path)
	}
	return nil
}

// List prints mtm's projects with their trees as JSON.
func (t *Tool) List(data string, args []string) error {
	flags := flag.NewFlagSet(t.Prog+" list", flag.ContinueOnError)
	flags.SetOutput(t.Stderr)
	flags.StringVar(&data, "data", data, "SQLite database path (mtm's)")
	words, err := parse(flags, args)
	if err != nil {
		return err
	}
	if len(words) > 0 {
		return fmt.Errorf("usage: %s list [-data PATH]", t.Prog)
	}
	projects, err := t.projects(data)
	if err != nil {
		return err
	}
	type row struct {
		ID          string      `json:"id"`
		Name        string      `json:"name"`
		Status      string      `json:"status"`
		Repo        string      `json:"repo,omitempty"`
		DefaultTree string      `json:"default_tree,omitempty"`
		Trees       []work.Tree `json:"trees"`
	}
	rows := make([]row, 0, len(projects))
	for _, p := range projects {
		trees := p.Trees
		if trees == nil {
			trees = []work.Tree{}
		}
		rows = append(rows, row{ID: p.ID, Name: p.Name, Status: p.Status, Repo: p.Repo, DefaultTree: p.DefaultTree, Trees: trees})
	}
	enc := json.NewEncoder(t.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

// projects reads the projects in mtm's store at data.
func (t *Tool) projects(data string) ([]work.Project, error) {
	db, err := store.Open(data)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	w, err := work.Open(db)
	if err != nil {
		return nil, err
	}
	return w.Projects()
}
