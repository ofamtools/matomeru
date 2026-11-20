package main

import (
	"cmp"
	"errors"
	"maps"
	"strings"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/harness"
)

// agent.ask-new: send a question to a new agent. It composes two existing
// verbs: question.question.ask (o-questions, mounted) records the
// question verbatim, then agent.start starts an agent whose first prompt is
// the question and the question's id, told to record its answer with
// question.question.answer. The new pane is related to the question (a
// relation row of kind aboutRelationKind, agent.start's about).
//
// Record first, never refuse: with o-questions not mounted, or its ask
// failing, the agent still starts with the question, and a notice says the
// question was not recorded.
//
// From a key (g q, bindings/rows.json) with no question it opens the ask
// form: the question first, a text area focused in insert, then the launch
// options agent.start takes (harness, model, effort, fast, permission-mode,
// project, drive). An empty option is the harness's or project's default.

// askFormID is the ask form's modal instance.
const askFormID = "agent-ask-form"

// askQuestionNoun is the mounted noun whose ask verb records the question.
const askQuestionNoun = "question.question"

// aboutRelationKind is the kind of relation agent.start's about makes from
// the new pane to the thing it works on.
const aboutRelationKind = "about"

// settingAskPrompt is the first prompt agent.ask-new gives the new agent:
// {question} the question as typed, {id} its o-questions id ("" when it
// was not recorded).
const settingAskPrompt = "ask.prompt"

const defaultAskPrompt = "The owner asked this question (o-questions id {id}):\n\n{question}\n\n" +
	"Answer it. Then record your full answer on that question with question.question.answer " +
	"(target {id}) through the mtm MCP gateway, or `mtm question.question answer --target {id} --answer TEXT`."

// settingAskFollow says where the focus goes when agent.ask-new starts
// its agent: off (the default) keeps the page and focus where they were
// before the form opened and a notice names the new pane; on opens the
// Panes page on the new pane, as agent.start does.
const settingAskFollow = "ask.follow"

var askFollowSetting = ochazuke.Setting{Name: settingAskFollow, Type: ochazuke.SettingBoolean, Default: false,
	Description: "Open the new agent's pane once agent.ask-new (g q, ;) starts it; off keeps the page and focus you asked from and a notice names the new pane"}

// askPaneNameRunes is how long the new agent's pane name, the question
// on one line, may be before it is cut.
const askPaneNameRunes = 40

// settingAskEnterSends says whether enter sends the ask form: off (the
// default) never sends, enter moves to the next field (a new line in the
// question) and alt+enter sends from any field; on sends on enter once
// the question has text, as any form's enter does.
const settingAskEnterSends = "ask.enter-sends"

var askEnterSendsSetting = ochazuke.Setting{Name: settingAskEnterSends, Type: ochazuke.SettingBoolean, Default: false,
	Description: "Send the ask form (g q, ;) on enter once the question has text; off never sends on enter (a new line in the question, the next field elsewhere) and alt+enter sends"}

var askPromptSetting = ochazuke.Setting{Name: settingAskPrompt, Type: ochazuke.SettingString, Default: defaultAskPrompt,
	Description: "The first prompt agent.ask-new (g q) gives the new agent: {question} is the question as typed, {id} its o-questions id"}

// askFields are the ask form's fields in order. The first, the question,
// is the text area the form opens on.
var askFields = []string{"question", "harness", "model", "effort", "fast", "permission-mode", "project", "drive"}

// launchExtraParams are the launch options agent.start and agent.launch
// take beside harness and model: the first prompt and about
// (launchPromptParams), and the harness settings (launchSettingParams).
func (a *App) launchExtraParams() map[string]ochazuke.Parameter {
	out := a.launchSettingParams()
	maps.Copy(out, launchPromptParams)
	return out
}

// launchPromptParams are the first prompt and the thing the agent works on.
var launchPromptParams = map[string]ochazuke.Parameter{
	"prompt": {Type: "string", Description: "The agent's first prompt (default: the task's title, else none)"},
	"about":  {Type: "string", Noun: ochazuke.AnyNoun, Free: true, Description: "A thing the agent works on, noun:key: the new pane is related to it (kind " + aboutRelationKind + ")"},
}

// launchSettingParams are the harness settings a launch can set (effort,
// fast, permission-mode: the profile's settings rows, as flags or typed
// commands; Harness.LaunchSettings).
func (a *App) launchSettingParams() map[string]ochazuke.Parameter {
	out := map[string]ochazuke.Parameter{}
	for _, name := range harness.LaunchSettingNames {
		out[name] = launchSettingParam(name, "The harness's "+name+" at launch (its settings row in harnesses.json); empty: the harness's default")
	}
	return out
}

// launchSettingsOf are a launch request's harness settings by name.
func launchSettingsOf(args ochazuke.Arguments) map[string]string {
	out := map[string]string{}
	for _, name := range harness.LaunchSettingNames {
		if v := strings.TrimSpace(ochazuke.StringArg(args, name, "")); v != "" {
			out[name] = v
		}
	}
	return out
}

// askParams are agent.ask-new's parameters: the question and agent.start's
// launch options.
func (a *App) askParams(agent, model ochazuke.Parameter) map[string]ochazuke.Parameter {
	out := map[string]ochazuke.Parameter{
		"question": {Type: "string", Rest: true, Description: "The question, verbatim (several lines allowed); omitted from a key, the ask form opens"},
		"harness":  agent,
		"model":    model,
		"project":  {Type: "string", Noun: "project", Source: "projects.all", Free: true, Description: "The project the agent works in; empty: no project"},
		"drive":    driveParam,
	}
	maps.Copy(out, a.launchSettingParams())
	return out
}

// askAgent is agent.ask-new.
func (a *App) askAgent(ctx ochazuke.Ctx) ochazuke.Result {
	text := ochazuke.StringArg(ctx.Args, "question", "")
	if strings.TrimSpace(text) == "" {
		if a.cli {
			return ochazuke.Failed(errors.New("agent ask-new needs the question: mtm agent ask-new --question TEXT"))
		}
		return resultError(a.openAskForm(ctx.Args))
	}
	args := ctx.Args
	x, noun := a.mountedNoun(askQuestionNoun)
	if x == nil {
		ctx.Notify("o-questions is not mounted: the question goes to the agent unrecorded")
		return resultError(a.startAsked(args, ""))
	}
	ask := map[string]any{"question": text}
	if p := ochazuke.StringArg(args, "project", ""); p != "" {
		ask["project"] = p
	}
	return a.extRun(x, noun, "ask", "", ask, func(v any, err error) {
		id := ""
		if err != nil {
			a.r.UI().Notify("question not recorded (" + err.Error() + "); asking the agent anyway")
		} else if id = askedID(v); id == "" {
			a.r.UI().Notify("question recorded, but its id was not in the answer; asking the agent anyway")
		}
		if err := a.startAsked(args, id); err != nil {
			a.r.UI().Notify("agent not started: " + err.Error())
		}
	})
}

// startAsked runs agent.start for an asked question: the launch options as
// given, the prompt from ask.prompt, and about the question when it has an
// id. The session follows the project, as agent.launch's does.
func (a *App) startAsked(args ochazuke.Arguments, id string) error {
	p, err := a.startProject(ochazuke.Ctx{Args: args})
	if err != nil {
		a.r.UI().Notify(err.Error() + "; asking in no project")
	}
	start := projectArgs(p, noProjectSession)
	for _, name := range append([]string{"harness", "model", "drive"}, harness.LaunchSettingNames...) {
		if v := ochazuke.StringArg(args, name, ""); v != "" {
			start[name] = v
		}
	}
	start["focus"] = a.r.Settings().Bool(settingAskFollow)
	start["name"] = oneLineCut(ochazuke.StringArg(args, "question", ""), askPaneNameRunes)
	start["prompt"] = askPrompt(cmp.Or(a.r.Settings().String(settingAskPrompt), defaultAskPrompt), ochazuke.StringArg(args, "question", ""), id)
	if id != "" {
		start["about"] = ochazuke.Ref{Noun: askQuestionNoun, Key: id}.String()
	}
	_, err = a.r.Execute(ochazuke.Request{Verb: "start", Noun: "agent", Target: "work", Args: start})
	return err
}

// askPrompt fills the ask.prompt template.
func askPrompt(template, question, id string) string {
	if id == "" {
		id = "(not recorded)"
	}
	return strings.NewReplacer("{question}", question, "{id}", id).Replace(template)
}

// askedID is the question id in question.question.ask's answer
// ({"question": {"id": ...}}).
func askedID(v any) string {
	m, _ := v.(map[string]any)
	q, _ := m["question"].(map[string]any)
	id, _ := q["id"].(string)
	return id
}

// mountedNoun is the extension that mounted a noun (by its mtm name) and
// the noun's name in that tool's catalog; nil when none did.
func (a *App) mountedNoun(local string) (*extension, string) {
	want := a.r.CanonicalNoun(local)
	for _, x := range a.exts {
		for catalog, mounted := range x.nouns {
			if a.r.CanonicalNoun(mounted) == want {
				return x, catalog
			}
		}
	}
	return nil, ""
}

// relateAbout relates a launched pane to the thing it works on (about,
// noun:key); "" relates nothing. A failure is a notice: the pane is open.
func (a *App) relateAbout(window, about string) {
	if window == "" || about == "" {
		return
	}
	_, err := a.r.Execute(ochazuke.Request{Noun: relationNoun, Verb: "create", Args: ochazuke.Arguments{
		"kind": aboutRelationKind, "from": ochazuke.Ref{Noun: "pane", Key: window}.String(), "to": about,
	}})
	if err != nil {
		a.r.UI().Notify("pane " + window + " not related to " + about + ": " + err.Error())
	}
}

// openAskForm opens the ask form: the question first, focused in insert,
// then the launch options, each defaulted from the request, else the pin's
// project and its harness. Its run is agent.ask-new with the values.
func (a *App) openAskForm(given ochazuke.Arguments) error {
	if a.r.Instances[askFormID] != nil {
		return a.r.SetFocus(askFormID)
	}
	op := a.r.Operations.At(ochazuke.Key(a.r.CanonicalKey("agent.ask-new")))
	var fields []ochazuke.RecordField
	for _, name := range askFields {
		if p, ok := op.Parameters[name]; ok {
			fields = append(fields, ochazuke.RecordField{Name: name, Parameter: p})
		}
	}
	// The first field is the question: a text area the run needs.
	fields[0].Multiline, fields[0].Required = true, true
	pin, _ := a.project(a.pin)
	values := ochazuke.Record{"project": pin.ID, "harness": cmp.Or(pin.Settings.Harness, a.launchHarness())}
	for name, v := range given {
		if s, ok := v.(string); ok && s != "" {
			values[name] = s
		}
	}
	_, err := a.r.OpenRecordEditor(askFormID, ochazuke.RecordSpec{
		Title:      "Ask a new agent",
		Fields:     fields,
		Insert:     true,
		EnterMoves: !a.r.Settings().Bool(settingAskEnterSends),
		Load:       func() (ochazuke.Record, error) { return values, nil },
		Submit: func(rec ochazuke.Record) ochazuke.Result {
			args := ochazuke.Arguments{}
			for name, v := range rec {
				if v != "" {
					args[name] = v
				}
			}
			return resultOf(a.r.Execute(ochazuke.Request{Noun: "agent", Verb: "ask-new", Args: args}))
		},
	})
	return err
}
