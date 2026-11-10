package main

import (
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
)

// Page details: the text beside or below a page's lists about the selected
// row, an ochazuke Detail each (AddDetail), so J and K scroll it while the
// focus stays in the list (counts work: 10J) and the footer reads J/K
// detail only while it overflows. A new item starts at the top; a tail
// detail (a screen, a transcript: the newest at the bottom) starts at its
// end instead and follows the end while it is there, so new lines show
// until K scrolls back.

// The page details.
const (
	panesDetailID   = "panes/detail"
	workDetailID    = "work/detail"
	notesDetailID   = "notes/detail"
	reviewsDetailID = "reviews/detail"
	dirsDetailID    = "directories/detail"
	contextDetailID = "context/detail"
)

// pageDetails are the pages' details.
type pageDetails struct {
	panes, work, notes, reviews, ext, context *pageDetail
	// The linked details (detail_links.go).
	dirs, saved, vim *linkedDetail
}

// detailEnd scrolls a detail to its end; Scroll clamps it.
const detailEnd = 1 << 30

// pageDetail is one page's detail and its tail state.
type pageDetail struct {
	d    *ochazuke.Detail
	r    *ochazuke.Runtime
	show *ochazuke.ShowForm
	// rel is the stack of related sections about what the detail shows
	// (notes about it, ...), nil when the detail has none.
	rel *ochazuke.Related
	// tail reports whether the shown text is newest-at-the-bottom: it
	// starts at the end and follows it.
	tail func() bool
	// item is the item of the last frame; last the end offset it measured.
	item string
	last int
}

// addDetail adds a detail under page; item names what it shows now, tail
// (optional) says whether that is newest-at-the-bottom text.
func (a *App) addDetail(id, page string, item func() string, tail func() bool) (*pageDetail, error) {
	d, err := a.r.AddDetail(id, item, ochazuke.Parent(page))
	if err != nil {
		return nil, err
	}
	return &pageDetail{d: d, r: a.r, tail: tail}, nil
}

// The related stack of a detail: the sections of the things that name what
// the detail shows (a note's about, a task's project), the referrers of its
// noun, drawn under its text. ochazuke owns the stack, its keys and its
// settings detail.related (off: nothing read or drawn) and
// detail.related-hidden; mtm adds these two, read by every detail's stack.
const (
	// settingRelatedHidden lists the section ids (NOUN.PROP, or a PREFIX*)
	// kept out of mtm's detail stacks. The default keeps to the notes: the
	// things that only share a project or task with the row are rows of
	// their own pages.
	settingRelatedHidden = "notes.related-hidden"
	// settingRelatedEmpty is what a section with nothing in it shows:
	// header (the heading and its count), hide (nothing) or open.
	settingRelatedEmpty = "notes.related-empty"
)

var (
	relatedHiddenSetting = ochazuke.Setting{Name: settingRelatedHidden, Type: ochazuke.SettingList,
		Default:     []string{"note.task", "pane.*", "task.*", "session.*", "review.*", "action-item.*", "schedule.*", "saved.*", "vim.*", "context-item.*"},
		Description: "Related sections kept out of mtm's details, as NOUN.PROP ids (a trailing * is a prefix). The default keeps notes and history, hides note.task already shown by note.about, and hides other record and framework referrers; list fewer to show more. detail.related turns the stacks off"}
	relatedEmptySetting = ochazuke.Setting{Name: settingRelatedEmpty, Type: ochazuke.SettingString, Default: ochazuke.RelatedEmptyHide,
		Enum:        []string{ochazuke.RelatedEmptyHeader, ochazuke.RelatedEmptyHide, ochazuke.RelatedEmptyOpen},
		Description: "What a related section with nothing in it shows in a detail: hide (nothing), header (its heading and count) or open (the heading and an empty row)"}
)

func (a *App) detailHiddenSetting() ochazuke.Setting {
	setting := relatedHiddenSetting
	hidden := append([]string(nil), setting.Default.([]string)...)
	for _, ref := range a.r.Referrers("") {
		if a.r.Noun(ref.Noun).Builtin {
			hidden = append(hidden, ref.ID())
		}
	}
	setting.Default = hidden
	return setting
}

// addDetailRelated gives the detail p a related stack about the thing
// target names now (noun and key; "" for none). The stack is a child of
// the detail, so its sections are focus stops while the detail draws them.
func (a *App) addDetailRelated(p *pageDetail, target func() (noun, key string)) error {
	rel, err := a.r.AddRelated(p.d.ID()+"/related", detailRelatedSpec(target), ochazuke.Parent(p.d.ID()))
	p.rel = rel
	return err
}

func detailRelatedSpec(target func() (noun, key string)) ochazuke.RelatedSpec {
	return ochazuke.RelatedSpec{Target: target, Enabled: ochazuke.RelatedDetailSetting,
		Hidden: settingRelatedHidden, Empty: settingRelatedEmpty}
}

// The show form owns the single related stack; the app supplies its visibility
// settings and registered custom views instead of drawing a second stack.
func (a *App) addDetailShow(id, page string, item func() string, tail func() bool, target func() (noun, key string), data func() string) (*pageDetail, error) {
	p, err := a.addDetail(id, page, item, tail)
	if err != nil {
		return nil, err
	}
	// Once focus enters the record or its sections it no longer names a list
	// row. Keep following the same selection until focus returns to the page.
	var noun, key string
	follow := func() (string, string) {
		if !p.recordFocused() {
			noun, key = target()
		}
		return noun, key
	}
	related := detailRelatedSpec(follow)
	related.History = true
	p.show, err = a.r.AddShowForm(id+"/show", ochazuke.ShowFormSpec{Target: follow, Data: data, Related: &related}, ochazuke.Parent(page))
	if err != nil {
		return nil, err
	}
	p.rel = p.show.Related()
	return p, nil
}

func (p *pageDetail) recordFocused() bool {
	focus := p.r.BaseFocus()
	return p.show != nil && p.show.Holds(focus) ||
		p.rel != nil && slices.Contains(p.r.InstancePath(focus), p.rel.ID())
}

func (p *pageDetail) showingForm() bool {
	return p.show != nil && p.show.Form() != nil && p.r.Settings().Bool(ochazuke.DetailShowFormSetting)
}

func (p *pageDetail) recordBlock(text string, width, height int) ochazuke.Block {
	if p.showingForm() {
		p.hide()
		return p.show.Block(ansi.Wrap(text, width, ""), width, height)
	}
	return ochazuke.Text(p.lines(p.withRelated(text, width), width, width, height))
}

// Keep custom components intact, including their cursor and focus placement.
func (p *pageDetail) under(render func(int, int) ochazuke.Block, width, height int) ochazuke.Block {
	if !p.showingForm() || !p.recordFocused() {
		return render(width, height)
	}
	top := max(1, height*2/5)
	head := render(width, top)
	tail := p.show.Block("", width, max(1, height-top-1))
	return stackBlocks(head, top, tail)
}

// addDetailAbout is addDetail with the related stack about target.
func (a *App) addDetailAbout(id, page string, item func() string, tail func() bool, target func() (noun, key string)) (*pageDetail, error) {
	p, err := a.addDetail(id, page, item, tail)
	if err != nil {
		return nil, err
	}
	return p, a.addDetailRelated(p, target)
}

// relatedText is the stack drawn width cells wide ("" when the detail has
// none, it is off or nothing is shown).
func (p *pageDetail) relatedText(width int) string {
	if p.rel == nil || width < 1 {
		return ""
	}
	return p.rel.Text(width)
}

// withRelated is text with the stack under it, a blank line between.
func (p *pageDetail) withRelated(text string, width int) string {
	stack := p.relatedText(width)
	switch {
	case stack == "":
		return text
	case text == "":
		return stack
	}
	return text + "\n\n" + stack
}

// window is the detail's lines of text in width by height (wrapped to
// width when width > 0) and the position marker ("" when it fits).
func (p *pageDetail) window(text string, width, height int) (string, string) {
	tail := p.tail != nil && p.tail()
	// At the end since the last frame (no K in between): stay there.
	follow := tail && p.d.Offset() >= p.last
	body, marker := p.d.Window(text, width, height)
	if tail && (p.d.Item() != p.item || follow) {
		p.d.Scroll(detailEnd)
		body, marker = p.d.Window(text, width, height)
	}
	p.item = p.d.Item()
	off := p.d.Offset()
	p.last = p.d.Scroll(detailEnd)
	p.d.Scroll(off - p.last)
	return body, marker
}

// lines is fit padded to exactly height lines.
func (p *pageDetail) lines(text string, wrap, width, height int) string {
	return fillLines(p.fit(text, wrap, width, height), height, width)
}

// fit is the window of text (wrapped to wrap when wrap > 0) with each
// line cut to width, the position marker taking the last line when the
// text overflows; at most height lines.
func (p *pageDetail) fit(text string, wrap, width, height int) string {
	if height <= 1 {
		body, _ := p.window(text, wrap, height)
		return cutLines(body, width)
	}
	// Windowed one line short first, so a scroll to the end is not
	// clamped a line early; text that fits the whole height takes it.
	body, marker := p.window(text, wrap, height-1)
	if marker == "" || p.last+height-1 <= height {
		body, _ = p.window(text, wrap, height)
		return cutLines(body, width)
	}
	return fillLines(body, height-1, width) + "\n" + ansi.Truncate(marker, width, "")
}

// cutLines cuts each line of text to width.
func cutLines(text string, width int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "")
	}
	return strings.Join(lines, "\n")
}

// hide records that the page draws no detail this frame, so J and K do
// not answer for text that is not on screen; a tail detail shown again
// opens at its end.
func (p *pageDetail) hide() {
	p.d.Window("", 0, 0)
	p.last = 0
}

// scroll moves the detail by lines and returns the new offset.
func (p *pageDetail) scroll(lines int) int { return p.d.Scroll(lines) }

// fillLines is text as exactly height lines, each cut to width.
func fillLines(text string, height, width int) string {
	lines := strings.Split(text, "\n")
	if text == "" {
		lines = nil
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	lines = lines[:max(0, height)]
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "")
	}
	return strings.Join(lines, "\n")
}

// trimBlankTail drops a screen capture's blank last lines, so a tail
// detail ends on the last line holding text, not the empty rows below a
// shell's prompt.
func trimBlankTail(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
