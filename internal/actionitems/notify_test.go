package actionitems

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestActionItemNotifySelectionAndSummary(t *testing.T) {
	t.Parallel()
	for _, count := range []int{5, 6} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			t.Parallel()
			s, _, clock := openStore(t)
			opts := NotifyOptions{Enabled: true, Sources: []string{"slack", "gh"}, Since: *clock}
			old, _, err := s.Add(Spec{Source: "slack", Title: "before baseline"})
			if err != nil {
				t.Fatal(err)
			}
			*clock++
			for i := range count {
				if _, _, err := s.Add(Spec{Source: "slack", Title: fmt.Sprint("ping ", i), Why: "mentioned"}); err != nil {
					t.Fatal(err)
				}
			}
			ignored, _, err := s.Add(Spec{Source: "mtm", Title: "pane signal"})
			if err != nil {
				t.Fatal(err)
			}
			var sent []string
			n, err := s.NotifyDue(*clock, opts, func(title, body string) (bool, error) { sent = append(sent, title+": "+body); return true, nil })
			if err != nil || n != count {
				t.Fatalf("sent %d: %v", n, err)
			}
			if count == 6 {
				if !reflect.DeepEqual(sent, []string{"mtm inbox: 6 new action items"}) {
					t.Fatalf("burst = %#v", sent)
				}
			} else {
				want := []string{"mtm: slack · ping 0: mentioned", "mtm: slack · ping 1: mentioned", "mtm: slack · ping 2: mentioned", "mtm: slack · ping 3: mentioned", "mtm: slack · ping 4: mentioned"}
				if !reflect.DeepEqual(sent, want) {
					t.Fatalf("at threshold = %#v", sent)
				}
			}
			for _, id := range []string{old.ID, ignored.ID} {
				it, err := s.Get(id)
				if err != nil || it.NotifiedAt != 0 {
					t.Fatalf("excluded item %+v: %v", it, err)
				}
			}
			n, err = s.NotifyDue(*clock, opts, func(string, string) (bool, error) { t.Error("repeat sent"); return true, nil })
			if err != nil || n != 0 {
				t.Fatalf("repeat %d %v", n, err)
			}
		})
	}
}

func TestActionItemNotifyLeaseFailureAndConcurrentArchive(t *testing.T) {
	t.Parallel()
	s, _, clock := openStore(t)
	it, _, err := s.Add(Spec{Source: "manual", Title: "forced", Notify: true})
	if err != nil {
		t.Fatal(err)
	}
	opts := NotifyOptions{Enabled: true, Since: *clock - 1}
	claims, err := s.claimNotifications(*clock, opts)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims %+v: %v", claims, err)
	}
	// An unexpired crashed worker suppresses sends, but expiry permits a retry.
	n, err := s.NotifyDue(*clock, opts, func(string, string) (bool, error) { t.Error("live lease sent"); return true, nil })
	if err != nil || n != 0 {
		t.Fatalf("live lease %d %v", n, err)
	}
	*clock += NotifyClaimTTL.Milliseconds()
	n, err = s.NotifyDue(*clock, opts, func(string, string) (bool, error) { return false, errors.New("offline") })
	if err == nil || n != 0 {
		t.Fatalf("failure %d %v", n, err)
	}
	failed, err := s.Get(it.ID)
	if err != nil || failed.NotifyClaim != "" || failed.NotifyClaimUntil != 0 || failed.NotifiedAt != 0 {
		t.Fatalf("failure retained receipt or lease %+v %v", failed, err)
	}
	n, err = s.NotifyDue(*clock, opts, func(string, string) (bool, error) {
		// This write would deadlock if sending held a database transaction.
		if _, err := s.Archive(it.ID); err != nil {
			t.Fatal(err)
		}
		return true, errors.New("extra sender offline")
	})
	if err == nil || n != 1 {
		t.Fatalf("partial %d %v", n, err)
	}
	done, err := s.Get(it.ID)
	if err != nil || done.State(*clock) != StateArchived || done.NotifiedAt != *clock || done.NotifyClaim != "" {
		t.Fatalf("finish overwrote concurrent archive %+v %v", done, err)
	}
}

func TestActionItemNotifyOffAndMarks(t *testing.T) {
	t.Parallel()
	s, _, clock := openStore(t)
	for _, mark := range []string{"read", "snoozed", "archived", "forced"} {
		it, _, err := s.Add(Spec{Source: "manual", Title: mark, Notify: true})
		if err != nil {
			t.Fatal(err)
		}
		switch mark {
		case "read":
			_, err = s.SetRead(it.ID, true)
		case "snoozed":
			_, err = s.Snooze(it.ID, *clock+1000)
		case "archived":
			_, err = s.Archive(it.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	opts := NotifyOptions{Since: *clock - 1}
	n, err := s.NotifyDue(*clock, opts, func(string, string) (bool, error) { t.Error("opt-out sent even a forced item"); return true, nil })
	if err != nil || n != 0 {
		t.Fatalf("opt-out %d %v", n, err)
	}
	opts.Enabled = true
	var sent []string
	n, err = s.NotifyDue(*clock, opts, func(title, _ string) (bool, error) { sent = append(sent, title); return true, nil })
	if err != nil || n != 1 || !reflect.DeepEqual(sent, []string{"mtm: manual · forced"}) {
		t.Fatalf("marks %d %#v %v", n, sent, err)
	}
}

func TestDismissedNotificationsRemainAcknowledged(t *testing.T) {
	t.Parallel()
	s, _, clock := openStore(t)
	opts := NotifyOptions{Enabled: true, Sources: []string{"slack"}, Since: *clock}
	*clock++
	old, _, err := s.Add(Spec{Source: "slack", Title: "old unread", Key: "old"})
	if err != nil {
		t.Fatal(err)
	}
	dismissed := *clock
	if err := s.DismissNotices([]string{old.ID, "deleted-item", old.ID}); err != nil {
		t.Fatal(err)
	}
	*clock++
	if _, err := s.SetRead(old.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRead(old.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Add(Spec{Source: "slack", Title: "refreshed unread", Key: "old", Reopen: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.DismissNotices([]string{old.ID}); err != nil {
		t.Fatal(err)
	}
	if it, err := s.Get(old.ID); err != nil || it.NoticeDismissedAt != dismissed || it.Read() || it.ArchivedAt != 0 || it.SnoozedUntil != 0 {
		t.Fatalf("read/refresh/repeated clear changed acknowledgement or inbox state: %+v %v", it, err)
	}
	var sent []string
	deliver := func(title, _ string) (bool, error) { sent = append(sent, title); return true, nil }
	if n, err := s.NotifyDue(*clock, opts, deliver); err != nil || n != 0 || len(sent) != 0 {
		t.Fatalf("dismissed item reached desktop delivery: %d %v %v", n, sent, err)
	}
	if _, _, err := s.Add(Spec{Source: "slack", Title: "fresh item", Key: "fresh"}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.NotifyDue(*clock, opts, deliver); err != nil || n != 1 || !reflect.DeepEqual(sent, []string{"mtm: slack · fresh item"}) {
		t.Fatalf("fresh item did not notify on its own: %d %v %v", n, sent, err)
	}
	// Explicitly reopening an archived item starts a new notification cycle.
	if _, err := s.Archive(old.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Add(Spec{Source: "slack", Key: "old", Reopen: true}); err != nil {
		t.Fatal(err)
	}
	if it, err := s.Get(old.ID); err != nil || it.NoticeDismissedAt != 0 {
		t.Fatalf("reopen retained the old notification cycle: %+v %v", it, err)
	}
}
