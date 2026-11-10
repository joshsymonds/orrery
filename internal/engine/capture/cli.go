package capture

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Config supplies the environment, the claude lookup, and the output streams for Main. Environ is the
// environment a probe may inherit, filtered by the probe's allow-list. LookPath finds the claude binary.
type Config struct {
	Environ        []string
	LookPath       func(string) (string, error)
	Stdout, Stderr io.Writer
}

const (
	cliExitOK          = 0
	cliExitFail        = 1
	cliExitUsage       = 2
	cliCommand         = "claude"
	cliTempPattern     = "orrery-capture-"
	cliRawDirMode      = 0o700
	cliRawFileMode     = 0o600
	cliFixtureDirMode  = 0o750
	cliFixtureFileMode = 0o644
	cliUsage           = "usage: enginecapture record -raw DIR [-probe NAME] | " +
		"sanitize -raw DIR -out DIR | accept [-probe NAME]"
)

// Main runs the enginecapture subcommand named by args[0] and returns its exit status. Usage errors
// return 2 with the usage on cfg.Stderr. Failures of a probe return 1.
func Main(args []string, cfg Config) int {
	if len(args) == 0 {
		return cliUsageError(cfg, "")
	}
	switch args[0] {
	case "record":
		return cliRecord(args[1:], cfg)
	case "sanitize":
		return cliSanitise(args[1:], cfg)
	case "accept":
		return cliAccept(args[1:], cfg)
	default:
		return cliUsageError(cfg, fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}

// cliRecord runs the probes and keeps their raw output in the named directory.
func cliRecord(args []string, cfg Config) int {
	flags := cliFlags("record", cfg)
	raw := flags.String("raw", "", "directory that receives the raw output")
	name := flags.String("probe", "", "run only the probe with this name")
	if err := flags.Parse(args); err != nil {
		return cliFlagExit(err)
	}
	if *raw == "" {
		return cliUsageError(cfg, "record needs -raw DIR")
	}
	probes, err := cliSelect(*name)
	if err != nil {
		return cliUsageError(cfg, err.Error())
	}
	binary, err := cliBinary(cfg)
	if err != nil {
		return cliFail(cfg, err)
	}
	if err = os.MkdirAll(*raw, cliRawDirMode); err != nil {
		return cliFail(cfg, fmt.Errorf("capture: create raw directory: %w", err))
	}
	for _, result := range cliRunAll(probes, binary, *raw, cfg) {
		if result.err != nil {
			return cliExitFail
		}
	}
	return cliExitOK
}

// cliSanitise writes the sanitized fixtures of every probe whose raw output is in the raw directory.
func cliSanitise(args []string, cfg Config) int {
	flags := cliFlags("sanitize", cfg)
	raw := flags.String("raw", "", "directory of raw output from record")
	out := flags.String("out", "", "directory that receives the fixtures")
	if err := flags.Parse(args); err != nil {
		return cliFlagExit(err)
	}
	if *raw == "" || *out == "" {
		return cliUsageError(cfg, "sanitize needs -raw DIR and -out DIR")
	}
	if err := os.MkdirAll(*out, cliFixtureDirMode); err != nil {
		return cliFail(cfg, fmt.Errorf("capture: create fixture directory: %w", err))
	}
	if err := cliSanitiseAll(*raw, *out); err != nil {
		return cliFail(cfg, err)
	}
	return cliExitOK
}

// cliAccept reruns the probes into a temporary directory that it removes on every path, checks each
// probe's stdout, and fails when a probe is incomplete, timed out, malformed, or missing an expectation.
func cliAccept(args []string, cfg Config) (code int) {
	flags := cliFlags("accept", cfg)
	name := flags.String("probe", "", "check only the probe with this name")
	if err := flags.Parse(args); err != nil {
		return cliFlagExit(err)
	}
	probes, err := cliSelect(*name)
	if err != nil {
		return cliUsageError(cfg, err.Error())
	}
	binary, err := cliBinary(cfg)
	if err != nil {
		return cliFail(cfg, err)
	}
	dir, err := os.MkdirTemp("", cliTempPattern)
	if err != nil {
		return cliFail(cfg, fmt.Errorf("capture: create temporary directory: %w", err))
	}
	defer func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			fmt.Fprintf(cfg.Stderr, "enginecapture: remove %s: %v\n", dir, removeErr)
			code = cliExitFail
		}
	}()
	failed := false
	for _, result := range cliRunAll(probes, binary, dir, cfg) {
		failed = failed || cliProbeFailed(result)
	}
	if failed {
		return cliExitFail
	}
	return cliExitOK
}

// cliProbe is the result of one probe: what its summary line reports and what the exit status depends on.
type cliProbe struct {
	name     string
	started  bool
	outcome  Outcome
	report   Report
	checkErr error
	err      error
}

// cliRunAll runs each probe into dir, prints one summary line per probe, and returns the results.
func cliRunAll(probes []Probe, binary, dir string, cfg Config) []cliProbe {
	results := make([]cliProbe, 0, len(probes))
	for _, probe := range probes {
		result := cliRunProbe(probe, binary, dir, cfg.Environ)
		fmt.Fprintln(cfg.Stdout, cliSummary(result))
		results = append(results, result)
	}
	return results
}

// cliRunProbe runs probe with the claude at binary, keeps its raw output in dir, and checks its stdout.
// A probe whose run fails keeps the Outcome it observed, and the error is kept beside it.
func cliRunProbe(probe Probe, binary, dir string, environ []string) cliProbe {
	result := cliProbe{name: probe.Name}
	cmd, err := NewCommand(probe, binary, environ)
	if err != nil {
		result.err = fmt.Errorf("capture: prepare %s: %w", probe.Name, err)
		return result
	}
	result.started = true
	var in, out bytes.Buffer
	outcome, runErr := Run(context.Background(), probe, cmd, Sinks{In: &in, Out: &out})
	result.outcome = outcome
	result.err = errors.Join(
		runErr,
		cliRemoveDir(cmd.Dir),
		cliWriteRaw(dir, probe.Name, in.Bytes(), out.Bytes(), outcome.Stderr),
	)
	result.report, result.checkErr = Check(out.Bytes(), probe.Expect)
	return result
}

// cliProbeFailed reports whether accept must fail on result.
func cliProbeFailed(result cliProbe) bool {
	return result.err != nil || result.checkErr != nil || !result.outcome.Completed ||
		result.outcome.TimedOut || len(result.report.Missing) > 0
}

// cliRemoveDir removes the working directory of a probe that has run.
func cliRemoveDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("capture: remove working directory: %w", err)
	}
	return nil
}

// cliWriteRaw writes the raw input, the raw stdout, and the stderr of one probe into dir.
func cliWriteRaw(dir, name string, in, out []byte, stderr string) error {
	return errors.Join(
		cliWriteFile(filepath.Join(dir, name+".in.ndjson"), in, cliRawFileMode),
		cliWriteFile(filepath.Join(dir, name+".out.ndjson"), out, cliRawFileMode),
		cliWriteFile(filepath.Join(dir, name+".stderr"), []byte(stderr), cliRawFileMode),
	)
}

// cliWriteFile writes data to path with the given mode.
func cliWriteFile(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return fmt.Errorf("capture: write %s: %w", path, err)
	}
	return nil
}

// cliSanitiseAll writes the fixtures of every probe whose raw stdout is in rawDir. Reads stay inside
// rawDir and writes stay inside outDir.
func cliSanitiseAll(rawDir, outDir string) (err error) {
	raw, err := os.OpenRoot(rawDir)
	if err != nil {
		return fmt.Errorf("capture: open raw directory: %w", err)
	}
	out, err := os.OpenRoot(outDir)
	if err != nil {
		return errors.Join(fmt.Errorf("capture: open fixture directory: %w", err), raw.Close())
	}
	defer func() { err = errors.Join(err, raw.Close(), out.Close()) }()
	for _, probe := range Probes() {
		_, statErr := raw.Stat(probe.Name + ".out.ndjson")
		if errors.Is(statErr, fs.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return fmt.Errorf("capture: stat %s output: %w", probe.Name, statErr)
		}
		if probeErr := cliSanitiseProbe(probe.Name, raw, out); probeErr != nil {
			return probeErr
		}
	}
	return nil
}

// cliSanitiseProbe writes the fixtures of one probe. Both streams go through one Sanitiser, so an id
// maps to the same made-up value in the stdout fixture and the stdin fixture.
func cliSanitiseProbe(name string, raw, out *os.Root) error {
	sanitiser := NewSanitiser()
	for _, suffix := range []string{".out.ndjson", ".in.ndjson"} {
		data, err := raw.ReadFile(name + suffix)
		if err != nil {
			return fmt.Errorf("capture: read %s%s: %w", name, suffix, err)
		}
		fixture, err := cliSanitiseStream(sanitiser, data)
		if err != nil {
			return fmt.Errorf("capture: sanitize %s%s: %w", name, suffix, err)
		}
		if writeErr := out.WriteFile(name+suffix, fixture, cliFixtureFileMode); writeErr != nil {
			return fmt.Errorf("capture: write fixture %s%s: %w", name, suffix, writeErr)
		}
	}
	return nil
}

// cliSanitiseStream returns the sanitized form of every kept line of raw, each followed by a newline.
func cliSanitiseStream(sanitiser *Sanitiser, raw []byte) ([]byte, error) {
	var fixture bytes.Buffer
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		clean, keep, err := sanitiser.Line(line)
		if err != nil {
			return nil, err
		}
		if !keep {
			continue
		}
		fixture.Write(clean)
		fixture.WriteByte('\n')
	}
	return fixture.Bytes(), nil
}

// cliSummary formats the summary line of one probe. It names the completion, a timeout, the exit status,
// each missing expectation, each unknown type, each unanswered request, and any error.
func cliSummary(result cliProbe) string {
	fields := []string{
		cliStatus(result.outcome),
		"exit " + cliExitStatus(result),
		"missing " + cliExpectations(result.report.Missing),
		"unknown " + cliStrings(result.report.Unknown),
		"unanswered " + cliStrings(result.outcome.Unanswered),
	}
	if result.checkErr != nil {
		fields = append(fields, "check: "+result.checkErr.Error())
	}
	if result.err != nil {
		fields = append(fields, "error: "+result.err.Error())
	}
	return result.name + ": " + strings.Join(fields, "; ")
}

// cliStatus names whether the probe completed and whether it timed out.
func cliStatus(outcome Outcome) string {
	status := "incomplete"
	if outcome.Completed {
		status = "completed"
	}
	if outcome.TimedOut {
		status += ", timed out"
	}
	return status
}

// cliExitStatus names the exit status of a probe that started: its code, or the error the process ended
// with when it has no code.
func cliExitStatus(result cliProbe) string {
	if !result.started {
		return "not started"
	}
	if result.outcome.ExitErr == nil {
		return "0"
	}
	var exitErr *exec.ExitError
	if errors.As(result.outcome.ExitErr, &exitErr) && exitErr.ExitCode() >= 0 {
		return strconv.Itoa(exitErr.ExitCode())
	}
	return result.outcome.ExitErr.Error()
}

// cliExpectations formats each expectation that is missing, or none.
func cliExpectations(missing []Expectation) string {
	items := make([]string, 0, len(missing))
	for _, want := range missing {
		text := want.Type
		if want.Subtype != "" {
			text += "/" + want.Subtype
		}
		if want.Count > 1 {
			text += " x" + strconv.Itoa(want.Count)
		}
		items = append(items, text)
	}
	return cliStrings(items)
}

// cliStrings joins items with commas, or returns none when there are none.
func cliStrings(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

// cliSelect returns the probe named by name, or every probe when name is empty.
func cliSelect(name string) ([]Probe, error) {
	if name == "" {
		return Probes(), nil
	}
	for _, probe := range Probes() {
		if probe.Name == name {
			return []Probe{probe}, nil
		}
	}
	return nil, fmt.Errorf("unknown probe %q", name)
}

// cliBinary finds the claude binary with the configured lookup.
func cliBinary(cfg Config) (string, error) {
	binary, err := cfg.LookPath(cliCommand)
	if err != nil {
		return "", fmt.Errorf("capture: find %s: %w", cliCommand, err)
	}
	return binary, nil
}

// cliFlags returns a flag set for one subcommand that reports its errors on cfg.Stderr.
func cliFlags(name string, cfg Config) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(cfg.Stderr)
	return flags
}

// cliFlagExit returns the exit status for a flag parse error: success for -h, usage for anything else.
func cliFlagExit(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return cliExitOK
	}
	return cliExitUsage
}

// cliUsageError prints msg, when it is set, and the usage on cfg.Stderr, and returns the usage status.
func cliUsageError(cfg Config, msg string) int {
	if msg != "" {
		fmt.Fprintf(cfg.Stderr, "enginecapture: %s\n", msg)
	}
	fmt.Fprintln(cfg.Stderr, cliUsage)
	return cliExitUsage
}

// cliFail prints err on cfg.Stderr and returns the failure status.
func cliFail(cfg Config, err error) int {
	fmt.Fprintf(cfg.Stderr, "enginecapture: %v\n", err)
	return cliExitFail
}
