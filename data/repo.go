package data

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

// RepoInput describes a git repository to create or update as a KindRepo record.
type RepoInput struct {
	Name          string `json:"name"`
	Path          string `json:"path,omitempty"`
	RemoteURL     string `json:"remoteUrl,omitempty"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
	Source        string `json:"source,omitempty"`
}

// RepoSyncReport summarizes one SyncRepos run.
type RepoSyncReport struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Skipped int      `json:"skipped"`
	Sources []string `json:"sources,omitempty"`
	Repos   []Record `json:"repos,omitempty"`
}

func repoSchema() Schema {
	return Schema{
		Kind: KindRepo, Title: "Repo", Plural: "Repos", SummaryField: "name",
		UniqueFields: []string{"name"},
		Fields: []FieldSpec{
			{Name: "name", Type: core.ParamString, Required: true, Description: "repository name"},
			{Name: "path", Type: core.ParamPath, Description: "local repository root path"},
			str("remoteUrl", "git origin/remote URL"),
			str("defaultBranch", "default git branch"),
			{Name: "source", Type: core.ParamString, Enum: []string{"manual", "basedir", "o-proj", "harvested", "pane"}, Default: "manual", Description: "how this repo was registered"},
		},
		DefaultColumns: []string{"name", "path", "defaultBranch", "source"},
		Relations: universalRelations(
			RelationSpec{Kind: RelRepo, Title: "Linked tasks & lanes", TargetKinds: []string{KindTask, KindLane}, Direction: DirectionIncoming},
		),
		FactFields: []string{"source"},
		BadgeRules: []BadgeRule{
			{When: `fields.source == "o-proj"`, Text: "o-proj", Tone: "accent"},
			{When: `fields.source == "basedir"`, Text: "basedir", Tone: "info"},
			{When: `fields.source == "pane"`, Text: "pane", Tone: "muted"},
		},
	}
}

func normalizeRepoPath(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

// UpsertRepo registers or updates a KindRepo record, deduplicating by
// normalized local path (when non-empty) or case-insensitive name. Existing
// non-empty fields are preserved rather than overwritten. It returns the
// resulting record and whether a new record was created.
func UpsertRepo(store *Store, in RepoInput, actor core.Actor) (Record, bool, error) {
	rec, created, _, err := upsertRepoDetailed(store, in, actor)
	return rec, created, err
}

type normalizedRepoInput struct {
	name          string
	path          string
	remoteURL     string
	defaultBranch string
	source        string
}

func normalizeRepoInput(in RepoInput) (normalizedRepoInput, error) {
	out := normalizedRepoInput{
		path:          normalizeRepoPath(in.Path),
		name:          strings.TrimSpace(in.Name),
		remoteURL:     strings.TrimSpace(in.RemoteURL),
		defaultBranch: strings.TrimSpace(in.DefaultBranch),
		source:        strings.TrimSpace(in.Source),
	}
	if out.name == "" && out.path != "" {
		out.name = filepath.Base(out.path)
	}
	if out.name == "" {
		return normalizedRepoInput{}, errors.New("data: repo name or path is required")
	}
	if out.source == "" {
		out.source = "manual"
	}
	return out, nil
}

func findMatchingRepo(items []Record, normPath, name string) *Record {
	if normPath != "" {
		for i := range items {
			if existingPath := normalizeRepoPath(items[i].Text("path")); existingPath != "" && existingPath == normPath {
				cp := items[i]
				return &cp
			}
		}
	}
	for i := range items {
		if strings.EqualFold(strings.TrimSpace(items[i].Text("name")), name) {
			cp := items[i]
			return &cp
		}
	}
	return nil
}

func buildMissingRepoPatch(match Record, in normalizedRepoInput) map[string]any {
	patch := map[string]any{}
	if in.path != "" && strings.TrimSpace(match.Text("path")) == "" {
		patch["path"] = in.path
	}
	if in.remoteURL != "" && strings.TrimSpace(match.Text("remoteUrl")) == "" {
		patch["remoteUrl"] = in.remoteURL
	}
	if in.defaultBranch != "" && strings.TrimSpace(match.Text("defaultBranch")) == "" {
		patch["defaultBranch"] = in.defaultBranch
	}
	if strings.TrimSpace(match.Text("source")) == "" && in.source != "" {
		patch["source"] = in.source
	}
	return patch
}

func buildCreateRepoFields(in normalizedRepoInput) map[string]any {
	fields := map[string]any{
		"name":   in.name,
		"source": in.source,
	}
	if in.path != "" {
		fields["path"] = in.path
	}
	if in.remoteURL != "" {
		fields["remoteUrl"] = in.remoteURL
	}
	if in.defaultBranch != "" {
		fields["defaultBranch"] = in.defaultBranch
	}
	return fields
}

func upsertRepoDetailed(store *Store, in RepoInput, actor core.Actor) (rec Record, created, updated bool, err error) {
	if store == nil {
		return Record{}, false, false, errors.New("data: store is nil")
	}
	norm, err := normalizeRepoInput(in)
	if err != nil {
		return Record{}, false, false, err
	}
	res, err := store.Query(Query{Kind: KindRepo})
	if err != nil {
		return Record{}, false, false, err
	}
	if match := findMatchingRepo(res.Items, norm.path, norm.name); match != nil {
		patch := buildMissingRepoPatch(*match, norm)
		if len(patch) == 0 {
			return *match, false, false, nil
		}
		patched, _, err := store.Patch(PatchInput{
			Ref:     match.Ref(),
			Fields:  patch,
			Summary: fmt.Sprintf("update repo %s", norm.name),
		}, actor)
		if err != nil {
			return Record{}, false, false, err
		}
		return *patched, false, true, nil
	}
	createdRec, _, err := store.Create(CreateInput{Kind: KindRepo, Fields: buildCreateRepoFields(norm)}, actor)
	if err != nil {
		return Record{}, false, false, err
	}
	return *createdRec, true, false, nil
}

// DetectGitRepoRoot walks upward from dir looking for a .git directory or
// worktree .git file, extracting repository name, root path, default/current
// branch, and origin remote URL without spawning any subprocesses.
func DetectGitRepoRoot(dir string) (RepoInput, bool) {
	cur := normalizeRepoPath(dir)
	if cur == "" {
		return RepoInput{}, false
	}
	fi, err := os.Stat(cur)
	if err != nil {
		return RepoInput{}, false
	}
	if !fi.IsDir() {
		cur = filepath.Dir(cur)
	}
	for {
		markerPath := filepath.Join(cur, ".git")
		info, err := os.Lstat(markerPath)
		if err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			gitDir, commonDir := resolveGitDirs(cur, markerPath, info.IsDir())
			return RepoInput{
				Name:          filepath.Base(cur),
				Path:          cur,
				RemoteURL:     readGitRemoteURL(commonDir, gitDir),
				DefaultBranch: readGitDefaultBranch(commonDir, gitDir),
			}, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return RepoInput{}, false
		}
		cur = parent
	}
}

func resolveGitDirs(repoRoot, markerPath string, isDir bool) (gitDir, commonDir string) {
	if isDir {
		return markerPath, markerPath
	}
	b, err := os.ReadFile(markerPath)
	if err != nil {
		return markerPath, markerPath
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "gitdir:"); ok {
			target := strings.TrimSpace(rest)
			if target != "" {
				if !filepath.IsAbs(target) {
					target = filepath.Join(repoRoot, target)
				}
				gitDir = filepath.Clean(target)
				break
			}
		}
	}
	if gitDir == "" {
		return markerPath, markerPath
	}
	commonDir = gitDir
	if cb, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		rel := strings.TrimSpace(string(cb))
		if rel != "" {
			if !filepath.IsAbs(rel) {
				commonDir = filepath.Clean(filepath.Join(gitDir, rel))
			} else {
				commonDir = filepath.Clean(rel)
			}
		}
	}
	return gitDir, commonDir
}

func readGitDefaultBranch(commonDir, gitDir string) string {
	if b, err := os.ReadFile(filepath.Join(commonDir, "refs", "remotes", "origin", "HEAD")); err == nil {
		line := strings.TrimSpace(string(b))
		if ref, ok := strings.CutPrefix(line, "ref:"); ok {
			ref = strings.TrimSpace(ref)
			if br, ok := strings.CutPrefix(ref, "refs/remotes/origin/"); ok && br != "" {
				return br
			}
		}
	}
	for _, dir := range []string{gitDir, commonDir} {
		if dir == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, "HEAD"))
		if err != nil {
			continue
		}
		line := strings.TrimSpace(string(b))
		if ref, ok := strings.CutPrefix(line, "ref:"); ok {
			ref = strings.TrimSpace(ref)
			if br, ok := strings.CutPrefix(ref, "refs/heads/"); ok && br != "" {
				return br
			}
		}
	}
	return ""
}

func readGitRemoteURL(commonDir, gitDir string) string {
	for _, dir := range []string{commonDir, gitDir} {
		if dir == "" {
			continue
		}
		f, err := os.Open(filepath.Join(dir, "config"))
		if err != nil {
			continue
		}
		url := parseGitConfigRemoteURL(f)
		_ = f.Close()
		if url != "" {
			return url
		}
	}
	return ""
}

func parseGitConfigURLValue(line string) (string, bool) {
	k, v, ok := strings.Cut(line, "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(k), "url") {
		return "", false
	}
	val := strings.TrimSpace(v)
	return val, val != ""
}

func parseGitConfigRemoteURL(f *os.File) string {
	sc := bufio.NewScanner(f)
	section := ""
	firstRemote := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		val, ok := parseGitConfigURLValue(line)
		if !ok {
			continue
		}
		if section == `remote "origin"` {
			return val
		}
		if strings.HasPrefix(section, `remote "`) && firstRemote == "" {
			firstRemote = val
		}
	}
	return firstRemote
}

type repoSyncer struct {
	store  *Store
	actor  core.Actor
	report RepoSyncReport
}

func (s *repoSyncer) recordSource(src string) {
	if !slices.Contains(s.report.Sources, src) {
		s.report.Sources = append(s.report.Sources, src)
	}
}

func enrichRepoInput(in *RepoInput) {
	if in.Path == "" || (in.RemoteURL != "" && in.DefaultBranch != "") {
		return
	}
	det, ok := DetectGitRepoRoot(in.Path)
	if !ok {
		return
	}
	if in.Name == "" {
		in.Name = det.Name
	}
	if in.RemoteURL == "" {
		in.RemoteURL = det.RemoteURL
	}
	if in.DefaultBranch == "" {
		in.DefaultBranch = det.DefaultBranch
	}
}

func (s *repoSyncer) apply(in RepoInput) error {
	enrichRepoInput(&in)
	_, created, updated, err := upsertRepoDetailed(s.store, in, s.actor)
	if err != nil {
		return err
	}
	switch {
	case created:
		s.report.Created++
	case updated:
		s.report.Updated++
	default:
		s.report.Skipped++
	}
	return nil
}

func (s *repoSyncer) syncOProj(entries []RepoInput) error {
	if len(entries) == 0 {
		return nil
	}
	s.recordSource("o-proj")
	for _, entry := range entries {
		if strings.TrimSpace(entry.Source) == "" {
			entry.Source = "o-proj"
		}
		if err := s.apply(entry); err != nil {
			return err
		}
	}
	return nil
}

func (s *repoSyncer) syncBaseDirs(baseDirs *BaseDirStore) error {
	if baseDirs == nil {
		return nil
	}
	discovered := baseDirs.Repos()
	if len(discovered) > 0 {
		s.recordSource("basedir")
	}
	for _, r := range discovered {
		if err := s.apply(RepoInput{Name: r.Name, Path: r.Path, Source: "basedir"}); err != nil {
			return err
		}
	}
	return nil
}

func (s *repoSyncer) harvestRepoText(raw string) error {
	if raw == "" {
		return nil
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		in := RepoInput{Name: part, Source: "harvested"}
		if strings.HasPrefix(part, "/") || strings.HasPrefix(part, "~/") {
			if det, ok := DetectGitRepoRoot(part); ok {
				det.Source = "harvested"
				in = det
			} else {
				in = RepoInput{Path: part, Source: "harvested"}
			}
		}
		if err := s.apply(in); err != nil {
			return err
		}
	}
	return nil
}

func (s *repoSyncer) harvestRepoDirs(rec Record) error {
	for _, dirField := range []string{"defaultDir", "worktree"} {
		dir := strings.TrimSpace(rec.Text(dirField))
		if dir == "" {
			continue
		}
		if det, ok := DetectGitRepoRoot(dir); ok {
			det.Source = "harvested"
			if err := s.apply(det); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *repoSyncer) harvestRecords() error {
	s.recordSource("harvested")
	for _, kind := range []string{KindTask, KindLane} {
		res, err := s.store.Query(Query{Kind: kind})
		if err != nil {
			return err
		}
		for _, rec := range res.Items {
			if err := s.harvestRepoText(strings.TrimSpace(rec.Text("repo"))); err != nil {
				return err
			}
			if err := s.harvestRepoDirs(rec); err != nil {
				return err
			}
		}
	}
	return nil
}

// SyncRepos imports and deduplicates tracked git repositories from optional
// BaseDirStore scans, o-proj entries, and existing task/lane records.
func SyncRepos(store *Store, baseDirs *BaseDirStore, oProjEntries []RepoInput, harvestExisting bool, actor core.Actor) (RepoSyncReport, error) {
	if store == nil {
		return RepoSyncReport{}, errors.New("data: store is nil")
	}
	syncer := repoSyncer{store: store, actor: actor}
	if err := syncer.syncOProj(oProjEntries); err != nil {
		return syncer.report, err
	}
	if err := syncer.syncBaseDirs(baseDirs); err != nil {
		return syncer.report, err
	}
	if harvestExisting {
		if err := syncer.harvestRecords(); err != nil {
			return syncer.report, err
		}
	}
	if all, err := store.Query(Query{Kind: KindRepo}); err == nil {
		syncer.report.Repos = all.Items
	}
	return syncer.report, nil
}

// RecordRepos resolves the tracked repositories linked to rec via outgoing
// RelRepo relations, falling back to rec's legacy free-text "repo" field when
// no RelRepo relations exist.
func RecordRepos(store *Store, rec Record) ([]Record, []string) {
	if store != nil && rec.Kind != "" && rec.ID != "" {
		if linked, err := store.RelatedRecords(rec.Ref(), RelRepo, Outgoing); err == nil && len(linked) > 0 {
			repos := make([]Record, 0, len(linked))
			labels := make([]string, 0, len(linked))
			for _, r := range linked {
				repos = append(repos, r)
				label := strings.TrimSpace(r.Text("name"))
				if label == "" {
					label = strings.TrimSpace(r.Text("path"))
				}
				if label == "" {
					label = r.ID
				}
				labels = append(labels, label)
			}
			return repos, labels
		}
	}
	if legacy := strings.TrimSpace(rec.Text("repo")); legacy != "" {
		return nil, []string{legacy}
	}
	return nil, nil
}

// RepoTargetInfo extracts directory, repository identifier (preferring
// remoteUrl over name), and default branch for rec, handling both KindRepo
// records directly and records linked to KindRepo via RelRepo (with legacy
// "repo" fallback).
func RepoTargetInfo(store *Store, rec Record) (dir, repository, branch string) {
	if rec.Kind == KindRepo {
		dir = strings.TrimSpace(rec.Text("path"))
		repository = strings.TrimSpace(rec.Text("remoteUrl"))
		if repository == "" {
			repository = strings.TrimSpace(rec.Text("name"))
		}
		branch = strings.TrimSpace(rec.Text("defaultBranch"))
		return dir, repository, branch
	}
	repos, labels := RecordRepos(store, rec)
	if len(repos) > 0 {
		first := repos[0]
		dir = strings.TrimSpace(first.Text("path"))
		repository = strings.TrimSpace(first.Text("remoteUrl"))
		if repository == "" {
			repository = strings.TrimSpace(first.Text("name"))
		}
		branch = strings.TrimSpace(first.Text("defaultBranch"))
		return dir, repository, branch
	}
	if len(labels) > 0 {
		repository = labels[0]
	}
	return "", repository, ""
}
