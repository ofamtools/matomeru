package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtensionCatalogAndInstallCLI(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	goDir := filepath.Join(dir, "godir")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(goDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pre-install o-git in binDir.
	gitScript := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'o-git 2.1.0'; exit 0; fi\n"
	if err := os.WriteFile(filepath.Join(binDir, "o-git"), []byte(gitScript), 0o755); err != nil {
		t.Fatal(err)
	}

	// Provide a fake `go` binary on PATH that installs o-csv into binDir and prints GOPRIVATE.
	goScript := `#!/bin/sh
echo "GOPRIVATE=$GOPRIVATE ARGS=$*"
if [ "$1" = "install" ]; then
  case "$2" in
  *o-csv*)
    printf '#!/bin/sh\nif [ "$1" = "--version" ]; then echo "o-csv 3.0.0"; exit 0; fi\n' > "` + binDir + `/o-csv"
    chmod +x "` + binDir + `/o-csv"
    exit 0
    ;;
  esac
fi
echo "unknown package $2" >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(goDir, "go"), []byte(goScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", goDir+":"+binDir+":/usr/bin:/bin")

	c := cli{t: t, db: filepath.Join(dir, "mtm.db")}

	// 1. mtm extension catalog --json lists all 20 OFAM tools with status.
	items := decode[[]struct {
		Name     string `json:"name"`
		Category string `json:"category"`
		Package  string `json:"package"`
		Status   string `json:"status"`
		Version  string `json:"version"`
	}](t, c.ok("extension", "catalog", "--dir", binDir, "--json"))
	if len(items) != 20 {
		t.Fatalf("catalog length = %d, want 20", len(items))
	}
	byName := map[string]struct {
		Category, Package, Status, Version string
	}{}
	for _, it := range items {
		byName[it.Name] = struct{ Category, Package, Status, Version string }{it.Category, it.Package, it.Status, it.Version}
	}
	if byName["o-git"].Status != "healthy" || byName["o-git"].Version != "o-git 2.1.0" {
		t.Fatalf("o-git catalog item = %+v", byName["o-git"])
	}
	if byName["o-csv"].Status != "available" || byName["o-csv"].Category != "core" {
		t.Fatalf("o-csv catalog item = %+v", byName["o-csv"])
	}
	if byName["o-koe"].Package != "ofam.dev/o-koe/cmd/o-koe@latest" || byName["o-koe"].Category != "optional" {
		t.Fatalf("o-koe catalog item = %+v", byName["o-koe"])
	}

	// 2. mtm extension catalog (default table).
	tbl := c.ok("extension", "catalog", "--dir", binDir)
	if !strings.Contains(tbl, "NAME") || !strings.Contains(tbl, "o-git") || !strings.Contains(tbl, "o-csv") || !strings.Contains(tbl, "available") {
		t.Fatalf("catalog table:\n%s", tbl)
	}

	// 3. mtm extension discover --catalog includes available catalog rows.
	disc := decode[[]struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}](t, c.ok("extension", "discover", "--dir", binDir, "--catalog"))
	if len(disc) < 20 {
		t.Fatalf("discover --catalog returned %d entries, want >= 20", len(disc))
	}

	// 4. mtm extension install o-csv installs via fake go, waits, rediscovers, returns JSON.
	inst := decode[[]struct {
		Name     string `json:"name"`
		Status   string `json:"status"`
		ExitCode int    `json:"exitCode"`
		Output   string `json:"output"`
		Version  string `json:"version"`
		Binary   string `json:"binary"`
	}](t, c.ok("extension", "install", "o-csv", "--dir", binDir, "--goprivate", "ofam.dev", "--format", "json"))
	if len(inst) != 1 || inst[0].Name != "o-csv" || inst[0].Status != "installed" || inst[0].ExitCode != 0 ||
		!strings.Contains(inst[0].Output, "GOPRIVATE=ofam.dev") || inst[0].Version != "o-csv 3.0.0" {
		t.Fatalf("extension install json = %+v", inst)
	}

	// 5. Table output works too.
	instTbl := c.ok("extension", "install", "o-csv", "--dir", binDir, "--format", "table")
	if !strings.Contains(instTbl, "NAME") || !strings.Contains(instTbl, "o-csv") || !strings.Contains(instTbl, "installed") {
		t.Fatalf("extension install table:\n%s", instTbl)
	}

	// 6. Unknown extension fails cleanly.
	c.fails("extension", "install", "o-totally-unknown")
	c.fails("extension", "install")
}
