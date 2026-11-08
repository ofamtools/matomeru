package notices

import (
	"os"
	"strings"
	"testing"
)

func goModRequires(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	return ParseRequires(string(b))
}

// The embedded data names every module go.mod requires, at its version, and
// each linked module has a licence file of an allowed licence.
func TestEmbeddedDataIsCurrent(t *testing.T) {
	t.Parallel()
	ix, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if bad := ix.Check(goModRequires(t), Text); len(bad) > 0 {
		t.Fatalf("notices check failed (make licenses):\n%s", strings.Join(bad, "\n"))
	}
	all, small := len(ix.Linked(true)), len(ix.Linked(false))
	if small >= all || small == 0 {
		t.Errorf("nodaemon list has %d modules, default %d: want fewer", small, all)
	}
}

func sample() (Index, func(string) (string, error)) {
	const mit = "Permission is hereby granted, free of charge, to any person obtaining a copy\nTHE SOFTWARE IS PROVIDED \"AS IS\""
	h := Hash(mit)
	ix := Index{Modules: []Module{{Path: "example.com/a", Version: "v1.0.0", Files: []File{{Name: "LICENSE", Kind: KindLicense, SPDX: Classify(mit), Text: h}}}}}
	return ix, func(x string) (string, error) {
		if x == h {
			return mit, nil
		}
		return "", os.ErrNotExist
	}
}

func TestCheckPasses(t *testing.T) {
	t.Parallel()
	ix, text := sample()
	if bad := ix.Check(map[string]string{"example.com/a": "v1.0.0"}, text); len(bad) > 0 {
		t.Fatalf("clean data failed: %v", bad)
	}
}

// A planted licence file that names no known licence fails the check.
func TestCheckFailsOnUnknownLicence(t *testing.T) {
	t.Parallel()
	planted := "Copyright (c) Nobody. You may look at this code on Tuesdays."
	if got := Classify(planted); got != nil {
		t.Fatalf("planted text classified as %v", got)
	}
	ix, text := sample()
	h := Hash(planted)
	ix.Modules = append(ix.Modules, Module{Path: "example.com/b", Version: "v1.0.0", Files: []File{{Name: "LICENSE", Kind: KindLicense, SPDX: []string{Unknown}, Text: h}}})
	text2 := func(x string) (string, error) {
		if x == h {
			return planted, nil
		}
		return text(x)
	}
	bad := ix.Check(map[string]string{"example.com/a": "v1.0.0", "example.com/b": "v1.0.0"}, text2)
	if len(bad) != 1 || !strings.Contains(bad[0], "example.com/b") || !strings.Contains(bad[0], "unknown licence") {
		t.Fatalf("want one unknown-licence failure for example.com/b, got %v", bad)
	}
}

func TestCheckFailsOnDisallowedLicence(t *testing.T) {
	t.Parallel()
	ix, text := sample()
	ix.Modules[0].Files[0].SPDX = []string{"GPL-3.0-only"}
	bad := ix.Check(map[string]string{"example.com/a": "v1.0.0"}, text)
	if len(bad) != 1 || !strings.Contains(bad[0], "not allowed") {
		t.Fatalf("got %v", bad)
	}
}

func TestCheckFailsOnMissingLicenceFile(t *testing.T) {
	t.Parallel()
	ix, text := sample()
	ix.Modules[0].Files = nil
	bad := ix.Check(map[string]string{"example.com/a": "v1.0.0"}, text)
	if len(bad) != 1 || !strings.Contains(bad[0], "no licence file") {
		t.Fatalf("got %v", bad)
	}
}

// Data older than go.mod fails: a version bump, a new requirement, a dropped one.
func TestCheckFailsOnStaleData(t *testing.T) {
	t.Parallel()
	ix, text := sample()
	for name, req := range map[string]map[string]string{
		"bumped":  {"example.com/a": "v1.1.0"},
		"added":   {"example.com/a": "v1.0.0", "example.com/new": "v0.1.0"},
		"dropped": {},
	} {
		bad := ix.Check(req, text)
		if len(bad) == 0 || !strings.Contains(strings.Join(bad, "\n"), "stale") {
			t.Errorf("%s: want a stale failure, got %v", name, bad)
		}
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		text string
		want string
	}{
		{"Redistribution and use in source and binary forms, with or without modification, are permitted\nNeither the name of Google", "BSD-3-Clause"},
		{"Redistribution and use in source and binary forms\nTHIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND", "BSD-2-Clause"},
		{"Apache License\nVersion 2.0, January 2004", "Apache-2.0"},
		{"Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee", "ISC"},
		{"All of the code has been dedicated to the public domain", "Public-Domain"},
	} {
		if got := Classify(c.text); len(got) != 1 || got[0] != c.want {
			t.Errorf("Classify(%q) = %v, want %s", c.text, got, c.want)
		}
	}
}

func TestParseRequires(t *testing.T) {
	t.Parallel()
	got := ParseRequires("module m\n\nrequire a.b/c v1.0.0\n\nrequire (\n\tx.y/z v2.0.0 // indirect\n)\nreplace (\n\tq.r/s => ../s\n)\n")
	if len(got) != 2 || got["a.b/c"] != "v1.0.0" || got["x.y/z"] != "v2.0.0" {
		t.Fatalf("got %v", got)
	}
}
