package main

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"ofam.dev/ochazuke"
)

// The pane-driving guidance an agent used to read from `tuios --skill`, as
// mtm's own text: one markdown file per topic under guide/, embedded, so a
// new topic is a new file. agent.guide reads them; `mtm mcp instructions` is
// the shorter catalog guidance an MCP client is given at start.

//go:embed guide/*.md
var guideFiles embed.FS

const defaultGuideTopic = "core"

// guideTopics are the topic names, default first.
func guideTopics() []string {
	entries, _ := fs.ReadDir(guideFiles, "guide")
	var topics []string
	for _, e := range entries {
		topics = append(topics, strings.TrimSuffix(e.Name(), ".md"))
	}
	slices.Sort(topics)
	if i := slices.Index(topics, defaultGuideTopic); i > 0 {
		topics = slices.Insert(slices.Delete(topics, i, i+1), 0, defaultGuideTopic)
	}
	return topics
}

func (a *App) defineGuideVerb() {
	a.r.Define("agent").Verb("guide", a.agentGuide, "Print the guidance for driving panes through mtm: the verbs, the safety rules, waiting and reading. topic picks one (default "+defaultGuideTopic+"); all prints every topic").Fetch().Agent(ochazuke.AgentAllow)
	a.parameters("agent.guide", map[string]ochazuke.Parameter{
		"topic": {Type: "string", Description: "One of: " + strings.Join(guideTopics(), ", ") + " (default " + defaultGuideTopic + ")", Enum: guideTopics()},
		"all":   {Type: "boolean", Description: "Print every topic"},
	})
}

func (a *App) agentGuide(ctx ochazuke.Ctx) ochazuke.Result {
	topics := guideTopics()
	var pick []string
	if ctx.Args["all"] != true {
		topic := ochazuke.StringArg(ctx.Args, "topic", defaultGuideTopic)
		if !slices.Contains(topics, topic) {
			return ochazuke.Failed(fmt.Errorf("no guide topic %q; topics: %s", topic, strings.Join(topics, ", ")))
		}
		pick = []string{topic}
	} else {
		pick = topics
	}
	var out []string
	for _, t := range pick {
		raw, err := guideFiles.ReadFile("guide/" + t + ".md")
		if err != nil {
			return ochazuke.Failed(errors.New("guide topic " + t + ": " + err.Error()))
		}
		out = append(out, strings.TrimSpace(string(raw)))
	}
	return ochazuke.Done(map[string]any{"topics": topics, "shown": pick, "text": strings.Join(out, "\n\n")})
}
