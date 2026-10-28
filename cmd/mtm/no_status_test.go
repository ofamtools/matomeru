package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// deprecatedOchazuke are the ochazuke names that drew plain messages as
// errors: Status read as "any status" but is the error tone. Every
// message says Error (a failure) or Info (anything else).
//
// Interim: once mtm requires the ochazuke release with strictcheck, this
// test becomes `strictcheck.Check(t)`, which catches every deprecated
// name (tests included) by building with -tags ochazuke_strict.
var deprecatedOchazuke = map[string]string{
	"Ctx.Status": "Error for a failure, Info for anything else",
	"UI.Status":  "Error for a failure, Info for anything else",
	"UIStatus":   "UIError",
	"InfoSink":   "UISink",
}

// No non-test code in the module uses a deprecated ochazuke name. The
// check type-checks each package against its dependencies' export data,
// so a Status method of mtm's own types (setup.Options.Status) is fine.
func TestNoDeprecatedOchazukeStatus(t *testing.T) {
	out, err := exec.Command("go", "list", "-export", "-deps", "-json=ImportPath,Dir,GoFiles,Export,Module", "./...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	type pkg struct {
		ImportPath, Dir, Export string
		GoFiles                 []string
		Module                  *struct {
			Path string
			Main bool
		}
	}
	exports := map[string]string{}
	var mine []pkg
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p pkg
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		exports[p.ImportPath] = p.Export
		if p.Module != nil && p.Module.Main {
			mine = append(mine, p)
		}
	}
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		return os.Open(exports[path])
	})
	var found []string
	for _, p := range mine {
		var files []*ast.File
		for _, name := range p.GoFiles {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
		info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
		if _, err := (&types.Config{Importer: imp}).Check(p.ImportPath, fset, files, info); err != nil {
			t.Fatalf("type-check %s: %v", p.ImportPath, err)
		}
		for id, obj := range info.Uses {
			if obj.Pkg() == nil || obj.Pkg().Path() != "github.com/unixsuperhero/ochazuke" {
				continue
			}
			name := obj.Name()
			if fn, ok := obj.(*types.Func); ok {
				if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
					rt := recv.Type()
					if ptr, ok := rt.(*types.Pointer); ok {
						rt = ptr.Elem()
					}
					if named, ok := rt.(*types.Named); ok {
						name = named.Obj().Name() + "." + name
					}
				}
			}
			if use := deprecatedOchazuke[name]; use != "" {
				found = append(found, fset.Position(id.Pos()).String()+": ochazuke."+name+" is deprecated; use "+use)
			}
		}
	}
	if len(found) > 0 {
		t.Fatalf("deprecated ochazuke names in non-test code:\n%s", strings.Join(found, "\n"))
	}
}
