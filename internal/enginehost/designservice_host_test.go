package enginehost

import (
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/fleettest"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic names only. designHostController is the controller's host name, designHostAgent the listed host whose agent
// the fleet runs, and designHostUnlisted a name no host the controller runs work on answers to.
const (
	designHostController = "synthetic-controller"
	designHostAgent      = "synthetic-host-b"
	designHostUnlisted   = "synthetic-host-x"
)

// designHostPlacement returns the Placement of the controller, which places designHostAgent of fleet, with defaultHost
// as its default.
func designHostPlacement(fleet *fleettest.Fleet, defaultHost string) *Placement {
	return NewPlacement(PlacementConfig{
		Controller: designHostController,
		Default:    defaultHost,
		Hosts:      map[string]Reacher{designHostAgent: fleet.Clients[designHostAgent]},
	})
}

// designHostService returns the Designs service over st, which places each design's host with placement.
func designHostService(st *store.Store, placement HostPlacer) *designService {
	return &designService{designs: &Designs{Store: st, Now: designTestAt, Placement: placement}}
}

// createDesignOn returns the CreateDesign request for the design id titled "Hosted design" on the host named named,
// from the headless session.
func createDesignOn(id, named string) *connect.Request[v1.CreateDesignRequest] {
	req := connect.NewRequest(&v1.CreateDesignRequest{Id: id, Title: "Hosted design", Host: named})
	headlessCaller(req.Header())
	return req
}

// wantDesignHost creates the design id on the host named named through svc, and checks that the reply and the store
// both hold want as the design's host. It fails the test on error or on a host other than want.
func wantDesignHost(t *testing.T, st *store.Store, svc *designService, id, named, want string) {
	t.Helper()
	reply, err := svc.CreateDesign(t.Context(), createDesignOn(id, named))
	if err != nil {
		t.Fatalf("CreateDesign %s on %q: %v", id, named, err)
	}
	if host := reply.Msg.GetDesign().GetHost(); host != want {
		t.Errorf("CreateDesign %s reply host = %q, want %q", id, host, want)
	}
	record, err := st.Design(t.Context(), id)
	if err != nil {
		t.Fatalf("Design %s: %v", id, err)
	}
	if record.Host != want {
		t.Errorf("stored host of %s = %q, want %q", id, record.Host, want)
	}
}

// wantDesignRefusal checks that err is a Connect error with code whose message names host. It fails the test otherwise.
func wantDesignRefusal(t *testing.T, err error, code connect.Code, host string) {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error = %v, want a Connect error with code %v", err, code)
	}
	if connectErr.Code() != code {
		t.Errorf("code = %v, want %v", connectErr.Code(), code)
	}
	if !strings.Contains(connectErr.Message(), host) {
		t.Errorf("message = %q, want it to name %q", connectErr.Message(), host)
	}
}

// TestDesignCreateDefaultHostNoNameStoresDefault checks that a design that names no host takes the Placement's default,
// and that with no Placement it stores no host.
func TestDesignCreateDefaultHostNoNameStoresDefault(t *testing.T) {
	t.Parallel()
	fleet := fleettest.Start(t, designHostAgent)
	st := epicTestStore(t)
	seedContractSessions(t, st)

	wantDesignHost(t, st, designHostService(st, designHostPlacement(fleet, designHostAgent)),
		"design-default", "", designHostAgent)
	wantDesignHost(t, st, designHostService(st, nil), "design-no-placement", "", "")
}

// TestDesignCreateDefaultHostNamedReachable checks that a design that names a reachable listed host stores that host,
// though the Placement's default is the controller.
func TestDesignCreateDefaultHostNamedReachable(t *testing.T) {
	t.Parallel()
	fleet := fleettest.Start(t, designHostAgent)
	st := epicTestStore(t)
	seedContractSessions(t, st)

	wantDesignHost(t, st, designHostService(st, designHostPlacement(fleet, "")),
		"design-named", designHostAgent, designHostAgent)
}

// TestDesignCreateDefaultHostRefusesUnknownAndUnreachable checks that a design that names a host the controller does
// not run work on is refused as InvalidArgument, that one that names a listed host whose agent is down is refused as
// FailedPrecondition, that both messages name the host, and that a refused design stores nothing.
func TestDesignCreateDefaultHostRefusesUnknownAndUnreachable(t *testing.T) {
	t.Parallel()
	fleet := fleettest.Start(t, designHostAgent)
	st := epicTestStore(t)
	seedContractSessions(t, st)
	svc := designHostService(st, designHostPlacement(fleet, ""))

	_, err := svc.CreateDesign(t.Context(), createDesignOn("design-unknown", designHostUnlisted))
	wantDesignRefusal(t, err, connect.CodeInvalidArgument, designHostUnlisted)

	fleet.StopAgent(t, designHostAgent)
	fleet.WaitReach(t, designHostAgent, false)
	_, err = svc.CreateDesign(t.Context(), createDesignOn("design-unreachable", designHostAgent))
	wantDesignRefusal(t, err, connect.CodeFailedPrecondition, designHostAgent)

	if _, err = st.Design(t.Context(), "design-unknown"); !errors.Is(err, store.ErrNoDesign) {
		t.Errorf("Design after the unknown host's refusal: %v, want ErrNoDesign", err)
	}
	if _, err = st.Design(t.Context(), "design-unreachable"); !errors.Is(err, store.ErrNoDesign) {
		t.Errorf("Design after the unreachable host's refusal: %v, want ErrNoDesign", err)
	}

	_, err = designHostService(st, nil).CreateDesign(t.Context(), createDesignOn("design-bare", designHostAgent))
	wantDesignRefusal(t, err, connect.CodeInvalidArgument, designHostAgent)
}

// TestDesignCreateDefaultHostNoDefaultStoresController checks that with no default host a design that names no host
// stores the controller's host name, which its Placement places it on.
func TestDesignCreateDefaultHostNoDefaultStoresController(t *testing.T) {
	t.Parallel()
	fleet := fleettest.Start(t, designHostAgent)
	st := epicTestStore(t)
	seedContractSessions(t, st)

	wantDesignHost(t, st, designHostService(st, designHostPlacement(fleet, "")),
		"design-controller", "", designHostController)
}

// TestDesignHostGetAndListShowHost checks that a design stored on a named host reads back that host from GetDesign and
// from ListDesigns.
func TestDesignHostGetAndListShowHost(t *testing.T) {
	t.Parallel()
	fleet := fleettest.Start(t, designHostAgent)
	st := epicTestStore(t)
	seedContractSessions(t, st)
	svc := designHostService(st, designHostPlacement(fleet, ""))
	wantDesignHost(t, st, svc, "design-shown", designHostAgent, designHostAgent)

	got, err := svc.GetDesign(t.Context(), connect.NewRequest(&v1.GetDesignRequest{Id: "design-shown"}))
	if err != nil {
		t.Fatalf("GetDesign: %v", err)
	}
	if host := got.Msg.GetDesign().GetHost(); host != designHostAgent {
		t.Errorf("GetDesign host = %q, want %q", host, designHostAgent)
	}

	listed, err := svc.ListDesigns(t.Context(), connect.NewRequest(&v1.ListDesignsRequest{}))
	if err != nil {
		t.Fatalf("ListDesigns: %v", err)
	}
	found := false
	for _, design := range listed.Msg.GetDesigns() {
		if design.GetId() != "design-shown" {
			continue
		}
		found = true
		if host := design.GetHost(); host != designHostAgent {
			t.Errorf("ListDesigns host of design-shown = %q, want %q", host, designHostAgent)
		}
	}
	if !found {
		t.Fatalf("ListDesigns answered no design-shown")
	}
}
