package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// The data side of o-proj is an ochazuke runtime with one noun, project,
// behind ochazuke/cli (mounted in Main): o-proj project VERB [ARG...]
// [--ARG VALUE] runs a verb and prints its result as JSON, and o-proj
// catalog describes the noun for a host (nabe) that mounts the bin. The
// rows of project list are the registered projects, {id, label: name,
// data: {trees}}; a --target no list shows yet is revealed by loading
// them. There is no TUI: the runtime has no page and nothing renders it.

// projectData is a project row's data: the trees open prints.
type projectData struct {
	Trees []work.Tree `json:"trees"`
}

// projectRow is one element of project list.
type projectRow struct {
	ID    string      `json:"id"`
	Label string      `json:"label"`
	Data  projectData `json:"data"`
}

// shell is the NOUN VERB shell over a runtime holding the project noun;
// data is mtm's database.
func (t *Tool) shell(data string) (*ocli.Shell, error) {
	r := ochazuke.NewRuntime()
	list, err := r.AddList("projects", nil)
	if err != nil {
		return nil, err
	}
	load := func() ([]projectRow, error) {
		projects, err := t.projects(data)
		if err != nil {
			return nil, err
		}
		rows := make([]projectRow, 0, len(projects))
		items := make([]ochazuke.ListItem, 0, len(projects))
		for _, p := range projects {
			d := projectData{Trees: p.Trees}
			if d.Trees == nil {
				d.Trees = []work.Tree{}
			}
			rows = append(rows, projectRow{ID: p.ID, Label: p.Name, Data: d})
			items = append(items, ochazuke.ListItem{ID: p.ID, Label: p.Name, Noun: "project", Data: d})
		}
		return rows, list.SetItems(items)
	}
	result := func(v any, err error) ochazuke.Result {
		if err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(v)
	}
	r.Define("project").
		Verb("list", func(ochazuke.Ctx) ochazuke.Result { return result(load()) },
			"the projects in mtm's store: {id, label, data: {trees}}").Rows("project").
		Verb("new", func(c ochazuke.Ctx) ochazuke.Result { return result(t.newFromArgs(data, c.Args)) },
			"create ROOT/NAME, git init it, verify, commit, register, link from the hub").
		Verb("link", func(c ochazuke.Ctx) ochazuke.Result {
			all, _ := c.Args["all"].(bool)
			return result(t.LinkProjects(data, ochazuke.StringArg(c.Args, "hub", t.DefaultHub()), ochazuke.StringArg(c.Args, "name", ""), all))
		}, "link one registered project (name) or every one made by o-proj (all) from the hub: {linked, kept, missing, skipped}").
		Verb("unlink", func(c ochazuke.Ctx) ochazuke.Result {
			all, _ := c.Args["all"].(bool)
			return result(t.UnlinkProjects(data, ochazuke.StringArg(c.Args, "hub", t.DefaultHub()), ochazuke.StringArg(c.Args, "name", ""), all))
		}, "remove the hub link of one registered project (name) or of every project not made by o-proj (all): {unlinked, kept}").
		Verb("check", func(c ochazuke.Ctx) ochazuke.Result {
			dir, top, err := t.guard(ochazuke.StringArg(c.Args, "dir", "."), t.DefaultRoot())
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(map[string]string{"dir": dir, "toplevel": top})
		}, "refuse a directory whose git toplevel is $HOME or above the project root").
		Verb("templates", func(ochazuke.Ctx) ochazuke.Result {
			catalog, err := t.Templates()
			if catalog == nil {
				catalog = []Template{}
			}
			return result(catalog, err)
		},
			"the template catalog: {name, kind, path}").
		Verb("open", func(c ochazuke.Ctx) ochazuke.Result {
			d, ok := c.Instance.Data.(projectData)
			if c.Instance.Noun != "project" || !ok {
				return ochazuke.Failed(errors.New("project open needs --target PROJECT_ID, an id from project list"))
			}
			return ochazuke.Done(d.Trees)
		}, "print the target project's trees")
	params := func(key string, p map[string]ochazuke.Parameter) {
		op := r.Operations[key]
		op.Parameters = p
		r.Operations[key] = op
	}
	params("project.new", map[string]ochazuke.Parameter{
		"name":     {Type: "string", Required: true, Description: "the project directory's name under root"},
		"root":     {Type: "string", Description: "directory the project is created in (default ~/proj)"},
		"template": {Type: "string", Description: "a template name from project templates, or a path"},
		"github":   {Type: "boolean", Description: "create github.com/" + GitHubOwner + "/NAME and push"},
		"private":  {Type: "boolean", Description: "the GitHub repository is private (default true)"},
		"license":  {Type: "string", Enum: Licenses, Description: "the LICENSE written where absent (default mit)"},
		"existing": {Type: "boolean", Description: "adopt a directory that already has files"},
		"register": {Type: "boolean", Description: "add the project to mtm's store (default true)"},
		"hub":      {Type: "string", Description: "the hub directory that links NAME -> DIR (default $O_PROJ_HUB, else ~/hub/o); none skips"},
	})
	params("project.link", map[string]ochazuke.Parameter{
		"name": {Type: "string", Description: "the registered project to link (or all)"},
		"all":  {Type: "boolean", Description: "link every project made by o-proj"},
		"hub":  {Type: "string", Description: "the hub directory (default $O_PROJ_HUB, else ~/hub/o)"},
	})
	params("project.unlink", map[string]ochazuke.Parameter{
		"name": {Type: "string", Description: "the registered project to unlink (or all)"},
		"all":  {Type: "boolean", Description: "unlink every project not made by o-proj"},
		"hub":  {Type: "string", Description: "the hub directory (default $O_PROJ_HUB, else ~/hub/o)"},
	})
	params("project.check", map[string]ochazuke.Parameter{
		"dir": {Type: "string", Description: "the directory to check (default the working directory)"},
	})
	sh := ocli.New(r, "o-proj", os.Stdin, t.Stdout, t.Stderr)
	sh.Usage = ocli.DefaultUsage(t.Prog)
	sh.Reveal = func(string) error { _, err := load(); return err }
	return sh, nil
}

// newFromArgs runs New with the flags project new's arguments spell; its
// result is the project's name and directory.
func (t *Tool) newFromArgs(data string, args ochazuke.Arguments) (map[string]string, error) {
	name := ochazuke.StringArg(args, "name", "")
	root := ochazuke.StringArg(args, "root", t.DefaultRoot())
	argv := []string{"--root", root}
	for _, flag := range []string{"template", "license", "hub"} {
		if v := ochazuke.StringArg(args, flag, ""); v != "" {
			argv = append(argv, "--"+flag, v)
		}
	}
	for flag, when := range map[string]struct {
		name string
		want bool
	}{"--github": {"github", true}, "--existing": {"existing", true}, "--no-register": {"register", false}, "--private": {"private", true}, "--public": {"private", false}} {
		if v, set := args[when.name].(bool); set && v == when.want {
			argv = append(argv, flag)
		}
	}
	if err := t.New(data, append(argv, "--", name)); err != nil {
		return nil, err
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return map[string]string{"name": name, "dir": filepath.Join(base, name)}, nil
}
