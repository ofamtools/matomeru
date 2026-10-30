# README demo

`demo.tape` records `assets/demo.gif`, the animation under the README header. It drives `mtm` over invented projects, tasks and notes that `seed.sh` writes.

Regenerate from the repository root (needs [VHS](https://github.com/charmbracelet/vhs) and Go; `brew install vhs`):

```sh
vhs docs/vhs/demo.tape
```

It takes about a minute and writes a GIF of about 18 seconds and about 2 MB.

## Your data stays out of it

Off camera, the tape makes a fresh `/tmp/mtm.XXXX` and runs `seed.sh` on it. The script builds `mtm` into that directory and writes an `env` file that points `HOME`, every `XDG_*` directory and `PATH` inside it, unsets `TUIOS_*`, `MTM_CONFIG_DIR` and `MTM_SERVER`, and defines an `mtm` function that always passes `--data`, `--config` and `--socket` there. The socket has nothing listening, so mtm never reaches or starts a TUIOS daemon (the Panes page is not shown for that reason). The seed goes through mtm's own CLI (`project new`, `project edit`, `task new`, `task status`, `note new`). The tape deletes the directory when it ends.

To look at the demo data by hand:

```sh
D=$(mktemp -d /tmp/mtm.XXXX) && docs/vhs/seed.sh "$D" && (. "$D/env" && mtm); rm -rf "$D"
```

Keep the directory path short: unix socket paths are limited to about 104 bytes.

## Changing it

Edit the key steps between `Show` and `Hide`. Inside a chooser, move with `Down`, not `j`: typed letters go into the chooser's filter, and a typed name that matches nothing is taken as a new entry. Never send function keys. To stay under 2 MB, keep the window small (`Width`, `Height`), the run short and the pauses few. Check a few frames before committing, for example:

```sh
ffmpeg -i assets/demo.gif -vf fps=1 /tmp/frame%02d.png
```
