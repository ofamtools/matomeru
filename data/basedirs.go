package data

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"ofam.dev/ochazuke/core"
)

const defaultGitScanDepth = 3

// BaseDir describes a directory configured for Git repository discovery.
type BaseDir struct {
	Path          string    `json:"path"`
	MaxDepth      int       `json:"maxDepth"`
	LastScannedAt time.Time `json:"lastScannedAt"`
	RepoCount     int       `json:"repoCount"`
}

// DiscoveredRepo describes a repository and how it was found.
type DiscoveredRepo struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	BaseDir string `json:"baseDir"`
	Depth   int    `json:"depth"`
}

func normalizeDirectory(path string) (string, error) {
	if path == "" {
		return "", errors.New("data: directory path is empty")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("data: expand directory %s: %w", path, err)
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	return filepath.Abs(path)
}

func gitScanDepth(maxDepth int) int {
	if maxDepth <= 0 {
		return defaultGitScanDepth
	}
	return maxDepth
}

// ScanGitRepos finds Git roots at or below baseDir. The base itself is depth zero.
func ScanGitRepos(baseDir string, maxDepth int) ([]DiscoveredRepo, error) {
	base, err := normalizeDirectory(baseDir)
	if err != nil {
		return nil, err
	}
	return scanGitRepos(base, gitScanDepth(maxDepth))
}

func scanGitRepos(base string, maxDepth int) ([]DiscoveredRepo, error) {
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, fmt.Errorf("data: scan Git repositories under %s: %w", base, err)
	}
	defer root.Close()
	var repos []DiscoveredRepo
	// WalkDir does not follow directory symlinks. OpenRoot also permits an
	// explicitly selected base to be a symlink without changing its name.
	err = fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		depth := 0
		if relative != "." {
			switch entry.Name() {
			case ".git", "node_modules", "vendor", ".worktrees", "dist", "build", "target", ".cache":
				return filepath.SkipDir
			}
			depth = strings.Count(relative, "/") + 1
		}
		marker, err := root.Lstat(filepath.Join(filepath.FromSlash(relative), ".git"))
		if err == nil && (marker.IsDir() || marker.Mode().IsRegular()) {
			path := filepath.Join(base, filepath.FromSlash(relative))
			repos = append(repos, DiscoveredRepo{Path: path, Name: filepath.Base(path), BaseDir: base, Depth: depth})
			return filepath.SkipDir
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if depth >= maxDepth {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("data: scan Git repositories under %s: %w", base, err)
	}
	return repos, nil
}

type baseDirScan struct {
	BaseDir
	Repos []DiscoveredRepo `json:"repos"`
}

// BaseDirStore keeps the configured bases and their last successful scans.
type BaseDirStore struct {
	file    string
	writes  sync.Mutex
	loadErr error // guarded by writes

	mu    sync.RWMutex
	scans map[string]baseDirScan // snapshots are immutable once published
}

// NewBaseDirStore reads the saved repository index. An empty filePath keeps it in memory.
func NewBaseDirStore(filePath string) *BaseDirStore {
	s := &BaseDirStore{file: filePath}
	s.load()
	return s
}

func (s *BaseDirStore) load() {
	scans := map[string]baseDirScan{}
	s.loadErr = readPathIndex(s.file, &scans)
	if s.loadErr == nil {
		s.publish(scans)
	}
}

// List returns the bases in path order.
func (s *BaseDirStore) List() []BaseDir {
	scans := s.snapshot()
	bases := make([]BaseDir, 0, len(scans))
	for _, path := range slices.Sorted(maps.Keys(scans)) {
		bases = append(bases, scans[path].BaseDir)
	}
	return bases
}

// Add scans and saves a base directory. Adding a base again refreshes its scan.
func (s *BaseDirStore) Add(path string, maxDepth int, now time.Time) ([]DiscoveredRepo, error) {
	base, err := normalizeDirectory(path)
	if err != nil {
		return nil, err
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return nil, err
	}
	result, err := scanBaseDir(base, gitScanDepth(maxDepth), now)
	if err != nil {
		return nil, err
	}
	current := s.snapshot()
	scans := make(map[string]baseDirScan, len(current)+1)
	maps.Copy(scans, current)
	scans[base] = result
	if err := s.save(scans); err != nil {
		return nil, err
	}
	return slices.Clone(result.Repos), nil
}

// Remove forgets a base and its discovered repositories.
func (s *BaseDirStore) Remove(path string) error {
	base, err := normalizeDirectory(path)
	if err != nil {
		return err
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return err
	}
	current := s.snapshot()
	if _, ok := current[base]; !ok {
		return nil
	}
	scans := maps.Clone(current)
	delete(scans, base)
	return s.save(scans)
}

// ScanAll refreshes every base together. A positive depth sets a new common
// limit; otherwise each base keeps its depth, falling back to the default.
func (s *BaseDirStore) ScanAll(defaultDepth int, now time.Time) ([]DiscoveredRepo, error) {
	s.writes.Lock()
	defer s.writes.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return nil, err
	}
	current := s.snapshot()
	if len(current) == 0 {
		return nil, nil
	}
	scans := make(map[string]baseDirScan, len(current))
	for _, path := range slices.Sorted(maps.Keys(current)) {
		depth := defaultDepth
		if depth <= 0 {
			depth = gitScanDepth(current[path].MaxDepth)
		}
		result, err := scanBaseDir(path, depth, now)
		if err != nil {
			return nil, err
		}
		scans[path] = result
	}
	if err := s.save(scans); err != nil {
		return nil, err
	}
	return discoveredRepos(scans), nil
}

// Repos returns a path-ordered snapshot, identifying overlaps by the nearest base.
func (s *BaseDirStore) Repos() []DiscoveredRepo {
	return discoveredRepos(s.snapshot())
}

func (s *BaseDirStore) snapshot() map[string]baseDirScan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scans
}

func (s *BaseDirStore) publish(scans map[string]baseDirScan) {
	s.mu.Lock()
	s.scans = scans
	s.mu.Unlock()
}

func scanBaseDir(base string, depth int, now time.Time) (baseDirScan, error) {
	repos, err := scanGitRepos(base, depth)
	if err != nil {
		return baseDirScan{}, err
	}
	return baseDirScan{
		BaseDir: BaseDir{Path: base, MaxDepth: depth, LastScannedAt: now.UTC(), RepoCount: len(repos)},
		Repos:   repos,
	}, nil
}

func discoveredRepos(scans map[string]baseDirScan) []DiscoveredRepo {
	byPath := map[string]DiscoveredRepo{}
	for _, scan := range scans {
		for _, repo := range scan.Repos {
			cur, ok := byPath[repo.Path]
			if !ok || cmp.Or(cmp.Compare(repo.Depth, cur.Depth), cmp.Compare(repo.BaseDir, cur.BaseDir)) < 0 {
				byPath[repo.Path] = repo
			}
		}
	}
	var repos []DiscoveredRepo
	for _, path := range slices.Sorted(maps.Keys(byPath)) {
		repos = append(repos, byPath[path])
	}
	return repos
}

func (s *BaseDirStore) ensureLoaded() error {
	if s.loadErr != nil {
		s.load()
	}
	return s.loadErr
}

func (s *BaseDirStore) save(scans map[string]baseDirScan) error {
	if err := writePathIndex(s.file, scans); err != nil {
		return err
	}
	s.publish(scans)
	return nil
}

// EnsureProjectTasksForRepos imports missing root tasks as one change set.
func EnsureProjectTasksForRepos(store *Store, repos []DiscoveredRepo, actor core.Actor) (created int, err error) {
	if len(repos) == 0 {
		return 0, nil
	}
	_, err = store.write(actor, "files.repos.import", "import discovered repositories", 0, false, func(t *txn) error {
		paths, err := t.projectDirectoryPaths()
		if err != nil {
			return err
		}
		for _, repo := range repos {
			path, err := normalizeDirectory(repo.Path)
			if err != nil {
				return err
			}
			if paths[path] {
				continue
			}
			in := CreateInput{Kind: KindTask, Fields: map[string]any{"title": repo.Name, "defaultDir": repo.Path}}
			if err := t.createRecord(core.Ref{Kind: in.Kind, ID: newID()}, in, actor); err != nil {
				return err
			}
			paths[path] = true
			created++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return created, nil
}

func (t *txn) projectDirectoryPaths() (map[string]bool, error) {
	local, err := loadLocal(t.tx, KindTask, "")
	if err != nil {
		return nil, err
	}
	q := Query{Kind: KindTask, Roots: true}
	parentField := t.s.resolveParentField(q)
	sqlText, args := buildQuerySQL(q, parentField, len(local) == 0)
	rows, err := t.tx.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := map[string]bool{}
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		rec.Local = local[rec.Ref()]
		t.s.coerce(&rec)
		if _, hasParent := rec.RefField(parentField); hasParent {
			continue
		}
		if path, err := normalizeDirectory(rec.Text("defaultDir")); err == nil {
			paths[path] = true
		}
	}
	return paths, rows.Err()
}
