package main

import "testing"

// mtm's embedded default rows pass ochazuke's rule check on the built
// runtime: every action, argument and table they name is known, no two
// rows clash, and no redirect row is one that does nothing.
func TestDefaultRowsCheck(t *testing.T) {
	t.Parallel()
	a := open(t)
	if problems := a.r.CheckDefaultBindings(defaultRowsSource, defaultRows); len(problems) > 0 {
		t.Fatalf("%s:\n%v", defaultRowsSource, problems)
	}
}
