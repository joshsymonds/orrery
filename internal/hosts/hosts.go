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
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// Load reads and decodes the hosts file that Path names. A missing file is the zero File with no error, so a machine
// without hosts reads as it did before hosts.toml existed. Any other file is refused, with an error that names the
// file and the offending entry, when it does not parse, has a key that File does not declare, or breaks a rule: the
// top-level name, when set, and each host's name must be lowercase letters, digits and hyphens; host names are unique
// and none equals the top-level name; and each address is host:port with a non-empty host and a port from 1 to 65535.
// Hosts are returned in file order.
func Load(getenv func(string) string) (File, error) {
	path, err := Path(getenv)
	if err != nil {
		return File{}, err
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return File{}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("read hosts file %s: %w", path, err)
	}
	var file File
	meta, err := toml.Decode(string(data), &file)
	if err != nil {
		return File{}, fmt.Errorf("hosts file %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		return File{}, fmt.Errorf("hosts file %s: unknown key %s", path, strings.Join(keys, ", "))
	}
	err = validate(path, meta, file)
	if err != nil {
		return File{}, err
	}
	return file, nil
}

// validate refuses a decoded file that breaks a rule of Load. meta says whether the top-level name is set.
func validate(path string, meta toml.MetaData, file File) error {
	if meta.IsDefined("name") && !validName(file.Name) {
		return fmt.Errorf("hosts file %s: name %q is not lowercase letters, digits and hyphens", path, file.Name)
	}
	seen := make(map[string]bool, len(file.Hosts))
	for i, host := range file.Hosts {
		if !validName(host.Name) {
			return fmt.Errorf("hosts file %s: host %d: name %q is not lowercase letters, digits and hyphens",
				path, i+1, host.Name)
		}
		if host.Name == file.Name {
			return fmt.Errorf("hosts file %s: host %q is named as the controller", path, host.Name)
		}
		if seen[host.Name] {
			return fmt.Errorf("hosts file %s: host %q is named twice", path, host.Name)
		}
		seen[host.Name] = true
		if err := checkAddress(host.Address); err != nil {
			return fmt.Errorf("hosts file %s: host %q: %w", path, host.Name, err)
		}
	}
	return nil
}

// validName reports whether name is non-empty and made only of lowercase letters, digits and hyphens.
func validName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// checkAddress reports whether address is host:port with a non-empty host and a numeric port from 1 to 65535.
func checkAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("address %q: %w", address, err)
	}
	if host == "" {
		return fmt.Errorf("address %q has no host", address)
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return fmt.Errorf("address %q: port %q is not a number from 1 to 65535", address, port)
	}
	return nil
}
