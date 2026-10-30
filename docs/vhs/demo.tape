# The README demo: mtm over a throwaway database of invented projects,
# tasks and notes. Regenerate from the repository root:  vhs docs/vhs/demo.tape
#
# COLORTERM=truecolor is exported off camera so the theme draws in 24-bit
# colour. seed.sh builds mtm and seeds everything under a fresh /tmp/mtm.XXXX: HOME,
# XDG dirs, database, config and a socket nothing listens on. The recording
# never reads your own mtm data, config or TUIOS daemon.

Output assets/demo.gif

Require go

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

# Off camera: build, seed (projects, tasks, notes, and the recording's
# themes.json and settings.json), start on the Work page.
Hide
Type "export COLORTERM=truecolor D=$(mktemp -d /tmp/mtm.XXXX) && docs/vhs/seed.sh $D && . $D/env && clear && mtm"
Enter
Wait+Screen@90s /Matomeru/
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
# project, acme-web, at the top of the list. The title and the row take the
# pin. mtm draws the theme's colours before the pin as well (pin.colors
# accent): the mode badge, the panel borders and the hint keys are coloured
# from the first frame (chrome.color rich).
Ctrl+Space
Sleep 400ms
Type "project pin"
Sleep 300ms
Enter
Sleep 2s

# Step: Move through the projects and tasks
# j and k move down the project list, enter opens one, j moves through its
# tasks.
Type "j"
Sleep 450ms
Type "j"
Sleep 450ms
Type "k"
Sleep 400ms
Enter
Sleep 800ms
Type "j"
Sleep 350ms
Type "j"
Sleep 500ms

# Step: Filter the task list
# / filters the focused list as you type; enter keeps the filter.
Type "/"
Sleep 300ms
Type "refund"
Sleep 800ms
Enter
Sleep 500ms
Escape
Sleep 300ms

# Step: Tick a box in a note
# Three pages over (ctrl+j), the Notes page. A note is Markdown with a
# checklist: j moves between the boxes, x ticks one.
Ctrl+J
Sleep 200ms
Ctrl+J
Sleep 200ms
Ctrl+J
Sleep 700ms
Enter
Sleep 700ms
Type "j"
Sleep 400ms
Type "x"
Sleep 900ms

# Step: Every key, then the Settings page
# ? lists every key on screen; , opens the Settings page.
Escape
Sleep 300ms
Type "?"
Sleep 1.2s
Escape
Sleep 300ms
Type ","
Sleep 800ms
Type "j"
Sleep 350ms
Type "j"
Sleep 700ms
Type "q"
Sleep 800ms

Hide
Ctrl+C
Sleep 500ms
Type "cd / && rm -rf $D"
Enter
Sleep 500ms
