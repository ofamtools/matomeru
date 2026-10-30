package main

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// The Work page from sideBySide columns: the projects list on the left
// (about 30%), and on the right the highlighted project's header line, its
// task table, and under the table the selected task's detail (else the
// project's) or the record editor. The projects list is drawn first, so
// j/k spill from its last row into the task table. Narrower, the page
// stacks the projects over the tasks with the detail beside them
// (renderWorkDetail).

// renderWorkWide draws the wide Work page.
func (a *App) renderWorkWide(width, height int) ochazuke.Block {
	leftW := max(24, width*3/10)
	rightW := max(1, width-leftW-2)
	left := a.projectsHeading() + a.view(a.projectList, leftW, max(1, height-1))

	var lower ochazuke.Block
	editing := a.editor != nil && a.editor.Page == "work"
	detail, _ := a.workDetail()
	rest := max(2, height-1)
	tableH := rest
	if editing || detail != "" {
		tableH = max(2, min(a.taskTree.Height()+1, rest*3/5))
	}
	lowerH := max(1, rest-tableH-1)
	if editing {
		a.detail.work.hide()
		lower = a.editorPanel(rightW, lowerH)
	} else {
		lower = ochazuke.Text(lipgloss.NewStyle().Width(rightW).Render(a.detail.work.fit(detail, rightW, rightW, lowerH)))
	}
	right := ansi.Truncate(a.workHeader(), rightW, "…") + "\n" + a.view(a.taskTree, rightW, tableH)
	top := 1 + tableH + 1
	if editing || detail != "" {
		right += "\n\n" + lower.Text
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(leftW+2).Render(left), lipgloss.NewStyle().Width(rightW).Render(right))
	if editing {
		return lower.Offset(leftW+2, top).WithText(body)
	}
	return ochazuke.Text(body)
}

// projectsHeading counts the rows the projects list shows ("no project"
// included), so it agrees with the list's own "6–14 / 24" (UX review 2
// F22).
func (a *App) projectsHeading() string {
	return fmt.Sprintf("projects (%d)\n", a.projectList.Len())
}

// workHeader is the right pane's first line: the highlighted project's
// name (with the pin), default directory and task counts by status.
func (a *App) workHeader() string {
	switch {
	case a.all:
		return "all projects" + statusCounts(a.tasks)
	case !a.tasksOpen:
		return "tasks"
	case a.current == "":
		return "no project" + statusCounts(a.tasks)
	}
	p, ok := a.project(a.current)
	if !ok {
		return a.ownerLabel(a.current)
	}
	parts := []string{projectLabel(p)}
	if p.ID == a.pin {
		parts[0] += " 📌"
	}
	if dir := projectDir(p); dir != "" {
		home, _ := a.home()
		parts = append(parts, tildePath(dir, home))
	}
	return strings.Join(parts, " · ") + statusCounts(a.tasks)
}

// statusCounts is " · 2 active · 3 todo …" for the statuses present, in
// table order.
func statusCounts(tasks []work.Task) string {
	counts := map[string]int{}
	var order []string
	for _, t := range tasks {
		s := work.NormalStatus(t.Status)
		if counts[s] == 0 {
			order = append(order, s)
		}
		counts[s]++
	}
	if len(tasks) == 0 {
		return " · no tasks"
	}
	// tasks arrive sorted by status rank (refreshTasks), so order is too.
	out := ""
	for _, s := range order {
		out += fmt.Sprintf(" · %d %s", counts[s], s)
	}
	return out
}
