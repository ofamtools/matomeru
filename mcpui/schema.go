package mcpui

import "ofam.dev/ochazuke/core"

type props = map[string]any

func object(required []string, properties props) props {
	return props{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func text(desc string) props { return props{"type": "string", "description": desc} }

func flag(desc string) props { return props{"type": "boolean", "description": desc} }

func oneOf(desc string, values []string) props {
	return props{"type": "string", "description": desc, "enum": values}
}

// nodeSchema describes a ui.ViewNode. Only kind is required; the node's
// other keys are those of the ViewNode schema.
func nodeSchema(desc string) props {
	return props{
		"type": "object", "description": desc, "required": []string{"kind"},
		"properties": props{
			"kind":      oneOf("node kind", nodeKinds),
			"id":        text("node ID, unique on the page; needed to patch or answer the node later"),
			"title":     text("title"),
			"scope":     text("keymap scope when focused"),
			"focusable": flag("whether the node takes focus"),
			"unread":    flag("highlight as unread"),
			"props":     props{"type": "object", "description": "kind-specific payload"},
			"children":  props{"type": "array", "items": props{"type": "object"}},
		},
	}
}

func paramSpecSchema() props {
	types := []string{}
	for _, t := range []core.ParamType{core.ParamString, core.ParamInt, core.ParamFloat, core.ParamBool, core.ParamDuration,
		core.ParamPath, core.ParamRef, core.ParamList, core.ParamObject} {
		types = append(types, string(t))
	}
	return props{
		"type": "object", "required": []string{"name", "type"},
		"properties": props{
			"name":        text("field name; the key of the answer value"),
			"type":        oneOf("field type", types),
			"required":    flag("whether an answer is needed"),
			"default":     props{"description": "default value"},
			"enum":        props{"type": "array", "items": props{"type": "string"}, "description": "pick-one values"},
			"refKinds":    props{"type": "array", "items": props{"type": "string"}},
			"sourceQuery": text("Data API query for dynamic choices"),
			"description": text("help text"),
		},
	}
}

// ToolSpecs returns the four tools with JSON schemas of their real
// parameters: every property is a field the tool reads, and nothing else is
// accepted.
func (r *Router) ToolSpecs() []ToolSpec {
	pane := text("pane ID whose site holds the page")
	return []ToolSpec{
		{
			Name:        ToolCreatePage,
			Description: "Create a page (a ViewNode tree) in a pane's interactive site. Whenever an output, explanation, or context block exceeds 10 lines of text, put it on its own dedicated page with ui.create_page (using kind \"markdown\" for rich text) rather than packing it into a card or Questions page. Returns {id, ref, slug, updatedAt}.",
			InputSchema: object([]string{"title", "root"}, props{
				"pane":     pane,
				"task":     text("task ID the page belongs to; defaults to the pane's task"),
				"id":       text("page ID; generated when omitted"),
				"title":    text("page title, shown in the table of contents"),
				"slug":     text("stable name within the pane; derived from the title when omitted"),
				"pageKind": oneOf("what the page is for; default custom", pageKinds),
				"pinned":   flag("keep at the top of the table of contents"),
				"root":     nodeSchema("the page's ViewNode tree"),
			}),
		},
		{
			Name:        ToolUpdatePage,
			Description: "Update a page: prepend or append a node, replace the whole tree, or patch one node by ID. The page becomes unread. Prefer archive over delete when retiring content or pages.",
			InputSchema: object([]string{"page", "mode"}, props{
				"pane":     text("pane ID; needed when page is a slug"),
				"page":     text("page ID, or a slug within pane"),
				"mode":     oneOf("how to apply the node", []string{ModePrepend, ModeAppend, ModeReplace, ModePatch}),
				"node":     nodeSchema("the node to insert, to replace the root with, or to patch in"),
				"card":     nodeSchema("same as node; reads better for a card"),
				"root":     nodeSchema("same as node; reads better for replace"),
				"targetId": text("prepend/append: the container node to insert into (default the root); patch: the node to replace (default the node's own id)"),
				"title":    text("new page title"),
			}),
		},
		{
			Name:        ToolAsk,
			Description: "Ask the owner one question or a batch of questions: prepends unread question card(s) to the pane's pinned Questions page (created when missing). Keep question card details concise (<= 10 lines); if supporting output or context exceeds 10 lines of text, put that output on its own dedicated page with ui.create_page first and reference it from the question. Give either a single question (form, choices, or card) or a questions array for a multi-question batch with Submit All. Returns {id, ref, slug, updatedAt, cardId, cardIds?, batchId?}; read answers with ui.list_pages.",
			InputSchema: object([]string{}, props{
				"pane":    pane,
				"task":    text("task ID for a newly created questions page"),
				"page":    text("page ID or slug to ask on; default questions"),
				"id":      text("card ID (or batch ID when questions is set); generated when omitted"),
				"title":   text("the question (or batch title when questions is set); required for single questions unless card is given"),
				"details": text("context shown under the question"),
				"form": props{"type": "object", "required": []string{"fields"}, "additionalProperties": false, "properties": props{
					"fields":      props{"type": "array", "items": paramSpecSchema()},
					"submitLabel": text("label of the submit action"),
				}},
				"choices": props{"type": "array", "description": "pick-one options", "items": props{
					"type": "object", "required": []string{"id"}, "additionalProperties": false, "properties": props{
						"id": text("value returned as the answer's choice"), "label": text("short label"), "description": text("what picking it does"),
					}}},
				"card": nodeSchema("a whole question card, in place of title/form/choices"),
				"questions": props{"type": "array", "description": "batch of questions to ask together on the Questions page", "items": props{
					"type": "object", "additionalProperties": false, "properties": props{
						"id":      text("card ID; generated when omitted"),
						"title":   text("the question; required unless card is given"),
						"details": text("context shown under the question"),
						"form": props{"type": "object", "required": []string{"fields"}, "additionalProperties": false, "properties": props{
							"fields":      props{"type": "array", "items": paramSpecSchema()},
							"submitLabel": text("label of the submit action"),
						}},
						"choices": props{"type": "array", "description": "pick-one options", "items": props{
							"type": "object", "required": []string{"id"}, "additionalProperties": false, "properties": props{
								"id": text("value returned as the answer's choice"), "label": text("short label"), "description": text("what picking it does"),
							}}},
						"card": nodeSchema("a whole question card, in place of title/form/choices"),
					}}},
			}),
		},
		{
			Name:        ToolListPages,
			Description: "List the pages of a pane (or of every pane when pane is omitted) with unread state and each question's answer.",
			InputSchema: object([]string{}, props{
				"pane":            text("pane ID; omit to list every pane's pages, unread first"),
				"includeArchived": flag("also list archived pages"),
			}),
		},
	}
}
