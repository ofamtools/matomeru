// Command gen writes internal/notices/data from `go list -deps` and the
// module cache: for each linked module its licence and notice files, the
// texts deduplicated by hash. Run from the repository root:
//
//	go run ./internal/notices/gen            write the data
//	go run ./internal/notices/gen -check     fail when the data on disk is not what a write would produce
//
// Why not go-licenses: it needs a tool dependency, a template per output and
// a classifier library, and still stops at the first unknown licence; what
// mtm needs is small (a list of modules, a file each, a name for its text),
// and the same list drives the tests. Platforms: the union over darwin, linux
// and windows, so the data does not depend on the machine that generated it.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"ofam.dev/matomeru/internal/notices"
)

const target = "./cmd/mtm"

type pkg struct {
	Dir      string
	Standard bool
	Module   *struct {
		Path, Version, Dir string
		Main               bool
	}
}

type found struct {
	path, version, dir string
	main               bool
	dirs               map[string]bool // directories of linked packages
}

var licenseName = regexp.MustCompile(`(?i)^(licen[sc]e|copying|unlicense|notice|copyright|patents?)([-._].*)?$`)
var backtick = regexp.MustCompile("`([^`\\s]+)`")

func main() {
	check := flag.Bool("check", false, "compare with the data on disk instead of writing")
	out := flag.String("out", "internal/notices/data", "output directory")
	flag.Parse()
	if err := run(*out, *check); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(out string, check bool) error {
	gomod, err := os.ReadFile("go.mod")
	if err != nil {
		return err
	}
	requires := notices.ParseRequires(string(gomod))
	full := map[string]*found{}
	small := map[string]*found{}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		if err := collect(full, goos, ""); err != nil {
			return err
		}
		if err := collect(small, goos, "nodaemon"); err != nil {
			return err
		}
	}
	root, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return err
	}
	goroot := strings.TrimSpace(string(root))
	// Homebrew moves Go's LICENSE out of GOROOT, next to libexec.
	stdLicense := filepath.Join(goroot, "LICENSE")
	if _, err := os.Stat(stdLicense); err != nil {
		stdLicense = filepath.Join(filepath.Dir(goroot), "LICENSE")
	}

	files := map[string]string{} // hash to text
	var ix notices.Index
	paths := make([]string, 0, len(full))
	for p := range full {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		f := full[p]
		m := notices.Module{Path: p, Version: f.version, Main: f.main}
		var fl []notices.File
		var err error
		if p == "std" {
			m.Std, m.Version = true, ""
			fl, err = scan(p, goroot, []string{goroot}, files, "LICENSE", "PATENTS")
			if _, serr := os.Stat(filepath.Join(goroot, "LICENSE")); serr != nil && err == nil {
				b, rerr := os.ReadFile(stdLicense)
				if rerr != nil {
					return fmt.Errorf("the Go licence is in neither %s nor %s", goroot, filepath.Dir(goroot))
				}
				h := notices.Hash(string(b))
				files[h] = string(b)
				fl = append([]notices.File{{Name: "LICENSE", Kind: notices.KindLicense, SPDX: notices.Classify(string(b)), Text: h}}, fl...)
			}
		} else {
			fl, err = scan(p, f.dir, dirsOf(f), files)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		m.Files = fl
		_, inSmall := small[p]
		m.DaemonOnly = !inSmall
		ix.Modules = append(ix.Modules, m)
	}
	var unlinked []string
	for p := range requires {
		if _, ok := full[p]; !ok {
			unlinked = append(unlinked, p)
		}
	}
	sort.Strings(unlinked)
	for _, p := range unlinked {
		ix.Unlinked = append(ix.Unlinked, notices.Module{Path: p, Version: requires[p]})
	}
	return write(out, ix, files, check)
}

// collect lists the packages target links for goos (CGO off, so a
// cross-platform list works from any machine) and adds their modules.
func collect(into map[string]*found, goos, tags string) error {
	args := []string{"list", "-deps", "-json"}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	cmd := exec.Command("go", append(args, target)...)
	cmd.Env = append(os.Environ(), "GOOS="+goos, "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("go list (%s %s): %v\n%s", goos, tags, err, stderr.String())
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			return err
		}
		path, ver, dir, main := "std", "", "", false
		if !p.Standard {
			if p.Module == nil {
				continue
			}
			path, ver, dir, main = p.Module.Path, p.Module.Version, p.Module.Dir, p.Module.Main
		}
		f := into[path]
		if f == nil {
			f = &found{path: path, version: ver, dir: dir, main: main, dirs: map[string]bool{}}
			into[path] = f
		}
		f.dirs[p.Dir] = true
	}
	return nil
}

// dirsOf is the directories of f's linked packages, sorted.
func dirsOf(f *found) []string {
	var d []string
	for k := range f.dirs {
		d = append(d, k)
	}
	sort.Strings(d)
	return d
}

// scan is a module's licence and notice files: those at its root, in the
// directories of its linked packages and above them up to the root, and
// those its THIRD_PARTY.md names (copied notices). names, when given, are
// the only root files taken (the standard library).
func scan(module, root string, pkgDirs []string, texts map[string]string, names ...string) ([]notices.File, error) {
	rels := map[string]bool{}
	if len(names) > 0 {
		namedFiles(root, names, rels)
	} else {
		for _, d := range pkgDirs {
			for dir := d; strings.HasPrefix(dir, root); dir = filepath.Dir(dir) {
				licenseFilesIn(root, dir, rels)
				if dir == root {
					break
				}
			}
		}
		licenseFilesIn(root, root, rels)
		thirdPartyFiles(root, rels)
	}
	var names2 []string
	for r := range rels {
		names2 = append(names2, r)
	}
	sort.Strings(names2)
	var out []notices.File
	for _, rel := range names2 {
		f, err := noticeFile(module, root, rel, texts)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// licenseFilesIn adds the licence-named files directly in dir, as paths under root.
func licenseFilesIn(root, dir string, rels map[string]bool) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.Type().IsRegular() && licenseName.MatchString(e.Name()) {
			if rel, err := filepath.Rel(root, filepath.Join(dir, e.Name())); err == nil {
				rels[filepath.ToSlash(rel)] = true
			}
		}
	}
}

// namedFiles adds the root files in names that exist.
func namedFiles(root string, names []string, rels map[string]bool) {
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(root, n)); err == nil {
			rels[n] = true
		}
	}
}

// thirdPartyFiles adds the notices a fork copies into its tree and lists in THIRD_PARTY.md.
func thirdPartyFiles(root string, rels map[string]bool) {
	b, err := os.ReadFile(filepath.Join(root, "THIRD_PARTY.md"))
	if err != nil {
		return
	}
	for _, m := range backtick.FindAllStringSubmatch(string(b), -1) {
		if !licenseName.MatchString(filepath.Base(m[1])) {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(root, m[1]))
		for _, mm := range matches {
			if st, err := os.Stat(mm); err == nil && st.Mode().IsRegular() {
				rel, _ := filepath.Rel(root, mm)
				rels[filepath.ToSlash(rel)] = true
			}
		}
	}
}

// noticeFile reads one file under root, records its text by hash, and classifies it.
func noticeFile(module, root, rel string, texts map[string]string) (notices.File, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return notices.File{}, err
	}
	text := string(b)
	h := notices.Hash(text)
	texts[h] = text
	kind := notices.KindLicense
	base := strings.ToLower(filepath.Base(rel))
	if strings.HasPrefix(base, "notice") || strings.HasPrefix(base, "patent") || notices.Reviewed[module+" "+rel] != "" {
		kind = notices.KindNotice
	}
	spdx := notices.Classify(text)
	if len(spdx) == 0 && kind == notices.KindLicense {
		spdx = []string{notices.Unknown}
	}
	return notices.File{Name: rel, Kind: kind, SPDX: spdx, Text: h}, nil
}

// write lays out the data directory, or with check compares it.
func write(out string, ix notices.Index, texts map[string]string, check bool) error {
	want := map[string][]byte{}
	b, err := json.MarshalIndent(ix, "", " ")
	if err != nil {
		return err
	}
	want["notices.json"] = append(b, '\n')
	for h, t := range texts {
		want["texts/"+h+".txt"] = []byte(t)
	}
	have := map[string]bool{}
	filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			rel, _ := filepath.Rel(out, p)
			have[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	var diff []string
	for name, content := range want {
		old, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(old, content) {
			diff = append(diff, "changed or missing: "+name)
		}
	}
	for name := range have {
		if _, ok := want[name]; !ok {
			diff = append(diff, "stale: "+name)
		}
	}
	sort.Strings(diff)
	if check {
		if len(diff) > 0 {
			return fmt.Errorf("the notices data is out of date (make licenses):\n%s", strings.Join(diff, "\n"))
		}
		return nil
	}
	for _, d := range diff {
		name := strings.TrimPrefix(strings.TrimPrefix(d, "changed or missing: "), "stale: ")
		p := filepath.Join(out, filepath.FromSlash(name))
		if strings.HasPrefix(d, "stale: ") {
			if err := os.Remove(p); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, want[name], 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("%d modules, %d texts, %d files written\n", len(ix.Modules), len(texts), len(diff))
	return nil
}
