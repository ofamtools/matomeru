package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// tool is a Tool over a temp home: git runs with a fixed identity and no
// global or system config, XDG dirs point into the temp home, and gh calls
// are recorded instead of run.
func tool(t *testing.T) (*Tool, *bytes.Buffer, *[][]string) {
	t.Helper()
	home := t.TempDir()
	var out, errOut bytes.Buffer
	var gh [][]string
	env := append(os.Environ(),
		"HOME="+home,
		"XDG_DATA_HOME="+filepath.Join(home, "data"),
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"O_PROJ_HUB=",
		// git's directory and the system dirs only: a real o-inventory
		// (~/bin) never runs from a test.
		"PATH="+testPath(t),
	)
	tl := &Tool{Stdout: &out, Stderr: &errOut, Env: env, Home: home, Prog: "o-proj", User: "tester",
		Now: func() time.Time { return time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC) }}
	tl.Run = func(dir, name string, args ...string) ([]byte, error) {
		if name == "gh" {
			gh = append(gh, append([]string{name}, args...))
			return nil, nil
		}
		return tl.exec(dir, name, args...)
	}
	return tl, &out, &gh
}

func testPath(t *testing.T) string {
	t.Helper()
	g, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join([]string{filepath.Dir(g), "/usr/bin", "/bin"}, string(filepath.ListSeparator))
}

// setEnv replaces key in tl.Env.
func setEnv(tl *Tool, key, value string) {
	tl.Env = append(slices.DeleteFunc(tl.Env, func(kv string) bool { return strings.HasPrefix(kv, key+"=") }), key+"="+value)
}

func TestNewInstallsInventoryHook(t *testing.T) {
	tl, out, _ := tool(t)
	bin := t.TempDir()
	argv := filepath.Join(t.TempDir(), "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argv + "\n"
	if err := os.WriteFile(filepath.Join(bin, "o-inventory"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	setEnv(tl, "PATH", bin+string(filepath.ListSeparator)+testPath(t))
	root := filepath.Join(tl.Home, "proj")
	if err := tl.Main([]string{"-data", filepath.Join(tl.Home, "mtm.db"), "new", "demo", "--root", root}); err != nil {
		t.Fatal(err, out)
	}
	dir := filepath.Join(root, "demo")
	got, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	if want := "hook\ninstall\n" + dir + "\n"; string(got) != want {
		t.Fatalf("argv %q, want %q", got, want)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if n := len(lines); n < 2 || lines[n-2] != "hook: installed" || lines[n-1] != "cd "+dir {
		t.Fatalf("tail %q", lines)
	}
}

func TestNewSkipsHookWithoutInventory(t *testing.T) {
	tl, out, _ := tool(t)
	root := filepath.Join(tl.Home, "proj")
	if err := tl.Main([]string{"-data", filepath.Join(tl.Home, "mtm.db"), "new", "demo", "--root", root}); err != nil {
		t.Fatal(err, out)
	}
	if !strings.Contains(out.String(), "hook: skipped (o-inventory not on PATH)\ncd ") {
		t.Fatalf("out %q", out)
	}
}

func git(t *testing.T, tl *Tool, dir string, args ...string) string {
	t.Helper()
	out, err := tl.exec(dir, "git", args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func TestNewCreatesVerifiesCommitsRegisters(t *testing.T) {
	tl, out, _ := tool(t)
	root := filepath.Join(tl.Home, "proj")
	data := filepath.Join(tl.Home, "mtm.db")
	if err := tl.Main([]string{"-data", data, "new", "demo", "--root", root}); err != nil {
		t.Fatal(err, out)
	}
	dir := filepath.Join(root, "demo")
	if got := git(t, tl, dir, "rev-parse", "--show-toplevel"); !samePath(got, dir) {
		t.Fatalf("toplevel %s, want %s", got, dir)
	}
	for _, f := range []string{"AGENTS.md", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	if got := git(t, tl, dir, "log", "--format=%s"); got != "chore: new project demo" {
		t.Fatalf("log %q", got)
	}
	if got := git(t, tl, dir, "ls-tree", "--name-only", "HEAD"); got != ".gitignore\nAGENTS.md\nLICENSE" {
		t.Fatalf("tree %q", got)
	}
	if got := lastLine(out.String()); got != "cd "+dir {
		t.Fatalf("last line %q", got)
	}

	db, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := work.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := w.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Name != "demo" || ps[0].Status != "open" || ps[0].Created == 0 ||
		len(ps[0].Trees) != 1 || ps[0].Trees[0].Path != dir || ps[0].Trees[0].Kind != "dir" {
		t.Fatalf("projects %+v", ps)
	}
}

func TestNewDefaultDataIsXDG(t *testing.T) {
	tl, out, _ := tool(t)
	if err := tl.Main([]string{"new", "x", "--root", filepath.Join(tl.Home, "proj")}); err != nil {
		t.Fatal(err, out)
	}
	if _, err := os.Stat(filepath.Join(tl.Home, "data", "matomeru", "mtm.db")); err != nil {
		t.Fatal(err)
	}
}

func TestNewRefusesNonEmptyWithoutExisting(t *testing.T) {
	tl, out, _ := tool(t)
	root := filepath.Join(tl.Home, "proj")
	dir := filepath.Join(root, "old")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := tl.Main([]string{"new", "old", "--root", root, "--no-register"})
	if err == nil || !strings.Contains(err.Error(), "--existing") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Fatal("git init ran on a refused directory")
	}
	if err := tl.Main([]string{"new", "old", "--root", root, "--existing", "--no-register"}); err != nil {
		t.Fatal(err, out)
	}
	// the adopted file is not committed; only what new wrote is
	if got := git(t, tl, dir, "ls-tree", "--name-only", "HEAD"); got != ".gitignore\nAGENTS.md\nLICENSE" {
		t.Fatalf("tree %q", got)
	}
}

func TestNewGitHubArgv(t *testing.T) {
	tl, out, gh := tool(t)
	root := filepath.Join(tl.Home, "proj")
	if err := tl.Main([]string{"new", "pub", "--root", root, "--github", "--public", "--no-register"}); err != nil {
		t.Fatal(err, out)
	}
	dir := filepath.Join(root, "pub")
	want := []string{"gh", "repo", "create", "unixsuperhero/pub", "--public", "--source", dir, "--remote", "origin", "--push"}
	if len(*gh) != 1 || !slices.Equal((*gh)[0], want) {
		t.Fatalf("gh %q", *gh)
	}
	*gh = nil
	if err := tl.Main([]string{"new", "priv", "--root", root, "--github", "--no-register"}); err != nil {
		t.Fatal(err, out)
	}
	if len(*gh) != 1 || (*gh)[0][4] != "--private" {
		t.Fatalf("gh %q", *gh)
	}
}

// homeRepo is a temp "home" that is itself a git repository, with an
// un-inited project directory under proj.
func homeRepo(t *testing.T, tl *Tool) string {
	git(t, tl, tl.Home, "init", "--quiet")
	sub := filepath.Join(tl.Home, "proj", "forgot")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestCheck(t *testing.T) {
	tl, out, _ := tool(t)
	sub := homeRepo(t, tl)
	err := tl.Main([]string{"check", sub})
	if err == nil || !strings.Contains(err.Error(), "home directory") {
		t.Fatalf("err %v", err)
	}
	if err := tl.Main([]string{"new", "good", "--root", filepath.Join(tl.Home, "proj"), "--no-register"}); err != nil {
		t.Fatal(err, out)
	}
	good := filepath.Join(tl.Home, "proj", "good")
	out.Reset()
	if err := tl.Main([]string{"check", good}); err != nil {
		t.Fatal(err)
	}
	if !samePath(strings.TrimSpace(out.String()), good) {
		t.Fatalf("out %q", out)
	}
	// a subdirectory of a project's own repo passes
	if err := os.MkdirAll(filepath.Join(good, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := tl.Main([]string{"check", filepath.Join(good, "cmd")}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRepoAboveRoot(t *testing.T) {
	tl, _, _ := tool(t)
	other := filepath.Join(tl.Home, "work")
	if err := os.MkdirAll(filepath.Join(other, "proj", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, tl, other, "init", "--quiet")
	err := tl.Main([]string{"check", filepath.Join(other, "proj", "x"), "--root", filepath.Join(other, "proj")})
	if err == nil || !strings.Contains(err.Error(), "contains the project root") {
		t.Fatalf("err %v", err)
	}
}

func TestNewAbortsWhenToplevelIsAParent(t *testing.T) {
	tl, _, _ := tool(t)
	homeRepo(t, tl)
	// a fake git whose init does nothing, so the dir stays inside the home repo
	real := tl.Run
	tl.Run = func(dir, name string, args ...string) ([]byte, error) {
		if name == "git" && args[0] == "init" {
			return nil, nil
		}
		return real(dir, name, args...)
	}
	err := tl.Main([]string{"new", "forgot", "--root", filepath.Join(tl.Home, "proj"), "--existing", "--no-register"})
	if err == nil || !strings.Contains(err.Error(), "ABORT") {
		t.Fatalf("err %v", err)
	}
	if out, _ := exec.Command("git", "-C", tl.Home, "rev-parse", "--verify", "--quiet", "HEAD").Output(); len(out) > 0 {
		t.Fatal("committed into the home repo")
	}
}

func TestList(t *testing.T) {
	tl, out, _ := tool(t)
	data := filepath.Join(tl.Home, "mtm.db")
	root := filepath.Join(tl.Home, "proj")
	if err := tl.Main([]string{"new", "listed", "--root", root, "-data", data}); err != nil {
		t.Fatal(err, out)
	}
	out.Reset()
	if err := tl.Main([]string{"list", "-data", data}); err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Name   string      `json:"name"`
		Trees  []work.Tree `json:"trees"`
		Origin string      `json:"origin"`
	}
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err, out)
	}
	if len(rows) != 1 || rows[0].Name != "listed" || rows[0].Origin != Origin || len(rows[0].Trees) != 1 || rows[0].Trees[0].Path != filepath.Join(root, "listed") {
		t.Fatalf("rows %+v", rows)
	}
}

// licensed runs new with args in a fresh temp home and returns the project dir.
func licensed(t *testing.T, tl *Tool, args ...string) string {
	t.Helper()
	root := filepath.Join(tl.Home, "proj")
	if err := tl.Main(append([]string{"new", "lic", "--root", root, "--no-register"}, args...)); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "lic")
}

func TestNewLicenseDefaultIsMIT(t *testing.T) {
	tl, _, _ := tool(t)
	cfg := filepath.Join(tl.Home, "gitconfig")
	if err := os.WriteFile(cfg, []byte("[user]\n\tname = Ada Lovelace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tl.Env = append(tl.Env, "GIT_CONFIG_GLOBAL="+cfg)
	dir := licensed(t, tl)
	body, err := os.ReadFile(filepath.Join(dir, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "MIT License\n\nCopyright (c) 2026 Ada Lovelace\n") || !strings.Contains(string(body), "WITHOUT WARRANTY OF ANY KIND") {
		t.Fatalf("LICENSE %q", body)
	}
	if got := git(t, tl, dir, "ls-tree", "--name-only", "HEAD"); got != ".gitignore\nAGENTS.md\nLICENSE" {
		t.Fatalf("tree %q", got)
	}
}

func TestNewLicenseHolderFallsBackToUser(t *testing.T) {
	tl, _, _ := tool(t)
	tl.Env = append(tl.Env, "USER=someone")
	body, err := os.ReadFile(filepath.Join(licensed(t, tl, "--license", "mit"), "LICENSE"))
	if err != nil || !strings.Contains(string(body), "Copyright (c) 2026 someone\n") {
		t.Fatalf("LICENSE %q %v", body, err)
	}
}

func TestNewLicenseNone(t *testing.T) {
	tl, _, _ := tool(t)
	dir := licensed(t, tl, "--license", "none")
	if _, err := os.Stat(filepath.Join(dir, "LICENSE")); !os.IsNotExist(err) {
		t.Fatalf("LICENSE written: %v", err)
	}
	if got := git(t, tl, dir, "ls-tree", "--name-only", "HEAD"); got != ".gitignore\nAGENTS.md" {
		t.Fatalf("tree %q", got)
	}
}

func TestNewLicenseKeepsExisting(t *testing.T) {
	tl, out, _ := tool(t)
	dir := filepath.Join(tl.Home, "proj", "lic")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	licensed(t, tl, "--existing")
	if body, _ := os.ReadFile(filepath.Join(dir, "LICENSE")); string(body) != "mine\n" {
		t.Fatalf("LICENSE overwritten: %q", body)
	}
	if !strings.Contains(out.String(), "keep: "+filepath.Join(dir, "LICENSE")+" exists") {
		t.Fatalf("out %q", out)
	}
}

func TestNewLicenseRejectsUnknown(t *testing.T) {
	tl, _, _ := tool(t)
	root := filepath.Join(tl.Home, "proj")
	err := tl.Main([]string{"new", "lic", "--root", root, "--no-register", "--license", "gpl"})
	if err == nil || !strings.Contains(err.Error(), `--license "gpl": choose one of mit, none`) {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "lic")); !os.IsNotExist(err) {
		t.Fatal("a directory was made for a refused license")
	}
}

const readme = "# {{.Name}}\nmodule {{.Module}} on {{.Date}} by {{.User}}\n"

func catalog(t *testing.T, tl *Tool) {
	t.Helper()
	dir := filepath.Join(tl.TemplateDir(), "plain")
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "{{.Name}}"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"README.md.tmpl":             readme,
		"LICENSE":                    "keep {{.Name}} literal\n",
		"cmd/{{.Name}}/main.go.tmpl": "package main // {{.Name}}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeTar(t, filepath.Join(tl.TemplateDir(), "packed.tar.gz"), map[string]string{
		"README.md.tmpl": readme,
		"./notes.txt":    "plain\n",
	})
}

func writeTar(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(files[n])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(files[n])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTemplatesListing(t *testing.T) {
	tl, out, _ := tool(t)
	catalog(t, tl)
	if err := tl.Main([]string{"templates"}); err != nil {
		t.Fatal(err)
	}
	want := "packed\ttar.gz\t" + filepath.Join(tl.TemplateDir(), "packed.tar.gz") + "\nplain\tdir\t" + filepath.Join(tl.TemplateDir(), "plain") + "\n"
	if out.String() != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestNewWithTemplate(t *testing.T) {
	const wantReadme = "# app\nmodule github.com/unixsuperhero/app on 2026-10-24 by tester\n"
	for _, tc := range []struct {
		template string
		files    map[string]string
	}{
		{"plain", map[string]string{"README.md": wantReadme, "LICENSE": "keep {{.Name}} literal\n", "cmd/app/main.go": "package main // app\n"}},
		{"packed", map[string]string{"README.md": wantReadme, "notes.txt": "plain\n"}},
	} {
		t.Run(tc.template, func(t *testing.T) {
			tl, out, _ := tool(t)
			catalog(t, tl)
			root := filepath.Join(tl.Home, "proj")
			if err := tl.Main([]string{"new", "app", "--root", root, "--template", tc.template, "--no-register"}); err != nil {
				t.Fatal(err, out)
			}
			dir := filepath.Join(root, "app")
			committed := strings.Split(git(t, tl, dir, "ls-tree", "-r", "--name-only", "HEAD"), "\n")
			for name, body := range tc.files {
				got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
				if err != nil || string(got) != body {
					t.Fatalf("%s: %q %v", name, got, err)
				}
				if !slices.Contains(committed, name) {
					t.Fatalf("%s not committed: %q", name, committed)
				}
				if c := git(t, tl, dir, "show", "HEAD:"+name); c != strings.TrimSpace(body) {
					t.Fatalf("committed %s %q", name, c)
				}
			}
			for _, name := range committed {
				if strings.HasSuffix(name, ".tmpl") {
					t.Fatalf("a .tmpl was committed: %s", name)
				}
			}
		})
	}
}

func TestTemplateByPath(t *testing.T) {
	tl, out, _ := tool(t)
	catalog(t, tl)
	root := filepath.Join(tl.Home, "proj")
	path := filepath.Join(tl.TemplateDir(), "packed.tar.gz")
	if err := tl.Main([]string{"new", "bypath", "--root", root, "--template", path, "--no-register"}); err != nil {
		t.Fatal(err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "bypath", "notes.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestTemplateEscapeRejected(t *testing.T) {
	for _, name := range []string{"../evil.txt", "/abs.txt", "a/../../evil.txt", "{{.Root}}/../evil.txt"} {
		t.Run(name, func(t *testing.T) {
			tl, _, _ := tool(t)
			bad := filepath.Join(t.TempDir(), "bad.tar.gz")
			writeTar(t, bad, map[string]string{name: "x"})
			root := filepath.Join(tl.Home, "proj")
			err := tl.Main([]string{"new", "victim", "--root", root, "--template", bad, "--no-register"})
			if err == nil || !strings.Contains(err.Error(), "escapes") {
				t.Fatalf("err %v", err)
			}
			for _, p := range []string{filepath.Join(root, "evil.txt"), "/abs.txt", filepath.Join(tl.Home, "evil.txt")} {
				if _, err := os.Stat(p); err == nil {
					t.Fatalf("%s was written", p)
				}
			}
		})
	}
}

func TestProgName(t *testing.T) {
	for parent, want := range map[string]string{"": "o-proj", "mtm": "mtm proj", "o": "o proj"} {
		if got := progName(func(k string) string { return map[string]string{"ODEN_PARENT": parent}[k] }); got != want {
			t.Fatalf("ODEN_PARENT=%q: %q, want %q", parent, got, want)
		}
	}
}

func TestCatalogDescribesProjectNoun(t *testing.T) {
	tl, out, _ := tool(t)
	if err := tl.Main([]string{"catalog"}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema int    `json:"schema"`
		Name   string `json:"name"`
		Nouns  []struct {
			Name  string `json:"name"`
			Verbs []struct {
				Name   string                     `json:"name"`
				Kind   string                     `json:"kind"`
				Rows   string                     `json:"rows"`
				Params map[string]json.RawMessage `json:"params"`
			} `json:"verbs"`
		} `json:"nouns"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err, out)
	}
	if doc.Schema != 1 || doc.Name != "o-proj" {
		t.Fatalf("schema %d name %q", doc.Schema, doc.Name)
	}
	verbs := map[string]string{}
	for _, n := range doc.Nouns {
		if n.Name != "project" {
			continue
		}
		for _, v := range n.Verbs {
			verbs[v.Name] = v.Kind + "/" + v.Rows
		}
	}
	want := map[string]string{"list": "application/project", "new": "application/", "check": "application/", "templates": "application/", "open": "application/", "link": "application/", "unlink": "application/"}
	if !maps.Equal(verbs, want) {
		t.Fatalf("project verbs %v, want %v", verbs, want)
	}
}

func TestProjectShellNewListOpen(t *testing.T) {
	tl, out, _ := tool(t)
	data := filepath.Join(tl.Home, "mtm.db")
	root := filepath.Join(tl.Home, "proj")
	if err := tl.Main([]string{"-data", data, "project", "new", "shelled", "--root", root, "--license", "none"}); err != nil {
		t.Fatal(err, out)
	}
	dir := filepath.Join(root, "shelled")
	for _, line := range []string{"git init: " + dir, "commit: ", "register: project shelled", "cd " + dir, `"dir": "` + dir + `"`} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("missing %q in %q", line, out)
		}
	}
	if got := git(t, tl, dir, "ls-tree", "--name-only", "HEAD"); got != ".gitignore\nAGENTS.md" {
		t.Fatalf("tree %q", got)
	}

	out.Reset()
	if err := tl.Main([]string{"-data", data, "project", "list", "--json", "{}"}); err != nil {
		t.Fatal(err, out)
	}
	var rows []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Data  struct {
			Trees []work.Tree `json:"trees"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err, out)
	}
	if len(rows) != 1 || rows[0].ID == "" || rows[0].Label != "shelled" || len(rows[0].Data.Trees) != 1 || rows[0].Data.Trees[0].Path != dir {
		t.Fatalf("rows %+v", rows)
	}

	out.Reset()
	if err := tl.Main([]string{"-data", data, "project", "open", "--target", rows[0].ID, "--json", "{}", "--yes"}); err != nil {
		t.Fatal(err, out)
	}
	var trees []work.Tree
	if err := json.Unmarshal(out.Bytes(), &trees); err != nil || len(trees) != 1 || trees[0].Path != dir {
		t.Fatalf("open %q %v", out, err)
	}
	if err := tl.Main([]string{"-data", data, "project", "open"}); err == nil || !strings.Contains(err.Error(), "--target") {
		t.Fatalf("open without target: %v", err)
	}
}

func TestProjectShellNewFlags(t *testing.T) {
	tl, out, gh := tool(t)
	root := filepath.Join(tl.Home, "proj")
	if err := tl.Main([]string{"project", "new", "--root", root, "pub", "--github", "--private=false", "--register=false"}); err != nil {
		t.Fatal(err, out)
	}
	if len(*gh) != 1 || (*gh)[0][4] != "--public" {
		t.Fatalf("gh %q", *gh)
	}
	if !strings.Contains(out.String(), "register: skipped") {
		t.Fatalf("out %q", out)
	}
}

func TestProjectShellCheckAndTemplates(t *testing.T) {
	tl, out, _ := tool(t)
	sub := homeRepo(t, tl)
	if err := tl.Main([]string{"project", "check", sub}); err == nil || !strings.Contains(err.Error(), "home directory") {
		t.Fatalf("err %v", err)
	}
	out.Reset()
	if err := tl.Main([]string{"project", "templates"}); err != nil || strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("templates %q %v", out, err)
	}
	catalog(t, tl)
	out.Reset()
	if err := tl.Main([]string{"project", "templates"}); err != nil {
		t.Fatal(err)
	}
	var got []Template
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got) != 2 || got[0].Name != "packed" || got[1].Name != "plain" {
		t.Fatalf("templates %q %v", out, err)
	}
}
