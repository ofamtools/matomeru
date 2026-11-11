package main

import (
	"fmt"
	"maps"
	"slices"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

func extensionKeyProp(doc ocli.CatalogDoc, noun string) string {
	for _, n := range doc.Nouns {
		if n.Name == noun {
			for _, prop := range n.Props {
				if prop.Key {
					return prop.Name
				}
			}
		}
	}
	return ""
}

func extensionSubjectKey(doc ocli.CatalogDoc, noun string, args ochazuke.Arguments, target string) string {
	if field := extensionKeyProp(doc, noun); field != "" {
		if value := args[field]; value != nil && fmt.Sprint(value) != "" {
			return fmt.Sprint(value)
		}
	}
	return target
}

func extensionOpenSubject(doc ocli.CatalogDoc, noun, verb string, args ochazuke.Arguments, target string) string {
	if key := extensionSubjectKey(doc, noun, args, ""); key != "" {
		return key
	}
	for _, n := range doc.Nouns {
		if n.Name != noun {
			continue
		}
		for _, v := range n.Verbs {
			if v.Name != verb {
				continue
			}
			pathArg := func(name string) string {
				p := v.Params[name]
				if p.Type == ochazuke.PropPath {
					if value, ok := args[name].(string); ok {
						return value
					}
				}
				return ""
			}
			for _, name := range v.Positional {
				if value := pathArg(name); value != "" {
					return value
				}
			}
			for _, name := range slices.Sorted(maps.Keys(v.Params)) {
				if value := pathArg(name); value != "" {
					return value
				}
			}
		}
	}
	return target
}

func extensionHasHumanProp(doc ocli.CatalogDoc, noun string) bool {
	for _, n := range doc.Nouns {
		if n.Name == noun {
			for _, prop := range n.Props {
				if prop.Human {
					return true
				}
			}
		}
	}
	return false
}

func extensionNeedsCanonicalPreflight(op ochazuke.Operation, doc ocli.CatalogDoc, noun, key string) bool {
	return op.Standard == "" && op.HistoryKind == ochazuke.HistoryChange && !op.Creates && !op.Foreground && key != "" && extensionHasHumanProp(doc, noun)
}
