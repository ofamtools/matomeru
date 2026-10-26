<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img alt="matomeru" src="assets/logo-light.svg" width="360">
  </picture>
  <br>
  <a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/unixsuperhero/matomeru"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/unixsuperhero/matomeru"></a>
</p>

<p align="center">A terminal workspace for coding agents: projects, tasks, notes, live agent panes, their turns and your reviews, gathered in one TUI.</p>

<p align="center"><img alt="mtm over invented data: moving through projects and tasks, filtering a list, pinning a project from the palette, ticking a checklist in a note, the key help and the Settings page" src="assets/demo.gif" width="720"></p>

Matomeru (`mtm`) is built on [ochazuke](https://github.com/unixsuperhero/ochazuke) and runs its agents in panes of a [TUIOS](https://github.com/Gaurav-Gosain/tuios) daemon it starts for itself. Every action in the TUI is also a command: `mtm task new --title ...`, `mtm project pin`, `mtm catalog`.

Module `github.com/unixsuperhero/matomeru`; requires Go 1.26 or newer.

```sh
go install github.com/unixsuperhero/matomeru/cmd/mtm@latest
```
