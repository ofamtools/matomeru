#!/usr/bin/env bash
# seed.sh DIR: build mtm into DIR/bin and fill a throwaway database in DIR
# with invented projects, tasks and notes. Everything lives under DIR: HOME,
# the XDG directories, the database, the config and a socket nothing listens
# on, so neither this script nor the demo touches your own mtm data or
# daemon. It writes DIR/env, which defines an mtm function bound to DIR.
#
#	D=$(mktemp -d /tmp/mtm.XXXX) && docs/vhs/seed.sh "$D" && . "$D/env" && mtm
set -euo pipefail

D=${1:?usage: seed.sh DIR}
D=$(cd "$D" && pwd -P)
mkdir -p "$D"/{bin,home/code,data,config,cache,state,run}
GOWORK=off go build -o "$D/bin/mtm" ./cmd/mtm

# The socket path is short on purpose (unix sockets cap near 104 bytes).
cat > "$D/env" <<ENV
unset TUIOS_ENV TUIOS_SESSION TUIOS_PANE_ID TUIOS_SOCKET MTM_CONFIG_DIR MTM_SERVER
export HOME=$D/home XDG_DATA_HOME=$D/data XDG_CONFIG_HOME=$D/config
export XDG_CACHE_HOME=$D/cache XDG_STATE_HOME=$D/state XDG_RUNTIME_DIR=$D/run
export PATH=$D/bin:/usr/bin:/bin EDITOR=true VISUAL=true
mtm() { "$D/bin/mtm" --data "$D/data/mtm.db" --config "$D/config" --socket "$D/run/none.sock" "\$@"; }
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
  "turns.status-border": "all"
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
task() { # PROJECT STATUS TITLE
	local t
	t=$(q task new --project "$1" --title "$3" | id)
	[ "$2" = todo ] || q task status --id "$t" --value "$2" >/dev/null
}

web=$(project acme-web "#7FB685")
api=$(project billing-api "#E5A55D")
docs=$(project docs-site "#7AA2F7")

task "$web" active  "Checkout page: show the shipping estimate"
task "$web" todo    "Move the cart drawer to the new design tokens"
task "$web" blocked "Upgrade the image CDN (waiting on billing-api)"
task "$web" todo    "Flaky test: login redirect on Safari"
task "$web" done    "Dark mode for the order history"

task "$api" active  "Retry failed webhooks with backoff"
task "$api" todo    "Invoice PDF: add the tax id line"
task "$api" todo    "Rate limit the refunds endpoint"
task "$api" done    "Drop the legacy v1 coupons table"

task "$docs" todo   "Write the quickstart for the CLI"
task "$docs" active "Search: index the API reference"
task "$docs" done   "Fix broken links on the changelog page"

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
