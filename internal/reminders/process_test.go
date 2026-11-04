package reminders

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ofam.dev/matomeru/store"
)

// The child is a real second process with a fake sender that appends one line.
func TestReminderNotifyProcessHelper(t *testing.T) {
	t.Parallel()
	dir := os.Getenv("MTM_REMINDER_PROCESS_FIXTURE")
	if dir == "" {
		t.Skip("subprocess fixture only")
	}
	db, err := store.Open(filepath.Join(dir, "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.NotifyDue(time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC), func(Reminder) (bool, error) {
		f, err := os.OpenFile(filepath.Join(dir, "sent"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return false, err
		}
		_, err = f.WriteString("sent\n")
		return err == nil, errors.Join(err, f.Close())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReminderNotifyTwoProcessesSendOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	add(t, s, time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC), "daily")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-start
			cmd := exec.Command(bin, "-test.run=^TestReminderNotifyProcessHelper$", "-test.count=1")
			cmd.Env = append(os.Environ(), "MTM_REMINDER_PROCESS_FIXTURE="+dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("child: %v\n%s", err, out)
			}
		})
	}
	close(start)
	wg.Wait()
	raw, err := os.ReadFile(filepath.Join(dir, "sent"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "sent\n" {
		t.Fatalf("two-process sends = %q, want one", raw)
	}
}
