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
// is the framework's.
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
// The action history is in memory and per process: history list shows the
// requests this invocation ran.

// shell runs CLI commands against one runtime.
type shell struct {
	r *ochazuke.Runtime
	// reveal makes an instance exist for a target no list shows yet (the
	// app loads the rows that hold it); the shell settles what it
	// scheduled and checks again.
	reveal func(id string) error
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
	q, yes, err := s.request(args[0], args[1], args[2:])
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
	jobs := len(s.r.Jobs())
	value, err := s.call(q)
	if err != nil {
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
