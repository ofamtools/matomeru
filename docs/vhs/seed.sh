#!/usr/bin/env bash
# seed.sh DIR: build mtm into DIR/bin, fill a throwaway database in DIR with
# invented projects, tasks and notes, start a throwaway TUIOS server and
# launch three scripted demo agents (fake-agent.sh) in the first project.
# Everything lives under DIR: HOME, the XDG directories, the database, the
# config, the TUIOS server (named demo, its socket under DIR/run) and the
# agents' session logs, so neither this script nor the demo touches your own
# mtm data, your TUIOS sessions or your daemon. It writes DIR/env, which
# defines an mtm function bound to DIR and demo_stop, which kills the
# throwaway server.
#
#	D=$(mktemp -d /tmp/mtm.XXXX) && docs/vhs/seed.sh "$D" && . "$D/env" && mtm; demo_stop
set -euo pipefail

D=${1:?usage: seed.sh DIR}
D=$(cd "$D" && pwd -P)
VHS=$(cd "$(dirname "$0")" && pwd -P)
TUIOS_BIN=$(command -v tuios) || { echo "seed.sh: tuios is not on PATH (go install github.com/Gaurav-Gosain/tuios/cmd/tuios@latest)" >&2; exit 1; }
mkdir -p "$D"/{bin,home/code,data,config,cache,state,run}
GOWORK=off go build -o "$D/bin/mtm" ./cmd/mtm
ln -sf "$TUIOS_BIN" "$D/bin/tuios"

# The socket path is short on purpose (unix sockets cap near 104 bytes):
# the server named demo listens on $D/run/tuios/servers/demo/tuios.sock
# because XDG_RUNTIME_DIR is $D/run.
cat > "$D/env" <<ENV
unset TUIOS_ENV TUIOS_SESSION TUIOS_PANE_ID TUIOS_SOCKET TUIOS_SERVER MTM_CONFIG_DIR MTM_SERVER CLAUDE_CONFIG_DIR
export HOME=$D/home XDG_DATA_HOME=$D/data XDG_CONFIG_HOME=$D/config
export XDG_CACHE_HOME=$D/cache XDG_STATE_HOME=$D/state XDG_RUNTIME_DIR=$D/run
export PATH=$D/bin:/usr/bin:/bin EDITOR=true VISUAL=true
mtm() { "$D/bin/mtm" --data "$D/data/mtm.db" --config "$D/config" --socket "$D/run/tuios/servers/demo/tuios.sock" "\$@"; }
demo_stop() { "$D/bin/tuios" --server demo kill-server >/dev/null 2>&1 || true; }
cd "$D/home"
ENV
# The recording's look: a Catppuccin Mocha scheme with coloured panel
# borders (border blue, focused border and accent mauve; the
# chrome.rich.border row keeps the theme's blue over rich's built-in
# teal), chrome.color rich
# pinned (the mode badge filled, hint keys in the accent; pinned so a later
# change of the default cannot change the tape), the key echo in the footer,
# and every turn card tinted by its state.
cat > "$D/config/themes.json" <<'JSON'
{
  "mocha": {
    "background": "dark",
    "text": "#CDD6F4",
    "muted": "#9399B2",
    "accent": "#CBA6F7",
    "border": "#89B4FA",
    "surface": "#313244",
    "error": "#F38BA8",
    "tones": {
      "state.working": "#89B4FA",
      "state.needs-input": {"fg": "#F9E2AF", "bold": true},
      "state.done": "#A6E3A1",
      "marker.star": {"fg": "#F9E2AF", "bold": true},
      "chrome.rich.border": {"fg": "border"}
    }
  }
}
JSON
cat > "$D/config/settings.json" <<'JSON'
{
  "theme": "mocha",
  "chrome.color": "rich",
  "keys.echo": "footer",
  "keys.echo-linger": "4s",
  "turns.status-border": "all",
  "panes.status-border": "all",
  "panes.sections": ["waiting=queue", "errored=queue", "working=working"],
  "panes.queue-order": ["waiting", "errored", "outside"],
  "prompt.after-submit": "pick-task-if-unassociated",
  "turns.tools-open": true
}
JSON
# shellcheck disable=SC1091
. "$D/env"
cd "$OLDPWD"

id() { sed -n 's/^  "id": "\(.*\)",$/\1/p' | head -1; }
q() { mtm "$@" 2>/dev/null; }

project() { # NAME COLOR
	local p
	mkdir -p "$HOME/code/$1"
	p=$(q project new --name "$1" --dir "$HOME/code/$1" | id)
	q project edit --target "$p" --color "$2" >/dev/null
	echo "$p"
}
task() { # PROJECT STATUS TITLE: prints the task id
	local t
	t=$(q task new --project "$1" --title "$3" | id)
	[ "$2" = todo ] || q task status --id "$t" --value "$2" >/dev/null
	echo "$t"
}

web=$(project acme-web "#7FB685")
api=$(project billing-api "#E5A55D")
docs=$(project docs-site "#7AA2F7")

checkout=$(task "$web" active  "Checkout page: show the shipping estimate")
task "$web" todo    "Move the cart drawer to the new design tokens" >/dev/null
cdn=$(task "$web" blocked "Upgrade the image CDN (waiting on billing-api)")
flaky=$(task "$web" todo    "Flaky test: login redirect on Safari")
task "$web" done    "Dark mode for the order history" >/dev/null

task "$api" active  "Retry failed webhooks with backoff" >/dev/null
task "$api" todo    "Invoice PDF: add the tax id line" >/dev/null
task "$api" todo    "Rate limit the refunds endpoint" >/dev/null
task "$api" done    "Drop the legacy v1 coupons table" >/dev/null

task "$docs" todo   "Write the quickstart for the CLI" >/dev/null
task "$docs" active "Search: index the API reference" >/dev/null
task "$docs" done   "Fix broken links on the changelog page" >/dev/null

q note new --project "$web" --text "# Checkout release

Ship behind the \`checkout_v2\` flag, then ramp to 100%.

- [x] Shipping estimate API wired up
- [x] Copy reviewed
- [ ] Screenshot tests on mobile
- [ ] Ramp the flag to 25%
- [ ] Announce in #web" >/dev/null

q note new --project "$api" --text "# Webhook retries

Backoff: 1m, 5m, 30m, 2h, then give up and alert.

- [x] Store attempt count per delivery
- [ ] Dead-letter table
- [ ] Dashboard for stuck deliveries" >/dev/null

q note new --project "$docs" --text "# Docs ideas

- A page per command, generated from the catalog
- Copy buttons on every snippet" >/dev/null

# The demo agent: one harnesses.json row running fake-agent.sh, a scripted
# agent (it calls no model). Its prompt box types an answer into the pane
# (deliver send), since a scripted question is a plain read.
cat > "$D/config/harnesses.json" <<JSON
[{"name": "demo-agent", "command": ["$VHS/fake-agent.sh"], "prompt": "send-text",
  "ready": {"state": "idle", "timeoutMs": 10000},
  "promptBox": {"deliver": "send", "submit": "enter"}}]
JSON

# The throwaway TUIOS server and three agents in acme-web, each on a task:
# one waits for an approval, one keeps working, one finishes.
tuios --server demo start-server >/dev/null
for t in "$cdn" "$flaky" "$checkout"; do
	q agent launch --project "$web" --harness demo-agent --task "$t" >/dev/null
done
# Wait (up to 30s) until every agent has played its opening.
for _ in $(seq 60); do
	s=$(tuios --server demo list-agents 2>/dev/null || true)
	case $s in *needs_input*) case $s in *done*) case $s in *working*) break ;; esac ;; esac ;; esac
	sleep 0.5
done
