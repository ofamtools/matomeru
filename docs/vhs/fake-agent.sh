#!/bin/bash
# fake-agent.sh: the scripted demo agent of the README demo (demo.tape). It
# is NOT a real agent: it calls no model and changes no file. It plays a
# fixed script so the recording shows mtm's Inbox with agents in every
# state, the same on every run and offline.
#
# seed.sh registers it in the throwaway config as the harness demo-agent
# and launches it three times in the pinned project, each with a task's
# title as its first prompt. The prompt picks the scene (the table at the
# bottom):
#
#   ask   works, then waits for an approval (Needs you); an answer sends it
#         back to work.
#   work  keeps working (Working).
#   done  finishes its turn (done in the list).
#
# Each scene does three things a real agent does:
#   - prints a plausible transcript in its pane (the Screen tab);
#   - reports its state to the throwaway TUIOS server it runs in
#     (tuios set-agent-state: working, needs_input, done) with a session id;
#   - writes the session as a Claude Code style JSONL under
#     $HOME/.claude/projects (HOME is the demo's temp home), which mtm reads
#     as the pane's turns (the Turns tab). The state is reported with the
#     harness id claude-code because that is the log format mtm reads; the
#     log's records say "scripted demo agent".
set -u

TUIOS=${TUIOS:-tuios}
SID=$(uuidgen | tr 'A-Z' 'a-z')
LOG=$HOME/.claude/projects/demo-agent/$SID.jsonl
mkdir -p "${LOG%/*}"
N=0

# report STATE [ARGS...]: the pane's agent state, with the session id and
# its transcript, so mtm finds the log.
report() {
	local s=$1
	shift
	"$TUIOS" set-agent-state "$s" --harness claude-code --agent-session-id "$SID" --transcript-path "$LOG" "$@" >/dev/null 2>&1
}

# Transcript records (the shapes internal/turns/claude.go reads). Text
# passed in must not hold a double quote or a backslash.
now() { date -u +%Y-%m-%dT%H:%M:%SZ; }
rec() { # TYPE JSON-FIELDS
	N=$((N + 1))
	printf '{"type":"%s","uuid":"u%d","timestamp":"%s","sessionId":"%s","cwd":"%s",%s}\n' "$1" "$N" "$(now)" "$SID" "$PWD" "$2" >>"$LOG"
}
prompt_rec() { rec user "\"promptSource\":\"typed\",\"message\":{\"role\":\"user\",\"content\":\"$1\"}"; }
say_rec() { rec assistant "\"message\":{\"role\":\"assistant\",\"stop_reason\":\"${2:-tool_use}\",\"content\":[{\"type\":\"text\",\"text\":\"$1\"}]}"; }
tool_rec() { # ID NAME INPUT-JSON
	rec assistant "\"message\":{\"role\":\"assistant\",\"stop_reason\":\"tool_use\",\"content\":[{\"type\":\"tool_use\",\"id\":\"$1\",\"name\":\"$2\",\"input\":$3}]}"
}
result_rec() { # ID TEXT
	rec user "\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"$1\",\"content\":\"$2\",\"is_error\":false}]}"
}
end_rec() { rec system "\"subtype\":\"turn_duration\",\"durationMs\":${1:-42000}"; }

# On screen: a tool call and its result, a few lines of text.
c_dim=$'\e[2m' c_bold=$'\e[1m' c_tool=$'\e[38;2;137;180;250m' c_ok=$'\e[38;2;166;227;161m' c_ask=$'\e[38;2;249;226;175m' c_off=$'\e[0m'
show_tool() { printf '%s●%s %s%s%s\n' "$c_tool" "$c_off" "$c_bold" "$1" "$c_off"; sleep 0.4; printf '  %s⎿ %s%s\n' "$c_dim" "$2" "$c_off"; sleep 0.5; }
show_say() { printf '%s●%s %s\n' "$c_ok" "$c_off" "$1"; sleep 0.6; }

# One turn's worth of steps: tool call, result, on screen and in the log.
step() { # ID NAME INPUT-JSON LABEL RESULT
	tool_rec "$1" "$2" "$3"
	show_tool "$4" "$5"
	result_rec "$1" "$5"
}

banner() {
	clear
	printf '%sdemo-agent%s %s· a scripted demo agent, not a real one (docs/vhs/fake-agent.sh)%s\n\n' "$c_bold" "$c_off" "$c_dim" "$c_off"
}

# --- scenes ---

scene_done() { # the shipping estimate: an earlier turn, then this one
	prompt_rec "Why is the shipping estimate a day late on Fridays?"
	printf '%s> Why is the shipping estimate a day late on Fridays?%s\n' "$c_dim" "$c_off"
	step t1 Grep '{"pattern":"addBusinessDays","path":"src"}' 'Grep addBusinessDays' '2 files: src/checkout/estimate.ts, src/lib/dates.ts'
	step t2 Read '{"file_path":"src/lib/dates.ts"}' 'Read src/lib/dates.ts' 'Read 48 lines'
	say_rec "addBusinessDays counts Friday itself as day one, so a Friday order skips to Tuesday." end_turn
	show_say "addBusinessDays counts Friday itself as day one."
	end_rec 18000
	echo
	prompt_rec "$1"
	printf '%s> %s%s\n' "$c_dim" "$1" "$c_off"
	step t3 Edit '{"file_path":"src/lib/dates.ts","old_string":"  let day = 1;","new_string":"  let day = 0;"}' 'Edit src/lib/dates.ts' '1 line changed'
	step t4 Edit '{"file_path":"src/checkout/estimate.ts","old_string":"  return addBusinessDays(order.date, 3);","new_string":"  return addBusinessDays(order.date, carrier.days);"}' 'Edit src/checkout/estimate.ts' '1 line changed'
	step t5 Bash '{"command":"npm test -- estimate"}' 'Bash npm test -- estimate' '12 passed'
	say_rec "The estimate now counts from the next business day and uses the carrier's days. Tests pass." end_turn
	show_say "The estimate now counts from the next business day. Tests pass."
	end_rec 42000
	report done -m "estimate fixed, tests pass"
	while :; do sleep 3600; done
}

scene_work() { # the flaky test: keeps going
	prompt_rec "$1"
	printf '%s> %s%s\n' "$c_dim" "$1" "$c_off"
	step t1 Bash '{"command":"npx playwright test login --project=webkit --repeat-each=20"}' 'Bash playwright test login --repeat-each=20' '3 failed, 17 passed'
	step t2 Read '{"file_path":"tests/login.spec.ts"}' 'Read tests/login.spec.ts' 'Read 64 lines'
	say_rec "The redirect races the cookie write on WebKit. Trying a wait on the session request." tool_use
	show_say "The redirect races the cookie write on WebKit."
	# Then one step every few seconds, round after round.
	i=3
	while :; do
		sleep 8
		case $((i % 3)) in
		0) step "t$i" Edit '{"file_path":"tests/login.spec.ts","old_string":"  await page.click(submit);","new_string":"  await Promise.all([page.waitForResponse(/session/), page.click(submit)]);"}' 'Edit tests/login.spec.ts' '1 line changed' ;;
		1) step "t$i" Bash '{"command":"npx playwright test login --project=webkit --repeat-each=20"}' 'Bash playwright test login --repeat-each=20' "round $((i / 3)): 20 passed" ;;
		2) step "t$i" Grep '{"pattern":"page.click(submit)","path":"tests"}' 'Grep page.click(submit) tests' '4 more specs click before the cookie' ;;
		esac
		report working -m "login test, round $((i / 3))"
		i=$((i + 1))
	done
}

scene_ask() { # the CDN upgrade: needs an approval
	prompt_rec "$1"
	printf '%s> %s%s\n' "$c_dim" "$1" "$c_off"
	step t1 Read '{"file_path":"package.json"}' 'Read package.json' 'Read 41 lines'
	step t2 Bash '{"command":"npm view @acme/cdn-client version"}' 'Bash npm view @acme/cdn-client version' '4.2.0'
	tool_rec t3 Bash '{"command":"npm install @acme/cdn-client@4"}'
	printf '%s●%s %sBash npm install @acme/cdn-client@4%s\n\n' "$c_tool" "$c_off" "$c_bold" "$c_off"
	printf '%sAllow this command? (a scripted question: type an answer and press enter)%s\n> ' "$c_ask" "$c_off"
	report needs_input --kind approval -m "approve Bash: npm install @acme/cdn-client@4"
	IFS= read -r answer
	report working -m "installing @acme/cdn-client@4"
	result_rec t3 "added 3 packages, changed 1"
	printf '  %s⎿ added 3 packages, changed 1%s\n' "$c_dim" "$c_off"
	sleep 0.5
	step t4 Bash '{"command":"npm run build"}' 'Bash npm run build' 'built in 14.2s'
	i=5
	while :; do
		sleep 6
		report working -m "checking image URLs, page $((i - 4))"
		step "t$i" Grep '{"pattern":"cdn.acme.com/v3","path":"src"}' 'Grep cdn.acme.com/v3' "$((9 - i % 4)) matches left"
		i=$((i + 1))
	done
}

banner
# The prompt is echoed by the scene, not by the terminal, so mtm typing it
# early does not print it twice.
stty -echo 2>/dev/null
printf '> '
report idle -m "scripted demo agent: waiting for a prompt"
IFS= read -r prompt
stty echo 2>/dev/null
printf '\r\033[K'
report working -m "${prompt:0:60}"
# The scene table: which words of the first prompt play which scene.
case $prompt in
*CDN* | *cdn*) scene_ask "$prompt" ;;
*[Ff]laky*) scene_work "$prompt" ;;
*) scene_done "$prompt" ;;
esac
