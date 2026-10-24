package main

import (
	"fmt"
	"strings"
)

// Licenses are the values of new --license; the first is the default.
var Licenses = []string{"mit", "none"}

// checkLicense refuses a --license value outside Licenses.
func checkLicense(name string) error {
	for _, l := range Licenses {
		if name == l {
			return nil
		}
	}
	return fmt.Errorf("--license %q: choose one of %s", name, strings.Join(Licenses, ", "))
}

// holder is the copyright holder of a new project's LICENSE: git config
// user.name as seen from dir, else $USER, else the tool's user.
func (t *Tool) holder(dir string) string {
	if out, err := t.Run(dir, "git", "config", "user.name"); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return name
		}
	}
	if u := lookup(t.Env, "USER"); u != "" {
		return u
	}
	return t.User
}

// licenseText is the body of the named license ("" for none).
func licenseText(name string, year int, holder string) string {
	if name != "mit" {
		return ""
	}
	return fmt.Sprintf(mitLicense, year, holder)
}

const mitLicense = `MIT License

Copyright (c) %d %s

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`
