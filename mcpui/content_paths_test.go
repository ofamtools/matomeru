package mcpui

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestMarkdownBaseDirTools(t *testing.T) {
	f := setup(t)
	paneDir := filepath.Join(t.TempDir(), "worktree")
	explicitDir := filepath.Join(t.TempDir(), "explicit")
	filePath := filepath.Join(t.TempDir(), "docs", "plan.md")
	f.store.SetFilesRoot(t.TempDir())
	box := NewToolbox(f.store, f.r)
	box.Getenv = func(string) string { return "" }
	if _, _, err := f.store.Patch(data.PatchInput{
		Ref:    f.pane.Ref(),
		Fields: map[string]any{"cwd": paneDir},
	}, owner); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name     string
		args     map[string]any
		want     string
		testPage bool
	}{
		{"explicit", map[string]any{"pane": f.pane.ID, "baseDir": explicitDir, "cwd": paneDir}, explicitDir, true},
		{"alias", map[string]any{"cwd": explicitDir}, explicitDir, true},
		{"linked pane", nil, paneDir, true},
		{"file parent", map[string]any{"pane": f.pane.ID, "filePath": filePath}, filepath.Dir(filePath), false},
		{"managed file parent", nil, filepath.Join(f.store.FilesRoot(), data.CategoryDocuments), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			if tc.name == "linked pane" {
				ctx = WithPaneID(ctx, f.pane.ID)
			}
			call := func(tool string, args map[string]any) any {
				t.Helper()
				for key, value := range tc.args {
					args[key] = value
				}
				raw, err := json.Marshal(args)
				if err != nil {
					t.Fatal(err)
				}
				out, err := box.Call(ctx, agent, tool, raw)
				if err != nil {
					t.Fatalf("%s: %v", tool, err)
				}
				return out
			}
			if tc.testPage {
				out := call(ToolCreatePage, map[string]any{"title": tc.name, "root": map[string]any{"kind": "markdown"}})
				assertContentBaseDir(t, f.store, out.(*PageResult).Ref, tc.want)
			}
			out := call(ToolCreateDocument, map[string]any{"title": tc.name, "body": "# Plan"})
			assertContentBaseDir(t, f.store, out.(*CreateDocumentResult).Document.Ref(), tc.want)
		})
	}
}

func TestUpdatePageMarkdownBaseDir(t *testing.T) {
	f := setup(t)
	var res PageResult
	root := map[string]any{"kind": "markdown"}
	f.call(ToolCreatePage, map[string]any{"title": "Plan", "root": root, "baseDir": "/initial"}, &res)
	ref := res.Ref
	for _, tc := range []struct {
		update map[string]any
		want   string
	}{
		{map[string]any{"baseDir": "/explicit", "cwd": "/ignored"}, "/explicit"},
		{map[string]any{"cwd": "/alias"}, "/alias"},
		{nil, "/alias"},
		{map[string]any{"baseDir": ""}, ""},
	} {
		args := map[string]any{"page": ref.ID, "mode": ModeReplace, "root": root}
		for key, value := range tc.update {
			args[key] = value
		}
		f.call(ToolUpdatePage, args, &res)
		assertContentBaseDir(t, f.store, ref, tc.want)
	}
}

func assertContentBaseDir(t *testing.T, store *data.Store, ref core.Ref, want string) {
	t.Helper()
	rec, err := store.Get(ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Text("baseDir"); got != want {
		t.Errorf("%s baseDir = %q, want %q", data.RefText(ref), got, want)
	}
}
