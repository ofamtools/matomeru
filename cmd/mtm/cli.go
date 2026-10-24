package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/unixsuperhero/ochazuke"
)

// The CLI shell: mtm NOUN VERB [--target ID] [--json '{...}'] [--ARG VALUE]
// [--yes] runs one registered operation in this process through the
// runtime's CLI entry (Runtime.HandleCall, recorded with the source "cli"),
// the same registry the keys and the palette dispatch into. It knows no
// noun, verb or key: a command's words are the operation key, its --ARG
// flags are typed by the operation's own parameters, and everything else
// is the framework's. A VERB may be a unique prefix of one of the noun's
// verbs (the noun's prefix is resolved by the oden shell, oden.go).
//
// After the operation it settles the work the operation scheduled (the
// pending commands, delivered through Runtime.Deliver as the Bubble Tea
// loop would), so an application verb's job finishes before the process
// exits. A confirm modal the operation opened is answered: --yes runs
// confirm.accept, otherwise the prompt is printed and a line read from
// stdin (y or yes accepts; anything else, or end of input, declines with
// modal.pop and a non-zero exit). Any other modal (a prompt, the palette)
// needs the TUI and fails, naming the arguments to pass instead.
//
// The CLI has no screen, so a terminal an operation or a completion adds
// (a launch ends attached to its pane) is removed before the next work is
// run: its stream is released and its first read, cancelled, returns at
// once instead of waiting for the pane to change.
//
// A command without --target has no focus to act from, so the shell names
// the owner: the instance the app declares for a noun that has none of its
// own (owners: panes → panes/list, so panes refresh, whose job is scoped to
// the list, runs), else the one instance of the operation's noun when
// exactly one exists. With several the command runs as before and, if it
// fails, the error lists them to pass as --target (pane preview: one per
// pane row); with none it runs as before.
//
// The action history is in memory and per process: history list shows the
// requests this invocation ran.

// shell runs CLI commands against one runtime.
type shell struct {
	r *ochazuke.Runtime
	// reveal makes an instance exist for a target no list shows yet (the
	// app loads the rows that hold it); the shell settles what it
	// scheduled and checks again.
	reveal func(id string) error
	// owners maps a noun with no instance of its own to the instance its
	// verbs act for; a command of that noun without --target targets it.
	owners map[string]string
	in     *bufio.Reader
	out    io.Writer
	errOut io.Writer
}

const cliUsage = "usage: mtm [--data PATH] [--server NAME|default] [--socket PATH] NOUN VERB [--target ID] [--json '{...}'] [--ARG VALUE] [--yes]\n(operations list prints every NOUN VERB and its parameters)"

// run executes one command and prints its result as JSON.
func (s *shell) run(args []string) error {
	if len(args) < 2 {
		return errors.New(cliUsage)
	}
	verb, err := s.verb(args[0], args[1])
	if err != nil {
		return err
	}
	q, yes, err := s.request(args[0], verb, args[2:])
	if err != nil {
		return err
	}
	if q.Target != "" && q.Target != "focused-input" && s.r.Instances[q.Target] == nil {
		if err := s.reveal(q.Target); err != nil {
			return fmt.Errorf("target %s: %w", q.Target, err)
		}
		if err := s.settle(); err != nil {
			return fmt.Errorf("target %s: %w", q.Target, err)
		}
	}
	var candidates []string
	if q.Target == "" {
		candidates = s.ownersOf(q.Noun)
		if len(candidates) == 1 {
			q.Target = candidates[0]
		}
	}
	jobs := len(s.r.Jobs())
	value, err := s.call(q)
	if err != nil {
		if len(candidates) > 1 {
			return fmt.Errorf("%w; pass --target, one of: %s", err, listed(candidates))
		}
		return err
	}
	for {
		top := s.r.Instances[s.r.TopModal()]
		if top == nil {
			break
		}
		if top.Noun != "confirm" {
			return fmt.Errorf("%s opened a %s modal, which needs the TUI; pass its arguments instead (mtm operations list)", q.Key(), top.Noun)
		}
		if !yes && !s.accepts(top.Payload.Prompt) {
			if _, err := s.call(ochazuke.Request{Verb: "pop", Noun: "modal"}); err != nil {
				return err
			}
			return fmt.Errorf("declined: %s", top.Payload.Prompt)
		}
		if value, err = s.call(ochazuke.Request{Verb: "accept", Noun: "confirm"}); err != nil {
			return err
		}
	}
	settled := s.settle()
	for _, j := range s.r.Jobs()[min(jobs, len(s.r.Jobs())):] {
		fmt.Fprintf(s.errOut, "job #%d %s %s\n", j.Job, j.Verb, j.State)
	}
	if err := s.print(value); err != nil {
		return err
	}
	return settled
}

// verb resolves a verb word of noun: the exact verb, else the one verb
// of noun starting with it (task l is task launch). Several are an error
// naming them; none is the word unchanged, which request reports.
func (s *shell) verb(noun, word string) (string, error) {
	if _, ok := s.r.Operations[noun+"."+word]; ok {
		return word, nil
	}
	var matches []string
	for _, op := range s.r.Operations {
		if op.Noun == noun && strings.HasPrefix(op.Verb, word) {
			matches = append(matches, op.Verb)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return word, nil
	case 1:
		return matches[0], nil
	}
	return "", fmt.Errorf("%s %s is ambiguous: %s", noun, word, strings.Join(matches, ", "))
}

// ownersOf is the instances a command of noun without --target can act
// for: the declared owner when it exists, else every instance of noun (app
// aside), in id order.
func (s *shell) ownersOf(noun string) []string {
	if id := s.owners[noun]; id != "" && s.r.Instances[id] != nil {
		return []string{id}
	}
	var ids []string
	for id, inst := range s.r.Instances {
		if id != "app" && inst.Noun == noun {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// maxCandidates is how many instance ids an ambiguous command's error lists.
const maxCandidates = 10

// listed joins ids for an error, at most maxCandidates of them.
func listed(ids []string) string {
	if len(ids) <= maxCandidates {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(ids[:maxCandidates], ", "), len(ids)-maxCandidates)
}

// request builds the operation request: --target, --json and --yes are the
// shell's; every other --NAME VALUE (or --NAME=VALUE) is an argument typed
// by the operation's parameter of that name.
func (s *shell) request(noun, verb string, flags []string) (ochazuke.Request, bool, error) {
	q := ochazuke.Request{Noun: noun, Verb: verb, Args: ochazuke.Arguments{}}
	op, ok := s.r.Operations[q.Key()]
	if !ok {
		return q, false, fmt.Errorf("unknown operation %q (%s %s); operations list prints them", q.Key(), noun, verb)
	}
	yes := false
	for i := 0; i < len(flags); i++ {
		name, ok := strings.CutPrefix(flags[i], "--")
		if !ok || name == "" {
			return q, false, fmt.Errorf("unexpected argument %q; %s", flags[i], cliUsage)
		}
		name, value, inline := strings.Cut(name, "=")
		takeValue := func() (string, error) {
			if inline {
				return value, nil
			}
			if i+1 >= len(flags) {
				return "", fmt.Errorf("--%s needs a value", name)
			}
			i++
			return flags[i], nil
		}
		switch {
		case name == "yes":
			yes = true
			continue
		case name == "target":
			v, err := takeValue()
			if err != nil {
				return q, false, err
			}
			q.Target = v
			continue
		case name == "json":
			v, err := takeValue()
			if err != nil {
				return q, false, err
			}
			args, err := ochazuke.ParseArguments(v)
			if err != nil {
				return q, false, err
			}
			for k, v := range args {
				q.Args[k] = v
			}
			continue
		}
		p, ok := op.Parameters[name]
		if !ok {
			return q, false, fmt.Errorf("%s has no parameter %q", q.Key(), name)
		}
		if p.Type == "boolean" && !inline && (i+1 >= len(flags) || strings.HasPrefix(flags[i+1], "--")) {
			q.Args[name] = true
			continue
		}
		v, err := takeValue()
		if err != nil {
			return q, false, err
		}
		if q.Args[name], err = typed(p.Type, v); err != nil {
			return q, false, fmt.Errorf("--%s: %w", name, err)
		}
	}
	return q, yes, nil
}

// typed converts a flag value to its parameter's type; object and array
// values are JSON.
func typed(kind, v string) (any, error) {
	switch kind {
	case "integer":
		return strconv.Atoi(v)
	case "boolean":
		return strconv.ParseBool(v)
	case "object", "array":
		var out any
		err := json.Unmarshal([]byte(v), &out)
		return out, err
	}
	return v, nil
}

// call runs one request through the runtime's CLI entry.
func (s *shell) call(q ochazuke.Request) (any, error) {
	reply := make(chan ochazuke.Response, 1)
	s.r.HandleCall(ochazuke.CallMsg{Request: q, Reply: reply})
	res := <-reply
	if res.Error != "" {
		return nil, errors.New(res.Error)
	}
	return res.Result, nil
}

// accepts prints a confirm prompt and reads the answer from stdin.
func (s *shell) accepts(prompt string) bool {
	fmt.Fprintf(s.errOut, "%s [y/N] ", prompt)
	line, _ := s.in.ReadString('\n')
	fmt.Fprintln(s.errOut)
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// settle runs the scheduled work until none is left, delivering every
// message on this goroutine as the Bubble Tea loop would, and returns the
// errors of the completions it delivered.
func (s *shell) settle() error {
	var errs []error
	for {
		errs = append(errs, s.closeTerminals())
		cmd := s.r.PendingCommands()
		if cmd == nil {
			break
		}
		errs = append(errs, s.deliver(cmd))
	}
	return errors.Join(errs...)
}

// closeTerminals removes every terminal instance, in id order; closing one
// cancels its read and releases its stream.
func (s *shell) closeTerminals() error {
	var ids []string
	for id := range s.r.Instances {
		if s.r.Terminal(id) != nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var errs []error
	for _, id := range ids {
		errs = append(errs, s.r.RemoveInstance(id))
	}
	return errors.Join(errs...)
}

func (s *shell) deliver(cmd tea.Cmd) error {
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var errs []error
		for _, c := range msg {
			if c != nil {
				errs = append(errs, s.deliver(c))
			}
		}
		return errors.Join(errs...)
	case nil:
		return nil
	default:
		_, err := s.r.Deliver(msg)
		return err
	}
}

// print writes the result as JSON: an array one compact element per line
// (bindings list, history list read as tables), anything else indented.
func (s *shell) print(value any) error {
	if items, ok := value.([]any); ok && len(items) > 0 {
		lines := make([]string, len(items))
		for i, item := range items {
			data, err := json.Marshal(item)
			if err != nil {
				return err
			}
			lines[i] = string(data)
		}
		_, err := fmt.Fprintf(s.out, "[\n  %s\n]\n", strings.Join(lines, ",\n  "))
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.out, "%s\n", data)
	return err
}
