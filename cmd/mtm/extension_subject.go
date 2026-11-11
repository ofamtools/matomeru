package main

import (
	"fmt"

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
