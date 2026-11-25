package data

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWriteManagedBytes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed")
	pngBytes := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x01}

	p, err := WriteManagedBytes(root, CategoryAttachments, "att01", "Screen Shot 2026!", "png", pngBytes)
	if err != nil {
		t.Fatalf("WriteManagedBytes: %v", err)
	}
	if !filepath.IsAbs(p) {
		t.Fatalf("returned path %q is not absolute", p)
	}
	if !IsManagedPath(root, p) {
		t.Fatalf("IsManagedPath(%q, %q) = false", root, p)
	}
	if !strings.HasSuffix(p, filepath.Join(CategoryAttachments, "att01-screen-shot-2026.png")) {
		t.Fatalf("unexpected attachment path %q", p)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, pngBytes) {
		t.Fatalf("bytes mismatch: got %x, want %x", got, pngBytes)
	}

	// Default extension for empty ext is .bin, and relative root becomes absolute.
	p2, err := WriteManagedBytes(root, CategoryAttachments, "att02", "", "", []byte("raw"))
	if err != nil {
		t.Fatalf("WriteManagedBytes default ext: %v", err)
	}
	if !strings.HasSuffix(p2, filepath.Join(CategoryAttachments, "att02.bin")) {
		t.Fatalf("unexpected default ext path %q", p2)
	}

	// Validation errors.
	if _, err := WriteManagedBytes("", CategoryAttachments, "a", "t", "png", pngBytes); err == nil {
		t.Fatal("expected error for empty root")
	}
	if _, err := WriteManagedBytes(root, "///", "a", "t", "png", pngBytes); err == nil {
		t.Fatal("expected error for invalid category")
	}
	if _, err := WriteManagedBytes(root, CategoryAttachments, "///", "t", "png", pngBytes); err == nil {
		t.Fatal("expected error for invalid id")
	}
}

func TestParseRichTokens(t *testing.T) {
	input := strings.Join([]string{
		"Review @data/schema.go and (@task:t123), plus @context:ctx-9 and @agent:claude.",
		"Also check @data/schema.go again, @ui.page:p1, @README.md, and @main.go:42!",
		"Ignore emails like foo@bar.com, first.last+tag@sub.example.org, and user_1@host.io.",
		"Run $code-review, ($tdd), and $code-review; ignore $100 and foo$bar.",
		"Invoke /plan and `/review-pr` and /plan; ignore /usr/local/bin and https://example.com/foo/bar and a/b.",
	}, "\n")

	got := ParseRichTokens(input)
	want := RichTokens{
		Files:    []string{"data/schema.go", "README.md", "main.go:42"},
		Refs:     []string{"task:t123", "context:ctx-9", "agent:claude", "ui.page:p1"},
		Skills:   []string{"code-review", "tdd"},
		Commands: []string{"plan", "review-pr"},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseRichTokens mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestParseRichTokensStartOfStringAndEdgeCases(t *testing.T) {
	got := ParseRichTokens("/deploy $release @task:abc @src/app.go")
	want := RichTokens{
		Files:    []string{"src/app.go"},
		Refs:     []string{"task:abc"},
		Skills:   []string{"release"},
		Commands: []string{"deploy"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}

	empty := ParseRichTokens("plain text with email a@b.co, price $50, path /tmp/a.png, url https://x.dev/y")
	if len(empty.Files) != 0 || len(empty.Refs) != 0 || len(empty.Skills) != 0 || len(empty.Commands) != 0 {
		t.Fatalf("expected empty RichTokens, got %#v", empty)
	}
}

func TestEncodeDecodeAttachments(t *testing.T) {
	if got := DecodeAttachments(nil); got != nil {
		t.Fatalf("DecodeAttachments(nil) = %#v, want nil", got)
	}
	if got := DecodeAttachments([]any{}); got != nil {
		t.Fatalf("DecodeAttachments(empty) = %#v, want nil", got)
	}
	if got := EncodeAttachments(nil); got != nil {
		t.Fatalf("EncodeAttachments(nil) = %#v, want nil", got)
	}

	in := []Attachment{
		{
			Kind:   "image",
			Path:   "/abs/files/attachments/a1-shot.png",
			Label:  "Screenshot",
			Mime:   "image/png",
			Source: "clipboard",
		},
		{
			Kind:   "context",
			Path:   "https://github.com/org/repo/pull/42",
			Label:  "PR #42",
			Source: "agent",
			Ref:    "context:ctx42",
		},
	}

	// Direct []Attachment decode.
	direct := DecodeAttachments(in)
	if !reflect.DeepEqual(direct, in) {
		t.Fatalf("direct decode = %#v, want %#v", direct, in)
	}

	// Encode -> []any of map[string]any -> Decode.
	encoded := EncodeAttachments(in)
	if len(encoded) != 2 {
		t.Fatalf("encoded len = %d, want 2", len(encoded))
	}
	firstMap, ok := encoded[0].(map[string]any)
	if !ok {
		t.Fatalf("encoded[0] type = %T, want map[string]any", encoded[0])
	}
	if _, hasRef := firstMap["ref"]; hasRef {
		t.Fatalf("empty optional field ref should be omitted from encoded map: %#v", firstMap)
	}

	fromEncoded := DecodeAttachments(encoded)
	if !reflect.DeepEqual(fromEncoded, in) {
		t.Fatalf("decode from encoded = %#v, want %#v", fromEncoded, in)
	}

	// JSON round-trip (as stored in SQLite Record.Fields).
	rawJSON, err := json.Marshal(encoded)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var generic any
	if err := json.Unmarshal(rawJSON, &generic); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	fromJSON := DecodeAttachments(generic)
	if !reflect.DeepEqual(fromJSON, in) {
		t.Fatalf("decode from JSON round-trip = %#v, want %#v", fromJSON, in)
	}
}
