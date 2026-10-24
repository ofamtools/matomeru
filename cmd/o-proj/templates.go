package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// A template is a directory or a .tar.gz copied into a new project after git
// init and before the initial commit. Every path may use text/template
// actions ({{.Name}}); a file whose name ends in .tmpl is rendered with Go's
// text/template (the Go analogue of erb) and written without the suffix.

// Template is one catalog entry.
type Template struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // dir or tar.gz
	Path string `json:"path"`
}

// TemplateData is what a template renders with.
type TemplateData struct {
	Name, Dir, Root, Date, User, Module string
}

// TemplateDir is the catalog: $XDG_CONFIG_HOME/matomeru/templates, else
// ~/.config/matomeru/templates.
func (t *Tool) TemplateDir() string {
	base := lookup(t.Env, "XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(t.Home, ".config")
	}
	return filepath.Join(base, "matomeru", "templates")
}

// Templates lists the catalog by name; a missing catalog is empty.
func (t *Tool) Templates() ([]Template, error) {
	dir := t.TemplateDir()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Template
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if tpl, ok := templateAt(p, info); ok {
			out = append(out, tpl)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func templateAt(p string, info fs.FileInfo) (Template, bool) {
	base := filepath.Base(p)
	switch {
	case info.IsDir():
		return Template{Name: base, Kind: "dir", Path: p}, true
	case info.Mode().IsRegular() && strings.HasSuffix(base, ".tar.gz"):
		return Template{Name: strings.TrimSuffix(base, ".tar.gz"), Kind: "tar.gz", Path: p}, true
	}
	return Template{}, false
}

// FindTemplate resolves a catalog name, else a path to a directory or a
// .tar.gz.
func (t *Tool) FindTemplate(word string) (Template, error) {
	catalog, err := t.Templates()
	if err != nil {
		return Template{}, err
	}
	for _, tpl := range catalog {
		if tpl.Name == word {
			return tpl, nil
		}
	}
	if abs, err := filepath.Abs(word); err == nil {
		if info, err := os.Stat(abs); err == nil {
			if tpl, ok := templateAt(abs, info); ok {
				return tpl, nil
			}
		}
	}
	return Template{}, fmt.Errorf("no template %q in %s and no directory or .tar.gz at that path (see %s templates)", word, t.TemplateDir(), t.Prog)
}

// Apply copies or extracts tpl into dir and returns the paths it wrote,
// relative to dir. It never overwrites a file and refuses any path that
// escapes dir.
func Apply(tpl Template, dir string, data TemplateData) ([]string, error) {
	var wrote []string
	put := func(name string, mode fs.FileMode, body []byte) error {
		rel, err := render("path "+name, name, data)
		if err != nil {
			return err
		}
		if strings.HasSuffix(rel, ".tmpl") {
			out, err := render(name, string(body), data)
			if err != nil {
				return err
			}
			rel, body = strings.TrimSuffix(rel, ".tmpl"), []byte(out)
		}
		target, err := inside(dir, rel)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("template %s: %s already exists", tpl.Name, target)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, body, mode.Perm()|0o600); err != nil {
			return err
		}
		wrote = append(wrote, filepath.ToSlash(rel))
		return nil
	}
	var err error
	if tpl.Kind == "dir" {
		err = copyDir(tpl.Path, put)
	} else {
		err = untar(tpl.Path, put)
	}
	return wrote, err
}

func copyDir(root string, put func(string, fs.FileMode, []byte) error) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("template %s: %s is not a regular file", root, p)
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		return put(filepath.ToSlash(rel), info.Mode(), body)
	})
}

func untar(archive string, put func(string, fs.FileMode, []byte) error) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("template %s: %w", archive, err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("template %s: %w", archive, err)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return fmt.Errorf("template %s: %s is not a regular file or directory", archive, h.Name)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := put(h.Name, h.FileInfo().Mode(), body); err != nil {
			return err
		}
	}
}

// inside joins rel to dir, refusing absolute paths and any that leave dir.
func inside(dir, rel string) (string, error) {
	clean := path.Clean(strings.TrimPrefix(filepath.ToSlash(rel), "./"))
	if path.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("template entry %q escapes %s", rel, dir)
	}
	return filepath.Join(dir, filepath.FromSlash(clean)), nil
}

func render(name, text string, data TemplateData) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	tmpl, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
