package main

import (
	"fmt"
	"net"
	"os"

	"github.com/joshsymonds/orrery/internal/hosts"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/server"
)

// setServeHosts sets the controller's orrery name and one agent client for each host of the hosts file, in file
// order, on cfg. The name is the file's top-level name when it sets one, and the machine's host name otherwise, as
// orreryd names the controller. Each client dials its agent with net.Dialer and the default bound. Without a hosts
// file the name is the host name and no client is set, so serve is as it was before hosts.toml existed.
func setServeHosts(cfg *server.Config, getenv func(string) string) error {
	var file hosts.File
	if _, pathErr := hosts.Path(getenv); pathErr == nil {
		loaded, err := hosts.Load(getenv)
		if err != nil {
			return fmt.Errorf("serve: load hosts: %w", err)
		}
		file = loaded
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
		}))
	}
	cfg.HostName, cfg.Hosts = name, agents
	return nil
}
