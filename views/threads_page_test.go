package views

import (
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func threadsFixture(now time.Time) []data.AgentThread {
	return []data.AgentThread{
		{
			TaskID: "t1", TaskRef: core.Ref{Kind: "task", ID: "t1"}, Title: "Parser", ProjectID: "p1", ProjectTitle: "Apollo",
			PaneID: "pane1", PaneTitle: "parser pane", Harness: "claude", UnreadCount: 1, LastAt: now.Add(-5 * time.Minute),
			Messages: []data.ThreadMessage{
				{ID: "n1", AuthorKind: "owner", AuthorLabel: "owner", Body: "Please fix the lexer", CreatedAt: now.Add(-3 * time.Hour)},
				{ID: "n2", AuthorKind: "agent", AuthorLabel: "claude", PaneID: "pane1", Body: "Fixed in **lexer.go**", CreatedAt: now.Add(-5 * time.Minute), Unread: true,
					Attachments: []data.Attachment{{Kind: "file", Path: "/tmp/diff.patch", Label: "diff.patch"}}},
			},
		},
		{
			TaskID: "t2", Title: "Docs", LastAt: now.Add(-72 * time.Hour),
			Messages: []data.ThreadMessage{{ID: "n3", AuthorKind: "owner", AuthorLabel: "owner", Body: "Write the guide", CreatedAt: now.Add(-72 * time.Hour)}},
		},
	}
}

func TestThreadsPageViewRailTranscriptAndFacts(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	page := ThreadsPageView(ThreadsPageSpec{
		Threads: threadsFixture(now), SelectedIdx: 0, Now: now, IMessageStyle: true, ProjectTitle: "Apollo", Section: "threads",
	})
	if page.ID != ThreadsPageID || page.Scope != ScopeThreads {
		t.Fatalf("page = %s scope %s", page.ID, page.Scope)
	}
	wantFacts := map[string]any{"count": 2, "hasFilter": false, "hasSelection": true, "threadTaskId": "t1", "threadPaneId": "pane1",
		"unreadCount": 1, "messageCount": 2, "totalUnread": 1, "imessageStyle": true, "section": "threads"}
	for k, want := range wantFacts {
		if page.Facts[k] != want {
			t.Errorf("fact %s = %v, want %v", k, page.Facts[k], want)
		}
	}
	if split := page.Find("threads.split"); split == nil || split.Kind != ui.KindSplit || len(split.Children) != 2 {
		t.Fatalf("iMessage style is not a two-pane split: %+v", split)
	}

	rail := page.Find(ThreadsListID)
	if rail == nil || rail.Kind != ui.KindTable || !rail.Focusable {
		t.Fatalf("rail = %+v", rail)
	}
	if rail.Filter == nil || rail.Filter.ID != ThreadsListID {
		t.Fatalf("the rail must declare ViewNode.Filter so / works through trait.filter: %+v", rail.Filter)
	}
	rows := rail.Props["rows"].([]any)
	if len(rows) != 2 || rail.Props["selected"] != 0 {
		t.Fatalf("rows = %v selected %v", rows, rail.Props["selected"])
	}
	first, second := rows[0].(ui.TableRowSpec), rows[1].(ui.TableRowSpec)
	if got := first.Cells; got[0] != "Parser" || got[1] != "parser pane (claude)" || got[2] != "● 1" || got[3] != "2" || got[4] != "5m ago" {
		t.Errorf("unread thread row = %v", got)
	}
	if got := first.CellTones; got[0] != "unread" || got[2] != "unread" || got[4] != "state.working" {
		t.Errorf("unread thread tones = %v", got)
	}
	if got := second.Cells; got[1] != "no agent yet" || got[2] != "" || got[4] != "3d ago" {
		t.Errorf("read thread row = %v", got)
	}
	if got := second.CellTones; got[0] != "" || got[1] != "muted" || got[4] != "muted" {
		t.Errorf("read thread tones = %v", got)
	}

	// The transcript tells owner and agent apart and marks where unread starts.
	transcript := page.Find(ThreadsTranscriptID)
	if transcript == nil || transcript.Title != "Parser" {
		t.Fatalf("transcript = %+v", transcript)
	}
	owner, agent := page.Find("threads.msg.0.head"), page.Find("threads.msg.1.head")
	if !strings.Contains(nodeText(owner), " You ") || toneAt(owner, 1) != "badge.accent" {
		t.Errorf("owner header = %q tone %q", nodeText(owner), toneAt(owner, 1))
	}
	if txt := nodeText(agent); !strings.Contains(txt, "claude · parser pane") || !strings.Contains(txt, "5m ago") || !strings.Contains(txt, "● new") || toneAt(agent, 1) != "state.working" {
		t.Errorf("agent header = %q tone %q", txt, toneAt(agent, 1))
	}
	body := page.Find("threads.msg.1.body")
	if body == nil || body.Kind != ui.KindMarkdown || !strings.Contains(body.Props["markdown"].(string), "**lexer.go**") || !strings.Contains(body.Props["markdown"].(string), "diff.patch") {
		t.Errorf("agent body = %+v", body)
	}
	list := page.Find(ThreadsTranscriptID + ".list")
	var order []string
	for _, c := range list.Children {
		order = append(order, c.ID)
	}
	if got := strings.Join(order, " "); !strings.Contains(got, "threads.msg.0.gap "+ThreadsUnreadMarkID+" threads.msg.1.head") {
		t.Errorf("the unread divider is not right before the first unread message: %s", got)
	}
	if hint := nodeText(page.Find(ThreadsHintID)); !strings.Contains(hint, "r reply") || !strings.Contains(hint, "n new thread") || !strings.Contains(hint, "d dispatch to New or Existing Agent") {
		t.Errorf("hint = %q", hint)
	}
}

func TestThreadsPageViewCompactEmptyAndFiltered(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	compact := ThreadsPageView(ThreadsPageSpec{Threads: threadsFixture(now), SelectedIdx: 1, Now: now})
	if compact.Find("threads.split") != nil {
		t.Fatal("compact style still splits the page")
	}
	if compact.Facts["imessageStyle"] != false || compact.Facts["threadTaskId"] != "t2" || compact.Facts["threadPaneId"] != "" || compact.Find(ThreadsUnreadMarkID) != nil {
		t.Fatalf("compact facts = %v", compact.Facts)
	}
	head, body := compact.Find("threads.msg.0.head"), compact.Find("threads.msg.0.body")
	if nodeText(head) != " [You] 3d ago" || body.Kind != ui.KindText || !strings.Contains(nodeText(body), "Write the guide") {
		t.Fatalf("compact message = %q / %+v", nodeText(head), body)
	}

	empty := ThreadsPageView(ThreadsPageSpec{SelectedIdx: -1, Now: now, IMessageStyle: true})
	if empty.Facts["hasSelection"] != false || empty.Facts["count"] != 0 {
		t.Fatalf("empty facts = %v", empty.Facts)
	}
	if rail := empty.Find(ThreadsListID); rail.Kind != ui.KindText || !strings.Contains(nodeText(rail), "n starts a new thread") {
		t.Fatalf("empty rail = %+v", rail)
	}
	filtered := ThreadsPageView(ThreadsPageSpec{SelectedIdx: -1, Now: now, FilterQuery: "zzz", IMessageStyle: true})
	if filtered.Facts["hasFilter"] != true || !strings.Contains(nodeText(filtered.Find(ThreadsListID)), "No threads match") {
		t.Fatalf("filtered page = %v", filtered.Facts)
	}
}

func TestThreadRecencyTones(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		ago         time.Duration
		label, tone string
	}{
		{10 * time.Second, "just now", "state.working"},
		{90 * time.Minute, "1h ago", ""},
		{49 * time.Hour, "2d ago", "muted"},
		{-time.Hour, "just now", "state.working"},
	} {
		if label, tone := ThreadRecency(now.Add(-c.ago), now); label != c.label || tone != c.tone {
			t.Errorf("%v ago = %q/%q, want %q/%q", c.ago, label, tone, c.label, c.tone)
		}
	}
	if label, _ := ThreadRecency(time.Time{}, now); label != "" {
		t.Errorf("zero time = %q", label)
	}
}

func TestThreadsPageViewIndexShowFeedAndSnippets(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	threads := threadsFixture(now)
	threads = append(threads, data.AgentThread{
		TaskID: "t3", Title: "Long Thread", LastAt: now.Add(-1 * time.Minute),
		Messages: []data.ThreadMessage{
			{ID: "m1", AuthorKind: "owner", Body: "# Heading\n> First topic line\n- bullet item", CreatedAt: now.Add(-10 * time.Minute)},
			{ID: "m2", AuthorKind: "agent", AuthorLabel: "claude", Body: "Middle ignored detail", CreatedAt: now.Add(-8 * time.Minute)},
			{ID: "m3", AuthorKind: "owner", Body: "Second to last check", CreatedAt: now.Add(-4 * time.Minute)},
			{ID: "m4", AuthorKind: "agent", AuthorLabel: "claude", Body: "Latest reply here", CreatedAt: now.Add(-1 * time.Minute)},
		},
	})
	page := ThreadsPageView(ThreadsPageSpec{
		Threads: threads, SelectedIdx: 0, Now: now, IMessageStyle: true, IndexShow: true, ProjectTitle: "Apollo", Section: "threads",
	})
	if page.Find("threads.split") != nil {
		t.Fatal("IndexShow index view must not render legacy threads.split")
	}
	if page.Facts["threadsIndexShow"] != true || page.Facts["threadOpen"] != false || page.Facts["threadComposing"] != false {
		t.Fatalf("IndexShow facts = %v", page.Facts)
	}
	scroll := page.Find(ThreadsIndexScrollID)
	if scroll == nil || scroll.Kind != ui.KindScroll || scroll.Filter == nil || scroll.Filter.ID != ThreadsListID {
		t.Fatalf("index scroll = %+v", scroll)
	}
	card := page.Find(ThreadsItemPrefix + "t1")
	if card == nil || card.Kind != ui.KindCard || !card.Focusable || !card.Unread || card.Ref == nil || card.Ref.ID != "t1" {
		t.Fatalf("first index card = %+v", card)
	}
	summary, _ := card.Props["summary"].(string)
	if !strings.Contains(summary, "You: Please fix the lexer") || !strings.Contains(summary, "claude · parser pane: Fixed in **lexer.go**") {
		t.Errorf("card summary = %q", summary)
	}
	if card.Facts["threadItem"] != true || card.Facts["threadTaskId"] != "t1" || card.Facts["hasSelection"] != true {
		t.Errorf("card facts = %v", card.Facts)
	}
	longCard := page.Find(ThreadsItemPrefix + "t3")
	longSummary, _ := longCard.Props["summary"].(string)
	for _, want := range []string{"You: Heading First topic line bullet item", "· … ·", "You: Second to last check", "claude: Latest reply here"} {
		if !strings.Contains(longSummary, want) {
			t.Errorf("4+ msg snippet missing %q: %q", want, longSummary)
		}
	}
	if strings.Contains(longSummary, "Middle ignored detail") {
		t.Errorf("4+ msg snippet should omit middle messages: %q", longSummary)
	}
	if hint := nodeText(page.Find(ThreadsHintID)); !strings.Contains(hint, "enter open chat") || !strings.Contains(hint, "p switch") {
		t.Errorf("index hint = %q", hint)
	}

	empty := ThreadsPageView(ThreadsPageSpec{SelectedIdx: -1, Now: now, IndexShow: true})
	if n := empty.Find(ThreadsListID); n == nil || n.Kind != ui.KindText || !n.Focusable || n.Filter == nil {
		t.Fatalf("empty IndexShow node = %+v", n)
	}
}

func TestThreadsPageViewFullWidthShowAndComposer(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	spec := ThreadsPageSpec{
		Threads:       threadsFixture(now),
		SelectedIdx:   1,
		Now:           now,
		IMessageStyle: true,
		IndexShow:     true,
		OpenTaskID:    "t1",
		Composer: ThreadComposerSpec{
			Enabled:     true,
			Editing:     true,
			Value:       "Draft reply",
			Cursor:      11,
			Wrap:        true,
			TargetLabel: "parser pane (claude)",
		},
	}
	page := ThreadsPageView(spec)
	if page.Find("threads.split") != nil {
		t.Fatal("Show view must not render threads.split")
	}
	if page.Facts["threadsIndexShow"] != true || page.Facts["threadOpen"] != true || page.Facts["threadComposing"] != true ||
		page.Facts["threadTaskId"] != "t1" || page.Facts["threadPaneId"] != "pane1" || page.Facts["hasSelection"] != true {
		t.Fatalf("Show facts = %v", page.Facts)
	}
	if hdr := nodeText(page.Find("threads.show.header")); !strings.Contains(hdr, "esc/h Threads") || !strings.Contains(hdr, "Parser") {
		t.Errorf("show header = %q", hdr)
	}
	scroll := page.Find(ThreadsTranscriptID + ".scroll")
	if scroll == nil || scroll.Props["followTail"] != nil {
		t.Fatalf("show scroll should not force followTail: %+v", scroll)
	}
	m0, m1 := page.Find(ThreadsMsgPrefix+"0"), page.Find(ThreadsMsgPrefix+"1")
	if m0 == nil || m0.Kind != ui.KindCard || !m0.Focusable || m0.Facts["threadMessage"] != true || m0.Facts["messageIndex"] != 0 {
		t.Fatalf("msg 0 card = %+v", m0)
	}
	if m1 == nil || !m1.Unread || m1.Children[0].Kind != ui.KindMarkdown {
		t.Fatalf("msg 1 card = %+v", m1)
	}
	if page.Find(ThreadsUnreadMarkID) == nil {
		t.Fatal("missing unread divider in Show view")
	}
	comp := page.Find(ThreadsComposerInputID)
	if comp == nil || comp.Kind != ui.KindInput || !comp.Focusable || comp.Box.FixedH != 6 {
		t.Fatalf("composer node = %+v", comp)
	}
	if comp.Title != "Reply → parser pane (claude)" || comp.Props["value"] != "Draft reply" || comp.Props["multiline"] != true || comp.Facts["threadComposing"] != true {
		t.Fatalf("composer props/facts = %+v", comp)
	}
	if len(comp.Badges) == 0 || comp.Badges[0].Text != "TYPING" {
		t.Errorf("editing composer badges = %+v", comp.Badges)
	}

	// Resting state and plain-text body when IMessageStyle is false.
	spec.IMessageStyle = false
	spec.Composer.Editing = false
	plain := ThreadsPageView(spec)
	if m := plain.Find(ThreadsMsgPrefix + "0"); m == nil || m.Children[0].Kind != ui.KindText {
		t.Fatalf("non-imessage show body should be KindText: %+v", m)
	}
	if c := plain.Find(ThreadsComposerInputID); c == nil || c.Facts["threadComposing"] != false || !strings.Contains(c.Badges[0].Text, "enter / i") {
		t.Fatalf("resting composer = %+v", c)
	}
}
