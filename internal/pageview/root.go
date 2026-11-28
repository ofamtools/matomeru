// Package pageview decodes authored ViewNode content for storage and presentation.
package pageview

import (
	"encoding/json"
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/ui"
)

// Decode returns an independent tree from the stored object, a typed node, or
// a string containing JSON. Markdown uses the same props for drawing and actions.
func Decode(value any) (ui.ViewNode, error) {
	var root ui.ViewNode
	var encoded []byte
	if raw, ok := value.(string); ok {
		if strings.TrimSpace(raw) == "" {
			return root, nil
		}
		encoded = []byte(raw)
	} else {
		var err error
		encoded, err = json.Marshal(value)
		if err != nil {
			return root, err
		}
	}
	if err := json.Unmarshal(encoded, &root); err != nil {
		return ui.ViewNode{}, err
	}
	normalizeMarkdown(&root)
	return root, nil
}

func normalizeMarkdown(node *ui.ViewNode) {
	if node.Kind == ui.KindMarkdown && !hasMarkdown(node) {
		if text, _ := node.Props["text"].(string); strings.TrimSpace(text) != "" {
			node.Props["markdown"] = text
		}
	}
	for i := range node.Children {
		normalizeMarkdown(&node.Children[i])
	}
}

func hasMarkdown(node *ui.ViewNode) bool {
	markdown, _ := node.Props["markdown"].(string)
	return strings.TrimSpace(markdown) != ""
}

// Root reads a page's canvas. Older records can carry Markdown in their
// fields without a root, or alongside a root that declares only its kind.
func Root(page data.Record) (ui.ViewNode, error) {
	value, _ := page.Value("root")
	root, err := Decode(value)
	if err != nil {
		return root, fmt.Errorf("root of %s: %w", data.RefText(page.Ref()), err)
	}
	if root.Kind != "" && (root.Kind != ui.KindMarkdown || hasMarkdown(&root)) {
		return root, nil
	}
	for _, field := range []string{"markdown", "body", "summary"} {
		value, _ := page.Value(field)
		if markdown, _ := value.(string); strings.TrimSpace(markdown) != "" {
			root.Kind = ui.KindMarkdown
			if root.Props == nil {
				root.Props = map[string]any{}
			}
			root.Props["markdown"] = markdown
			return root, nil
		}
	}
	if root.Kind == "" {
		root.Kind = ui.KindVStack
	}
	return root, nil
}
