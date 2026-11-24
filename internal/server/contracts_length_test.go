package server_test

import (
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/joshsymonds/orrery/internal/contracttext"
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// useContractLengthSettings points XDG_CONFIG_HOME at a temporary directory for the test, and returns the path of the
// settings file the server reads from it.
func useContractLengthSettings(t *testing.T) string {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	return filepath.Join(xdg, "orrery", "settings.toml")
}

// writeContractLengthSettings writes content as the settings file at path, creating its directory.
func writeContractLengthSettings(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// contractLengthGet serves the store at storePath over Get and returns the reply for contract id.
func contractLengthGet(t *testing.T, storePath, id string) *orreryv1.ContractsGetResponse {
	t.Helper()
	client := contractsClient(t, contractsHandler(t, store.NewReader(storePath), nil))
	reply, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.ContractsGetRequest{Id: id}))
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	return reply.Msg
}

// contractLengthBodyB returns the engine body contract B's newest version holds, and its decision D1 answered, which
// the seeded history stores as version 2.
func contractLengthBodyB() (*enginev1.EpicContract, []*enginev1.ContractDecision) {
	body := contractTestBody(contractIDB, "Synthetic two revised", "serious")
	decisions := []*enginev1.ContractDecision{{
		Id:             "D1",
		Question:       "Synthetic question.",
		Options:        []string{"Synthetic option one.", "Synthetic option two."},
		Recommendation: "Synthetic option one.",
		Reason:         "Synthetic reason.",
		Row:            "F1",
		Answer:         "Synthetic option one.",
	}}
	return body, decisions
}

func TestContractsGetLengthEqualsLinesAboveChecks(t *testing.T) {
	useContractLengthSettings(t)
	st, path := openContractStore(t)
	seedContractHistory(t, st)
	body, decisions := contractLengthBodyB()

	length := contractLengthGet(t, path, contractIDB).GetLength()
	if length == nil {
		t.Fatalf("length unset, want the lines above the checks of the newest version")
	}
	if want := int64(contracttext.LinesAboveChecks(body, decisions)); length.GetLines() != want {
		t.Fatalf("length lines = %d, want %d", length.GetLines(), want)
	}
}

func TestContractsGetLengthDefaultThresholds(t *testing.T) {
	useContractLengthSettings(t)
	st, path := openContractStore(t)
	seedContractHistory(t, st)

	length := contractLengthGet(t, path, contractIDB).GetLength()
	if length == nil {
		t.Fatalf("length unset, want the default thresholds")
	}
	if length.GetWarn() != 41 || length.GetStrong() != 61 {
		t.Fatalf("thresholds = warn %d strong %d, want warn 41 strong 61", length.GetWarn(), length.GetStrong())
	}
}

func TestContractsGetLengthReadsSettingsThresholds(t *testing.T) {
	path := useContractLengthSettings(t)
	writeContractLengthSettings(t, path, "[contract]\nwarn_lines = 30\nstrong_warn_lines = 50\n")
	st, storePath := openContractStore(t)
	seedContractHistory(t, st)

	length := contractLengthGet(t, storePath, contractIDB).GetLength()
	if length == nil {
		t.Fatalf("length unset, want the thresholds the settings file sets")
	}
	if length.GetWarn() != 30 || length.GetStrong() != 50 {
		t.Fatalf("thresholds = warn %d strong %d, want warn 30 strong 50", length.GetWarn(), length.GetStrong())
	}
}

func TestContractsGetLengthCountsWorkingCopy(t *testing.T) {
	useContractLengthSettings(t)
	ctx := t.Context()
	st, path := openContractStore(t)
	proposal := contractTestVersion(
		t, contractTestBody(contractIDW, contractTitleVersion, "serious"), 1,
		contractWriterA, "Synthetic proposal.", contractTimeA, nil,
	)
	if err := st.ProposeContract(ctx, contractIDW, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	versionLines := contracttext.LinesAboveChecks(contractTestBody(contractIDW, contractTitleVersion, "serious"), nil)

	// The working copy adds a failure, so it counts one line more than the version it is made from.
	working := contractTestBody(contractIDW, contractTitleRows, "serious")
	working.Failures = append(working.Failures, &enginev1.EpicFailure{
		Id: "F2", Text: "Synthetic second failure.", Rating: "limited", Response: "watch",
	})
	encoded, err := protojson.Marshal(working)
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	writeWorkingRow(t, st, "F2", string(encoded), contractTimeBRev)
	want := int64(contracttext.LinesAboveChecks(working, nil))
	if want == int64(versionLines) {
		t.Fatalf("working copy and version both count %d lines, so the test cannot tell them apart", want)
	}

	length := contractLengthGet(t, path, contractIDW).GetLength()
	if length == nil {
		t.Fatalf("length unset, want the lines of the working copy")
	}
	if length.GetLines() != want {
		t.Fatalf("length lines = %d, want %d: the working copy's, not the version's %d", length.GetLines(), want,
			versionLines)
	}
}

func TestContractsGetLengthUnsetWhenSettingsDoNotParse(t *testing.T) {
	path := useContractLengthSettings(t)
	writeContractLengthSettings(t, path, "[contract\nwarn_lines = 30\n")
	st, storePath := openContractStore(t)
	seedContractHistory(t, st)

	msg := contractLengthGet(t, storePath, contractIDB)
	if msg.GetLength() != nil {
		t.Fatalf("length = %v, want unset when the settings file does not parse", msg.GetLength())
	}
	if msg.GetContract().GetId() != contractIDB || len(msg.GetVersions()) != 2 {
		t.Fatalf("contract = %v with %d versions, want the contract answered with its two versions",
			msg.GetContract(), len(msg.GetVersions()))
	}

	// The same contract has a length once the file parses, so the unset length is the settings file's doing.
	writeContractLengthSettings(t, path, "[contract]\nwarn_lines = 30\nstrong_warn_lines = 50\n")
	length := contractLengthGet(t, storePath, contractIDB).GetLength()
	if length == nil || length.GetWarn() != 30 || length.GetStrong() != 50 {
		t.Fatalf("length = %v once the settings file parses, want warn 30 strong 50", length)
	}
}
