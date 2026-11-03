package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func mcpProcessStart(pid int) (string, string, error) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", "", err
	}
	// comm (field 2) can contain spaces and parentheses; field 22 follows
	// the final closing parenthesis, not the second whitespace token.
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return "", "", fmt.Errorf("invalid proc stat for pid %d", pid)
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) <= 19 {
		return "", "", fmt.Errorf("missing proc start ticks for pid %d", pid)
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", "", fmt.Errorf("invalid proc start ticks for pid %d: %w", pid, err)
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", "", err
	}
	bootID := strings.TrimSpace(string(boot))
	if bootID == "" {
		return "", "", fmt.Errorf("kernel boot id is empty")
	}
	return bootID + "/" + fields[19], "linux:boot_id+/proc/pid/stat:starttime_ticks", nil
}
