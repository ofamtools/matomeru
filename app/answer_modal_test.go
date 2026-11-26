package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
)

// The answer modal names the question it answers, with its details, when it
// is opened away from the Questions page (here: the question's Show page), and
// questions.answerShowQuestion turns that header off.
func TestAnswerModalShowsQuestionOutsideQuestionsPage(t *testing.T) {
	a, d := demo(t)
	pane := titled(t, a, data.KindPane, "claude: wave 5B")
	const prompt, details = "Ship the W11 polish now?", "Gate is green; the release window closes on Friday."
	if _, err := a.Router.Ask(seedAgent, mcpui.AskArgs{
		Pane: pane.ID, ID: "q-w11-prompt", Title: prompt, Details: details,
		Choices: []mcpui.Choice{{ID: "ship", Label: "Ship it"}, {ID: "hold", Label: "Hold for review"}},
	}); err != nil {
		t.Fatal(err)
	}
	q, err := a.Store.GetByField(data.KindQuestion, "cardId", "q-w11-prompt")
	if err != nil {
		t.Fatal(err)
	}
	browseTo(t, a, d, *q)
	d.Keys("E")
	if cur, showing := a.Showing(); !showing || cur != q.Ref() {
		t.Fatalf("E did not open the question's Show page: %v %v", cur, showing)
	}
	d.Keys("a")
	if a.Modal() != "answer" {
		t.Fatalf("a on the Show page did not open the answer modal: modal=%q status=%q", a.Modal(), a.Status())
	}
	root := a.View(a.State())
	modal := root.Find("modal.answer")
	if modal == nil {
		t.Fatal("no answer overlay in the view")
	}
	qn, dn := modal.Find("answer.question"), modal.Find("answer.details")
	if qn == nil || !strings.Contains(data.Text(qn.Props["text"]), prompt) {
		t.Fatalf("answer modal has no question node holding the prompt: %+v", qn)
	}
	if dn == nil || data.Text(dn.Props["markdown"]) != details {
		t.Fatalf("answer modal has no details node: %+v", dn)
	}
	frame := d.Frame()
	for _, want := range []string{"Question: " + prompt, "Gate is green", "ship"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("answer modal frame is missing %q:\n%s", want, frame)
		}
	}

	d.Keys("esc", "esc")
	setSettingForTest(t, a, SetAnswerShowQuestion, false)
	d.Keys("a")
	if a.Modal() != "answer" {
		t.Fatalf("a did not reopen the answer modal: %q", a.Modal())
	}
	root = a.View(a.State())
	if root.Find("answer.question") != nil || root.Find("answer.details") != nil {
		t.Fatal("questions.answerShowQuestion off still renders the question header")
	}
}
