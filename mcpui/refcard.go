package mcpui

import "ofam.dev/ochazuke/ui"

// Shorthand ViewNode kinds for auto-hydrating noun/record reference cards inside
// ui.page JSON trees. They normalize during app-level page hydration into a
// standard 3-line bordered ref card backed by live store data.
const (
	KindRecordCard = "record.card"
	KindRefCard    = "ref.card"
)

// IsRecordCardNode reports whether n declares a live-hydrating record/noun ref
// card: either shorthand kind "record.card" / "ref.card", or kind "card" with
// props.recordCard=true or props.hydrateRef=true (and a Ref).
func IsRecordCardNode(n *ui.ViewNode) bool {
	if n == nil {
		return false
	}
	if n.Kind == KindRecordCard || n.Kind == KindRefCard {
		return true
	}
	if n.Kind != ui.KindCard || n.Ref == nil || n.Ref.Kind == "" || n.Ref.ID == "" {
		return false
	}
	return propBoolTrue(n.Props, "recordCard") || propBoolTrue(n.Props, "hydrateRef")
}

func propBoolTrue(props map[string]any, key string) bool {
	if props == nil {
		return false
	}
	b, _ := props[key].(bool)
	return b
}
