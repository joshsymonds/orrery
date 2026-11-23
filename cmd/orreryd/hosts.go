package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/hostfollow"
	"github.com/joshsymonds/orrery/internal/hosts"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/store"
)

// startHostFollows starts one hostfollow.Follow for each host of the hosts file that getenv locates, once st is open,
// and returns the func that stops them all and returns once each has returned. Each host's agent is reached by a client
// that dials with net.Dialer and keeps the default bound. With no store, or no hosts file, nothing starts and the func
// does nothing. A hosts file that does not load is logged, and nothing starts, since controllerHost has already refused
// the same file.
func startHostFollows(ctx context.Context, st *store.Store, getenv func(string) string, logger *slog.Logger) func() {
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
			Name: host.Name, Address: host.Address, Dial: dialer.DialContext, Logger: logger,
		})
		follows.Add(1)
		go func() {
			defer follows.Done()
			hostfollow.Follow(followCtx, hostfollow.Config{
				Host: host.Name, Client: client, Store: st, Now: time.Now, Retry: holder.SystemClock{}, Logger: logger,
			})
		}()
	}
	return func() {
		cancel()
		follows.Wait()
	}
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
