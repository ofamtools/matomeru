package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteTutorialDoc writes docs/TUTORIAL.md from TutorialMarkdown when
// MTM_WRITE_DOCS is set: MTM_WRITE_DOCS=1 go test ./app -run TestWriteTutorialDoc
func TestWriteTutorialDoc(t *testing.T) {
	if os.Getenv("MTM_WRITE_DOCS") == "" {
		t.Skip("set MTM_WRITE_DOCS=1 to write docs/TUTORIAL.md")
	}
	if err := os.WriteFile(filepath.Join("..", "docs", "TUTORIAL.md"), []byte(TutorialMarkdown()), 0o644); err != nil {
		t.Fatal(err)
	}
}
