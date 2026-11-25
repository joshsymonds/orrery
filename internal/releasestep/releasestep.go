// Package releasestep is the built-in mechanics of a release step. A release step that names a kind is not handed to an
// agent: orreryd runs `orrery release-step <kind>` under a holder, and that command carries out the step's fixed
// mechanics and then checks the step's postcondition in code. It exits 0 only when the postcondition holds, with the
// facts that show it on its stdout. A postcondition that already holds when the command starts is not carried out
// again, so a release whose target has moved past its candidate never moves the target back.
//
// The kinds are push-main, which fast-forwards origin's branch to the candidate; tag, which pushes a signed annotated
// version tag at it; pin-flake, which pins a flake input on the flake repository's origin to that tag; and update,
// which switches the machine to the flake and checks that its services run a build that holds the candidate.
package releasestep

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The kinds of a release step, as a project file or a contract names them.
const (
	// KindPushMain fast-forwards origin's branch to the candidate and pushes it. Its postcondition is that origin's
	// branch contains the candidate: the candidate is the branch's commit or an ancestor of it, so a branch that has
	// since moved past the candidate still meets it.
	KindPushMain = "push-main"
	// KindTag pushes a signed annotated tag at the candidate, named the next patch version above the newest version tag
	// on origin. Its postcondition is that a version tag on origin peels to a commit that contains the candidate: the
	// tag it pushed, which peels to the candidate itself, or a newer one a later release pushed.
	KindTag = "tag"
	// KindPinFlake pins a flake input to the newest version tag that contains the candidate, on the flake repository's
	// origin branch. Its postcondition is that origin's branch names such a tag in the input's url in flake.nix, and that
	// flake.lock locks the input at the commit that tag peels to.
	KindPinFlake = "pin-flake"
	// KindUpdate brings the flake repository's checkout up to its origin branch and runs the command that switches the
	// machine to it. Its postcondition is that every unit it names is active and runs a build whose commit contains the
	// candidate.
	KindUpdate = "update"
)

// Kinds returns every kind, in the order a release runs them.
func Kinds() []string {
	return []string{KindPushMain, KindTag, KindPinFlake, KindUpdate}
}

// Remote is the remote every kind reads and pushes.
const Remote = "origin"

// TagSlot is the placeholder a pin-flake url holds where the tag goes.
const TagSlot = "{tag}"

// Command is the subcommand of orrery that carries out a step of a kind: `orrery release-step <kind> <flags>`.
const Command = "release-step"

// The limits a step of each kind runs under. A push or a tag reaches origin a few times. A pin fetches the input and
// writes its lock. An update builds and switches the machine, which takes the longest.
const (
	pushLimit   = 10 * time.Minute
	tagLimit    = 10 * time.Minute
	pinLimit    = 30 * time.Minute
	updateLimit = 2 * time.Hour
)

// Params are the parameters of a release step's kind. Each kind reads only its own, and Problem refuses any other one
// a step sets.
type Params struct {
	// Branch is the branch of origin a push-main step fast-forwards, and the branch of the flake repository's origin a
	// pin-flake step pins on and an update step builds from.
	Branch string `json:"branch,omitempty"`
	// Prefix is what every version tag starts with, before MAJOR.MINOR.PATCH: the tags a tag step reads and names, and
	// a pin-flake step pins.
	Prefix string `json:"prefix,omitempty"`
	// Flake is the flake repository a pin-flake step pins in and an update step builds: an absolute path, or one that
	// starts with ~/ for the home directory.
	Flake string `json:"flake,omitempty"`
	// Input is the flake input a pin-flake step pins.
	Input string `json:"input,omitempty"`
	// URL is the url a pin-flake step sets the input to, with TagSlot where the tag goes.
	URL string `json:"url,omitempty"`
	// Run is the argv an update step runs in the flake repository to switch the machine to it.
	Run []string `json:"run,omitempty"`
	// Units are the systemd user units an update step checks run a build that contains the candidate.
	Units []string `json:"units,omitempty"`
}

// The names of the parameters, as a project file and a contract's params name them.
const (
	ParamBranch = "branch"
	ParamPrefix = "prefix"
	ParamFlake  = "flake"
	ParamInput  = "input"
	ParamURL    = "url"
	ParamRun    = "run"
	ParamUnits  = "units"
)

// kindParams names the parameters kind needs, in the order Problem checks them, and reports whether kind is a kind. A
// kind sets every one of its own and none of the others.
func kindParams(kind string) ([]string, bool) {
	switch kind {
	case KindPushMain:
		return []string{ParamBranch}, true
	case KindTag:
		return []string{ParamPrefix}, true
	case KindPinFlake:
		return []string{ParamBranch, ParamPrefix, ParamFlake, ParamInput, ParamURL}, true
	case KindUpdate:
		return []string{ParamBranch, ParamFlake, ParamRun, ParamUnits}, true
	default:
		return nil, false
	}
}

// allParams names every parameter, in the order Problem checks them.
func allParams() []string {
	return []string{ParamBranch, ParamPrefix, ParamFlake, ParamInput, ParamURL, ParamRun, ParamUnits}
}

// prefixPattern is the form of a tag prefix: letters, digits, dots, dashes, underscores and slashes, so a prefix never
// holds a glob character or white space.
var prefixPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// inputPattern is the form of a flake input's name.
var inputPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_'-]*$`)

// The reasons Problem refuses a step with.
const (
	reasonUnknownKind = "unknown kind %q: a release step's kind is one of %s, or empty for an agent step"
	reasonMissing     = "missing: a %s step needs it"
	reasonNotKind     = "not a parameter of a %s step"
	reasonNoKind      = "a step with no kind sets no parameters"
	reasonBlank       = "holds a blank element"
	reasonPrefix      = "holds a character other than a letter, digit, dot, dash, underscore or slash"
	reasonFlake       = "is neither an absolute path nor one that starts with ~/"
	reasonInput       = "is not a flake input name"
	reasonURL         = "holds " + TagSlot + " not exactly once"
)

// Problem returns the first parameter of a step of kind with params that is refused, and why, or "", "" when the step
// is well formed. An empty kind is an agent step, which sets no parameter. An unknown kind is named by the field kind.
// A kind is refused a parameter it needs and does not set, and one it sets and does not read. The values are checked
// for their form: a prefix of plain characters, a flake path that is absolute or starts with ~/, an input name, a url
// that holds TagSlot exactly once, and lists with no blank element.
func Problem(kind string, params Params) (field, reason string) {
	set := setParams(params)
	if kind == "" {
		if len(set) > 0 {
			return set[0], reasonNoKind
		}
		return "", ""
	}
	needed, known := kindParams(kind)
	if !known {
		return "kind", fmt.Sprintf(reasonUnknownKind, kind, strings.Join(Kinds(), ", "))
	}
	for _, name := range needed {
		if !slices.Contains(set, name) {
			return name, fmt.Sprintf(reasonMissing, kind)
		}
	}
	for _, name := range set {
		if !slices.Contains(needed, name) {
			return name, fmt.Sprintf(reasonNotKind, kind)
		}
	}
	return formProblem(params)
}

// setParams returns the names of the parameters params sets, in the order of allParams. A string is set when it is not
// blank, and a list when it has an element.
func setParams(params Params) []string {
	values := map[string]bool{
		ParamBranch: strings.TrimSpace(params.Branch) != "",
		ParamPrefix: strings.TrimSpace(params.Prefix) != "",
		ParamFlake:  strings.TrimSpace(params.Flake) != "",
		ParamInput:  strings.TrimSpace(params.Input) != "",
		ParamURL:    strings.TrimSpace(params.URL) != "",
		ParamRun:    len(params.Run) > 0,
		ParamUnits:  len(params.Units) > 0,
	}
	var set []string
	for _, name := range allParams() {
		if values[name] {
			set = append(set, name)
		}
	}
	return set
}

// formProblem returns the first parameter of params whose value has the wrong form, and why, or "", "" when every set
// parameter has its form. An unset parameter is not checked, since Problem has already refused one a kind needs.
func formProblem(params Params) (field, reason string) {
	switch {
	case params.Prefix != "" && !prefixPattern.MatchString(params.Prefix):
		return ParamPrefix, reasonPrefix
	case params.Flake != "" && !strings.HasPrefix(params.Flake, "/") && !strings.HasPrefix(params.Flake, "~/"):
		return ParamFlake, reasonFlake
	case params.Input != "" && !inputPattern.MatchString(params.Input):
		return ParamInput, reasonInput
	case params.URL != "" && strings.Count(params.URL, TagSlot) != 1:
		return ParamURL, reasonURL
	case slices.ContainsFunc(params.Run, isBlank):
		return ParamRun, reasonBlank
	case slices.ContainsFunc(params.Units, isBlank):
		return ParamUnits, reasonBlank
	}
	return "", ""
}

// isBlank reports whether text is empty after trimming.
func isBlank(text string) bool {
	return strings.TrimSpace(text) == ""
}

// The flags of `orrery release-step`, as Argv passes them and the command reads them.
const (
	flagCandidate = "candidate"
	flagTitle     = "title"
	flagBranch    = "branch"
	flagPrefix    = "prefix"
	flagFlake     = "flake"
	flagInput     = "input"
	flagURL       = "url"
	flagUnit      = "unit"
)

// Argv returns the argv that carries out a release step of kind with params from candidate, run by the orrery binary
// at orrery: `orrery release-step <kind> -candidate <candidate>`, then the flags of the kind's parameters, and for an
// update step its run argv after `--`. title names the change, which a tag step's message carries. A kind Problem
// refuses has no argv; the caller checks the step first.
func Argv(orrery, kind string, params Params, candidate, title string) []string {
	argv := []string{orrery, Command, kind, "-" + flagCandidate, candidate}
	switch kind {
	case KindPushMain:
		argv = append(argv, "-"+flagBranch, params.Branch)
	case KindTag:
		argv = append(argv, "-"+flagPrefix, params.Prefix, "-"+flagTitle, title)
	case KindPinFlake:
		argv = append(argv, "-"+flagBranch, params.Branch, "-"+flagPrefix, params.Prefix, "-"+flagFlake, params.Flake,
			"-"+flagInput, params.Input, "-"+flagURL, params.URL)
	case KindUpdate:
		argv = append(argv, "-"+flagBranch, params.Branch, "-"+flagFlake, params.Flake)
		for _, unit := range params.Units {
			argv = append(argv, "-"+flagUnit, unit)
		}
		argv = append(append(argv, "--"), params.Run...)
	}
	return argv
}

// Limit returns how long a step of kind may run before its holder stops it.
func Limit(kind string) time.Duration {
	switch kind {
	case KindPushMain:
		return pushLimit
	case KindTag:
		return tagLimit
	case KindPinFlake:
		return pinLimit
	default:
		return updateLimit
	}
}
