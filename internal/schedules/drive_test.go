package schedules

import (
	"context"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/drive"
)

// A command row's drive picks how it runs: exec (the default) through
// Exec, terminal through Runner.Drives, api refused as not available yet;
// a value that is no drive fails Check.
func TestCommandRowDrive(t *testing.T) {
	db, _ := openDB(t)
	c, f := start(), &fake{out: "ran"}
	r := newRunner(t, db, c, f, "me")
	d := def("sig")
	if d.DriveOf() != drive.Exec {
		t.Fatalf("default drive %q", d.DriveOf())
	}
	rec, err := r.RunNow(context.Background(), d)
	if err != nil || rec.Outcome != OutcomeOK || rec.Output != "ran" || f.count() != 1 || rec.Drive != "" {
		t.Fatalf("exec: %+v %v", rec, err)
	}

	var opened []string
	r.Drives[drive.Terminal] = func(_ context.Context, def Def, rec *Record) error {
		opened = append(opened, strings.Join(def.Command, " "))
		rec.Window = "w9"
		return nil
	}
	d.Drive = drive.Terminal
	rec, err = r.RunNow(context.Background(), d)
	if err != nil || rec.Outcome != OutcomeOK || rec.Drive != drive.Terminal || rec.Window != "w9" || len(opened) != 1 || f.count() != 1 {
		t.Fatalf("terminal: %+v %v opened %v", rec, err, opened)
	}

	d.Drive = drive.API
	rec, err = r.RunNow(context.Background(), d)
	if err != nil || rec.Outcome != OutcomeFailed || !strings.Contains(rec.Error, "drive api is not available yet") || f.count() != 1 {
		t.Fatalf("api: %+v %v", rec, err)
	}

	d.Drive = "pty"
	if err := d.Check(); err == nil || !strings.Contains(err.Error(), `drive "pty"`) {
		t.Fatalf("check: %v", err)
	}
}
