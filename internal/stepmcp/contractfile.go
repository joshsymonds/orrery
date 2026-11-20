package stepmcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// contractDecisionsKey is the top-level key of a contract file that holds its decisions.
const contractDecisionsKey = "decisions"

// readContractFile reads the contract file at path: one JSON object holding an EpicContract in protobuf JSON field
// names and, under decisions, an array of ContractDecision in protobuf JSON field names. protojson refuses any other
// unknown key, in the contract or in a decision. Every error names path.
func readContractFile(path string) (*v1.EpicContract, []*v1.ContractDecision, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, nil, fmt.Errorf("read contract file %s: %w", path, err)
	}
	var object map[string]json.RawMessage
	if err = json.Unmarshal(raw, &object); err != nil {
		return nil, nil, fmt.Errorf("contract file %s is not a JSON object: %w", path, err)
	}
	if object == nil {
		return nil, nil, fmt.Errorf("contract file %s is not a JSON object", path)
	}
	var decisions []*v1.ContractDecision
	if rawDecisions, ok := object[contractDecisionsKey]; ok {
		if decisions, err = decodeContractDecisions(rawDecisions); err != nil {
			return nil, nil, fmt.Errorf("contract file %s: %w", path, err)
		}
		delete(object, contractDecisionsKey)
	}
	body, err := json.Marshal(object)
	if err != nil {
		return nil, nil, fmt.Errorf("contract file %s: encode contract: %w", path, err)
	}
	var contract v1.EpicContract
	if err = protojson.Unmarshal(body, &contract); err != nil {
		return nil, nil, fmt.Errorf("contract file %s: %w", path, err)
	}
	return &contract, decisions, nil
}

// decodeContractDecisions decodes the decisions key of a contract file, an array of ContractDecision.
func decodeContractDecisions(raw json.RawMessage) ([]*v1.ContractDecision, error) {
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		return nil, fmt.Errorf("decisions is not an array: %w", err)
	}
	decisions := make([]*v1.ContractDecision, 0, len(elements))
	for i, element := range elements {
		var decision v1.ContractDecision
		if err := protojson.Unmarshal(element, &decision); err != nil {
			return nil, fmt.Errorf("decision %d: %w", i, err)
		}
		decisions = append(decisions, &decision)
	}
	return decisions, nil
}
