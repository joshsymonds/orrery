// Package hosts reads the remote hosts that this controller reaches, from the machine's hosts file. The file is
// $XDG_CONFIG_HOME/orrery/hosts.toml when XDG_CONFIG_HOME is set, else $HOME/.config/orrery/hosts.toml.
//
// The file is TOML. A top-level name is the controller's orrery name. Each entry of the hosts array of tables names one
// remote host with name and address, where address is the host's agent as host:port:
//
//	name = "desktop"
//
//	[[hosts]]
//	name = "workstation"
//	address = "workstation.example.ts.net:7443"
package hosts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Host is one remote host: its name, and the address of its agent as host:port.
type Host struct {
	Name    string `toml:"name"`
	Address string `toml:"address"`
}

// File is the hosts file as read: the controller's orrery name and the remote hosts that it reaches, in file order.
type File struct {
	Name  string `toml:"name"`
	Hosts []Host `toml:"hosts"`
}

// Path returns the hosts file's path. It is $XDG_CONFIG_HOME/orrery/hosts.toml when XDG_CONFIG_HOME is set, else
// $HOME/.config/orrery/hosts.toml. It fails when neither XDG_CONFIG_HOME nor HOME is set.
func Path(getenv func(string) string) (string, error) {
	config := getenv("XDG_CONFIG_HOME")
	if config == "" {
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("neither XDG_CONFIG_HOME nor HOME is set")
		}
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "orrery", "hosts.toml"), nil
}

// Load reads and decodes the hosts file that Path names. An absent file, and a file that does not parse, are errors
// that name the file.
func Load(getenv func(string) string) (File, error) {
	path, err := Path(getenv)
	if err != nil {
		return File{}, err
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return File{}, fmt.Errorf("no hosts file %s", path)
	}
	if err != nil {
		return File{}, fmt.Errorf("read hosts file %s: %w", path, err)
	}
	var file File
	if _, err = toml.Decode(string(data), &file); err != nil {
		return File{}, fmt.Errorf("hosts file %s: %w", path, err)
	}
	return file, nil
}
