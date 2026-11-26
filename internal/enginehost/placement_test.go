package enginehost

import (
	"errors"
	"strings"
	"testing"
)

// Synthetic names only. placementController is the controller's name, placementAlias the top-level name that also means
// it, and placementHostA and placementHostB the listed hosts.
const (
	placementController = "synthetic-controller"
	placementAlias      = "synthetic-desk"
	placementHostA      = "synthetic-host-a"
	placementHostB      = "synthetic-host-b"
)

// fakePlacementHost is a Reacher that reports a fixed reachability and a fixed load. Placement reads only Reach.
type fakePlacementHost struct {
	reachable bool
	load      float64
}

// Reach reports the fake's reachability. The channel is nil, since Placement never waits on a change.
func (f fakePlacementHost) Reach() (bool, <-chan struct{}) {
	return f.reachable, nil
}

// Load reports the fake's load, as a percentage, for the test that shows placement ignores it.
func (f fakePlacementHost) Load() float64 {
	return f.load
}

// placementHosts returns the listed hosts with a and b reachable or not as given.
func placementHosts(aReachable, bReachable bool) map[string]Reacher {
	return map[string]Reacher{
		placementHostA: fakePlacementHost{reachable: aReachable},
		placementHostB: fakePlacementHost{reachable: bReachable},
	}
}

// placementWith returns the Placement of the controller, its alias and the given default and hosts.
func placementWith(defaultHost string, hosts map[string]Reacher) *Placement {
	return NewPlacement(PlacementConfig{
		Controller: placementController, Aliases: []string{placementAlias}, Default: defaultHost, Hosts: hosts,
	})
}

// TestPlacementDefaultHostNoNamePlacesDefault shows that work naming no host takes the default host, and that a default
// that is the controller's alias places the controller.
func TestPlacementDefaultHostNoNamePlacesDefault(t *testing.T) {
	t.Run("a listed host", func(t *testing.T) {
		p := placementWith(placementHostB, placementHosts(true, true))
		got, err := p.Place("")
		if err != nil {
			t.Fatalf("Place(\"\"): %v", err)
		}
		if got != placementHostB {
			t.Errorf("Place(\"\") = %q, want the default %q", got, placementHostB)
		}
		if def := p.Default(); def != placementHostB {
			t.Errorf("Default() = %q, want %q", def, placementHostB)
		}
	})
	t.Run("the alias", func(t *testing.T) {
		p := placementWith(placementAlias, placementHosts(true, true))
		got, err := p.Place("")
		if err != nil {
			t.Fatalf("Place(\"\"): %v", err)
		}
		if got != placementController {
			t.Errorf("Place(\"\") = %q, want the controller %q", got, placementController)
		}
		if def := p.Default(); def != placementController {
			t.Errorf("Default() = %q, want the controller %q", def, placementController)
		}
	})
}

// TestPlacementDefaultHostNamedReachable shows that a name that is the controller or its alias places the controller,
// and that a listed reachable host places its own name, not the default.
func TestPlacementDefaultHostNamedReachable(t *testing.T) {
	p := placementWith(placementHostB, placementHosts(true, true))
	for _, named := range []string{placementController, placementAlias} {
		got, err := p.Place(named)
		if err != nil {
			t.Fatalf("Place(%q): %v", named, err)
		}
		if got != placementController {
			t.Errorf("Place(%q) = %q, want the controller %q", named, got, placementController)
		}
	}
	got, err := p.Place(placementHostA)
	if err != nil {
		t.Fatalf("Place(%q): %v", placementHostA, err)
	}
	if got != placementHostA {
		t.Errorf("Place(%q) = %q, want %q, not the default", placementHostA, got, placementHostA)
	}
}

// TestPlacementDefaultHostRefusesUnknownAndUnreachable shows that a name that is none of the controller, its alias or
// the listed hosts fails as unknown, and that a listed host that is unreachable fails as unreachable, each error naming
// the host. A default that is unreachable is refused like a named one: nothing falls back to another host.
func TestPlacementDefaultHostRefusesUnknownAndUnreachable(t *testing.T) {
	t.Run("unknown name", func(t *testing.T) {
		p := placementWith(placementHostB, placementHosts(true, true))
		got, err := p.Place("synthetic-nowhere")
		if !errors.Is(err, ErrUnknownHost) {
			t.Fatalf("Place(unknown) error = %v, want one matching ErrUnknownHost", err)
		}
		if !strings.Contains(err.Error(), "synthetic-nowhere") {
			t.Errorf("Place(unknown) error %q does not name the host", err)
		}
		if got != "" {
			t.Errorf("Place(unknown) = %q, want no placement", got)
		}
	})
	t.Run("unreachable named host", func(t *testing.T) {
		p := placementWith(placementHostB, placementHosts(false, true))
		got, err := p.Place(placementHostA)
		if !errors.Is(err, ErrUnreachableHost) {
			t.Fatalf("Place(%q) error = %v, want one matching ErrUnreachableHost", placementHostA, err)
		}
		if !strings.Contains(err.Error(), placementHostA) {
			t.Errorf("Place(%q) error %q does not name the host", placementHostA, err)
		}
		if got != "" {
			t.Errorf("Place(%q) = %q, want no placement", placementHostA, got)
		}
	})
	t.Run("unreachable default is not replaced", func(t *testing.T) {
		p := placementWith(placementHostB, placementHosts(true, false))
		got, err := p.Place("")
		if !errors.Is(err, ErrUnreachableHost) {
			t.Fatalf("Place(\"\") with the default unreachable error = %v, want one matching ErrUnreachableHost", err)
		}
		if !strings.Contains(err.Error(), placementHostB) {
			t.Errorf("Place(\"\") error %q does not name the default host", err)
		}
		if got != "" {
			t.Errorf("Place(\"\") = %q, want no placement", got)
		}
	})
}

// TestPlacementDefaultHostNoDefaultPlacesController shows that with no default set, work naming no host places the
// controller, and that Default reports no name.
func TestPlacementDefaultHostNoDefaultPlacesController(t *testing.T) {
	p := placementWith("", placementHosts(true, true))
	got, err := p.Place("")
	if err != nil {
		t.Fatalf("Place(\"\"): %v", err)
	}
	if got != placementController {
		t.Errorf("Place(\"\") = %q, want the controller %q", got, placementController)
	}
	if def := p.Default(); def != "" {
		t.Errorf("Default() = %q, want no default", def)
	}
}

// TestPlacementDefaultHostIgnoresLoad shows that the default is placed whatever the load: host b reports 100% load and
// host a 0%, and Place("") still places b.
func TestPlacementDefaultHostIgnoresLoad(t *testing.T) {
	hosts := map[string]Reacher{
		placementHostA: fakePlacementHost{reachable: true, load: 0},
		placementHostB: fakePlacementHost{reachable: true, load: 100},
	}
	if load := hosts[placementHostB].(fakePlacementHost).Load(); load != 100 {
		t.Fatalf("fake %s load = %v, want 100", placementHostB, load)
	}
	if load := hosts[placementHostA].(fakePlacementHost).Load(); load != 0 {
		t.Fatalf("fake %s load = %v, want 0", placementHostA, load)
	}
	p := placementWith(placementHostB, hosts)
	got, err := p.Place("")
	if err != nil {
		t.Fatalf("Place(\"\"): %v", err)
	}
	if got != placementHostB {
		t.Errorf("Place(\"\") = %q, want the default %q whatever its load", got, placementHostB)
	}
}
