package stepmcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// contractFixtureDecisions are two decisions with every field set, as a contract file may carry them.
func contractFixtureDecisions() []*v1.ContractDecision {
	return []*v1.ContractDecision{
		{
			Id:             "D1",
			Question:       "Which queue backs the outbox?",
			Options:        []string{"sqlite table", "in-memory channel"},
			Recommendation: "sqlite table",
			Reason:         "the outbox must survive a restart",
			Row:            "F1",
			Answer:         "sqlite table",
		},
		{
			Id:             "D2",
			Question:       "Which clock does the poller read?",
			Options:        []string{"wall clock", "injected clock"},
			Recommendation: "injected clock",
			Reason:         "the poller is tested under a fake clock",
			Row:            "R3",
			Answer:         "injected clock",
		},
	}
}

// writeContractFixture writes the JSON object as a contract file in a temp directory and returns its path. The
// contract is written in protobuf JSON field names, and decisions, when non-nil, is written as the decisions key.
func writeContractFixture(t *testing.T, contract map[string]any, decisions []map[string]any) string {
	t.Helper()
	if decisions != nil {
		contract["decisions"] = decisions
	}
	body, err := json.Marshal(contract)
	if err != nil {
		t.Fatalf("encode contract file: %v", err)
	}
	path := filepath.Join(t.TempDir(), "contract.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write contract file: %v", err)
	}
	return path
}

// contractFixtureObject is the fullContract as a JSON object in protobuf JSON field names.
func contractFixtureObject(t *testing.T) map[string]any {
	t.Helper()
	text, err := protojson.Marshal(fullContract())
	if err != nil {
		t.Fatalf("encode fixture contract: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(text, &object); err != nil {
		t.Fatalf("decode fixture contract: %v", err)
	}
	return object
}

// contractFixtureDecisionObjects is contractFixtureDecisions as JSON objects in protobuf JSON field names.
func contractFixtureDecisionObjects(t *testing.T) []map[string]any {
	t.Helper()
	decisions := contractFixtureDecisions()
	objects := make([]map[string]any, 0, len(decisions))
	for _, decision := range decisions {
		text, err := protojson.Marshal(decision)
		if err != nil {
			t.Fatalf("encode fixture decision: %v", err)
		}
		var object map[string]any
		if err := json.Unmarshal(text, &object); err != nil {
			t.Fatalf("decode fixture decision: %v", err)
		}
		objects = append(objects, object)
	}
	return objects
}

func TestContractRecordReadsContractAndEveryDecisionField(t *testing.T) {
	path := writeContractFixture(t, contractFixtureObject(t), contractFixtureDecisionObjects(t))

	contract, decisions, err := readContractFile(path)
	if err != nil {
		t.Fatalf("readContractFile: %v", err)
	}
	if !proto.Equal(contract, fullContract()) {
		t.Fatalf("contract = %s, want %s", marshalProto(t, contract), marshalProto(t, fullContract()))
	}
	want := contractFixtureDecisions()
	for i := range want {
		assertFullyPopulated(t, want[i].ProtoReflect(), "ContractDecision")
	}
	if len(decisions) != len(want) {
		t.Fatalf("decisions = %d, want %d", len(decisions), len(want))
	}
	for i := range want {
		if !proto.Equal(decisions[i], want[i]) {
			t.Fatalf("decision %d = %s, want %s", i, marshalProto(t, decisions[i]), marshalProto(t, want[i]))
		}
	}
}

func TestContractRecordWithoutDecisionsKeyReadsNoDecisions(t *testing.T) {
	path := writeContractFixture(t, contractFixtureObject(t), nil)

	contract, decisions, err := readContractFile(path)
	if err != nil {
		t.Fatalf("readContractFile: %v", err)
	}
	if !proto.Equal(contract, fullContract()) {
		t.Fatalf("contract = %s, want %s", marshalProto(t, contract), marshalProto(t, fullContract()))
	}
	if len(decisions) != 0 {
		t.Fatalf("decisions = %d, want none", len(decisions))
	}
}

func TestContractRecordRefusesMissingFileNamingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")

	_, _, err := readContractFile(path)
	if err == nil {
		t.Fatal("readContractFile of a missing file returned no error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not name path %s", err, path)
	}
}

func TestContractRecordRefusesNonObjectNamingPath(t *testing.T) {
	for _, body := range []string{`[]`, `null`, `"contract"`, `{"id": "a"`} {
		path := filepath.Join(t.TempDir(), "contract.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write contract file: %v", err)
		}

		_, _, err := readContractFile(path)
		if err == nil {
			t.Fatalf("readContractFile of %s returned no error", body)
		}
		if !strings.Contains(err.Error(), path) {
			t.Fatalf("error %q for %s does not name path %s", err, body, path)
		}
	}
}

func TestContractRecordRefusesUnknownTopLevelKeyNamingPath(t *testing.T) {
	contract := contractFixtureObject(t)
	contract["bogus"] = true
	path := writeContractFixture(t, contract, nil)

	_, _, err := readContractFile(path)
	if err == nil {
		t.Fatal("readContractFile with an unknown top-level key returned no error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not name path %s", err, path)
	}
}

func TestContractRecordRefusesDecisionWithUnknownKeyNamingPath(t *testing.T) {
	decisions := contractFixtureDecisionObjects(t)
	decisions[1]["bogus"] = "x"
	path := writeContractFixture(t, contractFixtureObject(t), decisions)

	_, _, err := readContractFile(path)
	if err == nil {
		t.Fatal("readContractFile with a decision carrying an unknown key returned no error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not name path %s", err, path)
	}
}
