package app

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// DefaultSysStatsInterval is how often the background system stats sampler refreshes.
const DefaultSysStatsInterval = 3 * time.Second

// SysStats is a pure, read-only snapshot of local process and machine telemetry
// rendered in the app chrome. Zero value means not yet sampled (Ready == false).
type SysStats struct {
	Ready          bool
	ProcCPUPct     float64 // mtm process CPU % of total machine capacity (0..100)
	HasProcCPU     bool
	SysCPUPct      float64 // whole machine busy CPU % (0..100)
	HasSysCPU      bool
	RAMUsedPct     float64 // % of total RAM used (0..100)
	RAMTotalBytes  uint64  // total installed RAM bytes
	HasRAM         bool
	DiskFreeBytes  uint64 // user-available free disk bytes
	DiskTotalBytes uint64 // total disk bytes
	HasDisk        bool
	SampledAt      time.Time
}

// sysStatsReader collects a single point-in-time SysStats snapshot off the UI thread.
type sysStatsReader interface {
	Read(ctx context.Context, diskPath string) SysStats
}

// sysStatsSampler samples system telemetry on a background goroutine and
// publishes lock-free snapshots for the UI thread.
type sysStatsSampler struct {
	diskPath string
	interval time.Duration
	onUpdate func()
	reader   sysStatsReader

	snap         atomic.Pointer[SysStats]
	started      atomic.Bool
	done         chan struct{}
	lastRendered string
}

// newSysStatsSampler constructs a background sampler without executing any
// syscalls or subprocesses on the caller's goroutine.
func newSysStatsSampler(diskPath string, interval time.Duration, onUpdate func()) *sysStatsSampler {
	if diskPath == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			diskPath = home
		} else {
			diskPath = "/"
		}
	}
	if interval <= 0 {
		interval = DefaultSysStatsInterval
	}
	return &sysStatsSampler{
		diskPath: diskPath,
		interval: interval,
		onUpdate: onUpdate,
		reader:   newPlatformSysStatsReader(),
		done:     make(chan struct{}),
	}
}

// Start launches the background sampling loop once. It takes an initial sample
// asynchronously and then ticks every interval until ctx is cancelled.
func (s *sysStatsSampler) Start(ctx context.Context) {
	if s == nil || !s.started.CompareAndSwap(false, true) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		defer close(s.done)
		s.sampleOnce(ctx)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sampleOnce(ctx)
			}
		}
	}()
}

func (s *sysStatsSampler) sampleOnce(ctx context.Context) {
	if ctx.Err() != nil || s.reader == nil {
		return
	}
	st := s.reader.Read(ctx, s.diskPath)
	if ctx.Err() != nil {
		return
	}
	cp := st
	s.snap.Store(&cp)

	rendered := FormatChromeSysStats(st, 1<<20)
	if rendered != s.lastRendered {
		s.lastRendered = rendered
		if s.onUpdate != nil {
			s.onUpdate()
		}
	}
}

// Snapshot returns the most recent telemetry snapshot without blocking or locking.
func (s *sysStatsSampler) Snapshot() SysStats {
	if s == nil {
		return SysStats{}
	}
	if p := s.snap.Load(); p != nil {
		return *p
	}
	return SysStats{}
}

// SetSnapshotForTest installs a deterministic snapshot for unit tests.
func (s *sysStatsSampler) SetSnapshotForTest(st SysStats) {
	if s == nil {
		return
	}
	cp := st
	s.snap.Store(&cp)
}

// FormatBytesCompact formats a byte count into a compact human-readable string
// such as "999B", "12K", "512M", "32G", "142G", "1.0T", or "2.4T".
func FormatBytesCompact(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	units := [...]string{"K", "M", "G", "T", "P", "E"}
	val := float64(b) / unit
	idx := 0
	for val >= unit && idx < len(units)-1 {
		val /= unit
		idx++
	}
	// Promote when rounding at the boundary (e.g. 1023.9G -> 1.0T).
	if val >= 9.95 && math.Round(val) >= unit && idx < len(units)-1 {
		val /= unit
		idx++
	}
	if val < 9.95 {
		return fmt.Sprintf("%.1f%s", val, units[idx])
	}
	return fmt.Sprintf("%.0f%s", math.Round(val), units[idx])
}

// FormatChromeSysStats formats s for the status chrome within maxWidth cells.
// Unavailable segments are omitted, and trailing segments are dropped when
// maxWidth is narrow so the output never wraps or exceeds maxWidth.
func FormatChromeSysStats(s SysStats, maxWidth int) string {
	if !s.Ready || maxWidth <= 0 {
		return ""
	}
	segs := make([]string, 0, 3)
	if cpuSeg := formatCPUSegment(s); cpuSeg != "" {
		segs = append(segs, cpuSeg)
	}
	if s.HasRAM && s.RAMTotalBytes > 0 {
		segs = append(segs, fmt.Sprintf("ram %d%%/%s", roundPct(s.RAMUsedPct), FormatBytesCompact(s.RAMTotalBytes)))
	}
	if s.HasDisk && s.DiskTotalBytes > 0 {
		segs = append(segs, fmt.Sprintf("disk %s/%s", FormatBytesCompact(s.DiskFreeBytes), FormatBytesCompact(s.DiskTotalBytes)))
	}
	for n := len(segs); n >= 1; n-- {
		cand := strings.Join(segs[:n], " · ")
		if visibleWidth(cand) <= maxWidth {
			return cand
		}
	}
	return ""
}

func formatCPUSegment(s SysStats) string {
	switch {
	case s.HasProcCPU && s.HasSysCPU:
		return fmt.Sprintf("cpu %d%%/%d%%", roundPct(s.ProcCPUPct), roundPct(s.SysCPUPct))
	case !s.HasProcCPU && s.HasSysCPU:
		return fmt.Sprintf("cpu -/%d%%", roundPct(s.SysCPUPct))
	case s.HasProcCPU && !s.HasSysCPU:
		return fmt.Sprintf("cpu %d%%/-", roundPct(s.ProcCPUPct))
	default:
		return ""
	}
}

// MergeChromeStatusLine joins left-aligned status text and right-aligned
// telemetry on a single line of width cells, leaving at least 2 spaces between
// them. If width is too narrow, right is progressively shortened by dropping
// trailing " · " segments and finally dropped so left is never truncated.
func MergeChromeStatusLine(left, right string, width int) string {
	right = strings.TrimSpace(right)
	if right == "" || width <= 0 {
		return left
	}
	const minGap = 2
	leftW := visibleWidth(left)
	avail := width - leftW - minGap
	if avail <= 0 {
		return left
	}
	fitted := fitRightSegments(right, avail)
	if fitted == "" {
		return left
	}
	gap := width - leftW - visibleWidth(fitted)
	if gap < minGap {
		return left
	}
	return left + strings.Repeat(" ", gap) + fitted
}

func fitRightSegments(right string, maxWidth int) string {
	if maxWidth <= 0 || right == "" {
		return ""
	}
	if visibleWidth(right) <= maxWidth {
		return right
	}
	parts := strings.Split(right, " · ")
	for n := len(parts) - 1; n >= 1; n-- {
		cand := strings.Join(parts[:n], " · ")
		if visibleWidth(cand) <= maxWidth {
			return cand
		}
	}
	return ""
}

// visibleWidth measures display width while ignoring ANSI CSI/OSC escape
// sequences so status lines remain aligned even if styled.
func visibleWidth(s string) int {
	if !strings.ContainsRune(s, '\x1b') {
		return utf8.RuneCountInString(s)
	}
	width := 0
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) {
			next := s[i+1]
			if next == '[' {
				i += 2
				for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
					i++
				}
				if i < len(s) {
					i++
				}
				continue
			}
			if next == ']' {
				i += 2
				for i < len(s) {
					if s[i] == '\x07' {
						i++
						break
					}
					if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		width++
		i += size
	}
	return width
}

func clampPct(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, -1) || v <= 0 {
		return 0
	}
	if math.IsInf(v, 1) || v >= 100 {
		return 100
	}
	return v
}

func roundPct(v float64) int {
	return int(math.Round(clampPct(v)))
}

// procCPUPercent computes process CPU utilization as a percentage (0..100) of
// total machine capacity from two cumulative process CPU nanosecond counters.
func procCPUPercent(prevCPUNanos, curCPUNanos int64, wallDelta time.Duration, numCPU int) (float64, bool) {
	if wallDelta <= 0 || numCPU <= 0 || curCPUNanos < prevCPUNanos {
		return 0, false
	}
	capacity := float64(wallDelta.Nanoseconds()) * float64(numCPU)
	if capacity <= 0 {
		return 0, false
	}
	cpuDelta := float64(curCPUNanos - prevCPUNanos)
	return clampPct((cpuDelta / capacity) * 100), true
}

// cpuTicks is one reading of cumulative machine-wide CPU time counters.
// Adapted from tuios/internal/app/sysinfo.go.
type cpuTicks struct {
	idle  uint64
	total uint64
}

// cpuBusyPercent computes busy CPU percentage (0..100) between two readings.
// Adapted from tuios/internal/app/sysinfo.go.
func cpuBusyPercent(prev, cur cpuTicks) float64 {
	if cur.total <= prev.total || cur.idle < prev.idle {
		return 0
	}
	total := cur.total - prev.total
	idle := cur.idle - prev.idle
	if idle >= total {
		return 0
	}
	return clampPct(100 * float64(total-idle) / float64(total))
}

// parseDarwinPSCPU parses `ps -A -o %cpu=` output into whole-machine busy CPU %
// normalized across numCPU logical cores.
func parseDarwinPSCPU(out string, numCPU int) (float64, bool) {
	if numCPU <= 0 {
		return 0, false
	}
	var sum float64
	parsed := 0
	for line := range strings.SplitSeq(out, "\n") {
		field := strings.TrimSpace(line)
		if field == "" {
			continue
		}
		v, err := strconv.ParseFloat(field, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			continue
		}
		sum += v
		parsed++
	}
	if parsed == 0 {
		return 0, false
	}
	return clampPct(sum / float64(numCPU)), true
}

// parseDarwinVMStat parses `vm_stat` output given total physical RAM bytes.
func parseDarwinVMStat(out string, totalBytes uint64, fallbackPageSize uint64) (float64, bool) {
	if totalBytes == 0 {
		return 0, false
	}
	pageSize := fallbackPageSize
	if pageSize == 0 {
		pageSize = 4096
	}
	var (
		active, wired, compressed              uint64
		free, inactive, speculative, purgeable uint64
		hasActive, hasWired, hasCompressed     bool
		hasFree, hasInactive                   bool
	)
	for line := range strings.SplitSeq(out, "\n") {
		if idx := strings.Index(line, "page size of "); idx >= 0 {
			rest := line[idx+len("page size of "):]
			if end := strings.Index(rest, " "); end > 0 {
				if ps, err := strconv.ParseUint(rest[:end], 10, 64); err == nil && ps > 0 {
					pageSize = ps
				}
			}
			continue
		}
		key, valStr, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		valStr = strings.TrimRight(strings.TrimSpace(valStr), ".")
		val, err := strconv.ParseUint(valStr, 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "Pages active":
			active, hasActive = val, true
		case "Pages wired down":
			wired, hasWired = val, true
		case "Pages occupied by compressor":
			compressed, hasCompressed = val, true
		case "Pages free":
			free, hasFree = val, true
		case "Pages inactive":
			inactive, hasInactive = val, true
		case "Pages speculative":
			speculative = val
		case "Pages purgeable":
			purgeable = val
		}
	}

	var usedBytes uint64
	switch {
	case hasCompressed && (hasActive || hasWired):
		usedBytes = (active + wired + compressed) * pageSize
	case hasFree || hasInactive:
		availBytes := (free + inactive + speculative + purgeable) * pageSize
		if availBytes < totalBytes {
			usedBytes = totalBytes - availBytes
		}
	default:
		return 0, false
	}
	return clampPct(float64(usedBytes) / float64(totalBytes) * 100), true
}

// parseProcStatCPUTicks parses the aggregate "cpu " line from Linux /proc/stat.
// Adapted from tuios/internal/app/sysinfo_linux.go.
func parseProcStatCPUTicks(content string) (cpuTicks, bool) {
	for line := range strings.SplitSeq(content, "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		if len(fields) < 4 {
			return cpuTicks{}, false
		}
		var t cpuTicks
		for i, f := range fields[:min(len(fields), 8)] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return cpuTicks{}, false
			}
			t.total += v
			if i == 3 {
				t.idle = v
			}
		}
		return t, true
	}
	return cpuTicks{}, false
}

// parseProcMeminfo parses Linux /proc/meminfo for RAM used % and total bytes.
func parseProcMeminfo(content string) (usedPct float64, totalBytes uint64, ok bool) {
	var (
		totalKB, availKB, freeKB, buffersKB, cachedKB uint64
		hasTotal, hasAvail, hasFree                   bool
	)
	for line := range strings.SplitSeq(content, "\n") {
		key, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		val, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(key) {
		case "MemTotal":
			totalKB, hasTotal = val, true
		case "MemAvailable":
			availKB, hasAvail = val, true
		case "MemFree":
			freeKB, hasFree = val, true
		case "Buffers":
			buffersKB = val
		case "Cached":
			cachedKB = val
		}
	}
	if !hasTotal || totalKB == 0 {
		return 0, 0, false
	}
	if !hasAvail {
		if !hasFree {
			return 0, 0, false
		}
		availKB = freeKB + buffersKB + cachedKB
	}
	totalBytes = totalKB * 1024
	if availKB >= totalKB {
		return 0, totalBytes, true
	}
	usedPct = clampPct(float64(totalKB-availKB) / float64(totalKB) * 100)
	return usedPct, totalBytes, true
}
