package main

import (
	"errors"
	"testing"

	"ofam.dev/ochazuke"
)

// A write's props read over the record keep what thingInt and thingString
// always did, and the differences from the hand-decoding are pinned here so
// they stay deliberate.
func TestThingOverSnoozeAndDifferences(t *testing.T) {
	t.Parallel()
	snooze := func(v any) int64 {
		t.Helper()
		p, err := thingOver(taskPatch{SnoozedUntil: 7}, ochazuke.Thing{"snoozed-until": v})
		if err != nil {
			t.Fatalf("snoozed-until %#v: %v", v, err)
		}
		return int64(p.SnoozedUntil)
	}
	for _, c := range []struct {
		in   any
		want int64
	}{
		{int64(1700000000000), 1700000000000}, {1700, 1700}, {float64(1700.4), 1700}, {float64(1700.6), 1701},
		{" 1700000000000 ", 1700000000000}, {"soon", 0}, {"", 0}, {nil, 0}, {true, 0},
	} {
		if got := snooze(c.in); got != c.want {
			t.Errorf("snoozed-until %#v = %d, want %d (as thingInt reads it)", c.in, got, c.want)
		}
	}
	act, err := thingOver(actionItemPatch{}, ochazuke.Thing{"snoozed-until": "42"})
	if err != nil || act.SnoozedUntil == nil || int64(*act.SnoozedUntil) != 42 {
		t.Errorf("action item string snooze: %v %v", act.SnoozedUntil, err)
	}
	if act, _ := thingOver(actionItemPatch{}, ochazuke.Thing{"title": "x"}); act.SnoozedUntil != nil {
		t.Error("an unnamed snooze must stay nil")
	}

	// A null bool is false and a null snooze 0, as the hand-decoding read
	// them, for every noun (a task's stored true, an action item's stored
	// snooze).
	nulls, err := thingOver(taskPatch{Easy: true, Private: true, Archived: true, SnoozedUntil: 7}, ochazuke.Thing{"easy": nil, "private": nil, "archived": nil, "snoozed-until": nil})
	if err != nil || nulls.Easy || nulls.Private || nulls.Archived || nulls.SnoozedUntil != 0 {
		t.Errorf("task nulls = %+v, %v; want false and 0", nulls, err)
	}
	seven, yes := wholeNumber(7), true
	aiNull, err := thingOver(actionItemPatch{SnoozedUntil: &seven, Read: &yes, Notify: true}, ochazuke.Thing{"snoozed-until": nil, "read": nil, "notify": nil})
	if err != nil || aiNull.SnoozedUntil == nil || *aiNull.SnoozedUntil != 0 || aiNull.Read == nil || *aiNull.Read || aiNull.Notify {
		t.Errorf("action item nulls = %+v, %v; want a named 0 and false", aiNull, err)
	}
	if kept, _ := thingOver(actionItemPatch{SnoozedUntil: &seven}, ochazuke.Thing{"title": "x"}); kept.SnoozedUntil == nil || *kept.SnoozedUntil != 7 {
		t.Error("a write that does not name the snooze keeps it")
	}

	// Deliberate difference: a null string keeps the stored value (it was "").
	p, err := thingOver(taskPatch{Title: "kept"}, ochazuke.Thing{"title": nil})
	if err != nil || p.Title != "kept" {
		t.Errorf("null title = %q, %v; want the stored one", p.Title, err)
	}
	// Deliberate difference: a wrong-typed bool or int is an ArgumentError (it was ignored).
	for _, bad := range []ochazuke.Thing{{"easy": "yes"}, {"archived": 3}, {"private": []any{}}} {
		_, err := thingOver(taskPatch{}, bad)
		var ae *ochazuke.ArgumentError
		if !errors.As(err, &ae) {
			t.Errorf("%v: want an ArgumentError, got %v", bad, err)
		}
	}
}
