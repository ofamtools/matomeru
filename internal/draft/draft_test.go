package draft

import (
	"errors"
	"os"
	"testing"
)

func TestDraftLifecycle(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.Save("task", "abc", map[string]string{"text": "half typed"}); err != nil {
		t.Fatal(err)
	}
	s = New(dir)
	var got map[string]string
	if err := s.Load("task", "abc", &got); err != nil || got["text"] != "half typed" {
		t.Fatalf("reopened: %v, %v", got, err)
	}
	if err := s.Save("task", "abc", make(chan int)); err == nil {
		t.Fatal("invalid JSON saved")
	}
	if err := s.Load("task", "abc", &got); err != nil || got["text"] != "half typed" {
		t.Fatalf("failed write lost draft: %v, %v", got, err)
	}
	rows, err := s.List("task")
	if err != nil || len(rows) != 1 || rows[0].ID != "abc" {
		t.Fatalf("list: %+v, %v", rows, err)
	}
	if err := s.Save("../task", "abc", got); err == nil {
		t.Fatal("path traversal accepted")
	}
	if err := s.Save("task", "../abc", got); err == nil {
		t.Fatal("id traversal accepted")
	}
	if err := s.Clear("task", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := s.Load("task", "abc", &got); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleared draft: %v", err)
	}
}
