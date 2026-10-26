# The README demo: mtm over a throwaway database of invented projects,
# tasks and notes. Regenerate from the repository root:  vhs docs/vhs/demo.tape
#
# seed.sh builds mtm and seeds everything under a fresh /tmp/mtm.XXXX: HOME,
# XDG dirs, database, config and a socket nothing listens on. The recording
# never reads your own mtm data, config or TUIOS daemon.

Output assets/demo.gif

Require go

Set Shell "bash"
Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1080
Set Height 600
Set Padding 16
Set WindowBar Colorful
Set Framerate 15
Set TypingSpeed 55ms
Set CursorBlink false

# Off camera: build, seed, start on the Work page.
Hide
Type "export D=$(mktemp -d /tmp/mtm.XXXX) && docs/vhs/seed.sh $D && . $D/env && clear && mtm"
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

# Move through the projects, open one, move through its tasks.
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

# Filter the task list.
Type "/"
Sleep 300ms
Type "refund"
Sleep 800ms
Enter
Sleep 500ms
Escape
Sleep 300ms

# The palette pins a project: the title and accent follow it.
Ctrl+Space
Sleep 400ms
Type "project pin"
Sleep 300ms
Enter
Sleep 700ms
Down
Sleep 400ms
Enter
Sleep 900ms

# Three pages over, the Notes page. A note is Markdown with a checklist:
# j moves between the boxes, x ticks one.
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

# Every key on screen, then the Settings page.
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
