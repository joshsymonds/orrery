package enginehost

import (
	"cmp"
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// contractChanges returns the ids of the failures, requirements, premises and decisions of next that differ from prev,
// then the names of the contract sections that differ. An id differs when it was added, was removed, or its message
// changed. Within each list the ids come in next's order, then the removed ids in prev's order. The sections are the
// proto field names of EpicContract in field-number order, never id, then decisions when the decision lists differ.
func contractChanges(prev, next *v1.ContractVersion) (rows, sections []string) {
	rows = append(rows, changedIDs(prev.GetContract().GetFailures(), next.GetContract().GetFailures(),
		(*v1.EpicFailure).GetId)...)
	rows = append(rows, changedIDs(prev.GetContract().GetRequirements(), next.GetContract().GetRequirements(),
		(*v1.EpicRequirement).GetId)...)
	rows = append(rows, changedIDs(prev.GetContract().GetPremises(), next.GetContract().GetPremises(),
		(*v1.EpicPremise).GetId)...)
	rows = append(rows, changedIDs(prev.GetDecisions(), next.GetDecisions(), (*v1.ContractDecision).GetId)...)

	sections = changedContractFields(prev.GetContract(), next.GetContract())
	if !decisionsEqual(prev.GetDecisions(), next.GetDecisions()) {
		sections = append(sections, "decisions")
	}
	return rows, sections
}

// changedIDs returns the ids of next that were added or whose message differs from prev's, in next's order, then the
// ids of prev that next no longer has, in prev's order.
func changedIDs[M proto.Message](prev, next []M, id func(M) string) []string {
	prevByID := make(map[string]M, len(prev))
	for _, m := range prev {
		prevByID[id(m)] = m
	}
	nextIDs := make(map[string]bool, len(next))
	var changed []string
	for _, m := range next {
		nextIDs[id(m)] = true
		if old, ok := prevByID[id(m)]; !ok || !proto.Equal(old, m) {
			changed = append(changed, id(m))
		}
	}
	for _, m := range prev {
		if !nextIDs[id(m)] {
			changed = append(changed, id(m))
		}
	}
	return changed
}

// changedContractFields returns the proto field names of the contract whose values differ, in field-number order. The
// id field is never named.
func changedContractFields(prev, next *v1.EpicContract) []string {
	prevRef := prev.ProtoReflect()
	nextRef := next.ProtoReflect()
	fields := prevRef.Descriptor().Fields()
	ordered := make([]protoreflect.FieldDescriptor, 0, fields.Len())
	for i := range fields.Len() {
		ordered = append(ordered, fields.Get(i))
	}
	slices.SortFunc(ordered, func(a, b protoreflect.FieldDescriptor) int {
		return cmp.Compare(a.Number(), b.Number())
	})
	var changed []string
	for _, fd := range ordered {
		if fd.Name() == "id" {
			continue
		}
		if !prevRef.Get(fd).Equal(nextRef.Get(fd)) {
			changed = append(changed, string(fd.Name()))
		}
	}
	return changed
}

// decisionsEqual reports whether two decision lists hold equal decisions in the same order.
func decisionsEqual(prev, next []*v1.ContractDecision) bool {
	if len(prev) != len(next) {
		return false
	}
	for i := range prev {
		if !proto.Equal(prev[i], next[i]) {
			return false
		}
	}
	return true
}
