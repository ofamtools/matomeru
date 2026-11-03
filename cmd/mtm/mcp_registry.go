//go:build darwin || linux

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

type mcpProcessIdentity struct {
	PID         int    `json:"pid"`
	StartID     string `json:"start_id,omitempty"`
	StartSource string `json:"start_source,omitempty"`
	StartError  string `json:"start_error,omitempty"`
}

type mcpServerRecord struct {
	Version         int                `json:"version"`
	LaunchID        string             `json:"launch_id"`
	DataPath        string             `json:"data_path"`
	ConfigDir       string             `json:"config_dir"`
	Server          mcpProcessIdentity `json:"server"`
	Launcher        mcpProcessIdentity `json:"launcher"`
	LauncherSource  string             `json:"launcher_source"`
	ClientOwnership string             `json:"client_ownership"`
}

func registerMCP(dataPath, configDir string) (func() error, error) {
	data, err := filepath.Abs(dataPath)
	if err == nil {
		data, err = filepath.EvalSymlinks(data)
	}
	if err != nil {
		return nil, fmt.Errorf("MCP registry database path: %w", err)
	}
	config, err := filepath.Abs(configDir)
	if err != nil {
		return nil, fmt.Errorf("MCP registry config path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(config); err == nil {
		config = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("MCP registry config path: %w", err)
	}
	self := mcpIdentifyProcess(os.Getpid())
	if self.StartError != "" {
		return nil, fmt.Errorf("MCP registry server identity: %s", self.StartError)
	}
	parent := os.Getppid()
	launcher := mcpIdentifyProcess(parent)
	if os.Getppid() != parent {
		launcher = mcpProcessIdentity{PID: parent, StartError: "parent changed while collecting process identity"}
	}
	record := mcpServerRecord{
		Version: 1, DataPath: data, ConfigDir: config, Server: self, Launcher: launcher,
		LauncherSource:  "os.Getppid at registration",
		ClientOwnership: "unverified: the observed parent may be a wrapper or a reparented process; inherited stdio does not identify the original client",
	}
	dir, err := mcpRegistryDirectory(data)
	if err != nil {
		return nil, fmt.Errorf("MCP registry directory: %w", err)
	}
	remove, err := createMCPRecord(dir, record, rand.Reader)
	if err != nil {
		return nil, errors.Join(err, dir.Close())
	}
	var once sync.Once
	var cleanupErr error
	return func() error {
		once.Do(func() { cleanupErr = errors.Join(remove(), dir.Close()) })
		return cleanupErr
	}, nil
}

func mcpIdentifyProcess(pid int) mcpProcessIdentity {
	id := mcpProcessIdentity{PID: pid}
	var err error
	id.StartID, id.StartSource, err = mcpProcessStart(pid)
	if err != nil {
		id.StartID, id.StartSource, id.StartError = "", "", err.Error()
	}
	return id
}

// Pin each registry directory by descriptor. Never follow registry symlinks,
// repair permissions, or chmod the selected database's existing directory.
func mcpRegistryDirectory(data string) (*os.File, error) {
	fd, err := unix.Open(filepath.Dir(data), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parent := os.NewFile(uintptr(fd), filepath.Dir(data))
	defer parent.Close()
	root, err := mcpPrivateDirectory(parent, filepath.Base(data)+".mcp")
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return mcpPrivateDirectory(root, "servers")
}

func mcpPrivateDirectory(parent *os.File, name string) (*os.File, error) {
	if err := unix.Mkdirat(int(parent.Fd()), name, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return nil, err
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), name)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, errors.Join(err, dir.Close())
	}
	if stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o7777 != 0o700 {
		return nil, errors.Join(fmt.Errorf("%s must be owned by uid %d with mode 0700", name, os.Geteuid()), dir.Close())
	}
	return dir, nil
}

// entropy is supplied by crypto/rand in production. O_EXCL distinguishes an
// actual name collision from every other failure and never follows the entry.
func createMCPRecord(dir *os.File, record mcpServerRecord, entropy io.Reader) (func() error, error) {
	for {
		var id [16]byte
		if _, err := io.ReadFull(entropy, id[:]); err != nil {
			return nil, fmt.Errorf("MCP registry launch id: %w", err)
		}
		record.LaunchID = hex.EncodeToString(id[:])
		name := record.LaunchID + ".json"
		fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("MCP registry create: %w", err)
		}
		file := os.NewFile(uintptr(fd), name)
		var created unix.Stat_t
		if err := unix.Fstat(fd, &created); err != nil {
			return nil, errors.Join(err, file.Close())
		}
		// Keep the created inode alive after the writer closes, so a replacement
		// cannot reuse its inode number and pass the cleanup identity check.
		pinFD, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
		if err != nil {
			return nil, errors.Join(err, removeMCPRecord(dir, name, created), file.Close())
		}
		pin := os.NewFile(uintptr(pinFD), name)
		var once sync.Once
		var cleanupErr error
		remove := func() error {
			once.Do(func() { cleanupErr = errors.Join(removeMCPRecord(dir, name, created), pin.Close()) })
			return cleanupErr
		}
		writeErr := file.Chmod(0o600) // Only the newly created inode; independent of umask.
		if writeErr == nil {
			writeErr = json.NewEncoder(file).Encode(record)
		}
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return nil, errors.Join(fmt.Errorf("MCP registry write: %w", err), remove())
		}
		return remove, nil
	}
}

func removeMCPRecord(dir *os.File, name string, created unix.Stat_t) error {
	var current unix.Stat_t
	err := unix.Fstatat(int(dir.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Mode&unix.S_IFMT != unix.S_IFREG || current.Dev != created.Dev || current.Ino != created.Ino {
		return nil
	}
	if err := unix.Unlinkat(int(dir.Fd()), name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	return nil
}
