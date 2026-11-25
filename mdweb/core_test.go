package mdweb

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func findWidget(t *testing.T, ir *DocumentIR, id string) WidgetSpec {
	t.Helper()
	for _, w := range ir.Widgets {
		if w.ID == id {
			return w
		}
	}
	t.Fatalf("widget %q not found in %d widgets", id, len(ir.Widgets))
	return WidgetSpec{}
}

func TestChecklistListAndTable(t *testing.T) {
	src := strings.Join([]string{
		"# Plan",
		"",
		"- [ ] First item",
		"- [x] Second item",
		"  - [X] Nested done",
		"",
		"| Item | Done |",
		"| --- | --- |",
		"| Alpha | [ ] Check me |",
		"",
	}, "\n")

	ir := ParseMarkdown(src, ParseOptions{File: "plan.md"})
	if ir.Stats.CheckboxTotal != 4 {
		t.Fatalf("CheckboxTotal = %d, want 4", ir.Stats.CheckboxTotal)
	}
	if ir.Stats.CheckboxChecked != 2 {
		t.Fatalf("CheckboxChecked = %d, want 2", ir.Stats.CheckboxChecked)
	}

	// Verify exact 1-byte ValueSpan and 3-byte Span on every checkbox.
	wantMarks := map[string]string{
		"chk-0": " ",
		"chk-1": "x",
		"chk-2": "X",
		"chk-3": " ",
	}
	for id, wantMark := range wantMarks {
		w := findWidget(t, ir, id)
		if got := src[w.ValueSpan.Start:w.ValueSpan.End]; got != wantMark {
			t.Errorf("%s ValueSpan slice = %q, want %q", id, got, wantMark)
		}
		if got := src[w.Span.Start:w.Span.End]; got != "["+wantMark+"]" {
			t.Errorf("%s Span slice = %q, want %q", id, got, "["+wantMark+"]")
		}
	}

	// Toggle first list checkbox.
	p1, err := ApplyMutation(src, ir, MutationRequest{Kind: WidgetCheckbox, ID: "chk-0", Checked: true})
	if err != nil {
		t.Fatalf("ApplyMutation chk-0: %v", err)
	}
	if !p1.Changed {
		t.Fatalf("expected Changed=true for chk-0")
	}
	if !strings.Contains(p1.NextSource, "- [x] First item\n- [x] Second item\n") {
		t.Fatalf("unexpected patched source:\n%s", p1.NextSource)
	}

	// Toggle table cell checkbox without disturbing surrounding bytes.
	ir2 := ParseMarkdown(p1.NextSource, ParseOptions{File: "plan.md"})
	p2, err := ApplyMutation(p1.NextSource, ir2, MutationRequest{Kind: WidgetCheckbox, ID: "chk-3", Checked: true})
	if err != nil {
		t.Fatalf("ApplyMutation chk-3: %v", err)
	}
	if !strings.Contains(p2.NextSource, "| Alpha | [x] Check me |") {
		t.Fatalf("table checkbox not patched:\n%s", p2.NextSource)
	}
	if !strings.HasPrefix(p2.NextSource, "# Plan\n\n- [x] First item\n- [x] Second item\n  - [X] Nested done\n") {
		t.Fatalf("unrelated bytes disturbed:\n%s", p2.NextSource)
	}

	// Corrupted source at ValueSpan must error.
	badSrc := strings.Replace(src, "- [ ] First item", "- [?] First item", 1)
	if _, err := ApplyMutation(badSrc, ir, MutationRequest{Kind: WidgetCheckbox, ID: "chk-0", Checked: true}); err == nil {
		t.Fatalf("expected error for corrupted checkbox byte")
	}
}

func TestInlineDirectivesAndEscaping(t *testing.T) {
	src := strings.Join([]string{
		`Owner: {{input:owner="Alice" placeholder="Name" type="text" label="Owner"}}`,
		`Count: {{text:qty=42 type="number"}}`,
		`Status: {{select:status="Open" options="Open|Closed"}}`,
		`Mode: {{radio:mode='Fast' options="Fast|Safe"}}`,
		`Trigger: {{action:deploy label="Deploy Now" style="primary" payload="prod"}}`,
		"",
	}, "\n")

	cur := src
	ir := ParseMarkdown(cur, ParseOptions{File: "form.md"})
	if ir.Stats.InputCount != 4 {
		t.Fatalf("InputCount = %d, want 4", ir.Stats.InputCount)
	}

	qty := findWidget(t, ir, "input:qty")
	if qty.Value != "42" || qty.InputType != "number" {
		t.Fatalf("qty widget = %+v, want value=42 inputType=number", qty)
	}

	// Update input with quotes and backslash.
	res, err := ApplyMutation(cur, ir, MutationRequest{
		Kind:  WidgetInput,
		ID:    "input:owner",
		Value: `Bob "B" \ C`,
	})
	if err != nil {
		t.Fatalf("mutate input:owner: %v", err)
	}
	cur = res.NextSource
	if !strings.Contains(cur, `{{input:owner="Bob \"B\" \\ C" placeholder="Name" type="text" label="Owner"}}`) {
		t.Fatalf("escaped directive not found:\n%s", cur)
	}

	// Re-parse and verify unescaped value round-trips identically.
	ir = ParseMarkdown(cur, ParseOptions{File: "form.md"})
	owner := findWidget(t, ir, "input:owner")
	if owner.Value != `Bob "B" \ C` {
		t.Fatalf("round-trip owner.Value = %q, want %q", owner.Value, `Bob "B" \ C`)
	}

	// Select validation: invalid option rejected, valid option applied.
	if _, err := ApplyMutation(cur, ir, MutationRequest{
		Kind:  WidgetSelect,
		ID:    "select:status",
		Value: "Invalid",
	}); err == nil {
		t.Fatalf("expected error for invalid select option")
	}
	res, err = ApplyMutation(cur, ir, MutationRequest{
		Kind:  WidgetSelect,
		ID:    "select:status",
		Value: "Closed",
	})
	if err != nil {
		t.Fatalf("mutate select:status: %v", err)
	}
	cur = res.NextSource
	if !strings.Contains(cur, `{{select:status="Closed" options="Open|Closed"}}`) {
		t.Fatalf("select not updated:\n%s", cur)
	}

	// Radio validation & mutation.
	ir = ParseMarkdown(cur, ParseOptions{File: "form.md"})
	if _, err := ApplyMutation(cur, ir, MutationRequest{
		Kind:  WidgetRadio,
		ID:    "radio:mode",
		Value: "Unknown",
	}); err == nil {
		t.Fatalf("expected error for invalid radio option")
	}
	res, err = ApplyMutation(cur, ir, MutationRequest{
		Kind:  WidgetRadio,
		ID:    "radio:mode",
		Value: "Safe",
	})
	if err != nil {
		t.Fatalf("mutate radio:mode: %v", err)
	}
	cur = res.NextSource
	if !strings.Contains(cur, `{{radio:mode="Safe" options="Fast|Safe"}}`) {
		t.Fatalf("radio not updated:\n%s", cur)
	}

	// Action mutation returns Changed=false with payload and preserves source.
	ir = ParseMarkdown(cur, ParseOptions{File: "form.md"})
	actRes, err := ApplyMutation(cur, ir, MutationRequest{
		Kind: WidgetAction,
		ID:   "action:deploy",
	})
	if err != nil {
		t.Fatalf("mutate action:deploy: %v", err)
	}
	if actRes.Changed || actRes.NextSource != cur || actRes.NewValue != "prod" {
		t.Fatalf("unexpected action result: %+v", actRes)
	}
}

func TestFencedFieldsAndInjectionGuard(t *testing.T) {
	src := strings.Join([]string{
		`~~~field:summary label="Summary" rows="5"`,
		"Initial line",
		"~~~",
		"",
		"```textarea name=\"notes\"",
		"Note A",
		"```",
		"",
	}, "\n")

	ir := ParseMarkdown(src, ParseOptions{File: "fields.md"})
	summary := findWidget(t, ir, "field:summary")
	if summary.Value != "Initial line" || summary.Rows != 5 || summary.Label != "Summary" {
		t.Fatalf("unexpected summary field widget: %+v", summary)
	}

	// Injecting triple backticks must be neutralized by indenting.
	res, err := ApplyMutation(src, ir, MutationRequest{
		Kind:  WidgetField,
		ID:    "field:notes",
		Value: "Line 1\n```\nLine 2",
	})
	if err != nil {
		t.Fatalf("mutate field:notes: %v", err)
	}
	if !strings.Contains(res.NextSource, "```textarea name=\"notes\"\nLine 1\n  ```\nLine 2\n```") {
		t.Fatalf("expected triple backticks to be indented:\n%s", res.NextSource)
	}
}

func TestMermaidOrientationAndPatching(t *testing.T) {
	flowLR := "flowchart LR\n  A[Start] --> B[End]"
	info := DetectMermaidOrientation(flowLR)
	if info != (MermaidOrientationInfo{DiagramType: "flowchart", Direction: "LR", Flippable: true}) {
		t.Fatalf("DetectMermaidOrientation(flowLR) = %+v", info)
	}

	flowTD, tdInfo := SetMermaidDirection(flowLR, "")
	if flowTD != "flowchart TD\n  A[Start] --> B[End]" || tdInfo.Direction != "TD" {
		t.Fatalf("flip LR->TD got %q (%+v)", flowTD, tdInfo)
	}
	backLR, backInfo := SetMermaidDirection(flowTD, "")
	if backLR != flowLR || backInfo.Direction != "LR" {
		t.Fatalf("flip TD->LR got %q (%+v)", backLR, backInfo)
	}

	// Bare flowchart defaults to TD; RL flips to BT and back.
	bareInfo := DetectMermaidOrientation("%% comment\ngraph\n  A --> B")
	if bareInfo.DiagramType != "flowchart" || bareInfo.Direction != "TD" || !bareInfo.Flippable {
		t.Fatalf("bare graph info = %+v", bareInfo)
	}
	rl := "flowchart RL\r\n  A --> B"
	bt, btInfo := SetMermaidDirection(rl, "")
	if bt != "flowchart BT\r\n  A --> B" || btInfo.Direction != "BT" {
		t.Fatalf("CRLF RL->BT got %q (%+v)", bt, btInfo)
	}
	rlAgain, _ := SetMermaidDirection(bt, "")
	if rlAgain != rl {
		t.Fatalf("CRLF BT->RL got %q, want %q", rlAgain, rl)
	}

	// State / class / ER diagrams.
	stateDefault := "stateDiagram-v2\n  [*] --> Active"
	stateLR, sInfo := SetMermaidDirection(stateDefault, "")
	if stateLR != "stateDiagram-v2\n  direction LR\n  [*] --> Active" || sInfo.Direction != "LR" {
		t.Fatalf("stateDiagram flip got %q (%+v)", stateLR, sInfo)
	}
	stateTB, sInfo2 := SetMermaidDirection(stateLR, "")
	if stateTB != "stateDiagram-v2\n  direction TB\n  [*] --> Active" || sInfo2.Direction != "TB" {
		t.Fatalf("stateDiagram flip back got %q (%+v)", stateTB, sInfo2)
	}
	if classInfo := DetectMermaidOrientation("classDiagram\n  direction RL\n  A <|-- B"); classInfo.DiagramType != "class" || classInfo.Direction != "RL" {
		t.Fatalf("classDiagram info = %+v", classInfo)
	}
	if erInfo := DetectMermaidOrientation("erDiagram\n  USER ||--o{ ORDER : places"); erInfo.DiagramType != "er" || erInfo.Direction != "TB" || !erInfo.Flippable {
		t.Fatalf("erDiagram info = %+v", erInfo)
	}

	// Non-flippable diagram.
	seq := "sequenceDiagram\n  A->>B: ping"
	seqInfo := DetectMermaidOrientation(seq)
	if seqInfo.Flippable || seqInfo.DiagramType != "other" {
		t.Fatalf("sequenceDiagram info = %+v", seqInfo)
	}
	unchanged, _ := SetMermaidDirection(seq, "")
	if unchanged != seq {
		t.Fatalf("non-flippable diagram should remain unchanged")
	}

	// Standalone .mmd file parsing + patching.
	mmdSrc := "flowchart LR\n  X --> Y\n"
	mmdIR := ParseMarkdown(mmdSrc, ParseOptions{File: "arch.mmd"})
	if len(mmdIR.Blocks) != 1 || mmdIR.Blocks[0].Direction != "LR" || !mmdIR.Blocks[0].Flippable {
		t.Fatalf("standalone mmd block = %+v", mmdIR.Blocks)
	}
	mmdPatch, err := ApplyMutation(mmdSrc, mmdIR, MutationRequest{
		Kind: WidgetMermaid,
		ID:   "mermaid-0",
		Code: "flowchart TD\n  X --> Y",
	})
	if err != nil {
		t.Fatalf("standalone mmd patch: %v", err)
	}
	if !mmdPatch.Changed || mmdPatch.NextSource != "flowchart TD\n  X --> Y\n" {
		t.Fatalf("unexpected standalone mmd patch: %+v", mmdPatch)
	}

	// External embed + inline fenced mermaid in one Markdown file.
	mdSrc := strings.Join([]string{
		"# Diagrams",
		"",
		"{{mermaid:./ext.mmd}}",
		"",
		"![[missing.mmd]]",
		"",
		"![Architecture](arch.mermaid)",
		"",
		"```mermaid title=\"Pipeline\"",
		"flowchart TD",
		"  A --> B",
		"```",
		"",
	}, "\n")

	mdIR := ParseMarkdown(mdSrc, ParseOptions{
		File: "doc.md",
		ResolveExternalMermaid: func(relPath string) (string, error) {
			switch relPath {
			case "./ext.mmd":
				return "flowchart LR\n  X --> Y\n", nil
			case "arch.mermaid":
				return "erDiagram\n  A ||--o{ B : rel\n", nil
			default:
				return "", errors.New("file not found")
			}
		},
	})
	if mdIR.Stats.MermaidCount != 4 {
		t.Fatalf("MermaidCount = %d, want 4", mdIR.Stats.MermaidCount)
	}

	// External embed mutation leaves NextSource untouched and reports Changed=true.
	extPatch, err := ApplyMutation(mdSrc, mdIR, MutationRequest{
		Kind: WidgetMermaid,
		ID:   "mermaid-0",
		Code: "flowchart TD\n  X --> Y",
	})
	if err != nil {
		t.Fatalf("external embed patch: %v", err)
	}
	if !extPatch.Changed || extPatch.NextSource != mdSrc || extPatch.Widget.SourceFile != "./ext.mmd" {
		t.Fatalf("unexpected external embed patch: %+v", extPatch)
	}

	// Inline fenced mermaid mutation updates only the body between fences.
	inlinePatch, err := ApplyMutation(mdSrc, mdIR, MutationRequest{
		Kind: WidgetMermaid,
		ID:   "mermaid-3",
		Code: "flowchart LR\n  A --> B",
	})
	if err != nil {
		t.Fatalf("inline mermaid patch: %v", err)
	}
	if !inlinePatch.Changed || !strings.Contains(inlinePatch.NextSource, "```mermaid title=\"Pipeline\"\nflowchart LR\n  A --> B\n```") {
		t.Fatalf("unexpected inline mermaid patch:\n%s", inlinePatch.NextSource)
	}
}

func TestTablesEscapedPipesAlertsAndHeadings(t *testing.T) {
	src := strings.Join([]string{
		"# Overview",
		"",
		"## Overview",
		"",
		"---",
		"",
		"> [!WARNING]",
		"> Watch out for **sharp** *edges* and ~~old~~ [docs](https://example.com)<br/>line two.",
		"",
		"| Expr | Align | Status |",
		"| :--- | :---: | ---: |",
		"| `a \\| b` | plain \\| pipe | {{select:s=\"OK\" options=\"OK|ERR\"}} |",
		"",
	}, "\n")

	ir := ParseMarkdown(src, ParseOptions{File: "spec.md"})
	if len(ir.Headings) != 2 || ir.Headings[0].ID != "overview" || ir.Headings[1].ID != "overview-2" {
		t.Fatalf("unexpected headings: %+v", ir.Headings)
	}
	if ir.Stats.TableCount != 1 {
		t.Fatalf("TableCount = %d, want 1", ir.Stats.TableCount)
	}

	var foundAlert, foundTable bool
	for _, b := range ir.Blocks {
		if b.Type == "blockquote" && b.Alert == "warning" {
			foundAlert = true
		}
		if b.Type == "table" {
			foundTable = true
			wantAlign := []string{"left", "center", "right"}
			if !reflect.DeepEqual(b.Alignments, wantAlign) {
				t.Fatalf("Alignments = %v, want %v", b.Alignments, wantAlign)
			}
			if len(b.Rows) != 1 || len(b.Rows[0]) != 3 {
				t.Fatalf("unexpected table shape: %+v", b.Rows)
			}
			firstCell := b.Rows[0][0]
			if len(firstCell) != 1 || firstCell[0].Type != "code" || firstCell[0].Value != "a | b" {
				t.Fatalf("escaped pipe in code span failed: %+v", firstCell)
			}
			secondCell := b.Rows[0][1]
			if plainTextOfInlines(secondCell) != "plain | pipe" {
				t.Fatalf("escaped pipe in text cell failed: %+v", secondCell)
			}
		}
	}
	if !foundAlert || !foundTable {
		t.Fatalf("foundAlert=%v foundTable=%v", foundAlert, foundTable)
	}
}

func TestUTF8ByteOffsetsAndDeterminism(t *testing.T) {
	src := strings.Join([]string{
		"# 日本語タイトル 🚀",
		"",
		"- [ ] Café task with {{input:who=\"太郎\" placeholder=\"名前\"}}",
		"",
		"| 項目 | 状態 |",
		"| --- | --- |",
		"| 🚀 テスト | [ ] 確認 |",
		"",
	}, "\n")

	ir1 := ParseMarkdown(src, ParseOptions{File: "utf8.md"})
	ir2 := ParseMarkdown(src, ParseOptions{File: "utf8.md"})

	if ir1.Revision != ir2.Revision || len(ir1.Revision) != 16 {
		t.Fatalf("revision mismatch or bad length: %q vs %q", ir1.Revision, ir2.Revision)
	}
	if !reflect.DeepEqual(ir1, ir2) {
		t.Fatalf("ParseMarkdown is not deterministic")
	}

	for _, w := range ir1.Widgets {
		if w.Span.Start < 0 || w.Span.End > len(src) || w.Span.Start >= w.Span.End {
			t.Fatalf("invalid Span on %s: %+v", w.ID, w.Span)
		}
		if w.ValueSpan.Start < 0 || w.ValueSpan.End > len(src) || w.ValueSpan.Start >= w.ValueSpan.End {
			t.Fatalf("invalid ValueSpan on %s: %+v", w.ID, w.ValueSpan)
		}
	}

	// Mutate both UTF-8 checkbox and inline directive, ensuring no UTF-8 corruption.
	p1, err := ApplyMutation(src, ir1, MutationRequest{Kind: WidgetCheckbox, ID: "chk-1", Checked: true})
	if err != nil {
		t.Fatalf("mutate UTF-8 table checkbox: %v", err)
	}
	if !strings.Contains(p1.NextSource, "| 🚀 テスト | [x] 確認 |") {
		t.Fatalf("UTF-8 table checkbox corrupted:\n%s", p1.NextSource)
	}

	ir3 := ParseMarkdown(p1.NextSource, ParseOptions{File: "utf8.md"})
	p2, err := ApplyMutation(p1.NextSource, ir3, MutationRequest{Kind: WidgetInput, ID: "input:who", Value: "花子"})
	if err != nil {
		t.Fatalf("mutate UTF-8 input: %v", err)
	}
	if !strings.Contains(p2.NextSource, `- [ ] Café task with {{input:who="花子" placeholder="名前"}}`) {
		t.Fatalf("UTF-8 input corrupted:\n%s", p2.NextSource)
	}

	// Empty document must serialize empty slices (not null) for Blocks, Headings, Widgets.
	emptyIR := ParseMarkdown("", ParseOptions{File: "empty.md"})
	rawJSON, err := json.Marshal(emptyIR)
	if err != nil {
		t.Fatalf("marshal empty IR: %v", err)
	}
	jsonStr := string(rawJSON)
	for _, want := range []string{`"headings":[]`, `"blocks":[]`, `"widgets":[]`} {
		if !strings.Contains(jsonStr, want) {
			t.Fatalf("expected %s in empty IR JSON: %s", want, jsonStr)
		}
	}
}
