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
