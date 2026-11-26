package data

import (
	"slices"

	"ofam.dev/ochazuke/core"
)

// TypeSpec declares the appearance and direct verbs of a pointer target type.
type TypeSpec struct {
	Kind, Parent, Glyph, Tone string
	Verbs                     []ChipVerb
}

type ChipVerb struct {
	ID, Label, Description, Command string
	Args                            []string
}

// PointerChip points to a value through the record or resource that supplied it.
// TargetKind can differ from Ref.Kind, for example when a lane supplies a branch.
type PointerChip struct {
	ID                   string
	Ref                  core.Ref
	TargetKind, TargetID string
	Label, Glyph, Tone   string
	Compact              bool
	Meta                 map[string]string
}

type TypeRegistry struct {
	types map[string]TypeSpec
}

// ResolvedChipVerb identifies where an effective verb was declared.
type ResolvedChipVerb struct {
	ChipVerb
	Kind string
}

func NewTypeRegistry(specs ...TypeSpec) *TypeRegistry {
	r := &TypeRegistry{types: make(map[string]TypeSpec, len(specs))}
	for _, spec := range specs {
		if spec.Kind != "" {
			r.types[spec.Kind] = cloneTypeSpec(spec)
		}
	}
	return r
}

func cloneTypeSpec(spec TypeSpec) TypeSpec {
	spec.Verbs = slices.Clone(spec.Verbs)
	for i := range spec.Verbs {
		spec.Verbs[i].Args = slices.Clone(spec.Verbs[i].Args)
	}
	return spec
}

func (r *TypeRegistry) Lookup(kind string) (TypeSpec, bool) {
	spec, ok := r.types[kind]
	return cloneTypeSpec(spec), ok
}

// ResolveLineage returns the registered kinds starting with kind, stopping at
// an unregistered parent or a cycle.
func (r *TypeRegistry) ResolveLineage(kind string) []string {
	var lineage []string
	seen := map[string]bool{}
	for kind != "" && !seen[kind] {
		spec, ok := r.types[kind]
		if !ok {
			break
		}
		lineage = append(lineage, kind)
		seen[kind] = true
		kind = spec.Parent
	}
	return lineage
}

// ResolveVerbs walks the lineage child first, so the first declaration of a
// verb ID wins. Each type's declaration order is preserved.
func (r *TypeRegistry) ResolveVerbs(kind string) []ChipVerb {
	var verbs []ChipVerb
	for _, resolved := range r.ResolveVerbOrigins(kind) {
		verbs = append(verbs, resolved.ChipVerb)
	}
	return verbs
}

func (r *TypeRegistry) ResolveVerbOrigins(kind string) []ResolvedChipVerb {
	var verbs []ResolvedChipVerb
	seen := map[string]bool{}
	for _, origin := range r.ResolveLineage(kind) {
		for _, verb := range r.types[origin].Verbs {
			if verb.ID == "" || seen[verb.ID] {
				continue
			}
			seen[verb.ID] = true
			verb.Args = slices.Clone(verb.Args)
			verbs = append(verbs, ResolvedChipVerb{ChipVerb: verb, Kind: origin})
		}
	}
	return verbs
}

const (
	ChipKindDir      = "dir"
	ChipKindWorktree = "worktree"
	ChipKindBranch   = "branch"
	ChipKindGitRef   = "git.ref"
	ChipKindPR       = "pr"
	ChipKindURL      = "url"
	ChipKindFile     = "file"
	ChipKindPath     = "path"
)

const (
	ChipCmdCD     = "path.cd"
	ChipCmdList   = "path.ls"
	ChipCmdFind   = "path.find"
	ChipCmdMove   = "path.move"
	ChipCmdDelete = "path.delete"
	ChipCmdOpen   = "path.open"
	ChipCmdEdit   = "path.edit"
	ChipCmdCopy   = "path.copy"
	ChipCmdReveal = "path.reveal"
)

func DefaultTypeRegistry() *TypeRegistry {
	verb := func(id, label, description, command string, args ...string) ChipVerb {
		return ChipVerb{ID: id, Label: label, Description: description, Command: command, Args: args}
	}
	return NewTypeRegistry(
		TypeSpec{Kind: ChipKindDir, Glyph: "▣", Tone: "info", Verbs: []ChipVerb{
			verb("cd", "Open shell", "Start a shell in this directory", ChipCmdCD),
			verb("ls", "List files", "List directory entries", ChipCmdList, "-la", "--"),
			verb("find", "Find files", "Search below this directory", ChipCmdFind),
			verb("move", "Move directory", "Choose a new path for this directory", ChipCmdMove),
			verb("delete", "Delete directory", "Delete this directory after confirmation", ChipCmdDelete),
		}},
		TypeSpec{Kind: ChipKindWorktree, Parent: ChipKindDir, Glyph: "⌘", Tone: "accent", Verbs: []ChipVerb{
			verb("status", "Git status", "Show changes in this worktree", "git.status"),
			verb("diff", "Git diff", "Inspect this worktree's changes", "git.diff"),
			verb("remove", "Remove worktree", "Remove this worktree after confirmation", "git.worktree.remove"),
		}},
		TypeSpec{Kind: ChipKindBranch, Parent: ChipKindGitRef, Glyph: "⎇", Tone: "accent", Verbs: []ChipVerb{
			verb("checkout", "Check out branch", "Switch the worktree to this branch", "git.checkout"),
			verb("rebase", "Rebase onto branch", "Rebase the current branch onto this branch", "git.rebase"),
			verb("merge", "Merge branch", "Merge this branch into the current branch", "git.merge"),
			verb("merge-base", "Find merge base", "Find the common ancestor of HEAD and this branch", "git.merge-base"),
			verb("sha", "Show branch SHA", "Resolve this branch to a commit", "git.sha"),
			verb("push", "Push branch", "Push this branch to its remote", "git.push"),
			verb("log", "Show branch log", "List this branch's commits", "git.log"),
		}},
		TypeSpec{Kind: ChipKindGitRef, Glyph: "#", Tone: "info", Verbs: []ChipVerb{
			verb("sha", "Show commit SHA", "Resolve this Git ref to a commit", "git.sha"),
			verb("log", "Git log", "List commits from this ref", "git.log"),
			verb("diff", "Git diff", "Compare changes with this ref", "git.diff"),
			verb("show", "Show commit", "Inspect this ref's commit", "git.show"),
		}},
		TypeSpec{Kind: ChipKindPR, Parent: ChipKindURL, Glyph: "⇧", Tone: "accent", Verbs: []ChipVerb{
			verb("checks", "PR checks", "Show check results for this pull request", "gh.pr.checks"),
			verb("view", "View pull request", "Show pull request details", "gh.pr.view"),
			verb("diff", "PR diff", "Inspect the pull request's diff", "gh.pr.diff"),
			verb("review", "Review pull request", "Inspect the pull request for review", "gh.pr.review"),
			verb("merge", "Merge pull request", "Choose how to merge this pull request", "gh.pr.merge"),
			verb("open", "Open pull request", "Open the pull request in the browser", "gh.pr.open"),
		}},
		TypeSpec{Kind: ChipKindURL, Glyph: "↗", Tone: "info", Verbs: []ChipVerb{
			verb("open", "Open link", "Open the URL in its configured handler", ChipCmdOpen),
			verb("copy", "Copy link", "Copy the URL to the clipboard", ChipCmdCopy),
		}},
		TypeSpec{Kind: ChipKindFile, Parent: ChipKindPath, Glyph: "▤", Tone: "info", Verbs: []ChipVerb{
			verb("open", "Open file", "Open the file in its configured handler", ChipCmdOpen),
			verb("edit", "Edit file", "Open the file in your editor", ChipCmdEdit),
			verb("copy", "Copy file path", "Copy the file path to the clipboard", ChipCmdCopy),
			verb("reveal", "Reveal file", "Show the file in your file manager", ChipCmdReveal),
		}},
		TypeSpec{Kind: ChipKindPath, Glyph: "•", Tone: "muted", Verbs: []ChipVerb{
			verb("copy", "Copy path", "Copy the path to the clipboard", ChipCmdCopy),
			verb("reveal", "Reveal path", "Show the path in your file manager", ChipCmdReveal),
		}},
	)
}
