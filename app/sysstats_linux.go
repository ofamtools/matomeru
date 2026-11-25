//go:build linux

package app

import (
	"context"
	"os"
	"runtime"
	"syscall"
	"time"
)

type linuxSysStatsReader struct {
	hasPrevProc      bool
	prevProcCPUNanos int64
	prevProcWall     time.Time

	hasPrevTicks bool
	prevTicks    cpuTicks
}

func newPlatformSysStatsReader() sysStatsReader {
	return &linuxSysStatsReader{}
}

func (r *linuxSysStatsReader) Read(_ context.Context, diskPath string) SysStats {
	var out SysStats

	if curCPUNanos, ok := readProcCPUNanos(); ok {
		now := time.Now()
		if r.hasPrevProc {
			if pct, valid := procCPUPercent(r.prevProcCPUNanos, curCPUNanos, now.Sub(r.prevProcWall), runtime.NumCPU()); valid {
				out.ProcCPUPct = pct
				out.HasProcCPU = true
			}
		}
		r.prevProcCPUNanos = curCPUNanos
		r.prevProcWall = now
		r.hasPrevProc = true
	}

	if raw, err := os.ReadFile("/proc/stat"); err == nil {
		if ticks, ok := parseProcStatCPUTicks(string(raw)); ok {
			if r.hasPrevTicks {
				out.SysCPUPct = cpuBusyPercent(r.prevTicks, ticks)
				out.HasSysCPU = true
			}
			r.prevTicks = ticks
			r.hasPrevTicks = true
		}
	}

	if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
		if usedPct, totalBytes, ok := parseProcMeminfo(string(raw)); ok {
			out.RAMUsedPct = usedPct
			out.RAMTotalBytes = totalBytes
			out.HasRAM = true
		}
	}

	if freeBytes, totalBytes, ok := readLinuxDisk(diskPath); ok {
		out.DiskFreeBytes = freeBytes
		out.DiskTotalBytes = totalBytes
		out.HasDisk = true
	}

	if out.HasProcCPU || out.HasSysCPU || out.HasRAM || out.HasDisk {
		out.Ready = true
		out.SampledAt = time.Now()
	}
	return out
}

func readProcCPUNanos() (int64, bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	return ru.Utime.Nano() + ru.Stime.Nano(), true
}

func readLinuxDisk(path string) (freeBytes, totalBytes uint64, ok bool) {
	if path == "" {
		path = "/"
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		if path == "/" {
			return 0, 0, false
		}
		if err = syscall.Statfs("/", &st); err != nil {
			return 0, 0, false
		}
	}
	if st.Bsize <= 0 || st.Blocks == 0 {
		return 0, 0, false
	}
	bsize := uint64(st.Bsize)
	return st.Bavail * bsize, st.Blocks * bsize, true
}
