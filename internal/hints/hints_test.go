package hints

import (
	"slices"
	"strings"
	"testing"
)

func TestDefaultAlphabetIsSafeUnderCtrl(t *testing.T) {
	if strings.ContainsAny(DefaultAlphabet, UnsafeCtrlLetters+"c") {
		t.Fatalf("DefaultAlphabet %q holds an unsafe letter", DefaultAlphabet)
	}
	if NormalizeAlphabet(DefaultAlphabet) != DefaultAlphabet {
		t.Fatal("DefaultAlphabet is not normalized")
	}
	for _, l := range Labels(2000, "") {
		if strings.ContainsAny(l, UnsafeCtrlLetters) {
			t.Fatalf("label %q holds an unsafe letter", l)
		}
	}
}

func TestNormalizeAlphabet(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ASDF", "asdf"},
		{"a-s;d1fa", "asdf"},
		{"a", DefaultAlphabet},
		{"", DefaultAlphabet},
		{"hjkl", "hjkl"}, // the caller's choice stands
	}
	for _, c := range cases {
		if got := NormalizeAlphabet(c.in); got != c.want {
			t.Errorf("NormalizeAlphabet(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLabelsAreShortestFirstAndPrefixFree(t *testing.T) {
	cases := []struct {
		n        int
		alphabet string
		want     []string
	}{
		{3, "abc", []string{"a", "b", "c"}},
		{4, "abc", []string{"a", "b", "ca", "cb"}},
		{5, "abc", []string{"a", "b", "ca", "cb", "cc"}},
		{6, "abc", []string{"a", "ba", "bb", "bc", "ca", "cb"}},
	}
	for _, c := range cases {
		if got := Labels(c.n, c.alphabet); !slices.Equal(got, c.want) {
			t.Errorf("Labels(%d, %q) = %q, want %q", c.n, c.alphabet, got, c.want)
		}
	}
	for _, alphabet := range []string{"", "ab", "asdf"} {
		for n := 1; n <= 400; n++ {
			labels := Labels(n, alphabet)
			if len(labels) != n {
				t.Fatalf("Labels(%d, %q) gave %d", n, alphabet, len(labels))
			}
			for i, a := range labels {
				if i > 0 && len(a) < len(labels[i-1]) {
					t.Fatalf("Labels(%d, %q) not shortest first: %q", n, alphabet, labels)
				}
				for j, b := range labels {
					if i != j && strings.HasPrefix(b, a) {
						t.Fatalf("Labels(%d, %q): %q is a prefix of %q", n, alphabet, a, b)
					}
				}
			}
		}
	}
}

func TestAssign(t *testing.T) {
	cases := []struct {
		name    string
		targets []Target
		want    []string
	}{
		{
			name:    "same text gets one label",
			targets: []Target{{Text: "x", Distance: 5}, {Text: "y", Distance: 1}, {Text: "x", Distance: 9}},
			want:    []string{"b", "a", "b"},
		},
		{
			name:    "nearest occurrence decides a text's rank",
			targets: []Target{{Text: "x", Distance: 5}, {Text: "y", Distance: 3}, {Text: "x", Distance: 0}},
			want:    []string{"a", "b", "a"},
		},
		{
			name:    "a tie goes to the text seen first",
			targets: []Target{{Text: "x", Distance: 2}, {Text: "y", Distance: 2}},
			want:    []string{"a", "b"},
		},
		{
			name:    "a key stands in for the text",
			targets: []Target{{Text: "https://x.io/", Key: "k"}, {Text: "https://x.io", Key: "k"}, {Text: "z", Distance: 1}},
			want:    []string{"a", "a", "b"},
		},
		{
			name: "the farthest texts get the longer labels",
			targets: []Target{
				{Text: "1", Distance: 4}, {Text: "2", Distance: 3}, {Text: "3", Distance: 2}, {Text: "4", Distance: 1},
			},
			want: []string{"cb", "ca", "b", "a"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Assign(c.targets, "abc"); !slices.Equal(got, c.want) {
				t.Fatalf("Assign = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDistanceIsRowsFirst(t *testing.T) {
	// cursor on the bottom row of an 80-column screen
	sameRowFar := Distance(23, 79, 23, 0, 80)
	rowAbove := Distance(22, 0, 23, 0, 80)
	if !(sameRowFar < rowAbove) {
		t.Fatalf("same row %d, row above %d", sameRowFar, rowAbove)
	}
	if Distance(20, 5, 23, 5, 80) != Distance(26, 5, 23, 5, 80) {
		t.Fatal("distance is not symmetric")
	}
}

func TestMatcher(t *testing.T) {
	labels := []string{"a", "sa", "sd", "a"}
	type press struct {
		key    rune
		action Action
		want   Result
		typed  string
	}
	cases := []struct {
		name  string
		steps []press
	}{
		{"one key completes", []press{{'a', Copy, Result{Complete, 0, Copy}, ""}}},
		{"a key starting no label is ignored", []press{
			{'z', Open, Result{NoMatch, -1, Copy}, ""},
			{'s', Copy, Result{Prefix, -1, Copy}, "s"},
			{'z', Copy, Result{NoMatch, -1, Copy}, "s"},
			{'d', Copy, Result{Complete, 2, Copy}, ""},
		}},
		{"a modifier on the first key counts", []press{
			{'S', Type, Result{Prefix, -1, Type}, "s"},
			{'a', Copy, Result{Complete, 1, Type}, ""},
		}},
		{"ctrl beats shift on any key", []press{
			{'s', Open, Result{Prefix, -1, Open}, "s"},
			{'D', Type, Result{Complete, 2, Open}, ""},
		}},
		{"shared label answers the first target", []press{{'A', Type, Result{Complete, 0, Type}, ""}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := NewMatcher(labels)
			for i, p := range c.steps {
				if got := m.Press(p.key, p.action); got != p.want {
					t.Fatalf("step %d: Press(%q) = %+v, want %+v", i, p.key, got, p.want)
				}
				if m.Typed() != p.typed {
					t.Fatalf("step %d: typed %q, want %q", i, m.Typed(), p.typed)
				}
			}
		})
	}
}

func TestMatcherBackspace(t *testing.T) {
	m := NewMatcher([]string{"sa", "sd"})
	m.Press('s', Open)
	m.Backspace()
	if m.Typed() != "" {
		t.Fatalf("typed %q", m.Typed())
	}
	if got := m.Press('s', Copy); got.Action != Copy {
		t.Fatalf("action kept after backspace: %+v", got)
	}
	m.Backspace()
	m.Backspace()
	if got := m.Press('s', Copy); got.State != Prefix {
		t.Fatalf("after backspaces: %+v", got)
	}
}
