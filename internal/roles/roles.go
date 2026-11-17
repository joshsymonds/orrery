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

// roleFile is the role file as read: its path, its keys and values, and the metadata that names each value's type.
type roleFile struct {
	path  string
	roles map[string]any
	md    toml.MetaData
}

// Agent returns the agent that role runs under, read from the role file. The file is
// $XDG_CONFIG_HOME/orrery/roles.toml when XDG_CONFIG_HOME is set, else $HOME/.config/orrery/roles.toml. Agent only
// reads the file. An absent file, a file that does not parse, a role that names no agent, and a value that is not a
// string are errors, and each names the file.
func Agent(getenv func(string) string, role string) (string, error) {
	file, err := readRoleFile(getenv)
	if err != nil {
		return "", err
	}
	return file.agent(role)
}

// AgentOr returns the agent that role runs under, as Agent does, or the agent that fallback runs under when the role
// file has no key for role. A role file that sets role to a value that names no agent is an error, as it is for Agent,
// and so is every error Agent returns for fallback.
func AgentOr(getenv func(string) string, role, fallback string) (string, error) {
	file, err := readRoleFile(getenv)
	if err != nil {
		return "", err
	}
	if _, present := file.roles[role]; !present {
		return file.agent(fallback)
	}
	return file.agent(role)
}

// readRoleFile reads and decodes the role file. An absent file and a file that does not parse are errors that name it.
func readRoleFile(getenv func(string) string) (roleFile, error) {
	path, err := rolePath(getenv)
	if err != nil {
		return roleFile{}, err
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return roleFile{}, fmt.Errorf("no role file %s", path)
	}
	if err != nil {
		return roleFile{}, fmt.Errorf("read role file %s: %w", path, err)
	}
	var roles map[string]any
	md, err := toml.Decode(string(data), &roles)
	if err != nil {
		return roleFile{}, fmt.Errorf("role file %s: %w", path, err)
	}
	return roleFile{path: path, roles: roles, md: md}, nil
}

// agent returns the agent the file names for role. A value that is not a string, and a role that names no agent, are
// errors that name the file.
func (f roleFile) agent(role string) (string, error) {
	value, present := f.roles[role]
	agent, isString := value.(string)
	if present && !isString {
		return "", fmt.Errorf("role file %s: %s must be a string, not %s", f.path, role, f.md.Type(role))
	}
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return "", fmt.Errorf("role file %s names no %s agent", f.path, role)
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
