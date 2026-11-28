package views

import (
	"strings"

	"ofam.dev/ochazuke/ui"
)

// AnswerChoice is one choice a question offers, with the label and
// description the asker gave it.
type AnswerChoice struct {
	ID          string
	Label       string
	Description string
}

// line is the choice as one readable row, or "" when the asker gave
// nothing beyond the id (the form already shows the id).
func (c AnswerChoice) line() string {
	text := strings.TrimSpace(strings.TrimSpace(c.Label) + " " + strings.TrimSpace(c.Description))
	if text == "" || text == c.ID {
		return ""
	}
	if c.ID == "" {
		return "• " + text
	}
	return "• " + c.ID + " — " + text
}

// AnswerQuestion is the question an answer modal is answering: the words the
// asker wrote, the context they gave under them, and what each choice means.
type AnswerQuestion struct {
	Prompt  string
	Details string
	Choices []AnswerChoice
}

// Empty reports whether there is nothing to show above the answer fields.
func (q AnswerQuestion) Empty() bool {
	return strings.TrimSpace(q.Prompt) == "" && strings.TrimSpace(q.Details) == "" && len(q.choiceLines()) == 0
}

func (q AnswerQuestion) choiceLines() []string {
	var out []string
	for _, c := range q.Choices {
		if l := c.line(); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// AnswerScrollID is the id of the viewport holding the question in the
// answer modal with the given id.
func AnswerScrollID(id string) string { return id + ".questionScroll" }

// AnswerBody is the body of the answer modal: the full question, its details
// and its choice descriptions in a scrolling viewport on top, then the answer
// form. The form keeps its full height and the viewport takes what is left,
// so a long question scrolls (offset, when >= 0, is the scroll position)
// instead of pushing the choices and input off screen.
func AnswerBody(id, title string, q AnswerQuestion, offset int, form ui.ViewNode) ui.ViewNode {
	var header []ui.ViewNode
	if prompt := strings.TrimSpace(q.Prompt); prompt != "" {
		header = append(header, ui.ViewNode{
			ID: id + ".question", Kind: ui.KindText, Tone: "accent",
			Props: map[string]any{"text": "Question: " + prompt, "wrap": true},
			Facts: map[string]any{"answerQuestion": true},
		})
	}
	if details := strings.TrimSpace(q.Details); details != "" {
		header = append(header, ui.ViewNode{
			ID: id + ".details", Kind: ui.KindMarkdown,
			Props: map[string]any{"markdown": details, "wrap": true},
			Facts: map[string]any{"answerDetails": true},
		})
	}
	if lines := q.choiceLines(); len(lines) > 0 {
		header = append(header, ui.ViewNode{
			ID: id + ".choices", Kind: ui.KindText, Tone: "muted",
			Props: map[string]any{"text": "Choices:\n" + strings.Join(lines, "\n"), "wrap": true},
			Facts: map[string]any{"answerChoices": true},
		})
	}
	body := ui.ViewNode{ID: id + ".body", Kind: ui.KindPanel, Title: title}
	if len(header) > 0 {
		scroll := ui.ViewNode{
			ID: AnswerScrollID(id), Kind: ui.KindScroll, Gap: 1,
			Props:    map[string]any{"scrollKey": id + "\x00" + q.Prompt},
			Facts:    map[string]any{"answerQuestionScroll": true},
			Children: header,
		}
		if offset >= 0 {
			scroll.Props["offset"] = offset
		}
		body.Gap = 1 // a blank row keeps the question apart from the fields
		body.Children = append(body.Children, scroll)
	}
	body.Children = append(body.Children, form)
	return body
}
