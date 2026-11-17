package work

import "strings"

// ExtRowOwner is a row of the ext_rows table, the project an extension's
// row was created in before the changes table held it (run plan 5.4a,
// OV2b): FoldChanges reads them once into changes rows (prop
// ExtProjectProp of the row's noun and key, the former project as
// ExtFormerProjectProp, AssignedAt as the change's time) and renames the
// table. Nothing else reads or writes it.
type ExtRowOwner struct {
	ID              string `json:"id"` // ExtRowKey(Extension, Noun, Row)
	Extension       string `json:"extension"`
	Noun            string `json:"noun"` // the extension's catalog noun
	Row             string `json:"row"`  // the row's own id, as the extension answers it
	ProjectID       string `json:"project_id"`
	FormerProjectID string `json:"former_project_id,omitempty"`
	AssignedAt      int64  `json:"assigned_at,omitempty"`
}

// ExtRowKey is the id of an ext_rows row. Extension and noun names hold no
// NUL; a row id may hold anything.
func ExtRowKey(extension, noun, row string) string {
	return strings.Join([]string{extension, noun, row}, "\x00")
}
