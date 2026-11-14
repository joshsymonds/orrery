// Package roles reads the agent each role runs under from the machine's role file: a TOML file whose top-level keys
// are role names and whose values are agent names.
package roles

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Agent returns the agent that role runs under, read from the role file. The file is
// $XDG_CONFIG_HOME/orrery/roles.toml when XDG_CONFIG_HOME is set, else $HOME/.config/orrery/roles.toml. Agent only
// reads the file. An absent file, a file that does not parse, a role that names no agent, and a value that is not a
// string are errors, and each names the file.
func Agent(getenv func(string) string, role string) (string, error) {
	path, err := rolePath(getenv)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("no role file %s", path)
	}
	if err != nil {
		return "", fmt.Errorf("read role file %s: %w", path, err)
	}
	var roles map[string]any
	md, err := toml.Decode(string(data), &roles)
	if err != nil {
		return "", fmt.Errorf("role file %s: %w", path, err)
	}
	value, present := roles[role]
	agent, isString := value.(string)
	if present && !isString {
		return "", fmt.Errorf("role file %s: %s must be a string, not %s", path, role, md.Type(role))
	}
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return "", fmt.Errorf("role file %s names no %s agent", path, role)
	}
	return agent, nil
}

// rolePath returns the role file's path, or an error when neither XDG_CONFIG_HOME nor HOME is set.
func rolePath(getenv func(string) string) (string, error) {
	config := getenv("XDG_CONFIG_HOME")
	if config == "" {
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("neither XDG_CONFIG_HOME nor HOME is set")
		}
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "orrery", "roles.toml"), nil
}
