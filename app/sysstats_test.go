package app

import (
	"context"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFormatBytesCompact(t *testing.T) {
	const (
		KiB = 1024
		MiB = 1024 * KiB
		GiB = 1024 * MiB
		TiB = 1024 * GiB
	)
	cases := []struct {
		bytes uint64
		want  string
	}{
		{0, "0B"},
		{999, "999B"},
		{1023, "1023B"},
		{KiB, "1.0K"},
		{12 * KiB, "12K"},
		{512 * MiB, "512M"},
		{16 * GiB, "16G"},
		{32 * GiB, "32G"},
		{142 * GiB, "142G"},
		{TiB, "1.0T"},
		{24 * TiB / 10, "2.4T"},
		{1024*GiB - 1, "1.0T"},
	}
	for _, tc := range cases {
		if got := FormatBytesCompact(tc.bytes); got != tc.want {
			t.Errorf("FormatBytesCompact(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

func TestChromeSysStatsFormattingAndDegradation(t *testing.T) {
	const (
		GiB = 1024 * 1024 * 1024
		TiB = 1024 * GiB
	)
	full := SysStats{
		Ready:          true,
		ProcCPUPct:     1.6,
		HasProcCPU:     true,
		SysCPUPct:      28.4,
		HasSysCPU:      true,
		RAMUsedPct:     64.2,
		RAMTotalBytes:  32 * GiB,
		HasRAM:         true,
		DiskFreeBytes:  142 * GiB,
		DiskTotalBytes: TiB,
		HasDisk:        true,
	}

	wantFull := "cpu 2%/28% · ram 64%/32G · disk 142G/1.0T"
	if got := FormatChromeSysStats(full, 80); got != wantFull {
		t.Fatalf("full format = %q, want %q", got, wantFull)
	}

	// Narrow enough to drop disk, keep cpu + ram.
	wantNoDisk := "cpu 2%/28% · ram 64%/32G"
	if got := FormatChromeSysStats(full, len([]rune(wantFull))-1); got != wantNoDisk {
		t.Fatalf("degraded (no disk) = %q, want %q", got, wantNoDisk)
	}

	// Narrow enough to keep only cpu.
	wantCPUOnly := "cpu 2%/28%"
	if got := FormatChromeSysStats(full, len([]rune(wantNoDisk))-1); got != wantCPUOnly {
		t.Fatalf("degraded (cpu only) = %q, want %q", got, wantCPUOnly)
	}

	// Too narrow for even cpu -> empty string.
	if got := FormatChromeSysStats(full, len([]rune(wantCPUOnly))-1); got != "" {
		t.Fatalf("too narrow = %q, want empty", got)
	}

	// Not ready or non-positive width -> empty string.
	if got := FormatChromeSysStats(SysStats{}, 80); got != "" {
		t.Fatalf("not ready = %q, want empty", got)
	}
	if got := FormatChromeSysStats(full, 0); got != "" {
		t.Fatalf("zero width = %q, want empty", got)
	}

	// Partial metrics: missing CPU omits cpu segment gracefully.
	noCPU := full
	noCPU.HasProcCPU = false
	noCPU.HasSysCPU = false
	if got := FormatChromeSysStats(noCPU, 80); got != "ram 64%/32G · disk 142G/1.0T" {
		t.Fatalf("noCPU = %q", got)
	}

	// Only sys CPU available before second rusage delta.
	sysOnly := full
	sysOnly.HasProcCPU = false
	if got := FormatChromeSysStats(sysOnly, 80); got != "cpu -/28% · ram 64%/32G · disk 142G/1.0T" {
		t.Fatalf("sysOnly = %q", got)
	}
}

func TestMergeChromeStatusLine(t *testing.T) {
	left := " NORMAL · panes"
	right := "cpu 2%/28% · ram 64%/32G · disk 142G/1.0T"

	// Wide line: right flush to width, gap >= 2.
	wide := MergeChromeStatusLine(left, right, 80)
	if !strings.HasPrefix(wide, left+"  ") || !strings.HasSuffix(wide, right) {
		t.Fatalf("wide merge unexpected: %q", wide)
	}
	if gotW := visibleWidth(wide); gotW != 80 {
		t.Fatalf("wide width = %d, want 80 (%q)", gotW, wide)
	}

	// Medium width: shrinks right (drops disk), preserves left untouched.
	mid := MergeChromeStatusLine(left, right, 45)
	if !strings.HasPrefix(mid, left+"  ") || !strings.HasSuffix(mid, "cpu 2%/28% · ram 64%/32G") {
		t.Fatalf("mid merge unexpected: %q", mid)
	}
	if gotW := visibleWidth(mid); gotW != 45 {
		t.Fatalf("mid width = %d, want 45 (%q)", gotW, mid)
	}

	// Tight width: shrinks right to cpu only.
	tight := MergeChromeStatusLine(left, right, 30)
	if !strings.HasPrefix(tight, left+"  ") || !strings.HasSuffix(tight, "cpu 2%/28%") {
		t.Fatalf("tight merge unexpected: %q", tight)
	}
	if gotW := visibleWidth(tight); gotW != 30 {
		t.Fatalf("tight width = %d, want 30 (%q)", gotW, tight)
	}

	// Too narrow: drops right completely, left never truncated.
	narrow := MergeChromeStatusLine(left, right, 24)
	if narrow != left {
		t.Fatalf("narrow merge = %q, want exact left %q", narrow, left)
	}

	// Left already wider than width: drops right, left untouched.
	longLeft := " NORMAL · panes · /some-very-long-filter-query"
	if got := MergeChromeStatusLine(longLeft, right, 20); got != longLeft {
		t.Fatalf("longLeft merge = %q, want %q", got, longLeft)
	}

	// ANSI-styled left measures visible width properly.
	styledLeft := "\x1b[1;34m NORMAL\x1b[0m"
	styled := MergeChromeStatusLine(styledLeft, "cpu 1%/5%", 24)
	if gotW := visibleWidth(styled); gotW != 24 {
		t.Fatalf("styled visible width = %d, want 24 (%q)", gotW, styled)
	}
}

func TestSysStatsParsersAndMath(t *testing.T) {
	// procCPUPercent
	if pct, ok := procCPUPercent(0, 450_000_000, 3*time.Second, 10); !ok || math.Abs(pct-1.5) > 0.01 {
		t.Fatalf("procCPUPercent(15%% of 1/10 cores) = %v, %v", pct, ok)
	}
	if pct, ok := procCPUPercent(0, 3_000_000_000, 3*time.Second, 10); !ok || math.Abs(pct-10.0) > 0.01 {
		t.Fatalf("procCPUPercent(1 full core of 10) = %v, %v", pct, ok)
	}
	if _, ok := procCPUPercent(500, 100, time.Second, 4); ok {
		t.Fatal("expected backwards counter to fail")
	}

	// cpuBusyPercent
	if got := cpuBusyPercent(cpuTicks{idle: 100, total: 200}, cpuTicks{idle: 175, total: 300}); got != 25 {
		t.Fatalf("cpuBusyPercent = %v, want 25", got)
	}
	if got := cpuBusyPercent(cpuTicks{idle: 50, total: 90}, cpuTicks{idle: 10, total: 100}); got != 0 {
		t.Fatalf("cpuBusyPercent backwards idle = %v, want 0", got)
	}

	// parseDarwinPSCPU
	psOut := "  12.5\n 100.0\n   7.5\n  0.0\n"
	if pct, ok := parseDarwinPSCPU(psOut, 4); !ok || math.Abs(pct-30.0) > 0.01 {
		t.Fatalf("parseDarwinPSCPU = %v, %v, want 30.0", pct, ok)
	}

	// parseDarwinVMStat (with compressor line)
	vmOut := strings.Join([]string{
		"Mach Virtual Memory Statistics: (page size of 16384 bytes)",
		"Pages free:                               32252.",
		"Pages active:                            611862.",
		"Pages inactive:                          595750.",
		"Pages speculative:                        16288.",
		"Pages wired down:                        209782.",
		"Pages purgeable:                          33555.",
		"Pages occupied by compressor:            595095.",
	}, "\n")
	total32G := uint64(32) * 1024 * 1024 * 1024
	if pct, ok := parseDarwinVMStat(vmOut, total32G, 4096); !ok || pct < 67 || pct > 68 {
		t.Fatalf("parseDarwinVMStat = %v, %v, want ~67.6%%", pct, ok)
	}

	// parseDarwinVMStat (fallback without compressor line)
	vmFallback := strings.Join([]string{
		"Mach Virtual Memory Statistics: (page size of 4096 bytes)",
		"Pages free:       262144.",
		"Pages inactive:   262144.",
		"Pages active:     524288.",
	}, "\n")
	total4G := uint64(4) * 1024 * 1024 * 1024
	if pct, ok := parseDarwinVMStat(vmFallback, total4G, 4096); !ok || math.Abs(pct-50.0) > 0.1 {
		t.Fatalf("parseDarwinVMStat fallback = %v, %v, want 50.0%%", pct, ok)
	}

	// Linux /proc/stat & /proc/meminfo
	procStat := "cpu  100 20 80 800 0 0 0 0 0 0\ncpu0 50 10 40 400 0 0 0 0 0 0\n"
	ticks, ok := parseProcStatCPUTicks(procStat)
	if !ok || ticks.idle != 800 || ticks.total != 1000 {
		t.Fatalf("parseProcStatCPUTicks = %+v, %v", ticks, ok)
	}
	meminfo := "MemTotal:       16777216 kB\nMemFree:         2000000 kB\nMemAvailable:    4194304 kB\n"
	usedPct, totalBytes, ok := parseProcMeminfo(meminfo)
	if !ok || totalBytes != 16*1024*1024*1024 || math.Abs(usedPct-75.0) > 0.01 {
		t.Fatalf("parseProcMeminfo = %v%%, %d, %v", usedPct, totalBytes, ok)
	}
}

type stubSysStatsReader struct {
	mu      sync.Mutex
	samples []SysStats
	calls   int
}

func (r *stubSysStatsReader) Read(_ context.Context, _ string) SysStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := r.calls
	r.calls++
	if len(r.samples) == 0 {
		return SysStats{}
	}
	if idx >= len(r.samples) {
		idx = len(r.samples) - 1
	}
	return r.samples[idx]
}

func TestSysStatsSamplerLifecycleAndDedup(t *testing.T) {
	const GiB = 1024 * 1024 * 1024
	s1 := SysStats{Ready: true, ProcCPUPct: 2, HasProcCPU: true, SysCPUPct: 20, HasSysCPU: true, RAMUsedPct: 50, RAMTotalBytes: 32 * GiB, HasRAM: true}
	s2Same := SysStats{Ready: true, ProcCPUPct: 2.1, HasProcCPU: true, SysCPUPct: 20.2, HasSysCPU: true, RAMUsedPct: 50.1, RAMTotalBytes: 32 * GiB, HasRAM: true}
	s3Diff := SysStats{Ready: true, ProcCPUPct: 4, HasProcCPU: true, SysCPUPct: 35, HasSysCPU: true, RAMUsedPct: 55, RAMTotalBytes: 32 * GiB, HasRAM: true}

	var updates atomic.Int32
	gotThird := make(chan struct{}, 1)
	sampler := newSysStatsSampler("/", 10*time.Millisecond, func() {
		n := updates.Add(1)
		if n == 2 {
			select {
			case gotThird <- struct{}{}:
			default:
			}
		}
	})
	sampler.reader = &stubSysStatsReader{samples: []SysStats{s1, s2Same, s3Diff}}

	// Before Start, Snapshot is empty and SetSnapshotForTest works deterministically.
	if snap := sampler.Snapshot(); snap.Ready {
		t.Fatalf("expected unready snapshot before Start, got %+v", snap)
	}
	sampler.SetSnapshotForTest(s1)
	if snap := sampler.Snapshot(); !snap.Ready || snap.SysCPUPct != 20 {
		t.Fatalf("SetSnapshotForTest failed: %+v", snap)
	}

	ctx, cancel := context.WithCancel(context.Background())
	sampler.Start(ctx)
	sampler.Start(ctx) // idempotent

	select {
	case <-gotThird:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for changed sample update (updates=%d)", updates.Load())
	}
	cancel()
	select {
	case <-sampler.done:
	case <-time.After(2 * time.Second):
		t.Fatal("sampler goroutine did not exit after ctx cancel")
	}

	// s1 -> update 1, s2Same -> deduplicated (no update), s3Diff -> update 2.
	if got := updates.Load(); got != 2 {
		t.Fatalf("onUpdate count = %d, want 2 (unchanged sample must not notify)", got)
	}
	if snap := sampler.Snapshot(); snap.SysCPUPct != 35 {
		t.Fatalf("final snapshot SysCPUPct = %v, want 35", snap.SysCPUPct)
	}
}

// A paused sampler (ui.chromeSystemStats off) reads nothing and wakes nobody;
// resuming samples at once.
func TestSysStatsSamplerPausedReadsNothing(t *testing.T) {
	reader := &stubSysStatsReader{samples: []SysStats{{Ready: true, SysCPUPct: 20, HasSysCPU: true}}}
	calls := func() int {
		reader.mu.Lock()
		defer reader.mu.Unlock()
		return reader.calls
	}
	var updates atomic.Int32
	sampler := newSysStatsSampler("/", 2*time.Millisecond, func() { updates.Add(1) })
	sampler.reader = reader
	sampler.SetPaused(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sampler.Start(ctx)
	time.Sleep(40 * time.Millisecond)
	if calls() != 0 || updates.Load() != 0 {
		t.Fatalf("paused sampler read %d times and notified %d times, want 0 and 0", calls(), updates.Load())
	}
	sampler.SetPaused(false)
	eventually(t, "a resumed sampler samples", func() bool { return calls() > 0 && updates.Load() == 1 })
}

func TestSysCPUSampleCadence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		age      time.Duration
		then, at float64
		haveLoad bool
		want     bool
	}{
		{"fresh reading is kept even when load jumps", sysCPUSampleMinTTL - time.Second, 1, 5, true, false},
		{"steady load keeps the reading", sysCPUSampleMinTTL + time.Second, 2.0, 2.1, true, false},
		{"moved load takes a new reading", sysCPUSampleMinTTL + time.Second, 2.0, 2.0 + sysCPULoadDelta, true, true},
		{"falling load counts as moved", sysCPUSampleMinTTL + time.Second, 2.0, 1.5, true, true},
		{"old reading is retaken on steady load", sysCPUSampleMaxTTL, 2.0, 2.0, true, true},
		{"without a load average the minimum age decides", sysCPUSampleMinTTL, 0, 0, false, true},
	} {
		if got := sysCPUSampleDue(tc.age, tc.then, tc.at, tc.haveLoad); got != tc.want {
			t.Errorf("%s: due = %t, want %t", tc.name, got, tc.want)
		}
	}
}

func TestSysStatsLivePlatformSample(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("live reader test runs on darwin/linux")
	}
	reader := newPlatformSysStatsReader()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Two samples ensure delta-based metrics populate cleanly on both darwin and linux.
	_ = reader.Read(ctx, "")
	time.Sleep(20 * time.Millisecond)
	snap := reader.Read(ctx, "")

	if !snap.Ready {
		t.Fatalf("expected Ready snapshot on %s, got %+v", runtime.GOOS, snap)
	}
	if !snap.HasRAM || snap.RAMTotalBytes == 0 {
		t.Errorf("expected valid RAM stats, got %+v", snap)
	}
	if !snap.HasDisk || snap.DiskTotalBytes == 0 {
		t.Errorf("expected valid Disk stats, got %+v", snap)
	}
	if !snap.HasSysCPU {
		t.Errorf("expected valid SysCPU stats, got %+v", snap)
	}
	if !snap.HasProcCPU {
		t.Errorf("expected valid ProcCPU stats, got %+v", snap)
	}
	formatted := FormatChromeSysStats(snap, 120)
	if formatted == "" {
		t.Fatalf("expected non-empty formatted chrome string for %+v", snap)
	}
	t.Logf("live %s sample: %s (raw=%+v)", runtime.GOOS, formatted, snap)
}

func TestChromeSysStatsAppIntegrationAndOptOut(t *testing.T) {
	a, _ := configured(t, "")
	a.Shell.W, a.Shell.H = 140, 40
	s := a.Shell.State
	sampler := newSysStatsSampler("", time.Hour, nil)
	sampler.SetSnapshotForTest(SysStats{
		Ready:          true,
		ProcCPUPct:     2,
		HasProcCPU:     true,
		SysCPUPct:      28,
		HasSysCPU:      true,
		RAMUsedPct:     64,
		RAMTotalBytes:  32 * (1 << 30),
		HasRAM:         true,
		DiskFreeBytes:  142 * (1 << 30),
		DiskTotalBytes: 1 << 40,
		HasDisk:        true,
	})
	a.rt.live.sysStats = sampler

	// Default (ui.chromeSystemStats = true, ui.topChromeRedesign = true): top chrome separator includes telemetry.
	root := a.View(s)
	stNode := root.Find("chrome.sep")
	if stNode == nil {
		stNode = root.Find("status")
	}
	if stNode == nil {
		t.Fatal("missing chrome.sep/status ViewNode")
	}
	got, _ := stNode.Props["text"].(string)
	wantSub := "cpu 2%/28% · ram 64%/32G · disk 142G/1.0T"
	if !strings.Contains(got, wantSub) {
		t.Fatalf("chrome line missing %q:\n  got: %q", wantSub, got)
	}

	// Opt-out (ui.chromeSystemStats = false): telemetry disappears cleanly.
	a.cfg.userSettings[SetChromeSystemStats] = false
	a.reloadSettings()
	a.applySettings()
	rootOff := a.View(s)
	stOff := rootOff.Find("chrome.sep")
	if stOff == nil {
		stOff = rootOff.Find("status")
	}
	if stOff == nil {
		t.Fatal("missing chrome.sep/status ViewNode when disabled")
	}
	gotOff, _ := stOff.Props["text"].(string)
	if strings.Contains(gotOff, "cpu ") || strings.Contains(gotOff, "ram ") || strings.Contains(gotOff, "disk ") {
		t.Fatalf("expected chrome stats hidden when %s=false, got: %q", SetChromeSystemStats, gotOff)
	}
}
