package app

import (
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/tree"
)

func newTerminalBindingArgs() map[string]any {
	return map[string]any{
		"kind":     data.KindPane,
		"defaults": map[string]any{"harness": "zsh"},
		"submit":   true,
	}
}

func newAgentBindingArgs() map[string]any {
	return map[string]any{
		"kind":           data.KindPane,
		"title":          "New Agent",
		"excludeChoices": map[string]any{"harness": []string{"zsh"}},
	}
}

// projectPanesSectionBindings returns declarative bindings for the Project Page
// Panes section, Project Nav panes items, and inline child pane views.
func projectPanesSectionBindings() []core.Binding {
	n := ModeNormal
	return []core.Binding{
		// Project Page Panes section (project.panes.tree).
		with(bind(n, scopeProjectPanes, "C", views.CmdNew, "new terminal"), newTerminalBindingArgs()),
		with(bind(n, scopeProjectPanes, "n", views.CmdNew, "new agent"), newAgentBindingArgs()),
		bind(n, scopeProjectPanes, "c", CmdPaneCompose, "compose prompt"),
		bind(n, scopeProjectPanes, "L", CmdPaneAssociate, "associate pane with task"),
		when(bind(n, scopeProjectPanes, "Y", CmdPaneApprove, "approve"), "facts.hasPendingApproval"),
		when(bind(n, scopeProjectPanes, "D", CmdPaneDeny, "deny"), "facts.hasPendingApproval"),
		hidden(when(bind(n, scopeProjectPanes, "z a", tree.CmdFoldToggle, "fold/unfold subtree"), "facts.isTree")),
		hidden(when(bind(n, scopeProjectPanes, "z o", tree.CmdFoldOpen, "unfold subtree"), "facts.isTree")),
		hidden(when(bind(n, scopeProjectPanes, "z c", tree.CmdFoldClose, "fold subtree"), "facts.isTree")),
		hidden(when(bind(n, scopeProjectPanes, "z R", tree.CmdFoldOpenAll, "unfold all"), "facts.isTree")),
		hidden(when(bind(n, scopeProjectPanes, "z M", tree.CmdFoldCloseAll, "fold all"), "facts.isTree")),

		// Project Page left-nav Panes section header & pane child nodes.
		when(with(bind(n, views.ScopeProjectNav, "C", views.CmdNew, "new terminal"), newTerminalBindingArgs()), "facts.section == \"panes\""),
		when(with(bind(n, views.ScopeProjectNav, "n", views.CmdNew, "new agent"), newAgentBindingArgs()), "facts.section == \"panes\""),
		when(bind(n, views.ScopeProjectNav, "c", CmdPaneCompose, "compose prompt"), "facts.section == \"panes\" && facts.navNode"),
		when(bind(n, views.ScopeProjectNav, "L", CmdPaneAssociate, "associate pane with task"), "facts.section == \"panes\" && facts.navNode"),
		when(bind(n, views.ScopeProjectNav, "Y", CmdPaneApprove, "approve"), "facts.section == \"panes\" && facts.hasPendingApproval"),
		when(bind(n, views.ScopeProjectNav, "D", CmdPaneDeny, "deny"), "facts.section == \"panes\" && facts.hasPendingApproval"),

		// Inline child pane view inside a dynamic Project Tab (c, t, Y, D live in projectTabsBindings).
		when(with(bind(n, views.ScopeProjectTab, "C", views.CmdNew, "new terminal"), newTerminalBindingArgs()), "facts.inChildPaneView"),
		when(with(bind(n, views.ScopeProjectTab, "n", views.CmdNew, "new agent"), newAgentBindingArgs()), "facts.inChildPaneView"),
		when(bind(n, views.ScopeProjectTab, "L", CmdPaneAssociate, "associate pane with task"), "facts.inChildPaneView"),
	}
}
