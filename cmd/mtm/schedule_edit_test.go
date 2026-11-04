package main

import (
	"testing"

	"ofam.dev/matomeru/internal/schedules"
)

func TestScheduleIntervalFromField(t *testing.T) {
	t.Parallel()
	var a App
	var d schedules.Def
	if err := a.applyScheduleField(&d, "interval-from", "finish"); err != nil || scheduleField(d, "interval-from") != "finish" || !d.FromFinish() {
		t.Fatalf("finish: %v %+v", err, d)
	}
	if err := a.applyScheduleField(&d, "interval-from", "end"); err == nil {
		t.Fatal("end accepted")
	}
	if err := a.applyScheduleField(&d, "interval-from", ""); err != nil || d.IntervalFrom != "" {
		t.Fatalf("clear: %v %+v", err, d)
	}
}
