package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"ofam.dev/ochazuke"
	"ofam.dev/ochazuke/cli"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "catalog" {
		if _, present := os.LookupEnv("OCHAZUKE_HOST"); present {
			fmt.Fprintln(os.Stderr, "catalog inherited host access")
			os.Exit(2)
		}
		fmt.Println(`{"schema":1,"name":"o-alpha","version":"1.0","nouns":[{"name":"tool","verbs":[{"name":"probe","kind":"application","agent":"allow"}]}]}`)
		return
	}
	host, ok, err := cli.HostFromEnv()
	if err != nil {
		fail(err)
	}
	if !ok {
		fmt.Println(`{"standalone":true}`)
		return
	}
	peers, err := host.Peers(context.Background())
	if err != nil {
		fail(err)
	}
	result, err := host.Run(context.Background(), "beta.doc.send", ochazuke.Arguments{"pane": "pane-9"})
	if err != nil {
		fail(err)
	}
	_, denied := host.Run(context.Background(), "extension.register", ochazuke.Arguments{"name": "forbidden"})
	refusal := ""
	if denied != nil {
		refusal = denied.Error()
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"peers": peers, "result": json.RawMessage(result), "refusal": refusal, "host": host.Command, "data": os.Getenv("MTM_DATA")})
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
