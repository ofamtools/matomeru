package main

import (
	"os"
	"testing"
)

// The tests may themselves run inside a pane of mtm: the pane and task of
// that pane must not become the defaults of the tools under test.
func TestMain(m *testing.M) {
	os.Unsetenv("MTM_PANE_ID")
	os.Unsetenv("MTM_TASK_ID")
	os.Exit(m.Run())
}
