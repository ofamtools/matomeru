package contextitems

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// OSC 8 hyperlinks (ESC ] 8 ; params ; URL ST, text, ESC ] 8 ; ; ST): the
// program names the target itself, so the target is the item and no
// pattern or wrap rule is needed. The emulator mtm replays a pane into
// (an ochazuke terminal engine: tuios.dev/vt or charmbracelet/x/vt) keeps each cell's link and its Render writes it
// back as OSC 8, so a live pane's frame rows carry them. As in tuios
// (internal/app/link_hover.go, markedLinkAt), one link is the run of
// cells with the same target and params, followed across rows; a bare URL
// in the link's text is its label, not its target (link_emit.go).

var osc8 = regexp.MustCompile("\x1b\\]8;([^;\x07\x1b]*);([^\x07\x1b]*)(?:\x07|\x1b\\\\)")

// stripLinks is ansi.Strip(row) and, per byte of it, the hyperlink the
// byte is drawn in ("" for none): the target and params, as one string.
func stripLinks(row string) (string, []string) {
	if !strings.Contains(row, "\x1b]8;") {
		return ansi.Strip(row), nil
	}
	var b strings.Builder
	var links []string
	link := ""
	add := func(seg string) {
		text := ansi.Strip(seg)
		b.WriteString(text)
		for range len(text) {
			links = append(links, link)
		}
	}
	last := 0
	for _, m := range osc8.FindAllStringSubmatchIndex(row, -1) {
		add(row[last:m[0]])
		last = m[1]
		link = ""
		if target := row[m[4]:m[5]]; target != "" {
			link = target + "\x00" + row[m[2]:m[3]]
		}
	}
	add(row[last:])
	return b.String(), links
}

// hyperlink is one link's run in the joined text: the target, the byte
// range it spans and the bytes drawn in it.
type hyperlink struct {
	target     string
	start, end int
	at         []Cell
}

// hyperlinks groups the bytes of text drawn in a hyperlink (linkAt, by
// cell) into runs: consecutive bytes with the same link, a run going on
// across a line break and the spaces around it, so a link wrapped onto the
// next row is one.
func hyperlinks(text string, at []Cell, linkAt map[[2]int]string) []hyperlink {
	if len(linkAt) == 0 {
		return nil
	}
	var out []hyperlink
	curLink := ""
	for i := range len(text) {
		l := ""
		if c := at[i]; c.Row >= 0 {
			l = linkAt[[2]int{c.Row, c.Col}]
		}
		if l == "" {
			continue
		}
		if n := len(out); n > 0 && l == curLink {
			if cur := &out[n-1]; cur.end == i || strings.Contains(text[cur.end:i], "\n") && strings.TrimSpace(text[cur.end:i]) == "" {
				cur.end = i + 1
				cur.at = append(cur.at, at[i])
				continue
			}
		}
		target, _, _ := strings.Cut(l, "\x00")
		out = append(out, hyperlink{target: target, start: i, end: i + 1, at: []Cell{at[i]}})
		curLink = l
	}
	return out
}

// hyperlinkFound is a hyperlink as find would report its target: a URL
// as link or pr, a file:// URL as the path it names, anything else
// (mailto:, a custom scheme) as a link. raw is what copy and type use.
func hyperlinkFound(target string, stat StatFunc) (f found, raw string) {
	u, err := url.Parse(target)
	if err == nil && u.Scheme == "file" && u.Path != "" {
		info, err := stat(u.Path)
		exists := err == nil
		kind := "file"
		if exists && info.IsDir() {
			kind = "dir"
		}
		return found{kind: kind, value: u.Path, exists: &exists}, u.Path
	}
	kind := "link"
	if err == nil && u.Host != "" {
		kind = linkKind(u)
	}
	return found{kind: kind, value: target}, target
}
