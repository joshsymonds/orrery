package stage

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/fleettest"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostrepo"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// designHostID is the id of the design the design host tests store their steps on.
const designHostID = "design-host"

// designHostRoles is the role file the design host tests run their scout, test runner and steelman under.
const designHostRoles = `scout = "scout-agent"` + "\n" + `test_runner = "runner-agent"` + "\n" +
	`steelman = "steelman-agent"` + "\n"

// hostStarter is a StepStarter that records each start it is given and stores a step row for it, as Steps.Start does:
// the row holds the start's host, under an id it counts up from.
type hostStarter struct {
	store  *store.Store
	starts []enginehost.StepStart
}

// Start records start and stores its step row, with start's host, and returns the row's id.
func (f *hostStarter) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	id := fmt.Sprintf("%032x", len(f.starts)+1)
	f.starts = append(f.starts, start)
	add := store.StepAdd{
		ID: id, Agent: start.Agent, Prompt: start.Prompt, Schema: start.Schema, Workspace: start.Workspace,
		Revision: start.Revision, ReadOnly: start.ReadOnly, DoneValue: start.DoneValue, Epic: start.Epic,
		Task: start.Task, Caller: start.Caller, Host: start.Host,
	}
	if err := f.store.ApplyEngine(ctx, []store.EngineOp{add}, stageResearchNow()); err != nil {
		return "", fmt.Errorf("store step %s: %w", id, err)
	}
	return id, nil
}

// designHostFixture is a Stage service over a store, with the fleet's hosts, roots and controller, and a controller
// repository that is a clone of the fleet's origin.
type designHostFixture struct {
	fleet   *fleettest.Fleet
	store   *store.Store
	starter *hostStarter
	service *Service
	repo    string
}

// newDesignHostFixture returns a fixture over a fleet of the agents named. Its service resolves the agents' hosts and
// roots, and names the fleet's controller as the controller.
func newDesignHostFixture(t *testing.T, agents ...string) designHostFixture {
	t.Helper()
	fleet := fleettest.Start(t, agents...)
	s := openStageStore(t)
	repo := cloneControllerRepo(t, fleet.Origin)
	roots := make(map[string]hostrepo.RootLister, len(agents))
	for _, name := range agents {
		roots[name] = fleet.Clients[name]
	}
	starter := &hostStarter{store: s}
	service := NewService(Config{
		Store: s, Steps: starter, Now: stageResearchNow, Getenv: stageRoleHome(t, designHostRoles),
		Hosts: fleet.Hosts, Controller: fleet.Controller, Roots: roots,
	})
	return designHostFixture{fleet: fleet, store: s, starter: starter, service: service, repo: repo}
}

// cloneControllerRepo clones origin on this machine into a directory of its own, named repo, and returns the clone's
// path.
func cloneControllerRepo(t *testing.T, origin string) string {
	t.Helper()
	dir := t.TempDir()
	gitOn(t, dir, "clone", "-q", origin, "repo")
	return filepath.Join(dir, "repo")
}

// gitOn runs git with args in dir on this machine, and returns its stdout with the surrounding space trimmed. It fails
// the test when git fails.
func gitOn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := hostexec.NewLocal().Git(t.Context(), hostexec.GitCall{Dir: dir, Args: args})
	if err != nil {
		t.Fatalf("git %s in %s: %v", strings.Join(args, " "), dir, err)
	}
	return strings.TrimSpace(string(out))
}

// createDesign stores the design designHostID on host.
func (fx designHostFixture) createDesign(t *testing.T, host string) {
	t.Helper()
	if err := fx.store.CreateDesign(t.Context(), store.Design{
		ID: designHostID, Title: "Design one", Conversation: "conversation-one", Host: host,
		Created: stageResearchNow(),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
}

// designHostWriter is the id of the headless session the design host tests create a design from.
const designHostWriter = "design-host-writer"

// createDefaultDesign creates the design designHostID through orreryd's Designs service, from a headless session,
// naming no host, with the controller's Placement listing host b and taking b as its default. It checks that the stored
// design holds b.
func (fx designHostFixture) createDefaultDesign(t *testing.T) {
	t.Helper()
	writer := store.EngineAdd{ID: designHostWriter, Dir: "/work/engine"}
	if err := fx.store.ApplyEngine(t.Context(), []store.EngineOp{writer}, stageResearchNow()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	placement := enginehost.NewPlacement(enginehost.PlacementConfig{
		Controller: fx.fleet.Controller, Default: "b",
		Hosts: map[string]enginehost.Reacher{"b": fx.fleet.Clients["b"]},
	})
	server := httptest.NewServer(enginehost.NewHandler(enginehost.ServeConfig{
		Designs: &enginehost.Designs{Store: fx.store, Now: stageResearchNow, Placement: placement},
	}))
	t.Cleanup(server.Close)
	req := connect.NewRequest(&v1.CreateDesignRequest{Id: designHostID, Title: "Design one"})
	req.Header().Set(protocol.HeaderSession, designHostWriter)
	designs := enginev1connect.NewDesignsClient(server.Client(), server.URL)
	if _, err := designs.CreateDesign(t.Context(), req); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	record, err := fx.store.Design(t.Context(), designHostID)
	if err != nil {
		t.Fatalf("Design: %v", err)
	}
	if record.Host != "b" {
		t.Errorf("stored host of a design that names no host = %q, want the default b", record.Host)
	}
}

// proposeDesignHostContract proposes a contract on designHostID, in the controller's repository, at version 1.
func (fx designHostFixture) proposeDesignHostContract(t *testing.T) {
	t.Helper()
	contract := &v1.EpicContract{
		Id: "contract-host", Repository: fx.repo, Title: "Host contract", Care: "serious", Intent: "intent",
		Checks: "go test ./internal/...",
	}
	body, err := protojson.Marshal(contract)
	if err != nil {
		t.Fatalf("encode contract: %v", err)
	}
	if err = fx.store.ProposeContractInDesign(t.Context(), designHostID, contract.GetId(), store.ContractVersion{
		Version: 1, Repository: fx.repo, Title: contract.GetTitle(), Care: contract.GetCare(), Body: string(body),
		Words: "words", Writer: "conversation-one", At: stageResearchNow().Unix(),
	}); err != nil {
		t.Fatalf("ProposeContractInDesign: %v", err)
	}
}

// controllerHead returns the HEAD commit of the controller's repository.
func (fx designHostFixture) controllerHead(t *testing.T) string {
	t.Helper()
	return gitOn(t, fx.repo, "rev-parse", "HEAD")
}

// wantStepOn checks that the steps row of step holds host, and that the last start the service made ran in host's
// checkout of the repository, which the fleet's root for host holds.
func (fx designHostFixture) wantStepOn(t *testing.T, step, host string) {
	t.Helper()
	row, err := fx.store.Step(t.Context(), step)
	if err != nil {
		t.Fatalf("Step %s: %v", step, err)
	}
	if row.Host != host {
		t.Errorf("host of the step row = %q, want %q", row.Host, host)
	}
	if len(fx.starter.starts) == 0 {
		t.Fatal("the service made no start")
	}
	want := filepath.Join(fx.fleet.Roots[host], "repo")
	if got := fx.starter.starts[len(fx.starter.starts)-1].Workspace; got != want {
		t.Errorf("workspace of the step = %q, want the checkout %q on %s", got, want, host)
	}
}

// TestDesignHostResearchStartsOnDesignHost checks that a design that names no host stores the default b, and that its
// research step starts on b, at the HEAD commit of the controller's repository, which b's checkout holds.
func TestDesignHostResearchStartsOnDesignHost(t *testing.T) {
	t.Parallel()
	fx := newDesignHostFixture(t, "b")
	fx.createDefaultDesign(t)
	reply, err := fx.service.Research(t.Context(), connect.NewRequest(&v1.ResearchRequest{
		Design: designHostID, Repository: fx.repo, Question: "Does Foo call Bar?",
	}))
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	fx.wantStepOn(t, reply.Msg.GetStep().GetStep(), "b")
	if got, want := fx.starter.starts[0].Revision, fx.controllerHead(t); got != want {
		t.Errorf("revision of the step = %q, want the controller's HEAD %q", got, want)
	}
}

// TestDesignHostReproduceStartsOnDesignHost checks that a reproduce step of a design on host b starts on b.
func TestDesignHostReproduceStartsOnDesignHost(t *testing.T) {
	t.Parallel()
	fx := newDesignHostFixture(t, "b")
	fx.createDesign(t, "b")
	reply, err := fx.service.Reproduce(t.Context(), connect.NewRequest(&v1.ReproduceRequest{
		Design: designHostID, Repository: fx.repo, Revision: fx.controllerHead(t), Command: "go test ./...",
	}))
	if err != nil {
		t.Fatalf("Reproduce: %v", err)
	}
	fx.wantStepOn(t, reply.Msg.GetStep().GetStep(), "b")
}

// TestDesignHostSteelmanStartsOnDesignHost checks that a steelman discovery step of a design on host b starts on b.
func TestDesignHostSteelmanStartsOnDesignHost(t *testing.T) {
	t.Parallel()
	fx := newDesignHostFixture(t, "b")
	fx.createDesign(t, "b")
	fx.proposeDesignHostContract(t)
	reply, err := fx.service.Steelman(t.Context(), connect.NewRequest(&v1.SteelmanRequest{Design: designHostID}))
	if err != nil {
		t.Fatalf("Steelman: %v", err)
	}
	fx.wantStepOn(t, reply.Msg.GetStep().GetStep(), "b")
}

// TestDesignHostNamedHostA checks that a design named a, rather than the default b, starts its steps on a.
func TestDesignHostNamedHostA(t *testing.T) {
	t.Parallel()
	fx := newDesignHostFixture(t, "a")
	fx.createDesign(t, "a")
	reply, err := fx.service.Research(t.Context(), connect.NewRequest(&v1.ResearchRequest{
		Design: designHostID, Repository: fx.repo, Question: "Does Foo call Bar?",
	}))
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	fx.wantStepOn(t, reply.Msg.GetStep().GetStep(), "a")
}

// TestDesignHostBlockedNamesHostAndGitError checks that a research start whose revision the origin lacks is refused as
// a failed precondition that names host b and carries git's error, and that it stores no step and starts nothing.
func TestDesignHostBlockedNamesHostAndGitError(t *testing.T) {
	t.Parallel()
	fx := newDesignHostFixture(t, "b")
	fx.createDesign(t, "b")
	missing := strings.Repeat("1", 40)

	_, err := fx.service.Research(t.Context(), connect.NewRequest(&v1.ResearchRequest{
		Design: designHostID, Repository: fx.repo, Revision: missing, Question: "Does Foo call Bar?",
	}))
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
		t.Fatalf("Research code = %v, want %v; error %v", code, connect.CodeFailedPrecondition, err)
	}
	if !strings.Contains(err.Error(), "hostrepo: b: ") {
		t.Errorf("error %q does not name host b", err)
	}
	var gitErr *hostexec.GitError
	if !errors.As(err, &gitErr) {
		t.Errorf("error %q carries no git error", err)
	}
	if len(fx.starter.starts) != 0 {
		t.Errorf("the refused start made %d starts, want none", len(fx.starter.starts))
	}
	rows, err := fx.store.DesignSteps(t.Context(), designHostID)
	if err != nil {
		t.Fatalf("DesignSteps: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("the refused start stored %d design step rows, want none", len(rows))
	}
}
