package mdweb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// IndexSourceKind describes how a local path was registered in the index.
type IndexSourceKind string

const (
	SourceFile IndexSourceKind = "file"
	SourceDir  IndexSourceKind = "dir"
	SourceScan IndexSourceKind = "scan"
)

// IndexSource records a persisted single file, single directory, or scanned base directory.
type IndexSource struct {
	ID        string          `json:"id"`
	Kind      IndexSourceKind `json:"kind"`
	Path      string          `json:"path"`
	Recursive bool            `json:"recursive"`
	AddedAt   time.Time       `json:"addedAt"`
	ScannedAt time.Time       `json:"scannedAt,omitempty"`
}

// IndexEntry is a single entry in the web viewer index. Paired Markdown and HTML
// files share one IndexEntry carrying both MarkdownPath and HTMLPath.
type IndexEntry struct {
	ID           string    `json:"id"`
	Key          string    `json:"key"`
	Title        string    `json:"title"`
	Dir          string    `json:"dir"`
	MarkdownPath string    `json:"markdownPath,omitempty"`
	HTMLPath     string    `json:"htmlPath,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	SortTime     time.Time `json:"sortTime"`
	Source       string    `json:"source"`
	Paired       bool      `json:"paired"`
}

// IndexSnapshot is the serialized and API representation of the persistent index.
type IndexSnapshot struct {
	Version int           `json:"version"`
	Sources []IndexSource `json:"sources"`
	Entries []IndexEntry  `json:"entries"`
	Count   int           `json:"count"`
}

// IndexStore manages persistent index sources, scanning, MD+HTML pairing, and newest-first sorting.
type IndexStore struct {
	mu      sync.RWMutex
	path    string
	baseDir string
	sources []IndexSource
	entries []IndexEntry
	mdSet   map[string]bool
	htmlSet map[string]bool
	byMD    map[string]IndexEntry
	byHTML  map[string]IndexEntry
}

type scannedFile struct {
	absPath   string
	dir       string
	base      string
	origStem  string
	stem      string
	ext       string
	isHTML    bool
	createdAt time.Time
	updatedAt time.Time
	source    string
}

var (
	skipIndexDirs = map[string]bool{
		".git": true, "node_modules": true, ".worktrees": true,
		"vendor": true, "dist": true, "build": true, "__pycache__": true,
	}
	filenameDateRe = regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`)
	bareDateStemRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	htmlTitleRe    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	htmlH1Re       = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	htmlTagRe      = regexp.MustCompile(`<[^>]+>`)
)

// DefaultIndexPath returns ~/.local/share/matomeru/mdweb-index.json.
func DefaultIndexPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "mdweb-index.json")
	}
	return filepath.Join(home, ".local", "share", "matomeru", "mdweb-index.json")
}

// NewIndexStore opens or initializes a persistent IndexStore.
// Passing "-" or "memory" disables disk persistence.
func NewIndexStore(indexPath, baseDir string) *IndexStore {
	resolved := strings.TrimSpace(indexPath)
	switch resolved {
	case "":
		resolved = DefaultIndexPath()
	case "-", "memory":
		resolved = ""
	default:
		resolved = expandUserPath(resolved)
	}
	s := &IndexStore{
		path:    resolved,
		baseDir: baseDir,
		mdSet:   map[string]bool{},
		htmlSet: map[string]bool{},
		byMD:    map[string]IndexEntry{},
		byHTML:  map[string]IndexEntry{},
	}
	s.load()
	return s
}

// Path returns the configured persistence file path (empty if in-memory only).
func (s *IndexStore) Path() string { return s.path }

// Snapshot returns a copy of all sources and entries sorted newest-first.
func (s *IndexStore) Snapshot() IndexSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

// AllowsMarkdownPath reports whether absPath is present as a MarkdownPath in the index.
func (s *IndexStore) AllowsMarkdownPath(absPath string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mdSet[filepath.Clean(absPath)]
}

// AllowsHTMLPath reports whether absPath is present as an HTMLPath in the index.
func (s *IndexStore) AllowsHTMLPath(absPath string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.htmlSet[filepath.Clean(absPath)]
}

// PairedHTMLForMarkdown returns the paired HTMLPath for a Markdown file, if any.
func (s *IndexStore) PairedHTMLForMarkdown(absPath string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ent, ok := s.byMD[filepath.Clean(absPath)]; ok {
		return ent.HTMLPath
	}
	return ""
}

// PairedMarkdownForHTML returns the paired MarkdownPath for an HTML file, if any.
func (s *IndexStore) PairedMarkdownForHTML(absPath string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ent, ok := s.byHTML[filepath.Clean(absPath)]; ok {
		return ent.MarkdownPath
	}
	return ""
}

// Add registers a single file, single directory, or scanned directory in the index.
func (s *IndexStore) Add(rawPath string, mode IndexSourceKind, recursive bool) (IndexSnapshot, error) {
	absPath, err := resolveIndexTargetPath(rawPath, s.baseDir)
	if err != nil {
		return IndexSnapshot{}, err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return IndexSnapshot{}, &httpError{http.StatusNotFound, "path not found: " + rawPath}
		}
		return IndexSnapshot{}, &httpError{http.StatusBadRequest, err.Error()}
	}
	kind, err := validateSourceKind(mode, info, absPath)
	if err != nil {
		return IndexSnapshot{}, err
	}
	if kind == SourceFile {
		recursive = false
	}

	now := time.Now().UTC()
	src := IndexSource{
		ID:        stableID("src", string(kind)+":"+absPath),
		Kind:      kind,
		Path:      absPath,
		Recursive: recursive,
		AddedAt:   now,
	}
	if kind != SourceFile {
		src.ScannedAt = now
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.upsertSourceLocked(src)
	s.rebuildLocked()
	if err := s.saveLocked(); err != nil {
		return IndexSnapshot{}, err
	}
	return s.snapshotLocked(), nil
}

// Scan registers a base directory and scans it for Markdown and HTML files.
func (s *IndexStore) Scan(rawDir string, recursive bool) (IndexSnapshot, error) {
	return s.Add(rawDir, SourceScan, recursive)
}

// Remove deletes a source by ID or path and rebuilds the index.
func (s *IndexStore) Remove(idOrPath string) (IndexSnapshot, bool, error) {
	key := strings.TrimSpace(idOrPath)
	if key == "" {
		return IndexSnapshot{}, false, &httpError{http.StatusBadRequest, "missing source id or path"}
	}
	normPath, _ := resolveIndexTargetPath(key, s.baseDir)

	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]IndexSource, 0, len(s.sources))
	removed := false
	for _, src := range s.sources {
		if src.ID == key || src.Path == key || (normPath != "" && src.Path == normPath) {
			removed = true
			continue
		}
		next = append(next, src)
	}
	if !removed {
		return s.snapshotLocked(), false, nil
	}
	s.sources = next
	s.rebuildLocked()
	if err := s.saveLocked(); err != nil {
		return IndexSnapshot{}, true, err
	}
	return s.snapshotLocked(), true, nil
}

// Refresh rescans all registered sources against disk, updates timestamps and pairing, and persists.
func (s *IndexStore) Refresh() (IndexSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for i := range s.sources {
		if s.sources[i].Kind != SourceFile {
			s.sources[i].ScannedAt = now
		}
	}
	s.rebuildLocked()
	if err := s.saveLocked(); err != nil {
		return IndexSnapshot{}, err
	}
	return s.snapshotLocked(), nil
}

func (s *IndexStore) snapshotLocked() IndexSnapshot {
	sources := make([]IndexSource, len(s.sources))
	copy(sources, s.sources)
	entries := make([]IndexEntry, len(s.entries))
	copy(entries, s.entries)
	return IndexSnapshot{
		Version: 1,
		Sources: sources,
		Entries: entries,
		Count:   len(entries),
	}
}

func (s *IndexStore) upsertSourceLocked(src IndexSource) {
	for i, existing := range s.sources {
		if existing.Path == src.Path {
			if !existing.AddedAt.IsZero() {
				src.AddedAt = existing.AddedAt
			}
			s.sources[i] = src
			return
		}
	}
	s.sources = append(s.sources, src)
}

func (s *IndexStore) rebuildLocked() {
	s.entries = buildIndexEntries(s.sources)
	s.reindexLookupsLocked()
}

func (s *IndexStore) reindexLookupsLocked() {
	s.mdSet = make(map[string]bool, len(s.entries))
	s.htmlSet = make(map[string]bool, len(s.entries))
	s.byMD = make(map[string]IndexEntry, len(s.entries))
	s.byHTML = make(map[string]IndexEntry, len(s.entries))
	for _, ent := range s.entries {
		if ent.MarkdownPath != "" {
			clean := filepath.Clean(ent.MarkdownPath)
			s.mdSet[clean] = true
			s.byMD[clean] = ent
		}
		if ent.HTMLPath != "" {
			clean := filepath.Clean(ent.HTMLPath)
			s.htmlSet[clean] = true
			s.byHTML[clean] = ent
		}
	}
}

func (s *IndexStore) load() {
	if s.path == "" {
		return
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var snap IndexSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return
	}
	s.sources = snap.Sources
	s.entries = snap.Entries
	for i := range s.entries {
		s.entries[i].Paired = s.entries[i].MarkdownPath != "" && s.entries[i].HTMLPath != ""
	}
	sortIndexEntries(s.entries)
	s.reindexLookupsLocked()
}

func (s *IndexStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.snapshotLocked(), "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(s.path, string(data)+"\n")
}

func isMarkdownIndexFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown", ".mmd", ".mermaid":
		return true
	}
	return false
}

func isHTMLFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html", ".htm":
		return true
	}
	return false
}

func isIndexableFile(name string) bool {
	return isMarkdownIndexFile(name) || isHTMLFile(name)
}

func expandUserPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return home
		}
	}
	if strings.HasPrefix(trimmed, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, trimmed[2:])
		}
	}
	return trimmed
}

func resolveIndexTargetPath(rawPath, baseDir string) (string, error) {
	trimmed := expandUserPath(rawPath)
	if trimmed == "" || strings.ContainsRune(trimmed, 0) {
		return "", &httpError{http.StatusBadRequest, "invalid path"}
	}
	if !filepath.IsAbs(trimmed) && baseDir != "" {
		trimmed = filepath.Join(baseDir, filepath.FromSlash(trimmed))
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", &httpError{http.StatusBadRequest, err.Error()}
	}
	return filepath.Clean(abs), nil
}

func validateSourceKind(mode IndexSourceKind, info os.FileInfo, absPath string) (IndexSourceKind, error) {
	switch mode {
	case "":
		if info.IsDir() {
			return SourceDir, nil
		}
		if !isIndexableFile(absPath) {
			return "", &httpError{http.StatusBadRequest, "unsupported file type (expected .md, .markdown, .mmd, .mermaid, .html, or .htm)"}
		}
		return SourceFile, nil
	case SourceFile:
		if info.IsDir() {
			return "", &httpError{http.StatusBadRequest, "path is a directory; use directory or scan mode"}
		}
		if !isIndexableFile(absPath) {
			return "", &httpError{http.StatusBadRequest, "unsupported file type (expected .md, .markdown, .mmd, .mermaid, .html, or .htm)"}
		}
		return SourceFile, nil
	case SourceDir, SourceScan:
		if !info.IsDir() {
			return "", &httpError{http.StatusBadRequest, "path is not a directory"}
		}
		return mode, nil
	default:
		return "", &httpError{http.StatusBadRequest, "invalid mode: " + string(mode)}
	}
}

func stableID(prefix, value string) string {
	sum := sha256.Sum256([]byte(value))
	return prefix + "-" + hex.EncodeToString(sum[:6])
}

func buildIndexEntries(sources []IndexSource) []IndexEntry {
	files := collectAllSourceFiles(sources)
	byDir := map[string][]scannedFile{}
	for _, f := range files {
		byDir[f.dir] = append(byDir[f.dir], f)
	}
	var entries []IndexEntry
	for dir, dirFiles := range byDir {
		entries = append(entries, pairDirectoryFiles(dir, dirFiles)...)
	}
	sortIndexEntries(entries)
	return entries
}

func collectAllSourceFiles(sources []IndexSource) []scannedFile {
	seen := map[string]bool{}
	var out []scannedFile
	for _, src := range sources {
		for _, f := range collectSourceFiles(src) {
			if !seen[f.absPath] {
				seen[f.absPath] = true
				out = append(out, f)
			}
		}
	}
	return out
}

func collectSourceFiles(src IndexSource) []scannedFile {
	if src.Kind == SourceFile {
		if sf, ok := statScannedFile(src.Path, src.Path); ok {
			return []scannedFile{sf}
		}
		return nil
	}
	return scanDirectoryFiles(src.Path, src.Recursive)
}

func statScannedFile(absPath, source string) (scannedFile, bool) {
	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() || !isIndexableFile(absPath) {
		return scannedFile{}, false
	}
	clean := filepath.Clean(absPath)
	base := filepath.Base(clean)
	ext := strings.ToLower(filepath.Ext(base))
	origStem := strings.TrimSuffix(base, filepath.Ext(base))
	return scannedFile{
		absPath:   clean,
		dir:       filepath.Dir(clean),
		base:      base,
		origStem:  origStem,
		stem:      strings.ToLower(origStem),
		ext:       ext,
		isHTML:    isHTMLFile(base),
		createdAt: fileBirthTime(info),
		updatedAt: info.ModTime().UTC(),
		source:    source,
	}, true
}

func scanDirectoryFiles(rootDir string, recursive bool) []scannedFile {
	cleanRoot := filepath.Clean(rootDir)
	var out []scannedFile
	_ = filepath.WalkDir(cleanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path != cleanRoot {
			name := d.Name()
			if d.IsDir() {
				if !recursive || strings.HasPrefix(name, ".") || skipIndexDirs[name] {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(name, ".") || !isIndexableFile(name) {
				return nil
			}
			if sf, ok := statScannedFile(path, cleanRoot); ok {
				out = append(out, sf)
			}
		}
		return nil
	})
	return out
}

type stemBucket struct {
	md   []scannedFile
	html []scannedFile
}

func pairDirectoryFiles(dir string, files []scannedFile) []IndexEntry {
	sort.Slice(files, func(i, j int) bool {
		return filePairPriority(files[i]) < filePairPriority(files[j])
	})
	buckets := map[string]*stemBucket{}
	var stems []string
	for _, f := range files {
		b, ok := buckets[f.stem]
		if !ok {
			b = &stemBucket{}
			buckets[f.stem] = b
			stems = append(stems, f.stem)
		}
		if f.isHTML {
			b.html = append(b.html, f)
		} else {
			b.md = append(b.md, f)
		}
	}
	sort.Strings(stems)

	var entries []IndexEntry
	var looseMD, looseHTML []scannedFile
	for _, st := range stems {
		b := buckets[st]
		pairedCount := min(len(b.md), len(b.html))
		for i := 0; i < pairedCount; i++ {
			mdFile, htmlFile := b.md[i], b.html[i]
			entries = append(entries, makeIndexEntry(dir, st, &mdFile, &htmlFile))
		}
		looseMD = append(looseMD, b.md[pairedCount:]...)
		looseHTML = append(looseHTML, b.html[pairedCount:]...)
	}

	prefixEntries, remMD, remHTML := pairUnambiguousPrefixes(dir, looseMD, looseHTML)
	entries = append(entries, prefixEntries...)
	for i := range remMD {
		entries = append(entries, makeIndexEntry(dir, remMD[i].stem, &remMD[i], nil))
	}
	for i := range remHTML {
		entries = append(entries, makeIndexEntry(dir, remHTML[i].stem, nil, &remHTML[i]))
	}
	return entries
}

func filePairPriority(f scannedFile) string {
	rank := "2"
	switch f.ext {
	case ".md", ".html":
		rank = "0"
	case ".markdown", ".htm":
		rank = "1"
	}
	return f.stem + "|" + rank + "|" + f.absPath
}

func hasStemPrefix(htmlStem, mdStem string) bool {
	if len(mdStem) < 2 || bareDateStemRe.MatchString(mdStem) {
		return false
	}
	if len(htmlStem) <= len(mdStem)+1 || !strings.HasPrefix(htmlStem, mdStem) {
		return false
	}
	sep := htmlStem[len(mdStem)]
	return sep == '-' || sep == '_' || sep == '.'
}

func pairUnambiguousPrefixes(dir string, mds, htmls []scannedFile) ([]IndexEntry, []scannedFile, []scannedFile) {
	if len(mds) == 0 || len(htmls) == 0 {
		return nil, mds, htmls
	}
	htmlMatches := make([][]int, len(htmls))
	mdHitCount := make([]int, len(mds))
	for hi, hf := range htmls {
		for mi, mf := range mds {
			if hasStemPrefix(hf.stem, mf.stem) {
				htmlMatches[hi] = append(htmlMatches[hi], mi)
				mdHitCount[mi]++
			}
		}
	}

	usedMD := make([]bool, len(mds))
	usedHTML := make([]bool, len(htmls))
	var paired []IndexEntry
	for hi, matches := range htmlMatches {
		if len(matches) != 1 {
			continue
		}
		mi := matches[0]
		if mdHitCount[mi] != 1 || usedMD[mi] {
			continue
		}
		usedMD[mi] = true
		usedHTML[hi] = true
		paired = append(paired, makeIndexEntry(dir, mds[mi].stem, &mds[mi], &htmls[hi]))
	}
	return paired, filterUnusedFiles(mds, usedMD), filterUnusedFiles(htmls, usedHTML)
}

func filterUnusedFiles(files []scannedFile, used []bool) []scannedFile {
	var rem []scannedFile
	for i, f := range files {
		if !used[i] {
			rem = append(rem, f)
		}
	}
	return rem
}

func makeIndexEntry(dir, stem string, mdFile, htmlFile *scannedFile) IndexEntry {
	key := filepath.ToSlash(filepath.Join(dir, stem))
	ent := IndexEntry{
		ID:     stableID("ent", key),
		Key:    key,
		Dir:    dir,
		Paired: mdFile != nil && htmlFile != nil,
	}
	if mdFile != nil {
		ent.MarkdownPath = mdFile.absPath
		ent.Source = mdFile.source
		ent.CreatedAt = mdFile.createdAt
		ent.UpdatedAt = mdFile.updatedAt
	}
	if htmlFile != nil {
		ent.HTMLPath = htmlFile.absPath
		if ent.Source == "" {
			ent.Source = htmlFile.source
		}
		ent.CreatedAt = earliestNonZero(ent.CreatedAt, htmlFile.createdAt)
		ent.UpdatedAt = latestTime(ent.UpdatedAt, htmlFile.updatedAt)
	}
	ent.SortTime = computeSortTime(ent.CreatedAt, ent.UpdatedAt, stem)
	ent.Title = extractEntryTitle(mdFile, htmlFile, stem)
	return ent
}

func earliestNonZero(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() || a.Before(b) {
		return a
	}
	return b
}

func latestTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func computeSortTime(created, updated time.Time, stem string) time.Time {
	if !updated.IsZero() {
		return updated
	}
	if !created.IsZero() {
		return created
	}
	return parseFilenameDate(stem)
}

func parseFilenameDate(name string) time.Time {
	m := filenameDateRe.FindStringSubmatch(name)
	if len(m) < 2 {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02", m[1])
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func sortIndexEntries(entries []IndexEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if !a.SortTime.Equal(b.SortTime) {
			return a.SortTime.After(b.SortTime)
		}
		da, db := parseFilenameDate(filepath.Base(a.Key)), parseFilenameDate(filepath.Base(b.Key))
		if !da.Equal(db) {
			return da.After(db)
		}
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return a.Key < b.Key
	})
}

func extractEntryTitle(mdFile, htmlFile *scannedFile, fallbackStem string) string {
	if mdFile != nil {
		if title := extractMarkdownTitle(mdFile.absPath); title != "" {
			return title
		}
	}
	if htmlFile != nil {
		if title := extractHTMLTitle(htmlFile.absPath); title != "" {
			return title
		}
	}
	if mdFile != nil && mdFile.origStem != "" {
		return mdFile.origStem
	}
	if htmlFile != nil && htmlFile.origStem != "" {
		return htmlFile.origStem
	}
	return fallbackStem
}

func readHeadSnippet(absPath string, maxBytes int64) string {
	f, err := os.Open(absPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return ""
	}
	return string(buf)
}

func extractMarkdownTitle(absPath string) string {
	text := readHeadSnippet(absPath, 4096)
	if text == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if title, rest := extractFrontmatterTitle(lines); title != "" {
		return title
	} else {
		lines = rest
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	return ""
}

func extractFrontmatterTitle(lines []string) (string, []string) {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", lines
	}
	for i := 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "---" || trimmed == "..." {
			return "", lines[i+1:]
		}
		if val, ok := strings.CutPrefix(strings.ToLower(trimmed), "title:"); ok {
			_ = val
			rawVal := strings.TrimSpace(trimmed[len("title:"):])
			rawVal = strings.Trim(rawVal, `"'`)
			if rawVal != "" {
				return rawVal, lines[i+1:]
			}
		}
	}
	return "", lines
}

func extractHTMLTitle(absPath string) string {
	text := readHeadSnippet(absPath, 8192)
	if text == "" {
		return ""
	}
	for _, re := range []*regexp.Regexp{htmlTitleRe, htmlH1Re} {
		if m := re.FindStringSubmatch(text); len(m) >= 2 {
			cleaned := strings.TrimSpace(html.UnescapeString(htmlTagRe.ReplaceAllString(m[1], "")))
			cleaned = strings.Join(strings.Fields(cleaned), " ")
			if cleaned != "" {
				return cleaned
			}
		}
	}
	return ""
}

// FormatSortDate formats an entry's sort timestamp cleanly for display.
func FormatSortDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// FormatEntrySummary returns a compact human summary string for logging/diagnostics.
func FormatEntrySummary(e IndexEntry) string {
	kind := "md"
	if e.Paired {
		kind = "md+html"
	} else if e.HTMLPath != "" {
		kind = "html"
	}
	return fmt.Sprintf("%s [%s] (%s)", e.Title, kind, FormatSortDate(e.SortTime))
}
