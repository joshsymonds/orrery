package holder

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrExitExists means the exit file already exists, so the holder's exit record was already written.
var ErrExitExists = errors.New("exit file already exists")

// WriteExit writes exit as JSON to path. The file is created with mode 0600. WriteExit refuses with ErrExitExists
// when path exists and never replaces an existing file. It writes the record to a temporary file in path's
// directory and hard-links that file to path, so a reader sees either no file or the whole record. The temporary
// file is removed on every return.
func WriteExit(path string, exit Exit) (err error) {
	data, err := json.Marshal(exit)
	if err != nil {
		return fmt.Errorf("holder: encode exit: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("holder: create temporary exit file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if rmErr := os.Remove(tmpPath); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("holder: remove temporary exit file: %w", rmErr))
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return errors.Join(fmt.Errorf("holder: write temporary exit file: %w", err), tmp.Close())
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("holder: close temporary exit file: %w", err)
	}
	if err = os.Link(tmpPath, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrExitExists
		}
		return fmt.Errorf("holder: link exit %s: %w", path, err)
	}
	return nil
}

// ReadExit reads the exit record that WriteExit wrote to path.
func ReadExit(path string) (Exit, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Exit{}, fmt.Errorf("holder: read exit %s: %w", path, err)
	}
	var exit Exit
	if err = json.Unmarshal(data, &exit); err != nil {
		return Exit{}, fmt.Errorf("holder: decode exit %s: %w", path, err)
	}
	return exit, nil
}
