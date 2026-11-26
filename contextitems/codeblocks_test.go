package contextitems_test

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/contextitems"
)

func TestExtractFencedCodeBlocks(t *testing.T) {
	input := strings.Join([]string{
		"Intro line with https://example.com/ref",
		"```go title=main.go",
		"package main",
		"",
		"func main() {",
		"\tprintln(\"hello\")",
		"}",
		"```",
		"Between blocks.",
		"  ~~~bash",
		"  echo hi",
		"  ~~~",
		"Nested fence example:",
		"````markdown",
		"```go",
		"fmt.Println(1)",
		"```",
		"````",
		"Empty fence should be skipped:",
		"```python",
		"   ",
		"```",
		"Truncated unclosed fence at EOF:",
		"```sql",
		"SELECT id, title",
		"FROM records",
	}, "\n")

	items := contextitems.Extract(input, contextitems.Options{Source: contextitems.SourceResponse})

	var codes []contextitems.Item
	var urls []contextitems.Item
	for _, it := range items {
		switch it.Kind {
		case contextitems.KindCode:
			codes = append(codes, it)
		case contextitems.KindURL:
			urls = append(urls, it)
		}
	}

	if len(urls) != 1 || urls[0].Value != "https://example.com/ref" {
		t.Fatalf("urls = %+v, want single https://example.com/ref", urls)
	}
	if len(codes) != 4 {
		t.Fatalf("got %d code blocks, want 4: %+v", len(codes), codes)
	}

	if codes[0].Language != "go" || codes[0].Inline || !strings.Contains(codes[0].Value, "func main()") {
		t.Errorf("codes[0] = %+v, want go fenced block", codes[0])
	}
	if codes[1].Language != "bash" || codes[1].Value != "echo hi" {
		t.Errorf("codes[1] = %+v, want bash 'echo hi' (with 2-space indent stripped)", codes[1])
	}
	if codes[2].Language != "markdown" || !strings.Contains(codes[2].Value, "```go\nfmt.Println(1)\n```") {
		t.Errorf("codes[2] = %+v, want nested markdown fence preserved", codes[2])
	}
	if codes[3].Language != "sql" || codes[3].Value != "SELECT id, title\nFROM records" {
		t.Errorf("codes[3] = %+v, want unclosed EOF sql fence", codes[3])
	}
}

func TestExtractFencedCodeBlocksANSIAndNoSoftWrapCorruption(t *testing.T) {
	lines := []string{
		"\x1b[36m```sh\x1b[0m",
		"\x1b[32mhttps://example.com/very/long/url/\x1b[0m",
		"next-line-is-separate-arg",
		"```",
	}

	items := contextitems.ExtractLines(lines, contextitems.Options{
		Source:    contextitems.SourceScreen,
		WrapWidth: 30,
	})
	if len(items) != 1 || items[0].Kind != contextitems.KindCode {
		t.Fatalf("items = %+v, want single fenced code block", items)
	}
	want := "https://example.com/very/long/url/\nnext-line-is-separate-arg"
	if items[0].Value != want || items[0].Language != "sh" {
		t.Errorf("got lang=%q value=%q, want lang=sh value=%q", items[0].Language, items[0].Value, want)
	}
}

func TestExtractInlineCodeSpansAndNoiseHygiene(t *testing.T) {
	input := strings.Join([]string{
		"Run `go test ./...` or `` echo `hi` `` and check `` `wrapped` ``.",
		"Skip empty `   ` and unmatched ` stray backtick.",
		"",
		"Another paragraph with ` stray backtick should not pair across blank line.",
		"",
		"Also skip pure nav targets in backticks: `app/model.go`, `https://example.com/doc`, `ofam/matomeru#42`, `README.md`, `Makefile:12`.",
		"Keep commands mentioning a path: `go test ./app/model.go` and version `v1.2.3`.",
	}, "\n")

	items := contextitems.Extract(input, contextitems.Options{Source: contextitems.SourceResponse})

	var codeVals []string
	var fileVals []string
	var urlVals []string
	var prVals []string
	for _, it := range items {
		switch it.Kind {
		case contextitems.KindCode:
			if !it.Inline {
				t.Errorf("expected inline=true on %+v", it)
			}
			codeVals = append(codeVals, it.Value)
		case contextitems.KindFile:
			fileVals = append(fileVals, it.Value)
		case contextitems.KindURL:
			urlVals = append(urlVals, it.Value)
		case contextitems.KindPR:
			prVals = append(prVals, it.Value)
		}
	}

	for _, want := range []string{"go test ./...", "echo `hi`", "`wrapped`", "go test ./app/model.go", "v1.2.3"} {
		if !slices.Contains(codeVals, want) {
			t.Errorf("missing inline code %q; got codeVals=%q", want, codeVals)
		}
	}
	for _, forbidden := range []string{"app/model.go", "https://example.com/doc", "ofam/matomeru#42", "README.md", "Makefile:12", "   ", ""} {
		if slices.Contains(codeVals, forbidden) {
			t.Errorf("nav target or blank %q should NOT be duplicated as code; got codeVals=%q", forbidden, codeVals)
		}
	}
	if !slices.Contains(fileVals, "app/model.go") || !slices.Contains(fileVals, "Makefile:12") {
		t.Errorf("expected nav files extracted; got fileVals=%q", fileVals)
	}
	if !slices.Contains(urlVals, "https://example.com/doc") || !slices.Contains(prVals, "ofam/matomeru#42") {
		t.Errorf("expected nav url/pr extracted; urls=%q prs=%q", urlVals, prVals)
	}
}

func TestDisableCodeBlocksAndVisibleMatches(t *testing.T) {
	input := "See https://example.com and `fmt.Println(1)`\n```go\nfmt.Println(2)\n```"

	disabled := contextitems.Extract(input, contextitems.Options{
		Source:            contextitems.SourceResponse,
		DisableCodeBlocks: true,
	})
	for _, it := range disabled {
		if it.Kind == contextitems.KindCode {
			t.Fatalf("DisableCodeBlocks=true still extracted code item: %+v", it)
		}
	}
	if len(disabled) != 1 || disabled[0].Kind != contextitems.KindURL {
		t.Fatalf("disabled items = %+v, want only URL", disabled)
	}

	matches := contextitems.ExtractVisibleMatches(strings.Split(input, "\n"))
	for _, m := range matches {
		if m.Kind == contextitems.KindCode {
			t.Fatalf("ExtractVisibleMatches must never highlight code blocks: %+v", m)
		}
	}
	if len(matches) != 1 || matches[0].Kind != contextitems.KindURL {
		t.Fatalf("visible matches = %+v, want only URL", matches)
	}
}
