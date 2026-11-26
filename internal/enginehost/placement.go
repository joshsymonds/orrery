package enginehost

import (
	"errors"

	"github.com/joshsymonds/orrery/internal/node"
)

// ErrUnreachableHost means a host that work names is listed in hosts.toml but its agent cannot be reached now. The
// sentinel that means a name none of the controller, its aliases or the listed hosts answers to, ErrUnknownHost, is
// declared in manager.go and is the one Place returns for such a name.
var ErrUnreachableHost = errors.New("enginehost: host unreachable")

// Reacher reports whether a remote host's agent answers now, and returns a channel that closes at the next change of
// that. *node.Client satisfies it.
type Reacher interface {
	Reach() (bool, <-chan struct{})
}

// Compile-time check that *node.Client is a Reacher.
var _ Reacher = (*node.Client)(nil)

// HostPlacer places work on the host it names, and names the default host, which work naming no host takes.
type HostPlacer interface {
	// Place returns the orrery name of the host that work naming named runs on. A name that is none of the controller,
	// its aliases or the listed hosts fails with an error matching ErrUnknownHost, and a listed host whose agent cannot
	// be reached fails with an error matching ErrUnreachableHost.
	Place(named string) (string, error)
	// Default returns the orrery name of the default host, which work naming no host takes. Empty means the controller.
	Default() string
}

// PlacementConfig is what a Placement places work by. The controller's name and its aliases are the names that mean the
// controller, and the hosts are the remote hosts of hosts.toml, by name.
type PlacementConfig struct {
	// Controller is the name the controller's Local is routed under.
	Controller string
	// Aliases are other names that mean the controller, such as hosts.toml's top-level name.
	Aliases []string
	// Default is the default host's name, as hosts.toml names it. Empty means the controller.
	Default string
	// Hosts holds hosts.toml's remote hosts by name.
	Hosts map[string]Reacher
}

// Placement is the HostPlacer over a PlacementConfig.
type Placement struct {
	cfg PlacementConfig
}

// NewPlacement returns the Placement over cfg.
func NewPlacement(cfg PlacementConfig) *Placement {
	return &Placement{cfg: cfg}
}

// Compile-time check that *Placement is a HostPlacer.
var _ HostPlacer = (*Placement)(nil)

// Place returns the orrery name of the host that work naming named runs on. It is not implemented yet.
func (p *Placement) Place(_ string) (string, error) {
	return "", errors.ErrUnsupported
}

// Default returns the orrery name of the default host, as the config names it.
func (p *Placement) Default() string {
	return p.cfg.Default
}
