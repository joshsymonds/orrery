package stage

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/joshsymonds/orrery/internal/contracttext"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// designPacket returns the packet a steelman pass checks the design record with: each contract of the design in
// position order at its newest version, then each draft of the design in created order, each rendered by
// contracttext.Render with only the decisions still open. It also returns the repository a pass runs in, that of the
// design's first contract or else of its first draft. A design holding neither has no packet, and is refused as a
// failed precondition.
func (s *Service) designPacket(ctx context.Context, record store.DesignRecord) (string, string, error) {
	var blocks []string
	for _, member := range record.Contracts {
		block, err := s.contractBlock(ctx, member.ID)
		if err != nil {
			return "", "", internalError(err)
		}
		blocks = append(blocks, block)
	}
	drafts, err := s.cfg.Store.DesignDrafts(ctx, record.ID)
	if err != nil {
		return "", "", internalError(fmt.Errorf("list drafts of design %s: %w", record.ID, err))
	}
	for _, draft := range drafts {
		contract, decodeErr := packetContract(draft.Body)
		if decodeErr != nil {
			return "", "", internalError(fmt.Errorf("draft %s: %w", draft.ID, decodeErr))
		}
		blocks = append(blocks, contracttext.Render(contract, openDecisions(draft.Decisions)))
	}
	if len(blocks) == 0 {
		return "", "", refuse(connect.CodeFailedPrecondition,
			fmt.Sprintf("steelman: design %s holds no contract or draft to steelman", record.ID))
	}
	// Blocks are non-empty, so a design with no contract has a draft to take the repository from.
	var repository string
	if len(record.Contracts) > 0 {
		repository = record.Contracts[0].Repository
	} else {
		repository = drafts[0].Repository
	}
	return strings.Join(blocks, "\n"), repository, nil
}

// contractBlock returns the rendering of the contract id at its newest version, with its open decisions.
func (s *Service) contractBlock(ctx context.Context, id string) (string, error) {
	record, err := s.cfg.Store.Contract(ctx, id)
	if err != nil {
		return "", fmt.Errorf("read contract %s: %w", id, err)
	}
	newest, ok := newestVersion(record.Versions)
	if !ok {
		return "", fmt.Errorf("contract %s has no version", id)
	}
	contract, err := packetContract(newest.Body)
	if err != nil {
		return "", fmt.Errorf("contract %s version %d: %w", id, newest.Version, err)
	}
	return contracttext.Render(contract, openDecisions(newest.Decisions)), nil
}

// newestVersion returns the version of versions with the highest number, and false when there is none.
func newestVersion(versions []store.ContractVersion) (store.ContractVersion, bool) {
	if len(versions) == 0 {
		return store.ContractVersion{}, false
	}
	newest := versions[0]
	for _, version := range versions[1:] {
		if version.Version > newest.Version {
			newest = version
		}
	}
	return newest, true
}

// packetContract decodes the EpicContract a stored body holds, in protobuf JSON.
func packetContract(body string) (*v1.EpicContract, error) {
	var contract v1.EpicContract
	if err := protojson.Unmarshal([]byte(body), &contract); err != nil {
		return nil, fmt.Errorf("decode body: %w", err)
	}
	return &contract, nil
}

// openDecisions returns the decisions still open among decisions, the ones a packet asks the owner to answer, as
// contract decision messages.
func openDecisions(decisions []store.ContractDecision) []*v1.ContractDecision {
	var open []*v1.ContractDecision
	for _, decision := range decisions {
		if decision.Answer != "" {
			continue
		}
		open = append(open, &v1.ContractDecision{
			Id: decision.ID, Question: decision.Question, Options: decision.Options,
			Recommendation: decision.Recommendation, Reason: decision.Reason, Row: decision.Row,
		})
	}
	return open
}
