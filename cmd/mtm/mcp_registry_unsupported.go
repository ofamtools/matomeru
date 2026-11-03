//go:build !darwin && !linux

package main

import (
	"fmt"
	"runtime"
)

func registerMCP(dataPath, configDir string) (func() error, error) {
	return nil, fmt.Errorf("MCP ownership registry: precise process birth identity is unsupported on %s", runtime.GOOS)
}
