// Package notify delivers text through terminal and argv senders. It owns no
// timers, database state, or UI state; sources claim their own occurrences.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// OSCommand keeps AppleScript source constant; text is passed only through argv.
var OSCommand = []string{"/usr/bin/osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", "--", "{{title}}", "{{body}}"}

func Terminal(title, body string) string {
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, s)
	}
	// The fixed, nonnumeric prefix avoids OSC 9's ConEmu subcommand syntax.
	return "\x1b]9;mtm: " + clean(title) + " · " + clean(body) + "\x1b\\"
}

func Commands(setting, text string) ([][]string, error) {
	var commands [][]string
	if err := json.Unmarshal([]byte(text), &commands); err != nil {
		return nil, fmt.Errorf("%s: want a JSON array of argv arrays: %w", setting, err)
	}
	if commands == nil {
		return nil, fmt.Errorf("%s: use [] for no extra senders, not null", setting)
	}
	for _, argv := range commands {
		if len(argv) == 0 || argv[0] == "" {
			return nil, fmt.Errorf("%s: each sender needs an executable", setting)
		}
		for _, arg := range argv {
			if strings.ContainsRune(arg, 0) {
				return nil, fmt.Errorf("%s: an argument contains NUL", setting)
			}
		}
	}
	return commands, nil
}

// Sender is a configuration snapshot. Send succeeds when at least one endpoint
// accepts the text; partial errors remain visible but must not cause duplicates.
type Sender struct {
	Channel  string
	Commands [][]string
	Terminal func(context.Context, string) error
	Exec     func(context.Context, []string) (string, error)
}

func (s Sender) Send(ctx context.Context, title, body string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	sent := false
	var failures []error
	if s.Channel == "terminal" || s.Channel == "both" {
		err := s.Terminal(ctx, Terminal(title, body))
		if err == nil {
			sent = true
		} else {
			failures = append(failures, fmt.Errorf("terminal: %w", err))
		}
	}
	replace := strings.NewReplacer("{{title}}", title, "{{body}}", body)
	for _, template := range s.Commands {
		argv := make([]string, len(template))
		for i, arg := range template {
			argv[i] = replace.Replace(arg)
		}
		out, err := s.Exec(ctx, argv)
		if err == nil {
			sent = true
		} else {
			failures = append(failures, fmt.Errorf("%s: %w: %s", argv[0], err, strings.TrimSpace(out)))
		}
	}
	if !sent && len(failures) == 0 {
		failures = append(failures, errors.New("no notification sender succeeded"))
	}
	return sent, errors.Join(failures...)
}
