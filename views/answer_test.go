package views

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke/ui"
)

// AnswerBody keeps the full multi-line prompt, the details and the choice
// descriptions in one scroll viewport above the form, so the form always gets
// its own height; the offset reaches the viewport only when set.
func TestAnswerBodyQuestionViewport(t *testing.T) {
	form := ui.ViewNode{ID: "answer.form", Kind: ui.KindForm}
	q := AnswerQuestion{
		Prompt:  "First line\nSecond line",
		Details: strings.Repeat("long context ", 200),
		Choices: []AnswerChoice{{ID: "ship", Label: "Ship it", Description: "today"}, {ID: "hold"}},
	}
	body := AnswerBody("answer", "Ship?", q, 4, form)
	if len(body.Children) != 2 || body.Children[1].ID != "answer.form" {
		t.Fatalf("body is not [viewport, form]: %+v", body.Children)
	}
	sc := body.Children[0]
	if sc.ID != AnswerScrollID("answer") || sc.Kind != ui.KindScroll || sc.Props["offset"] != 4 {
		t.Fatalf("question viewport = %+v", sc)
	}
	if got := sc.Find("answer.question").Props["text"]; got != "Question: First line\nSecond line" {
		t.Fatalf("prompt text = %q", got)
	}
	if got := sc.Find("answer.details").Props["markdown"]; got != strings.TrimSpace(q.Details) {
		t.Fatal("details were cut")
	}
	if got := sc.Find("answer.choices").Props["text"]; got != "Choices:\n• ship — Ship it today" {
		t.Fatalf("choices text = %q", got)
	}
	if _, has := AnswerBody("answer", "Ship?", q, -1, form).Children[0].Props["offset"]; has {
		t.Fatal("offset -1 still pins the viewport")
	}
	if got := AnswerBody("answer", "t", AnswerQuestion{}, -1, form); len(got.Children) != 1 {
		t.Fatalf("an empty question still draws a viewport: %+v", got.Children)
	}
}
