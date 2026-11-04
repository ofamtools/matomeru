package actionitems

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"ofam.dev/matomeru/store"
)

// Each helper owns a separate SQLite connection in a real OS process. The
// winning fake sender stays blocked until the losing process finishes, proving
// that the claim, rather than just a fast receipt, excludes the second sender.
func TestActionItemNotifyTwoProcesses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() int64 { return 1000 })
	it, _, err := s.Add(Spec{Source: "slack", Title: "one ping"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	type child struct {
		cmd    *exec.Cmd
		stdin  io.WriteCloser
		lines  *bufio.Scanner
		stderr bytes.Buffer
	}
	children := make([]*child, 2)
	for i := range children {
		c := &child{cmd: exec.CommandContext(ctx, executable, "-test.run=^TestActionItemNotifyProcessHelper$")}
		c.cmd.Env = append(os.Environ(), "MTM_NOTIFY_TEST_DB="+path, "MTM_NOTIFY_TEST_LOG="+filepath.Join(dir, "sent"))
		c.cmd.Stderr = &c.stderr
		c.stdin, err = c.cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := c.cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		c.lines = bufio.NewScanner(stdout)
		if err := c.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children[i] = c
		if !c.lines.Scan() || c.lines.Text() != "ready" {
			t.Fatalf("child readiness %q: %s", c.lines.Text(), c.stderr.String())
		}
	}
	for _, c := range children {
		if _, err := io.WriteString(c.stdin, "g"); err != nil {
			t.Fatal(err)
		}
	}
	type response struct {
		index int
		line  string
	}
	responses := make(chan response, 2)
	for i, c := range children {
		go func() {
			if c.lines.Scan() {
				responses <- response{i, c.lines.Text()}
			} else {
				responses <- response{i, "EOF"}
			}
		}()
	}
	winner, loser := -1, -1
	for range children {
		response := <-responses
		switch response.line {
		case "sending":
			winner = response.index
		case "done:0":
			loser = response.index
		default:
			t.Fatalf("child %d: %q", response.index, response.line)
		}
	}
	if winner < 0 || loser < 0 || winner == loser {
		t.Fatalf("winner %d loser %d", winner, loser)
	}
	if _, err := io.WriteString(children[winner].stdin, "f"); err != nil {
		t.Fatal(err)
	}
	if !children[winner].lines.Scan() || children[winner].lines.Text() != "done:1" {
		t.Fatalf("winner receipt %q", children[winner].lines.Text())
	}
	for _, c := range children {
		c.stdin.Close()
		for c.lines.Scan() {
		} // drain the testing footer before Wait
		if err := c.cmd.Wait(); err != nil {
			t.Fatalf("child: %v: %s", err, c.stderr.String())
		}
	}
	log, err := os.ReadFile(filepath.Join(dir, "sent"))
	if err != nil || string(log) != "mtm: slack · one ping\n" {
		t.Fatalf("deliveries %q: %v", log, err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err = Open(db)
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.Get(it.ID)
	if err != nil || done.NotifiedAt != 2000 || done.NotifyClaim != "" {
		t.Fatalf("receipt %+v %v", done, err)
	}
}

func TestActionItemNotifyProcessHelper(t *testing.T) {
	t.Parallel()
	path := os.Getenv("MTM_NOTIFY_TEST_DB")
	if path == "" {
		t.Skip("subprocess helper")
	}
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("ready")
	if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	n, err := s.NotifyDue(2000, NotifyOptions{Enabled: true, Sources: []string{"slack"}, Since: 999}, func(title, _ string) (bool, error) {
		file, err := os.OpenFile(os.Getenv("MTM_NOTIFY_TEST_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return false, err
		}
		_, err = io.WriteString(file, title+"\n")
		err = errors.Join(err, file.Close())
		if err != nil {
			return false, err
		}
		fmt.Println("sending")
		_, err = io.ReadFull(os.Stdin, make([]byte, 1))
		return err == nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("done:%d\n", n)
}
