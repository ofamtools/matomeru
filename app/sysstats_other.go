//go:build !darwin && !linux

package app

import "context"

type noopSysStatsReader struct{}

func newPlatformSysStatsReader() sysStatsReader {
	return noopSysStatsReader{}
}

func (noopSysStatsReader) Read(context.Context, string) SysStats {
	return SysStats{}
}
