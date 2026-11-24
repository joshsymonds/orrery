package enginehost

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/contracttext"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// stageChecksQualityBar is the Quality Bar line a contract's checks carry verbatim.
const stageChecksQualityBar = "Quality Bar: " + contracttext.QualityBar

// stageChecksNotVerbatim returns the checks a propose or revise refuses for their Quality Bar line: one whose line
// differs from the verbatim line by one character, one with a trailing space, and checks without the line.
func stageChecksNotVerbatim() []struct {
	name   string
	checks string
} {
	return []struct {
		name   string
		checks string
	}{
		{name: "one character off", checks: "go test ./...\n" + strings.TrimSuffix(stageChecksQualityBar, ".") + ";"},
		{name: "trailing space", checks: "go test ./...\n" + stageChecksQualityBar + " "},
		{name: "line missing", checks: "go test ./..."},
	}
}

// stageChecksLength is a length case: a contract with lines lines above its checks, and the warning it gives, which is
// "none", "warn" or "strong".
type stageChecksLength struct {
	lines int
	want  string
}

// stageChecksSend sends body with decisions to the Contracts server, from the headless session, and returns the
// warnings the reply gives.
type stageChecksSend func(
	t *testing.T,
	server contractTestServer,
	body *v1.EpicContract,
	decisions []*v1.ContractDecision,
) ([]string, error)

// stageChecksPropose proposes body with decisions.
func stageChecksPropose(
	t *testing.T,
	server contractTestServer,
	body *v1.EpicContract,
	decisions []*v1.ContractDecision,
) ([]string, error) {
	t.Helper()
	call := connect.NewRequest(&v1.ProposeContractRequest{
		Id:        body.GetId(),
		Contract:  body,
		Decisions: decisions,
		Words:     contractTestWords,
	})
	headlessCaller(call.Header())
	reply, err := server.contracts.ProposeContract(t.Context(), call)
	if err != nil {
		return nil, err
	}
	return reply.Msg.GetWarnings(), nil
}

// stageChecksRevise revises the contract of the Contracts tests to body with decisions. It proposes the default
// contract first, so that the revise has a version to write over; the server it runs on must hold no contract yet.
func stageChecksRevise(
	t *testing.T,
	server contractTestServer,
	body *v1.EpicContract,
	decisions []*v1.ContractDecision,
) ([]string, error) {
	t.Helper()
	if _, err := proposeContract(t, server.contracts, headlessCaller, contractTestBody(server.repo),
		contractTestDecisions()); err != nil {
		t.Fatalf("seed ProposeContract: %v", err)
	}
	call := connect.NewRequest(&v1.ReviseContractRequest{
		Id:        body.GetId(),
		Contract:  body,
		Decisions: decisions,
		Words:     contractTestReviseWords,
	})
	headlessCaller(call.Header())
	reply, err := server.contracts.ReviseContract(t.Context(), call)
	if err != nil {
		return nil, err
	}
	return reply.Msg.GetWarnings(), nil
}

// stageChecksSenders returns the calls that write a contract's body: a propose, and a revise of a proposed contract.
func stageChecksSenders() []struct {
	name string
	send stageChecksSend
} {
	return []struct {
		name string
		send stageChecksSend
	}{
		{name: "propose", send: stageChecksPropose},
		{name: "revise", send: stageChecksRevise},
	}
}

// stageChecksGetenv returns the environment of a machine whose XDG_CONFIG_HOME is xdg and whose other variables are the
// Contracts tests'.
func stageChecksGetenv(xdg string) func(string) string {
	return func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return xdg
		}
		return epicTestGetenv(key)
	}
}

// writeStageChecksSettings writes content as the settings file under xdg, and returns the file's path.
func writeStageChecksSettings(t *testing.T, xdg, content string) string {
	t.Helper()
	dir := filepath.Join(xdg, "orrery")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, "settings.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// stageChecksServer serves the Contracts of the tests with getenv, over a fresh store, with the headless session
// seeded.
func stageChecksServer(t *testing.T, getenv func(string) string) contractTestServer {
	t.Helper()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	return startContractServer(t, st, &Contracts{
		Store: st, Now: contractTestAt, Getenv: getenv, OriginMain: contractTestOriginMain,
	})
}

// stageChecksBody returns the smallest contract the length cases write: the Contracts tests' contract with one
// requirement and none of the sections a valid contract may leave empty. With no decisions it has 41 lines above its
// checks, and the length cases pad it with exclusions, one line each.
func stageChecksBody(repo string) *v1.EpicContract {
	body := contractTestBody(repo)
	body.Failures = nil
	body.Requirements = body.Requirements[:1]
	body.Premises = nil
	body.Additions = nil
	body.Exclusions = nil
	body.Alternatives = nil
	body.ReleaseSteps = nil
	return body
}

// stageChecksAboveChecks returns body with exclusions added until its contract has lines lines above its checks, with
// decisions. Each exclusion is one line.
func stageChecksAboveChecks(
	t *testing.T,
	body *v1.EpicContract,
	decisions []*v1.ContractDecision,
	lines int,
) *v1.EpicContract {
	t.Helper()
	for n := contracttext.LinesAboveChecks(body, decisions); n < lines; n++ {
		body.Exclusions = append(body.Exclusions, fmt.Sprintf("Synthetic exclusion %d.", n))
	}
	if got := contracttext.LinesAboveChecks(body, decisions); got != lines {
		t.Fatalf("LinesAboveChecks = %d, want %d", got, lines)
	}
	return body
}

// wantStageChecksWarnings asserts that warnings are what want says a contract of lines lines gives at the thresholds
// warn and strong: "none" for no warning, "warn" for exactly one warning naming the count and warn, and "strong" for
// exactly one stronger warning naming the count and strong, which tells the writer to cut or split.
func wantStageChecksWarnings(t *testing.T, warnings []string, want string, lines, warn, strong int) {
	t.Helper()
	if want == "none" {
		if len(warnings) != 0 {
			t.Errorf("warnings = %q, want none at %d lines", warnings, lines)
		}
		return
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %q, want exactly one at %d lines", warnings, lines)
	}
	threshold := warn
	if want == "strong" {
		threshold = strong
	}
	warning := warnings[0]
	if !strings.Contains(warning, strconv.Itoa(lines)) || !strings.Contains(warning, strconv.Itoa(threshold)) {
		t.Errorf("warning %q does not name %d lines and the %d threshold", warning, lines, threshold)
	}
	if got := strings.Contains(warning, "cut or split"); got != (want == "strong") {
		t.Errorf("warning %q tells the writer to cut or split = %v, want %v", warning, got, want == "strong")
	}
}

// testStageChecksLengths runs each length case through each write, against Contracts that read getenv, and asserts the
// warnings each reply gives and that no write is refused for its length. The contracts have no decisions.
func testStageChecksLengths(t *testing.T, getenv func(string) string, warn, strong int, cases []stageChecksLength) {
	t.Helper()
	for _, sender := range stageChecksSenders() {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s at %d lines", sender.name, tc.lines), func(t *testing.T) {
				t.Parallel()
				server := stageChecksServer(t, getenv)
				body := stageChecksAboveChecks(t, stageChecksBody(server.repo), nil, tc.lines)
				warnings, err := sender.send(t, server, body, nil)
				if err != nil {
					t.Fatalf("%s refused a contract %d lines above its checks: %v", sender.name, tc.lines, err)
				}
				wantStageChecksWarnings(t, warnings, tc.want, tc.lines, warn, strong)
			})
		}
	}
}

// TestContractStageChecksProposeRefusesQualityBarNotVerbatim checks that a propose whose checks lack the Quality Bar
// line, or carry it differing by one character or with a trailing space, is refused as not verbatim, and that it stores
// nothing.
func TestContractStageChecksProposeRefusesQualityBarNotVerbatim(t *testing.T) {
	t.Parallel()
	for _, tc := range stageChecksNotVerbatim() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := stageChecksServer(t, epicTestGetenv)
			body := contractTestBody(server.repo)
			body.Checks = tc.checks

			_, err := proposeContract(t, server.contracts, headlessCaller, body, contractTestDecisions())
			wantConnectError(t, err, connect.CodeInvalidArgument, "checks: the Quality Bar line is not verbatim")
			_, err = server.contracts.GetContract(
				t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: epicServiceID}))
			wantConnectError(t, err, connect.CodeNotFound, "contract "+epicServiceID+" not found")
		})
	}
}

// TestContractStageChecksReviseRefusesQualityBarNotVerbatim checks that a revise whose checks lack the Quality Bar
// line, or carry it differing by one character or with a trailing space, is refused as not verbatim, and that the
// contract keeps its version 1.
func TestContractStageChecksReviseRefusesQualityBarNotVerbatim(t *testing.T) {
	t.Parallel()
	for _, tc := range stageChecksNotVerbatim() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := stageChecksServer(t, epicTestGetenv)
			if _, err := proposeContract(t, server.contracts, headlessCaller, contractTestBody(server.repo),
				contractTestDecisions()); err != nil {
				t.Fatalf("seed ProposeContract: %v", err)
			}
			req := reviseRequest(server.repo)
			req.Contract.Checks = tc.checks

			_, err := reviseContract(t.Context(), server.contracts, headlessCaller, req)
			wantConnectError(t, err, connect.CodeInvalidArgument, "checks: the Quality Bar line is not verbatim")
			got, err := server.contracts.GetContract(
				t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: epicServiceID}))
			if err != nil {
				t.Fatalf("GetContract: %v", err)
			}
			if version := got.Msg.GetContract().GetVersion(); version != 1 {
				t.Errorf("contract is at version %d after the refused revise, want 1", version)
			}
		})
	}
}

// TestContractStageChecksWarnsAtDefaultThresholds checks that under default settings a propose and a revise warn
// exactly once at 41 lines above the checks and below 61, give one stronger warning from 61 up, and never refuse a
// contract for its length.
func TestContractStageChecksWarnsAtDefaultThresholds(t *testing.T) {
	t.Parallel()
	testStageChecksLengths(t, epicTestGetenv, 41, 61, []stageChecksLength{
		{lines: 41, want: "warn"},
		{lines: 60, want: "warn"},
		{lines: 61, want: "strong"},
		{lines: 200, want: "strong"},
	})
}

// TestContractStageChecksSettingsMoveTheThresholds checks that the thresholds a propose and a revise warn at are the
// warn_lines and strong_warn_lines a settings.toml in a temporary XDG_CONFIG_HOME sets: no warning below warn_lines.
func TestContractStageChecksSettingsMoveTheThresholds(t *testing.T) {
	t.Parallel()
	xdg := t.TempDir()
	writeStageChecksSettings(t, xdg, "[contract]\nwarn_lines = 45\nstrong_warn_lines = 50\n")
	testStageChecksLengths(t, stageChecksGetenv(xdg), 45, 50, []stageChecksLength{
		{lines: 41, want: "none"},
		{lines: 44, want: "none"},
		{lines: 45, want: "warn"},
		{lines: 49, want: "warn"},
		{lines: 50, want: "strong"},
	})
}

// TestContractStageChecksSettingsErrorRefuses checks that a settings file whose threshold is not a positive integer
// refuses a propose, and a revise of a proposed contract, with its message, which names the file.
func TestContractStageChecksSettingsErrorRefuses(t *testing.T) {
	t.Parallel()
	wantRefusal := func(t *testing.T, err error, path string) {
		t.Helper()
		if err == nil {
			t.Fatalf("error = nil, want a refusal naming %s", path)
		}
		if got := connect.CodeOf(err); got != connect.CodeInternal {
			t.Errorf("code = %v, want %v", got, connect.CodeInternal)
		}
		if !strings.Contains(err.Error(), path) {
			t.Errorf("error %q does not name %s", err, path)
		}
	}

	t.Run("propose", func(t *testing.T) {
		t.Parallel()
		xdg := t.TempDir()
		path := writeStageChecksSettings(t, xdg, "[contract]\nwarn_lines = 0\n")
		server := stageChecksServer(t, stageChecksGetenv(xdg))
		_, err := proposeContract(t, server.contracts, headlessCaller, contractTestBody(server.repo),
			contractTestDecisions())
		wantRefusal(t, err, path)
	})

	t.Run("revise", func(t *testing.T) {
		t.Parallel()
		xdg := t.TempDir()
		server := stageChecksServer(t, stageChecksGetenv(xdg))
		if _, err := proposeContract(t, server.contracts, headlessCaller, contractTestBody(server.repo),
			contractTestDecisions()); err != nil {
			t.Fatalf("seed ProposeContract: %v", err)
		}
		path := writeStageChecksSettings(t, xdg, "[contract]\nstrong_warn_lines = -1\n")
		_, err := reviseContract(t.Context(), server.contracts, headlessCaller, reviseRequest(server.repo))
		wantRefusal(t, err, path)
	})
}

// TestContractStageChecksLongContractIsStored checks that a propose of a contract 200 lines above its checks stores it
// as version 1, and that a revise of one stores it as version 2, each read back unchanged.
func TestContractStageChecksLongContractIsStored(t *testing.T) {
	t.Parallel()
	t.Run("propose", func(t *testing.T) {
		t.Parallel()
		server := stageChecksServer(t, epicTestGetenv)
		body := stageChecksAboveChecks(t, stageChecksBody(server.repo), nil, 200)

		warnings, err := stageChecksPropose(t, server, body, nil)
		if err != nil {
			t.Fatalf("ProposeContract refused a long contract: %v", err)
		}
		wantStageChecksWarnings(t, warnings, "strong", 200, 41, 61)
		got, err := server.contracts.GetContract(
			t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: epicServiceID}))
		if err != nil {
			t.Fatalf("GetContract: %v", err)
		}
		if want := wantStoredContract(body, nil, contractTestHeadless); !proto.Equal(got.Msg.GetContract(), want) {
			t.Errorf("GetContract after a long propose = %v, want %v", got.Msg.GetContract(), want)
		}
	})

	t.Run("revise", func(t *testing.T) {
		t.Parallel()
		server := stageChecksServer(t, epicTestGetenv)
		body := stageChecksAboveChecks(t, stageChecksBody(server.repo), nil, 200)

		warnings, err := stageChecksRevise(t, server, body, nil)
		if err != nil {
			t.Fatalf("ReviseContract refused a long contract: %v", err)
		}
		wantStageChecksWarnings(t, warnings, "strong", 200, 41, 61)
		got, err := server.contracts.GetContract(
			t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: epicServiceID}))
		if err != nil {
			t.Fatalf("GetContract after revise: %v", err)
		}
		versions := got.Msg.GetContract().GetVersions()
		if len(versions) != 2 {
			t.Fatalf("contract holds %d versions after the revise, want 2", len(versions))
		}
		if stored := versions[1].GetContract(); !proto.Equal(stored, body) {
			t.Errorf("version 2 body = %v, want the long contract %v", stored, body)
		}
	})
}
