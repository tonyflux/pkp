package winframe

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// FileName is the saved frame's file in the config directory.
const FileName = "window.json"

// Load reads the frame Save wrote; nil without error when there is none.
func Load(path string) (*Rect, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Rect
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Save replaces the frame at path atomically, creating the directory.
func Save(path string, r Rect) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
