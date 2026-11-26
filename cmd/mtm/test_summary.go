package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/oden"
)

func mtmTestFormatCommand() oden.Command {
	params := []oden.Parameter{{Name: "asterisk", Type: "bool", Description: "Use an asterisk for each passing test"}}
	return oden.Command{
		Name:       "format-test",
		Summary:    "read Go test JSON on stdin and print a compact report",
		Usage:      "[--asterisk]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			if len(p.Positional()) != 0 {
				return c.Usage("[--asterisk]")
			}
			outcomes, err := data.ParseGoTestJSON(c.Stdin)
			if err != nil {
				return err
			}
			report := data.FormatCompactTestSummary(outcomes, p.Bool("asterisk"))
			file, isFile := c.Stdout.(*os.File)
			color := isFile && defaultIsTTY(file) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
			if err := writeTestFormat(c.Stdout, report, color); err != nil {
				return err
			}
			if report.Failed > 0 {
				return &oden.ExitError{Code: 1}
			}
			return nil
		},
	}
}

func writeTestFormat(output io.Writer, report data.CompactTestReport, color bool) error {
	var text strings.Builder
	for _, run := range report.Runs {
		if color {
			switch run.Tone {
			case "success":
				text.WriteString("\x1b[32m")
			case "muted":
				text.WriteString("\x1b[2m")
			case "error":
				text.WriteString("\x1b[31m")
			}
		}
		text.WriteString(run.Text)
		if color {
			text.WriteString("\x1b[0m")
		}
	}
	if text.Len() != 0 {
		text.WriteByte('\n')
	}
	fmt.Fprintf(&text, "%d passed, %d failed, %d skipped\n", report.Passed, report.Failed, report.Skipped)
	for _, failure := range report.FailureDetails {
		fmt.Fprintf(&text, "\n✗ %s\n", strings.TrimSpace(failure.Package+" "+failure.Name))
		if log := strings.TrimRight(failure.Output, "\r\n"); log != "" {
			fmt.Fprintln(&text, log)
		}
	}
	_, err := io.WriteString(output, text.String())
	return err
}
