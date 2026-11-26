package main

import (
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/joshsymonds/orrery/internal/hosts"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/server"
)

// setServeHosts sets the controller's orrery name and one agent client for each host of the hosts file, in file
// order, on cfg. The name is the file's top-level name when it sets one, and the machine's host name otherwise, as
// orreryd names the controller. Each client dials its agent with net.Dialer and the default bound, and sends the fleet
// secret that the file at fleetSecretFile holds. Without a hosts file the name is the host name and no client is set,
// so serve is as it was before hosts.toml existed. A hosts file that lists a host with no usable fleet secret is an
// error, as fleetSecretOf gives it.
func setServeHosts(cfg *server.Config, getenv func(string) string, fleetSecretFile string) error {
	var file hosts.File
	if _, pathErr := hosts.Path(getenv); pathErr == nil {
		loaded, err := hosts.Load(getenv)
		if err != nil {
			return fmt.Errorf("serve: load hosts: %w", err)
		}
		file = loaded
	}
	secret, err := fleetSecretOf(file, fleetSecretFile)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	name := file.Name
	if name == "" {
		host, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("serve: host name: %w", err)
		}
		name = host
	}
	var dialer net.Dialer
	agents := make([]*node.Client, 0, len(file.Hosts))
	for _, host := range file.Hosts {
		agents = append(agents, node.NewClient(node.ClientConfig{
			Name:    host.Name,
			Address: host.Address,
			Dial:    dialer.DialContext,
			Secret:  secret,
		}))
	}
	cfg.HostName, cfg.Hosts = name, agents
	return nil
}

// fleetSecretOf returns the fleet secret that the file at path holds, which each client of a hosts file's agent sends.
// A file that lists no host needs none, so it returns "" and no error. A file that lists a host needs one: an unset
// path, or a file that node.ReadSecret refuses, is an error that names --fleet-secret.
func fleetSecretOf(file hosts.File, path string) (string, error) {
	if len(file.Hosts) == 0 {
		return "", nil
	}
	if path == "" {
		return "", errors.New("--fleet-secret: needed, since hosts.toml lists a host, the file of the fleet secret")
	}
	secret, err := node.ReadSecret(path)
	if err != nil {
		return "", fmt.Errorf("--fleet-secret: %w", err)
	}
	return secret, nil
}
