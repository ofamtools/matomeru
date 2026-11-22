// Command mcp-agent is an MCP client that does what an agent in a pane does:
// it starts a lane, logs to it, sends the owner a comparison page and a
// question, and reads back what the store holds.
//
//	go run ./examples/mcp-agent                         serve mcpui.ServeStdio in this process, over io.Pipe
//	go run ./examples/mcp-agent --cmd "mtm mcp serve"   talk to a real server over its stdin and stdout
//	go run ./examples/mcp-agent --smoke                 the in-process run, checked; exits 0
//
// The protocol is JSON-RPC 2.0, one message per line.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/ui"
)

// client is the whole MCP client: write a request line, read the reply line.
type client struct {
	in   io.Writer
	out  *bufio.Reader
	next int
}

func (c *client) request(method string, params any) (map[string]any, error) {
	c.next++
	line, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.next, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if _, err := c.in.Write(append(line, '\n')); err != nil {
		return nil, err
	}
	reply, err := c.out.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("%s: no reply: %w", method, err)
	}
	var msg struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(reply, &msg); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if msg.Error != nil {
		return nil, fmt.Errorf("%s: %s", method, msg.Error.Message)
	}
	return msg.Result, nil
}

// call runs one tool and returns its structured result.
func (c *client) call(tool string, args any) (map[string]any, error) {
	res, err := c.request("tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return nil, err
	}
	if res["isError"] == true {
		return nil, fmt.Errorf("%s: %v", tool, res["content"])
	}
	out, _ := res["structuredContent"].(map[string]any)
	return out, nil
}

func at(m map[string]any, path ...string) any {
	var v any = m
	for _, key := range path {
		next, _ := v.(map[string]any)
		v = next[key]
	}
	return v
}

// agent is the session. pane names the pane the agent runs in; a real agent
// leaves it out and the server reads $MTM_PANE_ID.
func agent(c *client, pane string, say func(string, ...any)) error {
	init, err := c.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": "example-agent"}})
	if err != nil {
		return err
	}
	say("initialize: %v, protocol %v", at(init, "serverInfo", "name"), init["protocolVersion"])

	list, err := c.request("tools/list", nil)
	if err != nil {
		return err
	}
	var names []string
	for _, t := range list["tools"].([]any) {
		names = append(names, t.(map[string]any)["name"].(string))
	}
	say("tools/list: %d tools: %s", len(names), strings.Join(names, " "))
	for _, need := range []string{"lane.start", "lane.log", "ui.create_page", "ui.ask", "ui.list_pages", "data.query"} {
		if !strings.Contains(" "+strings.Join(names, " ")+" ", " "+need+" ") {
			return fmt.Errorf("the server has no %s tool", need)
		}
	}

	// A lane: what this agent is doing, claimed by its pane.
	lane, err := c.call("lane.start", map[string]any{"title": "Compare storage engines", "status": "running", "pane": pane, "branch": "storage-compare", "agentModel": "example"})
	if err != nil {
		return err
	}
	laneID, _ := at(lane, "lane", "id").(string)
	say("lane.start: lane %s, claimed by %v", laneID, at(lane, "claim", "to", "id"))
	for _, e := range [][2]string{{"milestone", "benchmarks collected"}, {"decision", "SQLite in WAL mode: one file, no server"}, {"verification", "go test -race passes"}} {
		if _, err := c.call("lane.log", map[string]any{"lane": laneID, "kind": e[0], "text": e[1]}); err != nil {
			return err
		}
	}
	say("lane.log: 3 timeline entries")

	// A page for the owner: a comparison grid of cards.
	card := func(id, title, summary, badge, tone string) ui.ViewNode {
		return ui.ViewNode{ID: id, Kind: ui.KindCard, Scope: "choice", Focusable: true, Border: true, Title: title,
			Badges: []ui.Badge{{Text: badge, Tone: tone}}, Props: map[string]any{"summary": summary}}
	}
	page, err := c.call("ui.create_page", map[string]any{"pane": pane, "title": "Storage engines compared", "slug": "storage", "pageKind": "response",
		"root": ui.ViewNode{Kind: ui.KindVStack, Gap: 1, Children: []ui.ViewNode{
			{ID: "cmp.intro", Kind: ui.KindMarkdown, Props: map[string]any{"markdown": "## Storage engines\n\nThree options, measured on the same 10k records."}},
			{ID: "cmp.grid", Kind: ui.KindGrid, Gap: 1, MinColW: 30, Children: []ui.ViewNode{
				card("cmp.sqlite", "SQLite (WAL)", "One file. 41k writes/s. Readers never block.", "recommended", "success"),
				card("cmp.bolt", "bbolt", "One file. 9k writes/s. One writer at a time.", "simple", ""),
				card("cmp.pg", "Postgres", "A server to run. 60k writes/s over a socket.", "heavy", "warning"),
			}},
		}}})
	if err != nil {
		return err
	}
	pageID, _ := page["id"].(string)
	say("ui.create_page: page %s (slug %v)", pageID, page["slug"])
	if _, err := c.call("lane.attach", map[string]any{"lane": laneID, "kind": "ui.page", "id": pageID}); err != nil {
		return err
	}

	// A question: it waits, unread, on the pane's Questions page.
	asked, err := c.call("ui.ask", map[string]any{"pane": pane, "id": "q-engine", "title": "Which engine do we ship?",
		"choices": []map[string]any{{"id": "sqlite", "label": "SQLite"}, {"id": "bbolt", "label": "bbolt"}, {"id": "postgres", "label": "Postgres"}}})
	if err != nil {
		return err
	}
	say("ui.ask: card %v on page %v", asked["cardId"], asked["slug"])

	pages, err := c.call("ui.list_pages", map[string]any{"pane": pane})
	if err != nil {
		return err
	}
	open := 0
	for _, p := range pages["pages"].([]any) {
		questions, _ := at(p.(map[string]any), "questions").([]any) // left out of a page with none
		for _, q := range questions {
			if q.(map[string]any)["answered"] != true {
				open++
			}
		}
	}
	say("ui.list_pages: %d pages, %d question waiting for the owner", len(pages["pages"].([]any)), open)

	notes, err := c.call("data.query", map[string]any{"kind": "note", "query": "decision:", "filterField": "title"})
	if err != nil {
		return err
	}
	lanes, err := c.call("data.query", map[string]any{"kind": "lane"})
	if err != nil {
		return err
	}
	say("data.query: %v lane, %v decision in its timeline", lanes["total"], notes["total"])
	if laneID == "" || pageID == "" || len(pages["pages"].([]any)) != 2 || open != 1 || lanes["total"] != float64(1) || notes["total"] != float64(1) {
		return fmt.Errorf("the store does not hold what was sent: %v pages, %d open, %v lanes, %v decisions", len(pages["pages"].([]any)), open, lanes["total"], notes["total"])
	}
	return nil
}

func run(command string, quiet bool) error {
	say := func(format string, args ...any) {
		if !quiet {
			fmt.Printf(format+"\n", args...)
		}
	}
	if command != "" {
		// A real server: any command that speaks MCP on its stdin and stdout.
		cmd := exec.Command("/bin/sh", "-c", command)
		cmd.Stderr = os.Stderr
		in, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		err = agent(&client{in: in, out: bufio.NewReader(out)}, "example-pane", say)
		in.Close()
		if werr := cmd.Wait(); err == nil {
			err = werr
		}
		return err
	}
	// In process: the same server function mtm runs, over two pipes.
	store, err := data.Open(":memory:")
	if err != nil {
		return err
	}
	defer store.Close()
	toServer, clientOut := io.Pipe()
	serverOut, toClient := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := mcpui.ServeStdio(context.Background(), toServer, toClient, store, mcpui.New(store))
		toClient.Close()
		done <- err
	}()
	err = agent(&client{in: clientOut, out: bufio.NewReader(serverOut)}, "example-pane", say)
	clientOut.Close() // end of input: the server returns
	if serr := <-done; err == nil {
		err = serr
	}
	return err
}

func main() {
	smoke := flag.Bool("smoke", false, "run in process, check the results, print one line")
	command := flag.String("cmd", "", `server command to spawn, such as "mtm mcp serve --db /tmp/example.db"`)
	flag.Parse()
	if err := run(*command, *smoke); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-agent:", err)
		os.Exit(1)
	}
	if *smoke {
		fmt.Println("mcp-agent: ok (initialize, tools/list, lane.start, lane.log, ui.create_page, ui.ask, ui.list_pages, data.query)")
	}
}
