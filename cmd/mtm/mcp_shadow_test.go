package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/unixsuperhero/oden"
)

// An extension registered as mcp (o-mcp) mounts its nouns under the
// namespace mcp, but mtm's own mcp command (the catalog MCP gateway
// harnesses run as mtm mcp serve) keeps the word; the extension's nouns
// stay reachable by their full names and bare aliases, and help names the
// shadowed namespace.
func TestMCPCommandWinsOverAnExtensionNamedMCP(t *testing.T) {
	t.Parallel()
	bin, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "mcp", "--bin", bin)
	dir := t.TempDir() // no PATH tools: never the owner's o-mcp
	run := func(stdin string, args ...string) (string, string, error) {
		var out, errOut bytes.Buffer
		err := openAt(t, path).commands(args, strings.NewReader(stdin), &out, &errOut, nil, oden.WithPath(dir)).Run(context.Background())
		return out.String(), errOut.String(), err
	}
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}` + "\n"
	if out, errOut, err := run(initialize, "mcp", "serve"); err != nil || !strings.Contains(out, `"protocolVersion"`) {
		t.Fatalf("mtm mcp serve: %v\n%s\n%s", err, out, errOut)
	}
	if out, errOut, err := run("", "mcp", "instructions"); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("mtm mcp instructions: %v\n%s\n%s", err, out, errOut)
	}
	for _, args := range [][]string{{"mcp.item", "list"}, {"mcp.", "item", "list"}, {"item", "list"}} {
		if out, errOut, err := run("", args...); err != nil || !strings.Contains(out, "one") {
			t.Fatalf("mtm %s: %v\n%s\n%s", strings.Join(args, " "), err, out, errOut)
		}
	}
	out, errOut, err := run("", "--help")
	if err != nil || !strings.Contains(out, "shadows alias mcp of mcp. namespace") || !strings.Contains(out, "\n  mcp.item, item ") {
		t.Fatalf("mtm --help: %v\n%s\n%s", err, out, errOut)
	}
}
