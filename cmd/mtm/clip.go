package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/oden"
)

// systemClipboardCopy puts text on the system clipboard (pbcopy on macOS,
// else the first of wl-copy, xclip and xsel that is installed). Tests swap it.
var systemClipboardCopy = func(text string) error {
	candidates := [][]string{{"pbcopy"}}
	if runtime.GOOS != "darwin" {
		candidates = append(candidates, []string{"wl-copy"}, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	}
	for _, argv := range candidates {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}
	return errors.New("no system clipboard command found (pbcopy, wl-copy, xclip or xsel)")
}

// clipContent is the content of `mtm clip put`: the positional words, or
// stdin when they are "-".
func clipContent(words []string, stdin io.Reader) (string, error) {
	content := strings.Join(words, " ")
	if content != "-" {
		return content, nil
	}
	raw, err := io.ReadAll(stdin)
	return strings.TrimRight(string(raw), "\n"), err
}

// clipAuthorDefault is who a CLI put is from when --author is not given: an
// agent inside a Matomeru pane, the owner in any other shell.
func clipAuthorDefault() string {
	if strings.TrimSpace(os.Getenv(mcpui.EnvPaneID)) != "" {
		return data.ClipAuthorAgent
	}
	return data.ClipAuthorOwner
}

func mtmClipPutCommand(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "title", Type: "string", Description: "One-line label (derived from the content when omitted)"},
		{Name: "kind", Type: "string", Enum: data.ClipKinds, Description: "What the content is (default: url or path when it looks like one, else text)"},
		{Name: "author", Type: "string", Enum: data.ClipAuthorKinds, Description: "Who shares the clip (default: agent inside a pane, else owner)"},
		{Name: "project", Type: "string", Description: "Project or task ID (default: $MTM_TASK_ID or the pane's task)"},
		{Name: "copy", Type: "bool", Description: "Also copy the content to the system clipboard"},
		paramFormatJSON,
	}
	return oden.Command{
		Name:       "put",
		Summary:    "share text, a path, a URL, code or a reference on the shared clipboard",
		Usage:      "[content...|-] [--title T] [--kind text|path|url|code|ref] [--project ID] [--copy]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			content, err := clipContent(p.Positional(), c.Stdin)
			if err != nil {
				return err
			}
			if strings.TrimSpace(content) == "" {
				return errors.New("usage: mtm clip put <content...> (or - to read stdin)")
			}
			author := p.String("author")
			if author == "" {
				author = clipAuthorDefault()
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()
			clip, err := store.PutClip(mcpui.NewClipboardTools(store).Defaults(data.ClipInput{
				Title: p.String("title"), Content: content, Kind: p.String("kind"), AuthorKind: author, Project: p.String("project"),
			}), cliActor)
			if err != nil {
				return err
			}
			if p.Bool("copy") {
				if err := systemClipboardCopy(content); err != nil {
					return fmt.Errorf("clip %s was shared, but not copied: %w", clip.ID, err)
				}
			}
			return printRecordOne(c.Stdout, store, data.KindClip, recordFormat(p), clip)
		},
	}
}

func mtmClipListCommand(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "project", Type: "string", Description: "Only clips of this project (a project or task ID)"},
		{Name: "author", Type: "string", Enum: data.ClipAuthorKinds, Description: "Only clips shared by the owner or by agents"},
		{Name: "kind", Type: "string", Enum: data.ClipKinds, Description: "Only clips of this kind"},
		{Name: "limit", Type: "int", Default: "0", Description: "Keep the newest N clips (0 = all)"},
		paramFormatJSON,
	}
	return oden.Command{
		Name:       "list",
		Summary:    "list the shared clipboard, newest first",
		Usage:      "[--project ID] [--author owner|agent] [--kind KIND] [--limit N] [--format json|table]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()
			clips, err := store.ListClips(data.ClipFilter{Project: p.String("project"), AuthorKind: p.String("author"), ClipKind: p.String("kind"), Limit: p.Int("limit")})
			if err != nil {
				return err
			}
			return printRecords(c.Stdout, store, data.KindClip, recordFormat(p), clips)
		},
	}
}

func mtmClipCopyCommand(g globalOpts) oden.Command {
	const use = "usage: mtm clip copy [<id>] (no id copies the newest clip)"
	return oden.Command{
		Name:    "copy",
		Summary: "copy a clip's content to the system clipboard (the newest clip when no id is given)",
		Usage:   "[<id>]",
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(nil)
			if err != nil {
				return err
			}
			pos := p.Positional()
			if len(pos) > 1 {
				return errors.New(use)
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()
			var id string
			if len(pos) == 1 {
				id, err = resolveRecordID(c.Context, store, data.KindClip, strings.TrimPrefix(pos[0], data.KindClip+":"), c.Stdin, use)
			} else {
				id, err = newestClipID(store)
			}
			if err != nil {
				return err
			}
			clip, err := store.GetClip(id)
			if err != nil {
				return err
			}
			if err := systemClipboardCopy(clip.Text("content")); err != nil {
				return err
			}
			if _, err := store.MarkClipCopied(clip.ID, time.Now(), cliActor); err != nil {
				return err
			}
			_, err = fmt.Fprintf(c.Stdout, "copied clip %s to the system clipboard: %s\n", clip.ID, clip.Text("title"))
			return err
		},
	}
}

func newestClipID(store *data.Store) (string, error) {
	clips, err := store.ListClips(data.ClipFilter{Limit: 1})
	if err != nil {
		return "", err
	}
	if len(clips) == 0 {
		return "", errors.New("the shared clipboard is empty")
	}
	return clips[0].ID, nil
}

// withClipCommands gives the clip noun its shared clipboard verbs: put and
// copy, and a list that is newest first and filters by project.
func withClipCommands(children []oden.Command, g globalOpts) []oden.Command {
	children = slices.DeleteFunc(children, func(c oden.Command) bool { return c.Name == "list" })
	return append(children, mtmClipPutCommand(g), mtmClipListCommand(g), mtmClipCopyCommand(g))
}
