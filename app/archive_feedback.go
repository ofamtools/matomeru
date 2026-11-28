package app

import (
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// wroteRecord reports an archive or restore of one record by what it is and
// what it is called (archived lane "Title" · u undoes), so the status line
// says which row just left or came back.
func (a *App) wroteRecord(what string, ref core.Ref, cs *data.ChangeSet, err error) {
	label := fmt.Sprintf("%s %q", a.kindNoun(ref.Kind, false), a.title(ref))
	if err != nil {
		a.fail(what+" "+label, err)
		return
	}
	a.wrote(batchPastTense(what)+" "+label, cs, nil)
}

// batchNoun names a batch of records: the kind's plural when they share one
// (3 lanes), "items" when they do not.
func (a *App) batchNoun(refs []core.Ref) string {
	for _, ref := range refs {
		if ref.Kind != refs[0].Kind {
			return "items"
		}
	}
	return a.kindNoun(refs[0].Kind, true)
}

// kindNoun is a kind's schema title (or plural) in lower case, the kind
// itself when the schema names none.
func (a *App) kindNoun(kind string, plural bool) string {
	schema := a.schema(kind)
	noun := schema.Title
	if plural {
		noun = schema.Plural
	}
	if noun = strings.ToLower(strings.TrimSpace(noun)); noun == "" {
		return kind
	}
	return noun
}
