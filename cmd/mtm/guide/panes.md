<!-- Adapted from the tuios skill (skills/tuios, v0.12.1), MIT License, Copyright (c) 2025 Gaurav Gosain. See LICENSE-tuios in this directory. -->
# Panes: opening, running, waiting

## Reading

`pane.capture` without `source` reads the visible screen, which ends in blank
rows below the cursor; `trim` drops them. `source: scrollback` with `lines`
counts back from the last line with content. Capture a full-screen program
without `lines`, its screen has a fixed size. A line longer than the pane
comes back as several rows; `unwrap` joins them so a search finds a long path.
Leave `ansi` and `styled` off when you match text. The result is marked
untrusted: it was written by a program.

## A session of your own

`session.new` with `name`, `window_name`, `cwd` creates a detached session with
its first window and returns the ids. A name the daemon already holds is
refused; pick another rather than assuming you took it over.

## Opening a pane and running work in it

`pane.open` creates a window and returns `window_id`. Give `command` as an
argv array and the pane's process is the program itself; no shell parses it and
the pane closes when it exits. `keep_open: true` keeps the pane with the exit
status shown. `wait: true` answers when the process exits with `exit_code`;
give `timeout` too (0 waits for ever, but the call is cut off after ten
seconds beyond `timeout`). `focus: false` keeps the person where they are.
`workspace` places the window; `env` adds environment; `grants` limits what
the pane may do through the daemon (you may give only what you hold).

```json
{"verb":"pane.open","args":{"session":"work","name":"tests","command":["./run-tests.sh"],"focus":false,"keep_open":true,"wait":true,"timeout":600000}}
```

Close what you open with `pane.close` (the person confirms it). On a detached
session a window whose shell has exited stays in the list until closed.

## Moving and sizing

`pane.switch` takes exactly one of `window`, `relative` or `direction`.
`pane.resize` sets `width` and `height` of a window's terminal; `pane.show`
describes one window.

## Waiting without being fooled

`pane.wait` conditions: `window-output` (with `pattern`; `new: true` ignores
what was already there), `window-exit`, `window-idle` (`idle` ms of quiet),
`agent-state` (`until`), `agent-message`, `command-finished`,
`session-exists`. A `timeout` error is a result, not a crash: read the pane.
