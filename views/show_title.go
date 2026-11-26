package views

import (
	"path"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/ui"
)

const (
	// showTitleMaxLines caps the wrapped title of a Show header; the rest is
	// one "…" away in the Title prop and the header's copy fact.
	showTitleMaxLines = 4
	// showTitleChipMax caps one attachment chip's file name.
	showTitleChipMax = 36
)

// HeaderTitle is a record title as the Show header presents it: the prose,
// and the @/path attachments it carried lifted out of it.
type HeaderTitle struct {
	Prose       string
	Attachments []string
}

// SplitHeaderTitle lifts the @path attachments (file tokens that are paths:
// @/abs/shot.png, @~/notes/a.md, @docs/plan.md) out of a title, so a long
// path never crowds the prose. A title that is only attachments takes the
// first file's name as its prose.
func SplitHeaderTitle(title string) HeaderTitle {
	out := HeaderTitle{Prose: strings.TrimSpace(title)}
	for _, file := range data.ParseRichTokens(title).Files {
		if !strings.Contains(file, "/") {
			continue
		}
		token := "@" + file
		if !strings.Contains(out.Prose, token) {
			continue
		}
		out.Prose = strings.Replace(out.Prose, token, "", 1)
		out.Attachments = append(out.Attachments, file)
	}
	if len(out.Attachments) == 0 {
		return out
	}
	out.Prose = strings.Join(strings.Fields(out.Prose), " ")
	if out.Prose == "" {
		out.Prose = path.Base(out.Attachments[0])
	}
	return out
}

// attachmentChipLabel is the short name a chip shows for an attachment path.
func attachmentChipLabel(file string) string {
	name := path.Base(strings.TrimRight(file, "/"))
	if r := []rune(name); len(r) > showTitleChipMax {
		name = string(r[:showTitleChipMax-1]) + "…"
	}
	return "⎘ " + name
}

// wrapTitle breaks s into lines of at most width cells at word boundaries,
// splitting a word longer than a line, and keeps at most maxLines of them
// (the last one ends in "…" when text was dropped).
func wrapTitle(s string, width, maxLines int) []string {
	if width < 8 {
		width = 8
	}
	var lines []string
	cur := ""
	flush := func() {
		if cur != "" {
			lines = append(lines, cur)
			cur = ""
		}
	}
	for _, word := range strings.Fields(s) {
		for len([]rune(word)) > width {
			flush()
			r := []rune(word)
			lines = append(lines, string(r[:width]))
			word = string(r[width:])
		}
		switch {
		case cur == "":
			cur = word
		case len([]rune(cur))+1+len([]rune(word)) <= width:
			cur += " " + word
		default:
			flush()
			cur = word
		}
	}
	flush()
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
		last := []rune(lines[maxLines-1])
		if len(last) >= width {
			last = last[:width-1]
		}
		lines[maxLines-1] = string(last) + "…"
	}
	return lines
}

// showTitleLines are the title rows of the ledger Show header: the prose
// wrapped to the page width with a one-cell gutter on every row, then one
// row of attachment chips. Width 0 (unknown) leaves wrapping to the renderer.
func showTitleLines(title string, opts ShowOptions) []ui.ViewNode {
	ht := HeaderTitle{Prose: title}
	if opts.TitleAttachmentChips {
		ht = SplitHeaderTitle(title)
	}
	prose := []string{ht.Prose}
	if opts.Width > 0 {
		prose = wrapTitle(ht.Prose, opts.Width-3, showTitleMaxLines)
	}
	out := make([]ui.ViewNode, 0, len(prose)+1)
	for i, line := range prose {
		id := "show.header.title"
		if i > 0 {
			id = "show.header.title." + string(rune('0'+i))
		}
		out = append(out, ui.ViewNode{ID: id, Kind: ui.KindText, Props: map[string]any{
			"text": " " + line, "spans": []ui.ToneSpan{{From: 0, Tone: "accent"}},
		}})
	}
	if chips := showAttachmentChipLine(ht.Attachments, opts.Width); chips != nil {
		out = append(out, *chips)
	}
	return out
}

// showAttachmentChipLine is the row of attachment chips under a Show title,
// nil when the title carried none. The full paths ride in the attachments fact.
func showAttachmentChipLine(files []string, width int) *ui.ViewNode {
	if len(files) == 0 {
		return nil
	}
	labels := make([]string, len(files))
	for i, f := range files {
		labels[i] = attachmentChipLabel(f)
	}
	line := strings.Join(labels, "   ")
	if width > 0 {
		if r := []rune(line); len(r) > width-3 {
			line = string(r[:max(1, width-4)]) + "…"
		}
	}
	return &ui.ViewNode{ID: "show.header.attachments", Kind: ui.KindText,
		Props: map[string]any{"text": " " + line, "wrap": false, "spans": []ui.ToneSpan{{From: 0, Tone: "info"}}},
		Facts: map[string]any{"attachments": files, "copy": strings.Join(files, "\n")},
	}
}
