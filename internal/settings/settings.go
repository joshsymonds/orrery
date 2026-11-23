// Package settings reads the machine's settings file: a TOML file whose [contract] table sets the length thresholds a
// contract's propose and revise warn at. It imports only the standard library and the TOML decoder, so any package can
// read it.
package settings

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// The thresholds a contract warns at when the settings file does not set them: a warning from 41 lines above the
// checks, and a stronger one from 61.
const (
	defaultWarnLines       = 41
	defaultStrongWarnLines = 61
)

// Lengths are the contract length thresholds, in lines above a contract's checks.
type Lengths struct {
	WarnLines       int
	StrongWarnLines int
}

// fileContract is the settings file as read: its [contract] table, each key a pointer so an absent key is nil.
type fileContract struct {
	Contract struct {
		WarnLines       *int64 `toml:"warn_lines"`
		StrongWarnLines *int64 `toml:"strong_warn_lines"`
	} `toml:"contract"`
}

// ContractLengths returns the contract length thresholds the [contract] table of the settings file sets: warn_lines and
// strong_warn_lines, each defaulting to 41 and 61 when the file or the key is absent. The file is
// $XDG_CONFIG_HOME/orrery/settings.toml when XDG_CONFIG_HOME is set, else $HOME/.config/orrery/settings.toml. A file
// that does not parse, and a value that is not a positive integer, are errors that name the file.
func ContractLengths(getenv func(string) string) (Lengths, error) {
	path, err := settingsPath(getenv)
	if err != nil {
		return Lengths{}, err
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return Lengths{WarnLines: defaultWarnLines, StrongWarnLines: defaultStrongWarnLines}, nil
	}
	if err != nil {
		return Lengths{}, fmt.Errorf("read settings file %s: %w", path, err)
	}
	var file fileContract
	if _, err = toml.Decode(string(data), &file); err != nil {
		return Lengths{}, fmt.Errorf("settings file %s: %w", path, err)
	}
	warn, err := threshold(path, "warn_lines", file.Contract.WarnLines, defaultWarnLines)
	if err != nil {
		return Lengths{}, err
	}
	strong, err := threshold(path, "strong_warn_lines", file.Contract.StrongWarnLines, defaultStrongWarnLines)
	if err != nil {
		return Lengths{}, err
	}
	return Lengths{WarnLines: warn, StrongWarnLines: strong}, nil
}

// threshold returns the value of key in the settings file at path: fallback when it is nil, and an error naming the
// file when it is not a positive integer.
func threshold(path, key string, value *int64, fallback int) (int, error) {
	if value == nil {
		return fallback, nil
	}
	if *value <= 0 {
		return 0, fmt.Errorf("settings file %s: contract.%s must be a positive integer, not %d", path, key, *value)
	}
	return int(*value), nil
}

// settingsPath returns the settings file's path, or an error when neither XDG_CONFIG_HOME nor HOME is set.
func settingsPath(getenv func(string) string) (string, error) {
	config := getenv("XDG_CONFIG_HOME")
	if config == "" {
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("neither XDG_CONFIG_HOME nor HOME is set")
		}
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "orrery", "settings.toml"), nil
}
