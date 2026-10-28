package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// The clipboard as the prompt box reads it (ctrl+v, turn-prompt.paste).
// A terminal paste (cmd+v, a bracketed paste) carries only text, so an
// image or a file copied in Finder is read from the OS clipboard itself.
//
// This stays in mtm: it is macOS-only (osascript's JavaScript bridge to
// AppKit's NSPasteboard, which ships with the OS: no pngpaste, no cgo),
// and what the box does with each kind (save under mtm's data directory,
// a chip, an @ mention) is mtm's. ochazuke's yanks only write text. If
// another app wants to read images too, the reader (clipboardReader,
// macClipboard) moves to ochazuke as is; it imports nothing from mtm.

// clipboardContent is what the clipboard held, by precedence: files
// (Finder's copy puts their URLs and an icon bitmap; the files win, as
// omp's paste does), else an image (written as PNG to the path asked
// for), else text. All empty is an empty clipboard.
type clipboardContent struct {
	Files []string `json:"files,omitempty"`
	Image string   `json:"image,omitempty"`
	Text  string   `json:"text,omitempty"`
}

// clipboardReader reads the clipboard; an image goes to imagePath.
// macClipboard is the real one; tests replace it.
type clipboardReader interface {
	Read(ctx context.Context, imagePath string) (clipboardContent, error)
}

// macClipboard reads NSPasteboard through osascript -l JavaScript.
type macClipboard struct{}

// clipboardTimeout bounds one read (osascript starts in about 0.2s).
const clipboardTimeout = 5 * time.Second

// clipboardScript is the JXA reader: file URLs, else PNG (or TIFF made
// PNG, as a screenshot holds), else the string, as JSON on stdout.
const clipboardScript = `ObjC.import('AppKit');
function run(argv) {
  const pb = $.NSPasteboard.generalPasteboard;
  const opts = $.NSDictionary.dictionaryWithObjectForKey(true, $.NSPasteboardURLReadingFileURLsOnlyKey);
  const urls = pb.readObjectsForClassesOptions($.NSArray.arrayWithObject($.NSURL), opts);
  if (urls && !urls.isNil() && urls.count > 0) {
    const files = [];
    for (let i = 0; i < urls.count; i++) files.push(urls.objectAtIndex(i).path.js);
    return JSON.stringify({files: files});
  }
  let data = pb.dataForType($.NSPasteboardTypePNG);
  if (!data || data.isNil()) {
    const tiff = pb.dataForType($.NSPasteboardTypeTIFF);
    if (tiff && !tiff.isNil()) {
      const rep = $.NSBitmapImageRep.imageRepWithData(tiff);
      if (rep && !rep.isNil()) data = rep.representationUsingTypeProperties($.NSBitmapImageFileTypePNG, $());
    }
  }
  if (data && !data.isNil()) {
    if (!data.writeToFileAtomically(argv[0], true)) throw new Error("could not write " + argv[0]);
    return JSON.stringify({image: argv[0]});
  }
  const s = pb.stringForType($.NSPasteboardTypeString);
  if (s && !s.isNil()) return JSON.stringify({text: s.js});
  return "{}";
}`

func (macClipboard) Read(ctx context.Context, imagePath string) (clipboardContent, error) {
	if runtime.GOOS != "darwin" {
		return clipboardContent{}, fmt.Errorf("reading images from the clipboard is macOS only (%s)", runtime.GOOS)
	}
	ctx, cancel := context.WithTimeout(ctx, clipboardTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", clipboardScript, imagePath).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return clipboardContent{}, errors.New(strings.TrimSpace(string(ee.Stderr)))
		}
		return clipboardContent{}, err
	}
	var got clipboardContent
	if err := json.Unmarshal(out, &got); err != nil {
		return clipboardContent{}, fmt.Errorf("the clipboard reader answered %q: %w", strings.TrimSpace(string(out)), err)
	}
	return got, nil
}
