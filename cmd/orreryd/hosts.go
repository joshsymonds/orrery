package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostfollow"
	"github.com/joshsymonds/orrery/internal/hosts"
	"github.com/joshsymonds/orrery/internal/inapp"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// startHostFollows starts one hostfollow.Follow for each host of the hosts file that getenv locates, once st is open,
// and returns the func that stops them all and returns once each has returned. Each host's agent is reached by a client
// that dials with net.Dialer, keeps the default bound, and sends fleetSecret, the fleet secret that controllerHost read
// from the --fleet-secret file. Each follow's transitions are routed by hub as that host's pane notices
// (relay.observeRemote). With no store, or no hosts file, nothing starts and the func does nothing. A hosts file that
// does not load is logged, and nothing starts, since controllerHost has already refused the same file.
func startHostFollows(
	ctx context.Context, st *store.Store, getenv func(string) string, logger *slog.Logger, hub *relay,
	fleetSecret string,
) func() {
	if st == nil {
		return func() {}
	}
	file, err := loadHostsFile(getenv)
	if err != nil {
		logger.ErrorContext(ctx, "host follows: serving without them", "error", err)
		return func() {}
	}
	followCtx, cancel := context.WithCancel(ctx)
	var dialer net.Dialer
	var follows sync.WaitGroup
	for _, host := range file.Hosts {
		client := node.NewClient(node.ClientConfig{
			Name: host.Name, Address: host.Address, Dial: dialer.DialContext, Secret: fleetSecret, Logger: logger,
		})
		follows.Add(1)
		go func() {
			defer follows.Done()
			hostfollow.Follow(followCtx, hostfollow.Config{
				Host: host.Name, Client: client, Store: st, Now: time.Now, Retry: holder.SystemClock{}, Logger: logger,
				Transitions: func(name string, event node.PaneEvent) { hub.observeRemote(followCtx, name, event) },
			})
		}()
	}
	return func() {
		cancel()
		follows.Wait()
	}
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

// loadHostsFile returns the hosts file that getenv locates, as controllerHost reads it: the zero File when no path can
// be located, which is when neither XDG_CONFIG_HOME nor HOME is set, and the file's contents otherwise. A file that
// does not load is an error.
func loadHostsFile(getenv func(string) string) (hosts.File, error) {
	var file hosts.File
	if _, pathErr := hosts.Path(getenv); pathErr == nil {
		loaded, err := hosts.Load(getenv)
		if err != nil {
			return hosts.File{}, fmt.Errorf("load hosts: %w", err)
		}
		file = loaded
	}
	return file, nil
}

// hostClients returns one node.Client for each host of the hosts file that getenv locates, by the host's name. Each
// client dials its host's agent as startHostFollows dials it, and sends the fleet secret that the file at fleetSecretFile
// holds. Each client times its waits on clock, which node.NewClient takes as holder.SystemClock{} when clock is nil.
// startSessions routes each host's work through its client, and the caller-event delivery wakes panes through the same
// clients (see paneWaiters and holderHosts). A hosts file that does not load, or a fleet secret that fleetSecretOf
// refuses, is logged, and no client is returned. controllerHost and startHostFollows have already refused the same file
// and flag.
func hostClients(
	ctx context.Context, getenv func(string) string, logger *slog.Logger, fleetSecretFile string, clock holder.Clock,
) map[string]*node.Client {
	file, err := loadHostsFile(getenv)
	if err != nil {
		logger.ErrorContext(ctx, "remote hosts: serving without their agents", "error", err)
		return nil
	}
	secret, err := fleetSecretOf(file, fleetSecretFile)
	if err != nil {
		logger.ErrorContext(ctx, "remote hosts: serving without their agents", "error", err)
		return nil
	}
	var dialer net.Dialer
	clients := make(map[string]*node.Client, len(file.Hosts))
	for _, host := range file.Hosts {
		clients[host.Name] = node.NewClient(node.ClientConfig{
			Name: host.Name, Address: host.Address, Dial: dialer.DialContext, Secret: secret, Logger: logger, Clock: clock,
		})
	}
	return clients
}

// routedHosts returns the hostexec.Map that startSessions resolves each host's work through, and the node.Client of each
// hosts.toml host by the host's name. The controller's own Local is under hostName, and each client's RemoteHost is under
// its host's name, each passed through opts.wrapHost. A hosts file that does not load leaves the map with the controller
// alone and no clients (see hostClients).
func routedHosts(
	ctx context.Context, opts options, hostName string, getenv func(string) string, logger *slog.Logger,
) (hostexec.Map, map[string]*node.Client) {
	clients := hostClients(ctx, getenv, logger, opts.fleetSecret, opts.nodeClock)
	routed := hostexec.Map{hostName: opts.wrapHost(hostName, hostexec.NewLocal())}
	for name, client := range clients {
		routed[name] = opts.wrapHost(name, node.NewRemoteHost(client))
	}
	return routed, clients
}

// holderHosts returns the holderhost.Host of each client, by the host's name. The session manager starts a session on a
// remote host through the holder host of that name.
func holderHosts(clients map[string]*node.Client) map[string]holderhost.Host {
	hosts := make(map[string]holderhost.Host, len(clients))
	for name, client := range clients {
		hosts[name] = client
	}
	return hosts
}

// paneWaiters returns the enginehost.PaneWaker of each client, by the host's name. HostPaneWake wakes a pane on that host
// through it.
func paneWaiters(clients map[string]*node.Client) map[string]enginehost.PaneWaker {
	agents := make(map[string]enginehost.PaneWaker, len(clients))
	for name, client := range clients {
		agents[name] = client
	}
	return agents
}

// controllerPageNotices returns the PageNotices serve answers the Notices service with: it watches pages, reports each
// seen notice to router at now's time, and lists and marks read router's notice history. A pane key that names no host
// is the controller's own pane, named orrery, while the hosts file that getenv locates lists no other host, and is
// dropped once it lists one. A hosts file that does not load is an error.
func controllerPageNotices(
	pages *inapp.Hub, router *route.Router, orrery string, now func() time.Time, getenv func(string) string,
) (*enginehost.PageNotices, error) {
	file, err := loadHostsFile(getenv)
	if err != nil {
		return nil, err
	}
	return enginehost.NewPageNotices(
		pages, router, router, now, enginehost.WithControllerPanes(orrery, len(file.Hosts) > 0),
	), nil
}
