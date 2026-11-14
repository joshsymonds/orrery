package enginehost

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// epicIDMaxBytes caps the length of a contract's id.
const epicIDMaxBytes = 128

// The reasons a refusal gives after its field. Each says what the field must be, or what it may not sit beside.
const (
	epicReasonMissing    = "missing"
	epicReasonEmpty      = "empty"
	epicReasonNone       = "none"
	epicReasonDuplicate  = "duplicate"
	epicReasonUnknownID  = "unknown id"
	epicReasonLevel      = "not limited, serious or severe"
	epicReasonBesideItem = "set beside an item"
	epicReasonNotCommand = "not a command gate"
	epicReasonNotAgent   = "not an agent gate"
)

// The forms of the ids a contract names: its own id, and the F, R and P rows of its sections.
var (
	epicIDPattern            = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*-[0-9a-f]{7}/[a-z0-9]+(-[a-z0-9]+)*$`)
	epicFailureIDPattern     = regexp.MustCompile(`^F[1-9][0-9]*$`)
	epicRequirementIDPattern = regexp.MustCompile(`^R[1-9][0-9]*$`)
	epicPremiseIDPattern     = regexp.MustCompile(`^P[1-9][0-9]*$`)
	epicRowIDPattern         = regexp.MustCompile(`^[FPR][1-9][0-9]*$`)
)

// epicFieldError is a refusal of a CreateEpic request, naming the field it refuses.
type epicFieldError struct {
	field, reason string
}

func (e *epicFieldError) Error() string { return e.field + ": " + e.reason }

// checkEpic returns the first refusal of a CreateEpic request, checked in the order E1-D5 lists them, or nil when
// the request passes. It runs git in the contract's repository, reads the project file for the repository through
// getenv, and does not check the database.
func checkEpic(ctx context.Context, req *v1.CreateEpicRequest, getenv func(string) string) error {
	contract := req.GetContract()
	if contract == nil {
		return &epicFieldError{field: "contract", reason: epicReasonMissing}
	}
	if err := checkEpicHead(ctx, contract); err != nil {
		return err
	}
	if err := checkEpicBase(ctx, contract); err != nil {
		return err
	}
	if err := checkEpicGateDefs(ctx, contract, getenv); err != nil {
		return err
	}
	if err := checkEpicSections(contract); err != nil {
		return err
	}
	return checkEpicDecisions(req.GetDecisions())
}

// checkEpicGateDefs refuses the contract's gates, then the done and review gates that name them.
func checkEpicGateDefs(ctx context.Context, contract *v1.EpicContract, getenv func(string) string) error {
	defined, err := checkEpicGates(contract.GetGates())
	if err != nil {
		return err
	}
	return checkEpicGateRefs(ctx, contract, defined, getenv)
}

// checkEpicBase refuses a base that is blank or names no commit of the contract's repository. git exits non-zero for
// a name it cannot resolve, so any failure of the check means the base is not a commit there.
func checkEpicBase(ctx context.Context, contract *v1.EpicContract) error {
	base := contract.GetBase()
	if isBlank(base) {
		return &epicFieldError{field: "base", reason: epicReasonEmpty}
	}
	_, err := stepGit(
		ctx, contract.GetRepository(), "rev-parse", "--verify", "--quiet", "--end-of-options", base+"^{commit}",
	)
	if err != nil {
		return &epicFieldError{field: "base", reason: "not a commit in the repository"}
	}
	return nil
}

// checkEpicGates returns the gates the contract defines, each one gates.Define builds from its data. A gate Define
// refuses is refused at its index, and a name an earlier gate took is a duplicate.
func checkEpicGates(epicGates []*v1.EpicGate) ([]gates.Gate, error) {
	seen := make(map[string]bool, len(epicGates))
	defined := make([]gates.Gate, 0, len(epicGates))
	for i, epicGate := range epicGates {
		gate, err := gates.Define(
			epicGate.GetName(),
			epicGate.GetArgv(),
			epicGate.GetLimit(),
			epicGateNeeds(epicGate.GetNeeds()),
			epicGate.GetAgent(),
			epicGate.GetMust(),
			epicGate.GetMustNot(),
		)
		if err != nil {
			return nil, &epicFieldError{field: fmt.Sprintf("gates[%d]", i), reason: err.Error()}
		}
		if seen[gate.Name] {
			return nil, &epicFieldError{field: epicRowField("gates", i, "name"), reason: epicReasonDuplicate}
		}
		seen[gate.Name] = true
		defined = append(defined, gate)
	}
	return defined, nil
}

// epicGateNeeds returns the pool needs of an epic gate as gates.Need, in order.
func epicGateNeeds(needs []*v1.EpicGateNeed) []gates.Need {
	return epicMap(needs, func(need *v1.EpicGateNeed) gates.Need {
		return gates.Need{Pool: need.GetPool(), Count: int(need.GetCount())}
	})
}

// checkEpicGateRefs refuses the first done gate that does not resolve to a command gate, then the first review gate
// that does not resolve to an agent gate. A name resolves from the contract's gates, else from the repository's
// project file.
func checkEpicGateRefs(
	ctx context.Context,
	contract *v1.EpicContract,
	defined []gates.Gate,
	getenv func(string) string,
) error {
	resolve := func(name string) (gates.Resolved, error) {
		return gates.ResolveDefined(ctx, name, defined, contract.GetRepository(), getenv)
	}
	for i, name := range contract.GetDoneGates() {
		resolved, err := resolve(name)
		field := fmt.Sprintf("done_gates[%d]", i)
		if err != nil {
			return &epicFieldError{field: field, reason: err.Error()}
		}
		if resolved.Agent != "" {
			return &epicFieldError{field: field, reason: epicReasonNotCommand}
		}
	}
	for i, name := range contract.GetReviewGates() {
		resolved, err := resolve(name)
		field := fmt.Sprintf("review_gates[%d]", i)
		if err != nil {
			return &epicFieldError{field: field, reason: err.Error()}
		}
		if resolved.Agent == "" {
			return &epicFieldError{field: field, reason: epicReasonNotAgent}
		}
	}
	return nil
}

// checkEpicHead refuses the contract's id, repository, intent and care.
func checkEpicHead(ctx context.Context, contract *v1.EpicContract) error {
	id := contract.GetId()
	if !epicIDPattern.MatchString(id) {
		return &epicFieldError{field: "id", reason: "bad form, want <repo-id>-<7 hex>/<slug>"}
	}
	if len(id) > epicIDMaxBytes {
		return &epicFieldError{field: "id", reason: fmt.Sprintf("over %d bytes", epicIDMaxBytes)}
	}
	if err := checkEpicRepository(ctx, contract.GetRepository()); err != nil {
		return err
	}
	if isBlank(contract.GetIntent()) {
		return &epicFieldError{field: "intent", reason: epicReasonEmpty}
	}
	if !isEpicLevel(contract.GetCare()) {
		return &epicFieldError{field: "care", reason: epicReasonLevel}
	}
	return nil
}

// checkEpicRepository refuses a repository that is not an absolute path inside a git work tree. git exits non-zero
// outside a work tree, so any failure of the check means the directory is not one.
func checkEpicRepository(ctx context.Context, repository string) error {
	if !filepath.IsAbs(repository) {
		return &epicFieldError{field: "repository", reason: "not absolute"}
	}
	inside, err := stepGit(ctx, repository, "rev-parse", "--is-inside-work-tree")
	if err != nil || inside != "true" {
		return &epicFieldError{field: "repository", reason: "not inside a git work tree"}
	}
	return nil
}

// checkEpicSections refuses the failures, requirements, premises, additions and release steps, in that order.
func checkEpicSections(contract *v1.EpicContract) error {
	if err := checkEpicFailures(contract.GetFailures()); err != nil {
		return err
	}
	if err := checkEpicRequirements(contract.GetRequirements()); err != nil {
		return err
	}
	if err := checkEpicPremises(contract.GetPremises()); err != nil {
		return err
	}
	if err := checkEpicAdditions(contract); err != nil {
		return err
	}
	return checkEpicReleaseSteps(contract.GetReleaseSteps())
}

// checkEpicFailures refuses a failure whose id is not F<n> or repeats one, and whose rating is not a level.
func checkEpicFailures(failures []*v1.EpicFailure) error {
	seen := make(map[string]bool, len(failures))
	for i, failure := range failures {
		field := epicRowField("failures", i, "id")
		if err := checkEpicRowID(field, failure.GetId(), epicFailureIDPattern, seen); err != nil {
			return err
		}
		if !isEpicLevel(failure.GetRating()) {
			return &epicFieldError{field: epicRowField("failures", i, "rating"), reason: epicReasonLevel}
		}
	}
	return nil
}

// checkEpicRequirements refuses a contract with no requirements, and a requirement whose id is not R<n> or repeats
// one, or whose evidence item is missing or refused.
func checkEpicRequirements(requirements []*v1.EpicRequirement) error {
	if len(requirements) == 0 {
		return &epicFieldError{field: "requirements", reason: epicReasonNone}
	}
	seen := make(map[string]bool, len(requirements))
	for i, requirement := range requirements {
		field := epicRowField("requirements", i, "id")
		if err := checkEpicRowID(field, requirement.GetId(), epicRequirementIDPattern, seen); err != nil {
			return err
		}
		if err := checkEpicRequirementItem(i, requirement); err != nil {
			return err
		}
	}
	return nil
}

// checkEpicRequirementItem refuses a requirement without an evidence item, or whose item evidence.Check refuses. A new
// epic sets neither the evidence nor the kind an epic stored before O3d2 kept, so either one beside the item is refused.
func checkEpicRequirementItem(i int, requirement *v1.EpicRequirement) error {
	item := requirement.GetItem()
	if item == nil {
		return &epicFieldError{field: epicRowField("requirements", i, "item"), reason: epicReasonMissing}
	}
	if lines := evidence.Check(evidenceItemOf(item)); len(lines) > 0 {
		// Each line is "<field>: <reason>", and its field is named under the item.
		field, reason, _ := strings.Cut(lines[0], ": ")
		return &epicFieldError{field: epicRowField("requirements", i, "item") + "." + field, reason: reason}
	}
	if requirement.GetEvidence() != "" {
		return &epicFieldError{field: epicRowField("requirements", i, "evidence"), reason: epicReasonBesideItem}
	}
	if requirement.GetKind() != "" {
		return &epicFieldError{field: epicRowField("requirements", i, "kind"), reason: epicReasonBesideItem}
	}
	return nil
}

// evidenceItemOf returns the evidence.Item an EvidenceItem message carries.
func evidenceItemOf(item *v1.EvidenceItem) evidence.Item {
	return evidence.Item{
		Capture:    item.GetCapture(),
		Limit:      item.GetLimit(),
		Artifacts:  item.GetArtifacts(),
		Supplied:   item.GetSupplied(),
		Conclusion: item.GetConclusion(),
		Judge:      item.GetJudge(),
	}
}

// checkEpicPremises refuses a premise whose id is not P<n> or repeats one.
func checkEpicPremises(premises []*v1.EpicPremise) error {
	seen := make(map[string]bool, len(premises))
	for i, premise := range premises {
		field := epicRowField("premises", i, "id")
		if err := checkEpicRowID(field, premise.GetId(), epicPremiseIDPattern, seen); err != nil {
			return err
		}
	}
	return nil
}

// checkEpicAdditions refuses an addition whose location has the form of an R, F or P id but names no row of the
// contract. It runs after the sections, so every id it compares against has passed its own check.
func checkEpicAdditions(contract *v1.EpicContract) error {
	rows := make(map[string]bool)
	for _, failure := range contract.GetFailures() {
		rows[failure.GetId()] = true
	}
	for _, requirement := range contract.GetRequirements() {
		rows[requirement.GetId()] = true
	}
	for _, premise := range contract.GetPremises() {
		rows[premise.GetId()] = true
	}
	for i, addition := range contract.GetAdditions() {
		location := addition.GetLocation()
		if epicRowIDPattern.MatchString(location) && !rows[location] {
			return &epicFieldError{field: epicRowField("additions", i, "location"), reason: epicReasonUnknownID}
		}
	}
	return nil
}

// checkEpicReleaseSteps refuses a release step whose undo is empty.
func checkEpicReleaseSteps(steps []*v1.EpicReleaseStep) error {
	for i, step := range steps {
		if isBlank(step.GetUndo()) {
			return &epicFieldError{field: epicRowField("release_steps", i, "undo"), reason: epicReasonEmpty}
		}
	}
	return nil
}

// checkEpicDecisions refuses a seed decision with an empty id, a repeated id, or empty text. These are checked as
// written: the contract names no trimming for them.
func checkEpicDecisions(decisions []*v1.EpicDecisionInput) error {
	seen := make(map[string]bool, len(decisions))
	for i, decision := range decisions {
		id := decision.GetId()
		if id == "" {
			return &epicFieldError{field: epicRowField("decisions", i, "id"), reason: epicReasonEmpty}
		}
		if seen[id] {
			return &epicFieldError{field: epicRowField("decisions", i, "id"), reason: epicReasonDuplicate}
		}
		seen[id] = true
		if decision.GetText() == "" {
			return &epicFieldError{field: epicRowField("decisions", i, "text"), reason: epicReasonEmpty}
		}
	}
	return nil
}

// checkEpicRowID refuses a row's id at field when it is not of the section's form or was seen already, and records
// it in seen.
func checkEpicRowID(field, id string, form *regexp.Regexp, seen map[string]bool) error {
	if !form.MatchString(id) {
		return &epicFieldError{field: field, reason: epicReasonUnknownID}
	}
	if seen[id] {
		return &epicFieldError{field: field, reason: epicReasonDuplicate}
	}
	seen[id] = true
	return nil
}

// epicRowField names a field of the row at index i of a section, as a refusal does: section[i].field.
func epicRowField(section string, i int, field string) string {
	return fmt.Sprintf("%s[%d].%s", section, i, field)
}

// isBlank reports whether s is empty after trimming white space.
func isBlank(s string) bool {
	return strings.TrimSpace(s) == ""
}

// isEpicLevel reports whether level is one of the levels a care or a rating may name.
func isEpicLevel(level string) bool {
	return level == "limited" || level == "serious" || level == "severe"
}
