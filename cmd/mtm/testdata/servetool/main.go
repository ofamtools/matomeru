// servetool is a served extension for mtm's persistent-connection tests
// (extension_pool_test.go): a tool on ochazuke's CLI shell that declares
// the host serve protocol. It keeps its items under MTM_DATA and appends a
// line per process start to MTM_DATA/servetool-starts, so a test counts
// the processes mtm started.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ofam.dev/ochazuke"
	"ofam.dev/ochazuke/cli"
	"ofam.dev/oden"
)

func main() {
	if dir := os.Getenv("MTM_DATA"); dir != "" && (len(os.Args) < 2 || os.Args[1] != "catalog") {
		if f, err := os.OpenFile(filepath.Join(dir, "servetool-starts"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
			f.Close()
		}
	}
	os.Exit(oden.Report(os.Stderr, "o-serve", run(os.Args[1:])))
}

func run(args []string) error {
	r := ochazuke.NewRuntime()
	dir, _ := os.MkdirTemp("", "servetool")
	defer os.RemoveAll(dir)
	r.Config().Dir = filepath.Join(dir, "config")
	r.Config().StateDir = filepath.Join(dir, "state")
	// Every read of the environment is per call, as a served tool's must be.
	items := func() []string {
		raw, _ := os.ReadFile(filepath.Join(os.Getenv("MTM_DATA"), "servetool-items"))
		return strings.Fields(string(raw))
	}
	allow := func(b *ochazuke.NounBuilder) { b.Agent(ochazuke.AgentAllow) }
	n := r.Define("item")
	allow(n.Verb("list", func(ochazuke.Ctx) ochazuke.Result {
		rows := []map[string]any{}
		for _, id := range items() {
			rows = append(rows, map[string]any{"id": id, "label": id, "cells": []string{id}})
		}
		return ochazuke.Done(rows)
	}, "List the items").Fetch())
	for _, source := range []string{"tags", "kinds", "colors"} {
		allow(n.Verb(source, func(ochazuke.Ctx) ochazuke.Result { return ochazuke.Done([]string{"a", "b"}) }, "Choices").Fetch())
	}
	allow(n.Verb("add", func(c ochazuke.Ctx) ochazuke.Result {
		f, err := os.OpenFile(filepath.Join(os.Getenv("MTM_DATA"), "servetool-items"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return ochazuke.Failed(err)
		}
		defer f.Close()
		name := ochazuke.StringArg(c.Args, "name", "")
		fmt.Fprintln(f, name)
		return ochazuke.Done(map[string]any{"added": name})
	}, "Add an item"))
	// whoami says which process answered and whether it was served.
	allow(n.Verb("whoami", func(ochazuke.Ctx) ochazuke.Result {
		return ochazuke.Done(map[string]any{"pid": os.Getpid(), "served": os.Getenv(cli.HostPipeEnv) == "1"})
	}, "Say which process answers").Fetch())
	// feed's store watch streams one event, then waits until it is
	// stopped: the shell writes each event as a line of the call's stdout.
	feed := ochazuke.Thing{"id": "w1"}
	r.Define("feed").Props(ochazuke.PropSpec{Name: "id", Key: true}).Store(ochazuke.Store{
		Get:  func(ochazuke.Ctx, string) (ochazuke.Thing, error) { return feed, nil },
		List: func(ochazuke.Ctx, ochazuke.ListQuery) ([]ochazuke.Thing, error) { return []ochazuke.Thing{feed}, nil },
		Watch: func(_ ochazuke.Ctx, _ ochazuke.WatchQuery, emit func(ochazuke.WatchEvent)) (func(), error) {
			emit(ochazuke.WatchEvent{Type: "added", ID: "w1", Hash: "h1"})
			return func() {}, nil
		},
	})
	// host asks mtm for its peers (Host.Call): over the pipe when served.
	allow(n.Verb("host", func(ochazuke.Ctx) ochazuke.Result {
		return ochazuke.Pending(func(ctx context.Context) (any, error) {
			host, ok, err := cli.HostFromEnv()
			if err != nil || !ok {
				return nil, fmt.Errorf("no host: %v", err)
			}
			peers, err := host.Peers(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]any{"peers": len(peers), "pid": os.Getpid()}, nil
		})
	}, "Ask the host for its peers"))
	choices := func(source string) ochazuke.Parameter {
		return ochazuke.Parameter{Type: "string", Source: "item." + source, Free: true}
	}
	if op, ok := r.Operations.Get("item.add"); ok {
		op.Parameters = map[string]ochazuke.Parameter{"name": {Type: "string"}, "tag": choices("tags"), "kind": choices("kinds"), "color": choices("colors")}
	}
	sh := cli.New(r, "o-serve", os.Stdin, os.Stdout, os.Stderr, cli.Serves())
	app := oden.New("o-serve", args, oden.WithBuiltins())
	cli.Mount(app, sh)
	return app.Run(context.Background())
}
