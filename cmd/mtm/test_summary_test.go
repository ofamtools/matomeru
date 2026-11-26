package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestTestFormatCLI(t *testing.T) {
	t.Setenv("MTM_CONFIG_DIR", t.TempDir())
	passed := `{"Action":"pass","Package":"example.org/pkg","Test":"TestPass"}` + "\n"
	failed := passed + `{"Action":"output","Package":"example.org/pkg","Test":"TestFail","Output":"assertion failed\n"}
{"Action":"fail","Package":"example.org/pkg","Test":"TestFail"}
{"Action":"skip","Package":"example.org/pkg","Test":"TestSkip"}
{"Action":"fail","Package":"example.org/pkg"}`
	for _, tc := range []struct {
		name, input, want string
		args              []string
		code              int
	}{
		{name: "passing", input: passed, want: ".\n1 passed, 0 failed, 0 skipped\n"},
		{name: "failures", input: failed, args: []string{"--asterisk"}, code: 1,
			want: "*✗s\n1 passed, 1 failed, 1 skipped\n\n✗ example.org/pkg TestFail\nassertion failed\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append([]string{"format-test"}, tc.args...)
			code := run(args, strings.NewReader(tc.input), &stdout, &stderr)
			if code != tc.code || stdout.String() != tc.want || stderr.Len() != 0 {
				t.Fatalf("exit %d, stdout %q, stderr %q; want exit %d, stdout %q", code, stdout.String(), stderr.String(), tc.code, tc.want)
			}
		})
	}
}
