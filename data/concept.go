package data

import "ofam.dev/ochazuke/core"

const (
	FieldConceptKind       = "conceptKind"
	FieldConceptTargetKind = "targetKind"
	FieldConceptStatus     = "conceptStatus"

	ConceptKindPage     = "page"
	ConceptKindNoun     = "noun"
	ConceptKindVerb     = "verb"
	ConceptKindView     = "view"
	ConceptKindFeature  = "feature"
	ConceptKindProp     = "prop"
	ConceptKindBinding  = "binding"
	ConceptKindSetting  = "setting"
	ConceptKindWorkflow = "workflow"

	ConceptStatusIdea        = "idea"
	ConceptStatusProposed    = "proposed"
	ConceptStatusActive      = "active"
	ConceptStatusImplemented = "implemented"
	ConceptStatusRetired     = "retired"
)

func conceptSchema() Schema {
	status := StatusColumn{Name: FieldConceptStatus, Title: "Status", Default: ConceptStatusIdea, Values: []StatusValue{
		{Name: ConceptStatusIdea, Label: "Idea", Position: 0, Open: true},
		{Name: ConceptStatusProposed, Label: "Proposed", Position: 1, Open: true},
		{Name: ConceptStatusActive, Label: "Active", Position: 2, Starts: true, Open: true},
		{Name: ConceptStatusImplemented, Label: "Implemented", Position: 3, Ends: true},
		{Name: ConceptStatusRetired, Label: "Retired", Position: 4, Ends: true},
	}}
	return Schema{
		Kind: KindConcept, Title: "Concept", Plural: "Concepts", SummaryField: "title",
		Fields: []FieldSpec{
			{Name: "title", Type: core.ParamString, Required: true, Description: "name of the concept"},
			{Name: FieldConceptKind, Type: core.ParamString, Default: ConceptKindFeature, Description: "what the concept describes or suggests", Enum: []string{
				ConceptKindPage, ConceptKindNoun, ConceptKindVerb, ConceptKindView, ConceptKindFeature,
				ConceptKindProp, ConceptKindBinding, ConceptKindSetting, ConceptKindWorkflow,
			}},
			str(FieldConceptTargetKind, "optional key of the concept's target"),
			statusField(status, "where the concept stands"),
			str("summary", "short description of the concept"),
			strFmt("body", "details of the concept", ShowFormatMarkdown),
		},
		StatusColumns:  []StatusColumn{status},
		DefaultColumns: []string{"title", FieldConceptKind, FieldConceptStatus, FieldConceptTargetKind, "summary"},
		Relations:      universalRelations(),
	}
}
