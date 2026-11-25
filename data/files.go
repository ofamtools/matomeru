package data

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Managed file categories under FilesRoot.
const (
	CategoryNotes       = "notes"
	CategoryDocuments   = "documents"
	CategoryArtifacts   = "artifacts"
	CategoryAttachments = "attachments"
)

// FilesRoot returns the canonical managed storage root for a store at dbPath:
// $MTM_FILES_DIR when set, else <dirname(dbPath)>/files for file-backed stores.
// For in-memory stores without $MTM_FILES_DIR it returns "" so ephemeral tests
// never write into the user's home directory unless configured explicitly.
func FilesRoot(dbPath string) string {
	if dir := strings.TrimSpace(os.Getenv("MTM_FILES_DIR")); dir != "" {
		return filepath.Clean(dir)
	}
	if dbPath == "" || dbPath == ":memory:" || strings.Contains(dbPath, "mode=memory") {
		return ""
	}
	p := dbPath
	if strings.HasPrefix(p, "file:") {
		p = strings.TrimPrefix(p, "file:")
		if base, _, ok := strings.Cut(p, "?"); ok {
			p = base
		}
		if p == "" || p == ":memory:" {
			return ""
		}
	}
	return filepath.Join(filepath.Dir(filepath.Clean(p)), "files")
}

// IsManagedPath reports whether p is located inside root.
func IsManagedPath(root, p string) bool {
	if root == "" || p == "" {
		return false
	}
	ar, err1 := filepath.Abs(root)
	ap, err2 := filepath.Abs(p)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(ar, ap)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// slugify turns s into a safe lowercase filename stem.
func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if unicode.IsSpace(r) || r == '-' || r == '_' || r == '/' || r == '.' {
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
		if b.Len() >= 64 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// safeSegment validates a single safe path segment (category or id).
func safeSegment(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// WriteManagedFile writes content atomically (temp file + rename) into
// <root>/<category>/<id>-<slug><ext> and returns the final absolute file path.
func WriteManagedFile(root, category, id, title, ext, content string) (string, error) {
	if strings.TrimSpace(ext) == "" {
		ext = ".md"
	}
	return WriteManagedBytes(root, category, id, title, ext, []byte(content))
}

// WriteManagedBytes writes raw bytes atomically (temp file + rename) into
// <root>/<category>/<id>-<slug><ext> and returns the final absolute file path.
func WriteManagedBytes(root, category, id, title, ext string, content []byte) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("data: managed files root is empty")
	}
	cat := safeSegment(category)
	if cat == "" {
		return "", fmt.Errorf("data: invalid managed file category %q", category)
	}
	safeID := safeSegment(id)
	if safeID == "" {
		return "", fmt.Errorf("data: invalid managed file id %q", id)
	}
	ext = strings.TrimSpace(ext)
	if ext == "" {
		ext = ".bin"
	} else if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	name := safeID
	if slug := slugify(title); slug != "" {
		name += "-" + slug
	}
	name += ext

	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("data: resolve managed root %s: %w", root, err)
	}
	dir := filepath.Join(absRoot, cat)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("data: create managed dir %s: %w", dir, err)
	}
	target := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".mtm-tmp-*")
	if err != nil {
		return "", fmt.Errorf("data: create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("data: write temp file %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("data: close temp file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return "", fmt.Errorf("data: rename %s to %s: %w", tmpPath, target, err)
	}
	cleanup = false
	return target, nil
}
