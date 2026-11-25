//go:build darwin

package app

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

const darwinCmdTimeout = time.Second

type darwinSysStatsReader struct {
	hasPrevProc      bool
	prevProcCPUNanos int64
	prevProcWall     time.Time
}

func newPlatformSysStatsReader() sysStatsReader {
	return &darwinSysStatsReader{}
}

func (r *darwinSysStatsReader) Read(ctx context.Context, diskPath string) SysStats {
	var out SysStats

	if !r.hasPrevProc {
		if cpuNanos, ok := readProcCPUNanos(); ok {
			r.prevProcCPUNanos = cpuNanos
			r.prevProcWall = time.Now()
			r.hasPrevProc = true
		}
	}

	if pct, ok := readDarwinSysCPU(ctx); ok {
		out.SysCPUPct = pct
		out.HasSysCPU = true
	}

	if usedPct, totalBytes, ok := readDarwinRAM(ctx); ok {
		out.RAMUsedPct = usedPct
		out.RAMTotalBytes = totalBytes
		out.HasRAM = true
	}

	if freeBytes, totalBytes, ok := readDarwinDisk(diskPath); ok {
		out.DiskFreeBytes = freeBytes
		out.DiskTotalBytes = totalBytes
		out.HasDisk = true
	}

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

func readDarwinSysCPU(ctx context.Context) (float64, bool) {
	subCtx, cancel := context.WithTimeout(ctx, darwinCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(subCtx, "ps", "-A", "-o", "%cpu=").Output()
	if err != nil {
		return 0, false
	}
	return parseDarwinPSCPU(string(out), runtime.NumCPU())
}

func readDarwinMemTotal() (uint64, bool) {
	raw, err := syscall.Sysctl("hw.memsize")
	if err != nil || len(raw) == 0 || len(raw) > 8 {
		return 0, false
	}
	var buf [8]byte
	copy(buf[:], raw)
	total := binary.LittleEndian.Uint64(buf[:])
	if total == 0 {
		return 0, false
	}
	return total, true
}

func readDarwinRAM(ctx context.Context) (usedPct float64, totalBytes uint64, ok bool) {
	totalBytes, ok = readDarwinMemTotal()
	if !ok {
		return 0, 0, false
	}
	subCtx, cancel := context.WithTimeout(ctx, darwinCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(subCtx, "vm_stat").Output()
	if err != nil {
		return 0, 0, false
	}
	usedPct, ok = parseDarwinVMStat(string(out), totalBytes, uint64(os.Getpagesize()))
	if !ok {
		return 0, 0, false
	}
	return usedPct, totalBytes, true
}

func readDarwinDisk(path string) (freeBytes, totalBytes uint64, ok bool) {
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
	if st.Bsize == 0 || st.Blocks == 0 {
		return 0, 0, false
	}
	bsize := uint64(st.Bsize)
	return st.Bavail * bsize, st.Blocks * bsize, true
}
