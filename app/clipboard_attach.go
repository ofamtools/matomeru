package app

import (
	"bytes"
	"errors"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
)

type clipboardEnv struct {
	goos     string
	lookPath func(file string) (string, error)
	runCmd   func(name string, args ...string) ([]byte, error)
}

var defaultClipboardEnv = clipboardEnv{
	goos:     runtime.GOOS,
	lookPath: exec.LookPath,
	runCmd: func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).Output()
	},
}

// captureClipboardFunc is the hook used by richComposerModal on ctrl+v so tests
// can inject deterministic clipboard payloads without touching the OS clipboard.
var captureClipboardFunc = captureClipboardAttachment

var pngMagicHeader = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// captureClipboardAttachment inspects the system clipboard and either:
//  1. Saves a PNG image atomically via data.WriteManagedBytes and returns an image Attachment.
//  2. Detects an existing local file path from pbpaste and returns a file/image Attachment.
//  3. Returns plain clipboard text so the caller can insert it into the input.
func captureClipboardAttachment(filesRoot, idPrefix string) (data.Attachment, string, error) {
	return captureClipboardAttachmentWith(filesRoot, idPrefix, defaultClipboardEnv)
}

func captureClipboardAttachmentWith(filesRoot, idPrefix string, env clipboardEnv) (data.Attachment, string, error) {
	if env.goos == "" {
		env.goos = runtime.GOOS
	}
	if env.lookPath == nil {
		env.lookPath = exec.LookPath
	}
	if env.runCmd == nil {
		env.runCmd = defaultClipboardEnv.runCmd
	}

	if env.goos == "darwin" {
		if pngBytes, ok := readDarwinClipboardPNG(env); ok && len(pngBytes) > 0 {
			root := strings.TrimSpace(filesRoot)
			if root == "" {
				root = filepath.Join(os.TempDir(), "matomeru-attachments")
			}
			prefix := strings.TrimSpace(idPrefix)
			if prefix == "" {
				prefix = fmt.Sprintf("clip-%d", time.Now().UnixNano())
			}
			savedPath, err := data.WriteManagedBytes(root, data.CategoryAttachments, prefix, "clipboard", ".png", pngBytes)
			if err != nil {
				return data.Attachment{}, "", err
			}
			return data.Attachment{
				Kind:   "image",
				Path:   savedPath,
				Label:  filepath.Base(savedPath),
				Mime:   "image/png",
				Source: "clipboard",
			}, "", nil
		}
	}

	raw, err := readClipboardText(env)
	if err != nil {
		return data.Attachment{}, "", err
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed != "" && !strings.ContainsAny(trimmed, "\r\n") {
		candidate := strings.Trim(trimmed, `"'`)
		expanded := expandHome(candidate)
		if info, statErr := os.Stat(expanded); statErr == nil && !info.IsDir() {
			abs, absErr := filepath.Abs(expanded)
			if absErr == nil {
				kind, mimeType := attachmentKindAndMime(abs)
				return data.Attachment{
					Kind:   kind,
					Path:   abs,
					Label:  filepath.Base(abs),
					Mime:   mimeType,
					Source: "clipboard",
				}, "", nil
			}
		}
	}
	return data.Attachment{}, raw, nil
}

func readDarwinClipboardPNG(env clipboardEnv) ([]byte, bool) {
	if _, err := env.lookPath("pngpaste"); err == nil {
		if out, err := env.runCmd("pngpaste", "-"); err == nil && len(out) > 0 {
			return out, true
		}
	}
	tmp, err := os.CreateTemp("", "mtm-clip-*.png")
	if err != nil {
		return nil, false
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	escaped := strings.ReplaceAll(tmpPath, `"`, `\"`)
	out, err := env.runCmd(
		"osascript",
		"-e", "try",
		"-e", "set pngData to the clipboard as «class PNGf»",
		"-e", fmt.Sprintf(`set fp to open for access POSIX file "%s" with write permission`, escaped),
		"-e", "set eof fp to 0",
		"-e", "write pngData to fp",
		"-e", "close access fp",
		"-e", "on error",
		"-e", "try",
		"-e", fmt.Sprintf(`close access POSIX file "%s"`, escaped),
		"-e", "end try",
		"-e", "end try",
	)
	if err != nil {
		return nil, false
	}
	if b, rerr := os.ReadFile(tmpPath); rerr == nil && len(b) > 0 {
		return b, true
	}
	if bytes.HasPrefix(out, pngMagicHeader) {
		return out, true
	}
	return nil, false
}

func readClipboardText(env clipboardEnv) (string, error) {
	if env.goos == "darwin" {
		out, err := env.runCmd("pbpaste")
		if err != nil {
			return "", fmt.Errorf("read clipboard: %w", err)
		}
		return string(out), nil
	}
	for _, cmd := range [][]string{
		{"pbpaste"},
		{"wl-paste", "-n"},
		{"xclip", "-selection", "clipboard", "-o"},
		{"xsel", "--clipboard", "--output"},
	} {
		if _, err := env.lookPath(cmd[0]); err != nil {
			continue
		}
		out, err := env.runCmd(cmd[0], cmd[1:]...)
		if err == nil {
			return string(out), nil
		}
	}
	return "", errors.New("no clipboard command available")
}

// attachmentFromPath validates that rawPath exists and is a regular file (not a
// directory), resolving its absolute path, kind ("image" or "file"), MIME type,
// and display label.
func attachmentFromPath(rawPath, source string) (data.Attachment, error) {
	cleaned := strings.Trim(strings.TrimSpace(rawPath), `"'`)
	if cleaned == "" {
		return data.Attachment{}, errors.New("attachment path is empty")
	}
	expanded := expandHome(cleaned)
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return data.Attachment{}, fmt.Errorf("resolve attachment path %q: %w", rawPath, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return data.Attachment{}, fmt.Errorf("stat attachment %q: %w", abs, err)
	}
	if info.IsDir() {
		return data.Attachment{}, fmt.Errorf("cannot attach directory %q", abs)
	}
	kind, mimeType := attachmentKindAndMime(abs)
	return data.Attachment{
		Kind:   kind,
		Path:   abs,
		Label:  filepath.Base(abs),
		Mime:   mimeType,
		Source: strings.TrimSpace(source),
	}, nil
}

func attachmentKindAndMime(path string) (kind, mimeType string) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image", "image/png"
	case ".jpg", ".jpeg":
		return "image", "image/jpeg"
	case ".gif":
		return "image", "image/gif"
	case ".webp":
		return "image", "image/webp"
	case ".svg":
		return "image", "image/svg+xml"
	default:
		mt := mime.TypeByExtension(ext)
		if semi := strings.IndexByte(mt, ';'); semi >= 0 {
			mt = strings.TrimSpace(mt[:semi])
		}
		return "file", mt
	}
}
