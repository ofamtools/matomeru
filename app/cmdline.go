package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/commandline"
	"ofam.dev/ochazuke/widgets/modal"
)

// Shell I/O redirection via Ochazuke's widgets/commandline:
//
//	!   run a shell command with $REF/$ID/$TITLE/$FILE/$DIR expanded
//	|   pipe the focused record into a shell command (captures stdout into a run + register)
//	r!  read a shell command's stdout into a new note about the focused record
//
// Inside the bar: full Readline editing, up/down per-prefix history, and
// ctrl+r <reg> to insert any Vim register ("", "0, "%, "a-"z, "+, ":, "/).

func (a *App) shellVars(ref *core.Ref) (vars map[string]string, dir string) {
	vars = map[string]string{}
	if ref == nil || ref.ID == "" {
		return vars, ""
	}
	vars["REF"] = data.RefText(*ref)
	vars["ID"] = ref.ID
	vars["TITLE"] = a.title(*ref)
	rec, err := a.Store.Get(*ref)
	if err != nil {
		if extRec, ok := a.cachedExternal(*ref); ok {
			rec = &extRec
		} else {
			return vars, ""
		}
	}
	if fp := rec.Text("filePath"); fp != "" {
		vars["FILE"] = expandHome(fp)
		dir = filepath.Dir(vars["FILE"])
	} else if u := rec.Text("url"); u != "" {
		vars["FILE"] = u
	}
	if d := rec.Text("defaultDir"); d != "" {
		dir = expandHome(d)
	} else if d := rec.Text("cwd"); d != "" {
		dir = expandHome(d)
	}
	if dir != "" {
		vars["DIR"] = dir
	}
	return vars, dir
}

func (a *App) shellOpen(s core.State, c core.Command) core.StepResult {
	a.syncPath(s)
	prefix := commandline.Prefix(c.Arg("prefix"))
	if prefix == "" {
		prefix = commandline.PrefixShell
	}
	if a.nav.cmdline == nil {
		a.nav.cmdline = commandline.New(prefix)
		a.nav.cmdline.RegisterLookup = func(reg rune) (string, bool) {
			v := a.Register(reg)
			return v, v != ""
		}
	} else {
		a.nav.cmdline.Open(prefix)
	}
	var target *core.Ref
	if ref, ok := a.subject(s, c); ok {
		r := ref
		target = &r
	}
	vars, dir := a.shellVars(target)
	a.nav.cmdline.Vars = vars

	title := "Shell (!)"
	switch prefix {
	case commandline.PrefixPipe:
		title = "Pipe focused record (|)"
		a.say("pipe focused record into shell · ctrl+r reg pastes · enter runs")
	case commandline.PrefixReadShell:
		title = "Read shell into note (r!)"
		a.say("read shell stdout into a new note · ctrl+r reg pastes · enter runs")
	default:
		a.say("run shell command · $REF $TITLE $FILE $DIR expand · ctrl+r reg pastes")
	}
	titleSuffix := ""
	if target != nil {
		titleSuffix = a.title(*target)
	}

	m := modal.NewCommandLineModal(a.nav.cmdline, modal.CommandLineOptions{
		ID:          "commandline",
		Scope:       "commandline",
		Mode:        ModeCommandLine,
		FocusID:     "commandline.input",
		Title:       title,
		TitleSuffix: titleSuffix,
		WidthPct:    78,
		VarHintKeys: []string{"REF", "DIR", "FILE"},
		OnSubmit: func(sub commandline.Submission) modal.Result {
			a.afterModal(func(st core.State) core.State {
				return a.execShellSubmission(st, sub, target, dir)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, m))
}

func (a *App) recordPayload(ref *core.Ref) string {
	if ref == nil || ref.ID == "" {
		return ""
	}
	rec, err := a.Store.Get(*ref)
	if err != nil {
		if extRec, ok := a.cachedExternal(*ref); ok {
			rec = &extRec
		} else {
			return data.RefText(*ref)
		}
	}
	if body := rec.Text("body"); body != "" {
		return body
	}
	b, err := json.MarshalIndent(rec.MergedFields(), "", "  ")
	if err != nil {
		return data.RefText(*ref)
	}
	return string(b) + "\n"
}

func (a *App) execShellSubmission(s core.State, sub commandline.Submission, target *core.Ref, dir string) core.State {
	cmdLine := strings.TrimSpace(sub.ShellCmd)
	if sub.Prefix == commandline.PrefixPipe {
		cmdLine = strings.TrimSpace(sub.PipeCmd)
	}
	if cmdLine == "" {
		a.say("empty command")
		return s
	}
	a.nav.Registers.SetSpecial(core.RegCommand, string(sub.Prefix)+sub.Raw)
	switch sub.Prefix {
	case commandline.PrefixShell:
		return a.startShell(s, "", cmdLine, dir)
	case commandline.PrefixPipe:
		stdinText := a.recordPayload(target)
		a.say("piping into %s…", clip(sub.Raw, 40))
		var taskRef *core.Ref
		if target != nil && target.Kind == data.KindTask {
			r := *target
			taskRef = &r
		}
		a.background(func(ctx context.Context) func(core.State) core.State {
			out, exitCode, runErr := runShellCapture(ctx, cmdLine, dir, stdinText)
			return func(s core.State) core.State {
				status := "done"
				if runErr != nil || exitCode != 0 {
					status = "failed"
				}
				fields := map[string]any{
					"title":     "| " + sub.Raw,
					"command":   cmdLine,
					"runStatus": status,
					"exitCode":  exitCode,
					"output":    out,
				}
				if dir != "" {
					fields["cwd"] = dir
				}
				if taskRef != nil {
					fields["task"] = *taskRef
				}
				rec, _, err := a.Store.Create(data.CreateInput{Kind: data.KindRun, Fields: fields}, User)
				if err != nil {
					a.fail("pipe run", err)
					return s
				}
				if out != "" {
					a.nav.Registers.Yank(0, out, false)
				}
				a.viewMemo.gen++
				selectRow(a.records(runsTable, data.KindRun), data.RefText(rec.Ref()))
				a.say("pipe finished (exit %d) · saved as run & yanked", exitCode)
				return s
			}
		})
		return s
	case commandline.PrefixReadShell:
		a.say("reading %s…", clip(sub.Raw, 40))
		var aboutRef *core.Ref
		if target != nil {
			r := *target
			aboutRef = &r
		}
		a.background(func(ctx context.Context) func(core.State) core.State {
			out, exitCode, runErr := runShellCapture(ctx, cmdLine, dir, "")
			return func(s core.State) core.State {
				if runErr != nil && out == "" {
					a.fail("read shell", runErr)
					return s
				}
				rec, cs, err := a.Store.Create(data.CreateInput{
					Kind:   data.KindNote,
					Fields: map[string]any{"title": "r! " + sub.Raw, "body": out},
				}, User)
				if err != nil {
					a.fail("create note from shell", err)
					return s
				}
				if aboutRef != nil {
					_ = a.ensureShadow(*aboutRef)
					_, _, _ = a.Store.Relate(data.RelateInput{From: rec.Ref(), Kind: data.RelAbout, To: *aboutRef}, User)
				}
				if out != "" {
					a.nav.Registers.Yank(0, out, false)
				}
				a.viewMemo.gen++
				a.wrote(fmt.Sprintf("captured note from r! (exit %d)", exitCode), cs, nil)
				return s
			}
		})
		return s
	}
	return s
}

func runShellCapture(ctx context.Context, command, dir, stdin string) (output string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	if dir != "" {
		cmd.Dir = dir
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err = cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return buf.String(), exitCode, err
}
