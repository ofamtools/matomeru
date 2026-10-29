package harness

import "fmt"

// How mtm's prompt box delivers what is typed to a pane (PromptBox).
const (
	// DeliverQueue is an agent's delivery: the box's send (a steer while
	// the agent works), queue (tuios's queue-prompt, typed once the agent
	// rests) and interrupt modes, never typed over a question the agent
	// waits on (only a queue is taken then).
	DeliverQueue = "queue"
	// DeliverSend is a terminal's: send-text types the text as one paste
	// (bracketed, when the program in the pane turned bracketed paste on),
	// then submits it as Submit says. The box has no other mode.
	DeliverSend = "send"
)

// How a send ends (PromptBox.Submit).
const (
	// SubmitEnter presses the Enter key of the program in the pane after
	// the paste (tuios's send-text submit): a shell runs the line.
	SubmitEnter = "enter"
	// SubmitNone leaves the text typed, unsubmitted.
	SubmitNone = "none"
)

// PromptBox is how mtm's prompt box writes to a pane running the harness:
// {"deliver": "queue", "submit": "enter"}. A field left out is the
// default: deliver queue, submit enter. Its key is promptBox, since prompt
// already says how the harness takes its first prompt at launch.
type PromptBox struct {
	Deliver string `json:"deliver,omitempty"` // DeliverQueue (default) or DeliverSend
	Submit  string `json:"submit,omitempty"`  // SubmitEnter (default) or SubmitNone
}

// AgentPromptBox is the built-in agents' box, and a pane's that tuios
// reports as an agent with no profile.
var AgentPromptBox = PromptBox{Deliver: DeliverQueue, Submit: SubmitEnter}

// TerminalPromptBox is the box of a pane with no profile that is no agent:
// a shell or any other terminal program. The text is typed, then Enter.
var TerminalPromptBox = PromptBox{Deliver: DeliverSend, Submit: SubmitEnter}

// Box is the harness's prompt box, each field it leaves out (or all, with
// no promptBox) from AgentPromptBox.
func (h Harness) Box() PromptBox {
	if h.PromptBox == nil {
		return AgentPromptBox
	}
	return h.PromptBox.Resolved()
}

// Resolved is b with each field it leaves out from AgentPromptBox.
func (b PromptBox) Resolved() PromptBox {
	if b.Deliver == "" {
		b.Deliver = AgentPromptBox.Deliver
	}
	if b.Submit == "" {
		b.Submit = AgentPromptBox.Submit
	}
	return b
}

func (b PromptBox) validate(name string) error {
	switch b.Deliver {
	case "", DeliverQueue, DeliverSend:
	default:
		return fmt.Errorf("harness %s: promptBox.deliver %q is not a delivery; use %q or %q", name, b.Deliver, DeliverQueue, DeliverSend)
	}
	switch b.Submit {
	case "", SubmitEnter, SubmitNone:
	default:
		return fmt.Errorf("harness %s: promptBox.submit %q is not a submit; use %q or %q", name, b.Submit, SubmitEnter, SubmitNone)
	}
	return nil
}
