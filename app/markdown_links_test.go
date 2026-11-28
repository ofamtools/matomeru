package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
)

func TestCanvasMarkdownLinksUseRecordBaseDir(t *testing.T) {
	for _, kind := range []string{data.KindPage, data.KindDocument} {
		t.Run(kind, func(t *testing.T) {
			f := setupNavTreeFixture(t)
			a, d := f.app, f.driver
			base := filepath.Join(t.TempDir(), "docs")
			child := filepath.Join(base, "child.md")
			notes := filepath.Join(filepath.Dir(base), "notes", "foo.md")
			nested := filepath.Join(filepath.Dir(notes), "nested.md")
			code := filepath.Join(base, "src", "main.go")
			writeLinkFixture(t, child, "# Child\n\n## Linked heading\n\nCHILD_MARKER\n")
			writeLinkFixture(t, notes, "# Notes\n\n[Nested](./nested.md)\n")
			writeLinkFixture(t, nested, "# Nested\n")
			writeLinkFixture(t, code, strings.Repeat("// Code\n", 20))
			t.Chdir(t.TempDir())

			body := "[Child](./child.md#linked-heading)\n\n[Notes](../notes/foo.md)\n\n[Code](src/main.go:12)"
			if kind == data.KindPage {
				page, err := a.Router.CreatePage(User, mcpui.CreatePageArgs{
					Title: "Markdown links", Pane: f.mainPane.ID, BaseDir: base,
					Root: ui.ViewNode{Kind: ui.KindMarkdown, Props: map[string]any{"markdown": body}},
				})
				if err != nil {
					t.Fatal(err)
				}
				a.Shell.State = a.jumpToPage(a.State(), page.ID)
			} else {
				_, _, err := a.Store.Patch(data.PatchInput{
					Ref: f.doc.Ref(), Fields: map[string]any{"body": body, "baseDir": base},
				}, User)
				if err != nil {
					t.Fatal(err)
				}
				d.Keys("5", "enter")
				a.projectCtrl().setNavSectionExpanded(f.proj.ID, "documents", true)
				a.Shell.State = withFocus(a.State(), projectNavNodePrefix+"documents."+f.doc.ID)
				a.viewMemo.gen++
			}
			d.Keys("enter")
			if a.Mode() != ModePage {
				t.Fatalf("opening source went to %q at %q, want ModePage", a.Mode(), a.Focus())
			}

			d.Keys("enter")
			reader := requireLinkReader(t, a, child)
			if got := reader.Model().SelectedHeading; got != 1 {
				t.Errorf("child heading = %d, want 1", got)
			}
			d.Keys("q", "l", "enter")
			requireLinkReader(t, a, notes)
			d.Keys("enter")
			requireLinkReader(t, a, nested)
			d.Keys("q", "l", "enter")
			viewer := a.activeCodeViewer()
			if viewer == nil || viewer.state.FilePath != code || viewer.state.CursorLine != 12 {
				t.Fatalf("expected code viewer at %s:12, status = %q", code, a.Status())
			}
		})
	}
}

func TestDocumentReaderMarkdownLinksUseRecordBaseDirAfterBack(t *testing.T) {
	a, d := demo(t)
	root := t.TempDir()
	base := filepath.Join(root, "links")
	document := filepath.Join(root, "stored", "document.md")
	child := filepath.Join(base, "sub", "child.md")
	grandchild := filepath.Join(base, "sub", "grandchild.md")
	writeLinkFixture(t, document, "[Child](./sub/child.md)")
	writeLinkFixture(t, child, "[Grandchild](./grandchild.md)")
	writeLinkFixture(t, grandchild, "# Grandchild")
	doc, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindDocument, Fields: map[string]any{"title": "Document", "filePath": document, "baseDir": base},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	browseTo(t, a, d, *doc)
	d.Keys("o")
	requireLinkReader(t, a, document)
	d.Keys("enter")
	requireLinkReader(t, a, child)
	d.Keys("enter")
	requireLinkReader(t, a, grandchild)
	d.Keys("backspace", "backspace", "enter")
	requireLinkReader(t, a, child)
}

func TestLinkBaseDirFallbacks(t *testing.T) {
	f := setupNavTreeFixture(t)
	a := f.app
	root := t.TempDir()
	doc, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindDocument, Fields: map[string]any{"title": "Fallbacks", "task": f.subTask.Ref()},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	ref := doc.Ref()
	assertBaseDir := func(want string) {
		t.Helper()
		if got := a.markdownBaseDir(a.State(), &ref, ""); got != want {
			t.Errorf("base directory = %q, want %q", got, want)
		}
	}
	assertBaseDir(f.proj.Text("defaultDir"))

	if _, _, err := a.Store.Relate(data.RelateInput{From: f.mainPane.Ref(), Kind: data.RelCreated, To: ref}, User); err != nil {
		t.Fatal(err)
	}
	paneDir := filepath.Join(root, "pane")
	if _, _, err := a.Store.Patch(data.PatchInput{Ref: f.mainPane.Ref(), Fields: map[string]any{"cwd": paneDir}}, User); err != nil {
		t.Fatal(err)
	}
	assertBaseDir(paneDir)

	fileDir := filepath.Join(root, "file")
	if _, _, err := a.Store.Patch(data.PatchInput{Ref: ref, Fields: map[string]any{"filePath": filepath.Join(fileDir, "doc.md")}}, User); err != nil {
		t.Fatal(err)
	}
	assertBaseDir(fileDir)

	if got, err := resolveMarkdownLink("relative.md", ""); err == nil {
		t.Errorf("unanchored Markdown link resolved to %q", got.path)
	}
}

func TestLinkLocationSuffixesAreSeparateFromPath(t *testing.T) {
	base := t.TempDir()
	link, err := resolveMarkdownLink("src/main.go:12:3#example", base)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "src", "main.go"); link.path != want {
		t.Errorf("link path = %q, want %q", link.path, want)
	}
	if got, want := link.link, (markdownLinkLocation{suffix: ":12:3#example", fragment: "example", line: 12}); got != want {
		t.Errorf("link location = %+v, want %+v", got, want)
	}
}

func requireLinkReader(t *testing.T, a *App, path string) *modal.MarkdownModal {
	t.Helper()
	reader := a.activeMarkdown("reader")
	if reader == nil || reader.Path() != path {
		got := a.Modal()
		if reader != nil {
			got = reader.Path()
		}
		t.Fatalf("reader opened %q, want %q, status = %q", got, path, a.Status())
	}
	return reader
}

func writeLinkFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
