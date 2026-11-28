package data

import (
	"path/filepath"
	"strings"
)

// MarkdownFileDir returns the containing directory of a document with a file path.
func MarkdownFileDir(filePath string) string {
	if filePath = strings.TrimSpace(filePath); filePath != "" {
		return filepath.Dir(filePath)
	}
	return ""
}

// MarkdownSourcePane finds the pane that owns a page or created a document.
// The pane field is authoritative when present. Earlier records can have
// only the corresponding relation.
func (s *Store) MarkdownSourcePane(rec Record) *Record {
	if ref, ok := rec.RefField("pane"); ok {
		if pane, err := s.Get(ref); err == nil {
			return pane
		}
	}
	for _, kind := range []string{RelCreated, RelPage} {
		rels, _ := s.Related(rec.Ref(), kind, Incoming)
		for _, rel := range rels {
			if rel.From.Kind == KindPane {
				if pane, err := s.Get(rel.From); err == nil {
					return pane
				}
			}
		}
	}
	return nil
}
