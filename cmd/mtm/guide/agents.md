<!-- Adapted from the tuios skill (skills/tuios, v0.12.1), MIT License, Copyright (c) 2025 Gaurav Gosain. See LICENSE-tuios in this directory. -->
# Agents: state, asking and queueing

States: none, working, needs_input, idle, done, errored, unknown.
`agent.scan` lists the agent panes of a session (`all` includes panes with no
agent, `all_sessions` every session when your scope allows). `agent.status`
reads one pane's state with what blocks it.

| Verb | Use it when | What it does |
| --- | --- | --- |
| `agent.ask` | You need the answer now | Waits until the agent is at rest, types the question, returns the answer. Refuses an agent on needs_input. |
| `agent.enqueue` | You give a busy agent its next task | Types the text as a prompt when the agent next comes to rest. Returns its place in the queue. |
| `pane.send-text` | The pane runs a shell or a program, not an agent | Types at once. Do not use it to talk to an agent. |
| `agent.peek-prompt` | An agent is on needs_input | Reads the prompt without attaching. Only the person answers it. |

`agent.set-status` reports your own state: `working` with a message while you
work, `needs_input` when you wait for approval. `agent.set-meta` records model,
context used and cost for display.
