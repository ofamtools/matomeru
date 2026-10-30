# The README demo: mtm's Inbox over a throwaway database of invented
# projects, tasks and notes, with three scripted demo agents (fake-agent.sh:
# no model, a fixed script) in the pinned project. Regenerate from the
# repository root (needs vhs, go and tuios on PATH):  vhs docs/vhs/demo.tape
#
# COLORTERM=truecolor is exported off camera so the theme draws in 24-bit
# colour. seed.sh builds mtm and seeds everything under a fresh /tmp/mtm.XXXX:
# HOME, XDG dirs, database, config, a throwaway TUIOS server named demo (its
# socket under that directory) and the agents' session logs. It launches the
# three agents there: one waits for an approval, one keeps working, one has
# finished. The recording never reads your own mtm data, config or TUIOS
# sessions, and it kills the throwaway server when it ends.

Output assets/demo.gif

Require go
Require tuios

Set Shell "bash"
Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1600
Set Height 900
Set Padding 16
Set WindowBar Colorful
Set Framerate 15
Set TypingSpeed 55ms
Set CursorBlink false

# Off camera: build, seed (projects, tasks, notes, the recording's
# themes.json, settings.json and harnesses.json), start the throwaway TUIOS
# server and the three agents, then mtm: j j k k reads each agent's turns
# once (so no "loading" on camera), then the Work page.
Hide
Type "export COLORTERM=truecolor D=$(mktemp -d /tmp/mtm.XXXX) && docs/vhs/seed.sh $D && . $D/env && clear && mtm"
Enter
Wait+Screen@120s /Working \(1\)/
Sleep 1s
Type "j"
Sleep 3s
Type "j"
Sleep 3s
Type "k"
Sleep 300ms
Type "k"
Sleep 1s
Ctrl+Space
Sleep 500ms
Type "page open work"
Sleep 300ms
Enter
Sleep 800ms
Show
Sleep 900ms

# Step: Pin a project
# The first thing in mtm: the palette's project pin pins the focused
# project, acme-web, at the top of the list. Every page now shows acme-web:
# its tasks, notes and agents.
Ctrl+Space
Sleep 400ms
Type "project pin"
Sleep 300ms
Enter
Sleep 1.5s

# Step: The Inbox
# I opens the Inbox: acme-web's three agents. Needs you (top) holds the one
# waiting for an approval, the list below it the one that finished, Working
# (bottom) the one still going. Each row's glyph and colour is its state; j
# and k move down and up across the three, and the side pane follows, its
# border in the state's colour (yellow, green, blue), with the agent's turns
# as cards.
Type "I"
Sleep 2.5s
Type "j"
Sleep 2.5s
Type "j"
Sleep 2s

# Step: A finished agent's turns
# k back to the finished agent; l steps into its turns (two cards, done in
# green) and l opens the last one: its tool calls, the edits as diffs.
Type "k"
Sleep 1.8s
Type "l"
Sleep 1.2s
Type "l"
Sleep 3s

# Step: Answer the agent that needs you
# g n jumps to Needs you; enter opens that agent live, its question on
# screen. The answer goes straight to it; the agent goes back to work and
# ctrl+a d comes back to the Inbox, where its row has moved down to Working.
Type "g"
Type "n"
Sleep 1.8s
Enter
Sleep 1.8s
Type "go ahead"
Sleep 500ms
Enter
Sleep 2s
Ctrl+A
Type "d"
Sleep 1.5s
Type "g"
Type "w"
Sleep 2.5s

Hide
Ctrl+C
Sleep 500ms
Type "demo_stop; cd / && rm -rf $D"
Enter
Sleep 1s
