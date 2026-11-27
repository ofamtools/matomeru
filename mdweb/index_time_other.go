//go:build !darwin

package mdweb

import (
	"os"
	"time"
)

func fileBirthTime(info os.FileInfo) time.Time {
	if info == nil {
		return time.Time{}
	}
	return info.ModTime().UTC()
}
