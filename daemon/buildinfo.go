package daemon

import (
	"os"
	"runtime"
	"runtime/debug"
	"strings"
)

// Version is the Matomeru release version. Can be overridden at link time with
// -ldflags "-X ofam.dev/matomeru/daemon.Version=...".
var Version = "1.58.0"

// BuildCommit and BuildTime can optionally be injected at link time; when
// empty, CurrentBuildInfo reads them from Go's embedded VCS metadata.
var (
	BuildCommit = ""
	BuildTime   = ""
)

// BuildInfo describes the exact binary version, key o-family dependency
// versions, and VCS revision of a running mtm client or daemon process.
type BuildInfo struct {
	Version         string `json:"version"`
	Commit          string `json:"commit,omitempty"`
	ShortSHA        string `json:"shortSha,omitempty"`
	Dirty           bool   `json:"dirty,omitempty"`
	BuiltAt         string `json:"builtAt,omitempty"`
	GoVersion       string `json:"goVersion"`
	OchazukeVersion string `json:"ochazukeVersion,omitempty"`
	OdenVersion     string `json:"odenVersion,omitempty"`
	Binary          string `json:"binary,omitempty"`
}

// CurrentBuildInfo returns version, key dependency versions (ochazuke, oden),
// VCS commit SHA, dirty state, build time, Go version, and executable path.
func CurrentBuildInfo() BuildInfo {
	info := BuildInfo{
		Version:   Version,
		Commit:    strings.TrimSpace(BuildCommit),
		BuiltAt:   strings.TrimSpace(BuildTime),
		GoVersion: runtime.Version(),
	}
	if exe, err := os.Executable(); err == nil {
		info.Binary = exe
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range bi.Deps {
			switch dep.Path {
			case "ofam.dev/ochazuke":
				info.OchazukeVersion = dep.Version
				if dep.Replace != nil && dep.Replace.Version != "" {
					info.OchazukeVersion = dep.Replace.Version
				} else if dep.Replace != nil && dep.Replace.Path != "" {
					info.OchazukeVersion = dep.Version + " (local)"
				}
			case "ofam.dev/oden":
				info.OdenVersion = dep.Version
				if dep.Replace != nil && dep.Replace.Version != "" {
					info.OdenVersion = dep.Replace.Version
				} else if dep.Replace != nil && dep.Replace.Path != "" {
					info.OdenVersion = dep.Version + " (local)"
				}
			}
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = s.Value
				}
			case "vcs.time":
				if info.BuiltAt == "" {
					info.BuiltAt = s.Value
				}
			case "vcs.modified":
				if s.Value == "true" {
					info.Dirty = true
				}
			}
		}
	}
	if len(info.Commit) >= 7 {
		info.ShortSHA = info.Commit[:7]
	} else {
		info.ShortSHA = info.Commit
	}
	return info
}

// SameBuild reports whether a daemon's reported status matches this binary's
// version and commit SHA.
func (b BuildInfo) SameBuild(st *StatusResult) bool {
	if st == nil {
		return false
	}
	if st.Commit == "" || b.Commit == "" {
		return st.Version != "" && st.Version == b.Version && st.Commit == b.Commit
	}
	return st.Version == b.Version && st.Commit == b.Commit && st.Dirty == b.Dirty
}
