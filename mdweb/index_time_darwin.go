//go:build darwin

package mdweb

import (
	"os"
	"syscall"
	"time"
)

func fileBirthTime(info os.FileInfo) time.Time {
	if info == nil {
		return time.Time{}
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st != nil {
		if st.Birthtimespec.Sec > 0 || st.Birthtimespec.Nsec > 0 {
			return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec).UTC()
		}
	}
	return info.ModTime().UTC()
}
