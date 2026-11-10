package main

import "ofam.dev/ochazuke"

// asDir and asFile mark a prop as a filesystem path (ochazuke.PropPath), so
// its noun gets the library's free verbs reveal, yank-path, open and
// open-with. Only props that always hold a path are marked: a context
// item's text is a path, a URL or a command by kind, so it is not.
func asDir(p ochazuke.PropSpec) ochazuke.PropSpec {
	p.Type, p.Path = ochazuke.PropPath, "dir"
	return p
}

func asFile(p ochazuke.PropSpec) ochazuke.PropSpec {
	p.Type, p.Path = ochazuke.PropPath, "file"
	return p
}
