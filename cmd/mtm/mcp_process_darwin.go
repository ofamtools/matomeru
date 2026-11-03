package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func mcpProcessStart(pid int) (string, string, error) {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", "", err
	}
	start := proc.Proc.P_starttime
	if int(proc.Proc.P_pid) != pid || start.Sec <= 0 || start.Usec < 0 || start.Usec >= 1_000_000 {
		return "", "", fmt.Errorf("kernel returned no valid birth identity for pid %d", pid)
	}
	return fmt.Sprintf("%d.%06d", start.Sec, start.Usec), "darwin:kern.proc.pid:P_starttime", nil
}
