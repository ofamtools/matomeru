package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/markdown"
	"ofam.dev/ochazuke/widgets/modal"
)

type markdownLinkLocation struct {
	suffix   string
	fragment string
	line     int
}

func (loc markdownLinkLocation) editorArgs(path string) string {
	if loc.line > 0 {
		return fmt.Sprintf(" +%d %s", loc.line, handlers.Quote(path))
	}
	return " " + handlers.Quote(path)
}

func (loc markdownLinkLocation) reveal(md *markdown.Model) {
	if loc.fragment != "" && jumpToAnchor(md, loc.fragment) {
		return
	}
	if loc.line > 0 {
		md.Offset = loc.line - 1
	}
}

// resolveMarkdownLink keeps URL and location syntax out of filesystem paths.
func resolveMarkdownLink(href, baseDir string) (openTarget, error) {
	href = strings.TrimSpace(href)
	if handlers.IsURL(href) {
		return openTarget{path: href}, nil
	}
	rawPath, fragment, _ := strings.Cut(href, "#")
	file, line, _ := splitContextFileLoc(rawPath)
	loc := markdownLinkLocation{suffix: href[len(file):], fragment: fragment, line: line}
	file = cleanLocalTargetPath(file)
	switch {
	case file == "" && strings.HasPrefix(href, "#"):
		return openTarget{link: loc}, nil
	case file == "":
		return openTarget{}, fmt.Errorf("empty Markdown link target")
	case filepath.IsAbs(file):
		file = filepath.Clean(file)
	case filepath.IsAbs(baseDir):
		file = filepath.Join(baseDir, file)
	default:
		return openTarget{}, fmt.Errorf("%s is relative, and this Markdown has no base directory", href)
	}
	return openTarget{path: file, title: filepath.Base(file), link: loc}, nil
}

// markdownBaseDir resolves the source record's paths without using process cwd.
func (a *App) markdownBaseDir(s core.State, from *core.Ref, filePath string) string {
	dirs := []string{}
	var task core.Ref
	if from != nil {
		if rec, err := a.Store.Get(*from); err == nil {
			dirs = append(dirs, rec.Text("baseDir"), data.MarkdownFileDir(rec.Text("filePath")), rec.Text("cwd"))
			if dir := anchoredMarkdownDir(dirs...); dir != "" {
				return dir
			}
			task, _ = rec.RefField("task")
			if pane := a.Store.MarkdownSourcePane(*rec); pane != nil {
				dirs = append(dirs, pane.Text("cwd"))
				if task.ID == "" {
					task, _ = pane.RefField("task")
				}
			}
		}
	}
	dirs = append(dirs, data.MarkdownFileDir(filePath))
	if dir := anchoredMarkdownDir(dirs...); dir != "" {
		return dir
	}
	if task.ID != "" {
		dirs = append(dirs, a.markdownProjectDir(task))
		if dir := anchoredMarkdownDir(dirs...); dir != "" {
			return dir
		}
	}
	project := core.Ref{Kind: data.KindTask, ID: a.activeProjectWorkspaceKey(s)}
	return anchoredMarkdownDir(append(dirs, a.markdownProjectDir(project))...)
}

func (a *App) markdownProjectDir(task core.Ref) string {
	if task.ID == "" {
		return ""
	}
	if dir := a.defaultDirForTask(task); dir != "" {
		return dir
	}
	if root, ok := a.topLevelTask(task); ok {
		task = root
	}
	if rec, err := a.Store.Get(task); err == nil {
		dir, _, _ := data.RepoTargetInfo(a.Store, *rec)
		return dir
	}
	return ""
}

// An explicitly relative directory needs a lower-priority absolute anchor.
func anchoredMarkdownDir(dirs ...string) string {
	var relative string
	for _, dir := range dirs {
		dir = cleanLocalTargetPath(dir)
		if filepath.IsAbs(dir) {
			return filepath.Join(dir, relative)
		}
		if relative == "" && dir != "" {
			relative = dir
		}
	}
	return ""
}

func (a *App) openCanvasURL(s core.State, href string) core.State {
	root := a.View(s)
	var from *core.Ref
	if content := root.Find("site.content"); content != nil {
		from = content.Ref
	}
	t, err := resolveMarkdownLink(href, a.markdownBaseDir(s, from, ""))
	if err != nil {
		a.say("%s", err)
		return s
	}
	if t.path == "" {
		headings := collectCanvasHeadings(root)
		for i, h := range headings {
			if slug(h.Text) == slug(t.link.fragment) {
				return a.jumpCanvasHeading(s, a.activeSitePageIDFor(s), headings, i)
			}
		}
		a.say("no heading #%s in this page", t.link.fragment)
		return s
	}
	t.from = from
	return a.openMarkdownLinkTarget(s, t, a.cfg.Handlers.Match(t.path))
}

func (a *App) openMarkdownLinkTarget(s core.State, t openTarget, rules []handlers.Rule) core.State {
	if len(rules) > 0 {
		if isMarkdownReader(rules[0]) {
			// A linked file is its own Markdown source.
			t.from = nil
		}
		return a.runHandler(s, rules[0], t)
	}
	if t.link.line > 0 {
		prior := a.activeCodeViewer()
		s = a.openCodeViewer(s, CodeOpenRequest{File: t.path, StartLine: t.link.line, SourceRef: t.from})
		if opened := a.activeCodeViewer(); opened != nil && opened != prior {
			opened.state.MoveTop(t.link.line)
		}
		return s
	}
	a.say("no handler opens %s", t.path)
	return s
}

// followLink retains reader history when navigating between Markdown files.
func (a *App) followLink(rm *modal.MarkdownModal, from *core.Ref, href string) modal.Result {
	if rm == nil {
		return modal.Result{Handled: true}
	}
	if rm.BackLen() > 0 {
		from = nil
	}
	t, err := resolveMarkdownLink(href, a.markdownBaseDir(a.Shell.State, from, rm.Path()))
	if err != nil {
		return modal.Result{Handled: true, Status: err.Error(), SetStatus: true}
	}
	md := rm.Model()
	if t.path == "" {
		if !jumpToAnchor(md, t.link.fragment) {
			return modal.Result{Handled: true, Status: fmt.Sprintf("no heading #%s in this document", t.link.fragment), SetStatus: true}
		}
		return modal.Result{Handled: true}
	}
	rules := a.cfg.Handlers.Match(t.path)
	if len(rules) > 0 && isMarkdownReader(rules[0]) {
		text, err := a.readFile(t.path)
		if err != nil {
			a.fail("read "+t.path, err)
			return modal.Result{Handled: true}
		}
		rm.Navigate(t.title, text, t.path)
		t.link.reveal(md)
		return modal.Result{Handled: true, Status: fmt.Sprintf("%s · backspace goes back", md.Title), SetStatus: true}
	}
	t.from = from
	a.afterModal(func(s core.State) core.State { return a.openMarkdownLinkTarget(s, t, rules) })
	return modal.Result{Handled: true}
}

func isMarkdownReader(rule handlers.Rule) bool {
	return rule.Kind == handlers.KindBuiltin && rule.Name == handlers.MarkdownReader
}
