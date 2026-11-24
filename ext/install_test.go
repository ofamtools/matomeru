package ext

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/runs"
	"ofam.dev/ochazuke/core"
)

func TestDefaultCatalogAndLookup(t *testing.T) {
	entries := CatalogEntries()
	if len(entries) != 20 {
		t.Fatalf("DefaultCatalog has %d entries, want 20", len(entries))
	}
	coreCount, optCount := 0, 0
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.Name] {
			t.Fatalf("duplicate catalog entry %q", e.Name)
		}
		seen[e.Name] = true
		if e.Package == "" || e.Summary == "" {
			t.Fatalf("incomplete catalog entry %+v", e)
		}
		switch e.Category {
		case CategoryCore:
			coreCount++
		case CategoryOptional:
			optCount++
		default:
			t.Fatalf("unexpected category %q for %s", e.Category, e.Name)
		}
	}
	if coreCount != 14 || optCount != 6 {
		t.Fatalf("catalog split core=%d optional=%d, want 14 and 6", coreCount, optCount)
	}
	koe, ok := LookupCatalog("o-koe")
	if !ok || koe.Package != "ofam.dev/o-koe/cmd/o-koe@latest" || koe.Category != CategoryOptional {
		t.Fatalf("LookupCatalog(o-koe) = %+v, %v", koe, ok)
	}
	if _, ok := LookupCatalog("o-does-not-exist"); ok {
		t.Fatal("LookupCatalog found unknown tool")
	}
}

func TestDiscoverIncludeCatalog(t *testing.T) {
	s := openStore(t)
	dir := t.TempDir()
	script(t, dir, "o-git", described, 0o755)

	// Without IncludeCatalog, missing catalog tools do not get records,
	// while installed tools still get enriched with installPkg and category.
	mOff := New(Options{Tools: []string{"o-git", "o-csv"}, IncludeCatalog: false})
	resOff, err := mOff.Discover(context.Background(), []string{dir}, s)
	if err != nil {
		t.Fatalf("Discover(IncludeCatalog=false): %v", err)
	}
	gitOff := byName(t, resOff, "o-git")
	if gitOff.Record == nil || gitOff.Record.Text("installPkg") != "ofam.dev/o-git@latest" || gitOff.Record.Text("category") != CategoryCore || gitOff.Record.Text("summary") != "git views" {
		t.Fatalf("o-git enriched = %+v", gitOff.Record)
	}
	if csvOff := byName(t, resOff, "o-csv"); csvOff.Record != nil {
		t.Fatalf("o-csv should not be created when IncludeCatalog=false: %+v", csvOff.Record)
	}

	// With IncludeCatalog=true, missing catalog tools are upserted as StatusAvailable.
	customCatalog := []CatalogEntry{
		{Name: "o-git", Package: "ofam.dev/o-git@latest", Summary: "catalog git summary", Category: CategoryCore},
		{Name: "o-csv", Package: "ofam.dev/o-csv@latest", Summary: "CSV tools", Category: CategoryCore},
		{Name: "o-koe", Package: "ofam.dev/o-koe/cmd/o-koe@latest", Summary: "Voice helper", Category: CategoryOptional},
	}
	mOn := New(Options{Tools: []string{"o-git"}, Catalog: customCatalog, IncludeCatalog: true})
	resOn, err := mOn.Discover(context.Background(), []string{dir}, s)
	if err != nil {
		t.Fatalf("Discover(IncludeCatalog=true): %v", err)
	}
	// Existing healthy o-git keeps its probed summary ("git views"), not overwritten by catalog summary.
	gitOn := byName(t, resOn, "o-git")
	if gitOn.Status != StatusHealthy || gitOn.Record.Text("summary") != "git views" {
		t.Fatalf("healthy o-git after catalog discover = %+v", gitOn.Record)
	}
	csvOn := byName(t, resOn, "o-csv")
	if csvOn.Status != StatusAvailable || csvOn.Record == nil || csvOn.Record.Text("runStatus") != StatusAvailable ||
		csvOn.Record.Text("installPkg") != "ofam.dev/o-csv@latest" || csvOn.Record.Text("category") != CategoryCore ||
		csvOn.Record.Text("summary") != "CSV tools" {
		t.Fatalf("available o-csv = %+v (rec=%+v)", csvOn, csvOn.Record)
	}
	koeOn := byName(t, resOn, "o-koe")
	if koeOn.Status != StatusAvailable || koeOn.Record == nil || koeOn.Record.Text("category") != CategoryOptional {
		t.Fatalf("available o-koe = %+v", koeOn)
	}

	// Rediscovery is idempotent (no new change sets).
	sets := changeSets(t, s)
	again, err := mOn.Discover(context.Background(), []string{dir}, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range again {
		if r.Changed {
			t.Fatalf("unexpected change on rediscover: %+v", r)
		}
	}
	if n := changeSets(t, s); n != sets {
		t.Fatalf("change sets %d -> %d on unchanged catalog rediscover", sets, n)
	}
}

func TestInstallAndTransitionToHealthy(t *testing.T) {
	s := openStore(t)
	binDir := t.TempDir()
	goDir := t.TempDir()

	// Fake `go` script: logs GOPRIVATE and args, and installs an executable `o-csv` into binDir.
	fakeGo := script(t, goDir, "fake-go", `
echo "GOPRIVATE=$GOPRIVATE ARGS=$*"
if [ "$1" = "install" ]; then
  cat > "`+binDir+`/o-csv" <<'EOF'
#!/bin/sh
case "$1" in
--version) echo "o-csv 2.0.0" ;;
capabilities) echo '{"summary":"CSV tools installed","capabilities":["csv.view"],"commands":["view"]}' ;;
esac
EOF
  chmod +x "`+binDir+`/o-csv"
  exit 0
fi
exit 1
`, 0o755)

	catalog := []CatalogEntry{
		{Name: "o-csv", Package: "ofam.dev/o-csv@latest", Summary: "CSV tools", Category: CategoryCore},
	}
	m := New(Options{
		Tools:          []string{"o-csv"},
		Catalog:        catalog,
		IncludeCatalog: true,
		GoBinary:       fakeGo,
		GoPrivate:      "ofam.dev",
	})
	ctx := context.Background()
	res, err := m.Discover(ctx, []string{binDir}, s)
	if err != nil {
		t.Fatal(err)
	}
	avail := byName(t, res, "o-csv")
	if avail.Status != StatusAvailable || avail.Record == nil {
		t.Fatalf("initial discover = %+v", avail)
	}

	runner := runs.New(s, runs.Options{})
	t.Cleanup(runner.Close)

	// Unknown extension fails cleanly.
	if _, err := m.Install(ctx, "o-unknown-xyz", runner); err == nil {
		t.Fatal("Install of unknown extension did not return an error")
	}

	// RunCommand refuses an available (not-yet-installed) extension.
	if _, err := m.RunCommand(ctx, "o-csv", []string{"view"}, runner); err == nil {
		t.Fatal("RunCommand on available extension did not refuse")
	}

	// Install runs fake-go with GOPRIVATE=ofam.dev and records a run.
	runRec, err := m.Install(ctx, "o-csv", runner)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	<-runner.Done(runRec.ID)
	finalRun, err := s.Get(core.Ref{Kind: data.KindRun, ID: runRec.ID})
	if err != nil {
		t.Fatal(err)
	}
	if finalRun.Text("runStatus") != runs.StatusDone ||
		!strings.Contains(finalRun.Text("output"), "GOPRIVATE=ofam.dev") ||
		!strings.Contains(finalRun.Text("output"), "install ofam.dev/o-csv@latest") {
		t.Fatalf("install run = %+v", finalRun.Fields)
	}

	// Re-discover transitions the same extension record from available -> healthy.
	after, err := m.Discover(ctx, []string{binDir}, s)
	if err != nil {
		t.Fatal(err)
	}
	healthy := byName(t, after, "o-csv")
	if healthy.Status != StatusHealthy || healthy.Record == nil || healthy.Record.ID != avail.Record.ID ||
		healthy.Record.Text("runStatus") != StatusHealthy || healthy.Record.Text("version") != "o-csv 2.0.0" {
		t.Fatalf("post-install discover = %+v (rec=%+v)", healthy, healthy.Record)
	}
	if _, err := os.Stat(filepath.Join(binDir, "o-csv")); err != nil {
		t.Fatalf("expected installed binary: %v", err)
	}
}
