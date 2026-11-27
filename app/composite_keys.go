package app

import (
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// featureKeyKind identifies the namespace of a Features page navigation key.
type featureKeyKind string

const (
	featureKeyPage      featureKeyKind = "page"
	featureKeyComponent featureKeyKind = "component"
	featureKeyNoun      featureKeyKind = "noun"
	featureKeyGroup     featureKeyKind = "group"
	featureKeyConcept   featureKeyKind = "concept"
)

// featureKey is a typed composite key identifying an item in the Features page
// navigation rail and detail pane. Its wire/UI string form is "<kind>:<name>",
// produced and parsed only at serialization boundaries.
type featureKey struct {
	Kind featureKeyKind
	Name string
}

func featurePageKey(pageID string) featureKey {
	return featureKey{Kind: featureKeyPage, Name: strings.TrimSpace(pageID)}
}

func featureComponentKey(compID string) featureKey {
	return featureKey{Kind: featureKeyComponent, Name: strings.TrimSpace(compID)}
}

func featureNounKey(kind string) featureKey {
	return featureKey{Kind: featureKeyNoun, Name: strings.TrimSpace(kind)}
}

func featureGroupKey(groupID string) featureKey {
	return featureKey{Kind: featureKeyGroup, Name: strings.TrimSpace(groupID)}
}

func featureConceptKey(conceptKind string) featureKey {
	return featureKey{Kind: featureKeyConcept, Name: strings.TrimSpace(conceptKind)}
}

func (k featureKey) IsZero() bool {
	return k.Kind == "" || k.Name == ""
}

// String serializes k to its canonical "<kind>:<name>" form for focus IDs,
// command arguments, and target tags.
func (k featureKey) String() string {
	if k.IsZero() {
		return ""
	}
	return string(k.Kind) + ":" + k.Name
}

// parseFeatureKey decodes a serialized "<kind>:<name>" key at a boundary.
func parseFeatureKey(raw string) (featureKey, bool) {
	prefix, name, ok := strings.Cut(strings.TrimSpace(raw), ":")
	if !ok {
		return featureKey{}, false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return featureKey{}, false
	}
	kind := featureKeyKind(strings.TrimSpace(prefix))
	switch kind {
	case featureKeyPage, featureKeyComponent, featureKeyNoun, featureKeyGroup, featureKeyConcept:
		return featureKey{Kind: kind, Name: name}, true
	default:
		return featureKey{}, false
	}
}

// threadReplySubtitle formats the composer subtitle for an append-only task
// thread using the canonical core.Ref presentation helper.
func threadReplySubtitle(taskID string) string {
	ref := core.Ref{Kind: data.KindTask, ID: strings.TrimSpace(taskID)}
	return fmt.Sprintf("Thread %s · append-only", data.RefText(ref))
}
