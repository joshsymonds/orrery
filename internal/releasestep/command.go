package releasestep

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// The exit statuses of `orrery release-step`. A step exits exitHeld only when its postcondition holds, exitFailed when
// it does not or the step failed, and exitUsage for a command line it refuses before any work. It never exits with the
// status orreryd reads as an environment failure.
const (
	exitHeld   = 0
	exitFailed = 1
	exitUsage  = 2
)

// errUsage marks a command line the command refuses before any work.
var errUsage = errors.New("usage")

// usageLine is the form of the command line, which a refused one is answered with.
const usageLine = "usage: orrery release-step push-main|tag|pin-flake|update -candidate SHA [FLAGS] [-- RUN...]"

// gitHost is the git a step runs: hostexec.Host's Git, which hostexec.Local carries out on this machine.
type gitHost interface {
	Git(ctx context.Context, call hostexec.GitCall) ([]byte, error)
}

// step is one run of `orrery release-step`: the seams it reaches the machine through, the repository it runs in, and
// where its facts and failures go.
type step struct {
	// git runs git.
	git gitHost
	// machine runs every other command and reads the files and links a step reads outside a repository.
	machine machine
	// repo is the working directory, a worktree of the epic's repository at the candidate.
	repo string
	// home is the home directory a flake path that starts with ~/ is read from, or "" when there is none.
	home string
	// tempDir is the directory a pin's temporary worktree is made under, or "" for the system's.
	tempDir string
	// stdout takes the facts that show the postcondition, one short plain line each.
	stdout io.Writer
	// stderr takes failures and the output of the commands a step runs.
	stderr io.Writer
}

// Main runs `orrery release-step <kind> <flags>` with args, the arguments after the subcommand, in the working
// directory, which is a worktree of the epic's repository at the candidate. It prints the facts that show the step's
// postcondition on stdout and its failures and long command output on stderr. It returns 0 only when the postcondition
// holds, 1 when it does not or the step failed, and 2 for a command line it refuses.
func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	repo, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "release-step: working directory: %v\n", err)
		return exitFailed
	}
	// A home directory that cannot be read matters only to a flake path that starts with ~/, which then fails.
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	release := &step{
		git:     hostexec.NewLocal(),
		machine: osMachine{},
		repo:    repo,
		home:    home,
		stdout:  stdout,
		stderr:  stderr,
	}
	return release.main(ctx, args)
}

// main parses args and runs the kind they name, and returns the command's exit status.
func (s *step) main(ctx context.Context, args []string) int {
	inv, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(s.stderr, "release-step: %v\n%s\n", err, usageLine)
		return exitUsage
	}
	if err = s.run(ctx, inv); err != nil {
		fmt.Fprintf(s.stderr, "release-step %s: %v\n", inv.kind, err)
		return exitFailed
	}
	return exitHeld
}

// invocation is a parsed command line: the kind, the candidate, the change's title and the kind's parameters.
type invocation struct {
	kind      string
	candidate string
	title     string
	params    Params
}

// unitList is the value of the repeatable -unit flag.
type unitList []string

// String returns the units joined by commas.
func (u *unitList) String() string {
	return strings.Join(*u, ",")
}

// Set adds one unit.
func (u *unitList) Set(value string) error {
	*u = append(*u, value)
	return nil
}

// parseArgs reads `<kind> -candidate SHA <flags>` from args, with an update step's run argv after `--`. Each kind
// takes only the flags of its own parameters, and Problem checks them as it checks a step's params. A tag step also
// takes -title. Any refusal wraps errUsage.
func parseArgs(args []string) (invocation, error) {
	if len(args) == 0 {
		return invocation{}, fmt.Errorf("%w: no kind", errUsage)
	}
	inv := invocation{kind: args[0]}
	if _, known := kindParams(inv.kind); !known {
		return invocation{}, fmt.Errorf("%w: unknown kind %q", errUsage, inv.kind)
	}
	flags := flag.NewFlagSet(Command+" "+inv.kind, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&inv.candidate, flagCandidate, "", "the commit the release carries")
	params := &inv.params
	var units unitList
	switch inv.kind {
	case KindPushMain:
		flags.StringVar(&params.Branch, flagBranch, "", "origin's branch to fast-forward")
	case KindTag:
		flags.StringVar(&params.Prefix, flagPrefix, "", "what every version tag starts with")
		flags.StringVar(&inv.title, flagTitle, "", "the change the tag's message names")
	case KindPinFlake:
		flags.StringVar(&params.Branch, flagBranch, "", "the flake origin's branch to pin on")
		flags.StringVar(&params.Prefix, flagPrefix, "", "what every version tag starts with")
		flags.StringVar(&params.Flake, flagFlake, "", "the flake repository")
		flags.StringVar(&params.Input, flagInput, "", "the flake input to pin")
		flags.StringVar(&params.URL, flagURL, "", "the input's url, with "+TagSlot+" where the tag goes")
	case KindUpdate:
		flags.StringVar(&params.Branch, flagBranch, "", "the flake origin's branch to build")
		flags.StringVar(&params.Flake, flagFlake, "", "the flake repository")
		flags.Var(&units, flagUnit, "a systemd user unit that must run the candidate's build")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return invocation{}, fmt.Errorf("%w: %w", errUsage, err)
	}
	params.Units = units
	rest := flags.Args()
	if inv.kind == KindUpdate {
		params.Run = rest
	} else if len(rest) > 0 {
		return invocation{}, fmt.Errorf("%w: unexpected argument %q", errUsage, rest[0])
	}
	if strings.TrimSpace(inv.candidate) == "" {
		return invocation{}, fmt.Errorf("%w: -%s: missing", errUsage, flagCandidate)
	}
	if field, reason := Problem(inv.kind, inv.params); field != "" {
		return invocation{}, fmt.Errorf("%w: %s: %s", errUsage, field, reason)
	}
	return inv, nil
}

// verdict is what a check of a postcondition found: whether it holds, the facts that show what was found, and, when it
// does not hold, why.
type verdict struct {
	held  bool
	facts []string
	why   string
}

// kindRun is the mechanics of one kind: the check of its postcondition and the step that brings it about.
type kindRun interface {
	// check reads the postcondition from origin's real state, fetching what it reads first.
	check(ctx context.Context) (verdict, error)
	// act carries out the step. It is called only after a check found the postcondition does not hold.
	act(ctx context.Context) error
}

// run carries out the step inv names with the mechanics of its kind (carry), from the candidate's full commit id. A
// flake path that starts with ~/ is read from the home directory.
func (s *step) run(ctx context.Context, inv invocation) error {
	candidate, err := s.resolveCandidate(ctx, inv.candidate)
	if err != nil {
		return err
	}
	params := inv.params
	switch inv.kind {
	case KindPushMain:
		return s.carry(ctx, &pushMain{s: s, candidate: candidate, branch: params.Branch})
	case KindTag:
		return s.carry(ctx, &tagStep{s: s, candidate: candidate, prefix: params.Prefix, title: inv.title})
	}
	flake, err := s.flakePath(params.Flake)
	if err != nil {
		return err
	}
	if inv.kind == KindPinFlake {
		return s.carry(ctx, &pinFlake{
			s: s, candidate: candidate, branch: params.Branch, prefix: params.Prefix, flake: flake, input: params.Input,
			url: params.URL,
		})
	}
	return s.carry(ctx, &update{
		s: s, candidate: candidate, branch: params.Branch, flake: flake, units: params.Units, argv: params.Run,
	})
}

// carry checks runner's postcondition, and when it already holds does nothing more. Otherwise it carries out the step,
// checks again, and fails when the postcondition does not hold.
func (s *step) carry(ctx context.Context, runner kindRun) error {
	before, err := runner.check(ctx)
	if err != nil {
		return err
	}
	if before.held {
		s.say(before.facts...)
		s.say("the postcondition held before the step, so nothing was done")
		return nil
	}
	if actErr := runner.act(ctx); actErr != nil {
		return actErr
	}
	after, err := runner.check(ctx)
	if err != nil {
		return err
	}
	s.say(after.facts...)
	if !after.held {
		return fmt.Errorf("the postcondition does not hold after the step: %s", after.why)
	}
	return nil
}

// resolveCandidate returns the full commit id of candidate in the repository, which must hold it.
func (s *step) resolveCandidate(ctx context.Context, candidate string) (string, error) {
	full, err := s.gitOut(ctx, s.repo, "rev-parse", "--verify", "--end-of-options", candidate+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("candidate %s is not a commit of the repository: %w", candidate, err)
	}
	return full, nil
}

// flakePath returns the flake repository's path: flake itself when it is absolute, or flake under the home directory
// when it starts with ~/.
func (s *step) flakePath(flake string) (string, error) {
	rest, home := strings.CutPrefix(flake, "~/")
	if !home {
		return filepath.Clean(flake), nil
	}
	if s.home == "" {
		return "", fmt.Errorf("flake %s starts with ~/ and there is no home directory", flake)
	}
	return filepath.Join(s.home, rest), nil
}

// say writes each fact to stdout as a line of its own.
func (s *step) say(facts ...string) {
	for _, fact := range facts {
		fmt.Fprintln(s.stdout, fact)
	}
}
