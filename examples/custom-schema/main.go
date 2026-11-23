// Command custom-schema registers a noun Matomeru has never heard of and gets
// its Index, Show and Edit views with no view code: they come from the schema.
//
//	go run ./examples/custom-schema [--smoke]
package main

import (
	"fmt"
	"os"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// outline lists a view's named nodes: kind, id and title.
func outline(n ui.ViewNode, depth int, out *[]string) {
	if n.ID != "" {
		*out = append(*out, fmt.Sprintf("%s%s %s %q", strings.Repeat("  ", depth), n.Kind, n.ID, n.Title))
	}
	for _, c := range n.Children {
		outline(c, depth+1, out)
	}
}

func run() (lines []string, err error) {
	store, err := data.Open(":memory:")
	if err != nil {
		return nil, err
	}
	defer store.Close()
	// 1. Declare the noun: fields infer the form widgets, the status column
	//    gives badges and a pick-one field.
	read := data.StatusColumn{Name: "readStatus", Title: "Read", Default: "unread", Values: []data.StatusValue{
		{Name: "unread", Tone: "warning", Position: 0, Open: true}, {Name: "read", Tone: "state.done", Position: 1, Ends: true}}}
	link := data.Schema{Kind: "link", Title: "Link", Plural: "Links", SummaryField: "title",
		Fields: []data.FieldSpec{
			{Name: "title", Type: core.ParamString, Required: true},
			{Name: "url", Type: core.ParamString, Required: true},
			{Name: "readStatus", Type: core.ParamString, Enum: read.Names(), Default: read.Default},
			{Name: "task", Type: core.ParamRef, RefKinds: []string{data.KindTask}}},
		StatusColumns: []data.StatusColumn{read}, DefaultColumns: []string{"title", "url", "readStatus"}, UniqueFields: []string{"url"}}
	store.Registry().Register(link)

	// 2. Store records of it like any other.
	me := core.Actor{Kind: "user", ID: "example"}
	for _, l := range [][2]string{{"Ochazuke", "https://ofam.dev/ochazuke"}, {"Matomeru", "https://ofam.dev/matomeru"}} {
		if _, _, err := store.Create(data.CreateInput{Kind: "link", Fields: map[string]any{"title": l[0], "url": l[1]}}, me); err != nil {
			return nil, err
		}
	}
	res, err := store.Query(data.Query{Kind: "link"})
	if err != nil {
		return nil, err
	}
	first := res.Items[0]

	// 3. The three views, straight from the schema.
	show, err := views.ShowView(store, first.Ref(), "")
	if err != nil {
		return nil, err
	}
	index := views.IndexView(link, views.TableFromRecords(link, res.Items), "links")
	edit := views.EditView(link, &first, views.FormForRecord(link, &first), "edit")
	for _, v := range []ui.ViewNode{index, show, edit} {
		outline(v, 0, &lines)
	}
	return lines, nil
}

func main() {
	lines, err := run()
	text := strings.Join(lines, "\n")
	for _, want := range []string{`panel links "Links"`, "table links.table", `card show.header "Ochazuke"`, `panel edit "Edit Ochazuke"`, "form edit.form"} {
		if err == nil && !strings.Contains(text, want) {
			err = fmt.Errorf("the views lack %s", want)
		}
	}
	switch {
	case err != nil:
		fmt.Fprintln(os.Stderr, "custom-schema:", err)
		os.Exit(1)
	case len(os.Args) > 1 && os.Args[1] == "--smoke":
		fmt.Printf("custom-schema: ok (%d view nodes for a noun declared in this file)\n", len(lines))
	default:
		fmt.Println(text)
	}
}
