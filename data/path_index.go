package data

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func readPathIndex(path string, dest any) error {
	if path == "" {
		return nil
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("data: read path index %s: %w", path, err)
	}
	if err := json.Unmarshal(content, dest); err != nil {
		return fmt.Errorf("data: decode path index %s: %w", path, err)
	}
	return nil
}

func writePathIndex(path string, value any) error {
	if path == "" {
		return nil
	}
	content, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("data: encode path index %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("data: create path index dir %s: %w", dir, err)
	}
	return writeAtomicFile(path, content)
}
