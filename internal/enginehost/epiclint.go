package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// The reasons a lint refusal gives after its field. A refused element is split into argv elements, or handed to a
// shell after -c; a clustered flag before it is written apart.
const (
	epicReasonArgvSpace = "holds white space: split the element into argv elements, " +
		"or pass the line to a shell after -c"
	epicReasonArgvQuote = "holds a quote the shell cannot close: split the element into argv elements, " +
		"or pass the line to a shell after -c"
	epicReasonArgvClustered = "holds white space after %s, and only -c exempts it: write the flags apart, as %s"
)

// The reasons a release step warning gives after its field. The version reason takes the version the step names.
const (
	epicReasonReleaseVersion = "names the fixed version %s: a release step's tag is chosen when the step runs; " +
		"name the next version above the newest tag instead"
	epicReasonReleaseCI = `a release does not wait for CI; write "do not wait for CI"`
)

// errShellQuote is what splitShellWords returns for a quote it cannot close.
var errShellQuote = errors.New("quote not closed")

// clusteredFlagPattern matches a short flag cluster: a dash and two or more letters.
var clusteredFlagPattern = regexp.MustCompile(`^-[A-Za-z]{2,}$`)

// checkContractLint returns the first refusal of an argv element the shell or the splitter would misread. It runs after
// every other check of checkEpic, so each element it reads has passed them. It checks the argv elements of the gates in
// order, then those of each requirement's capture.
func checkContractLint(contract *v1.EpicContract) error {
	for i, gate := range contract.GetGates() {
		if _, err := checkArgvElements(fmt.Sprintf("gates[%d].argv", i), gate.GetArgv()); err != nil {
			return err
		}
	}
	for i, requirement := range contract.GetRequirements() {
		if _, err := checkArgvElements(
			fmt.Sprintf("requirements[%d].item.capture", i), requirement.GetItem().GetCapture(),
		); err != nil {
			return err
		}
	}
	return nil
}

// contractLintWarnings returns the warnings the lint gives a contract that checkEpic passes, in order: one for each go
// test in a capture whose -run pattern matches a test at the base, then one for each release step whose text says
// merge, names a fixed version or waits on CI. Each names its field and reason. It runs after checkContractLint, so each
// argv element it reads has passed it. A git failure reading a go test's package is returned, so no check passes on it.
func contractLintWarnings(ctx context.Context, contract *v1.EpicContract) ([]string, error) {
	base, err := stepGitResolve(ctx, contract.GetRepository(), contract.GetBase())
	if err != nil {
		return nil, err
	}
	var warnings []string
	for i, requirement := range contract.GetRequirements() {
		list := fmt.Sprintf("requirements[%d].item.capture", i)
		argv := requirement.GetItem().GetCapture()
		var lines [][]string
		lines, err = checkArgvElements(list, argv)
		if err != nil {
			return nil, err
		}
		for _, args := range goTestArgs(argv, lines) {
			var warning string
			warning, err = goTestWarning(ctx, contract.GetRepository(), base, list, args)
			if err != nil {
				return nil, err
			}
			if warning != "" {
				warnings = append(warnings, warning)
			}
		}
	}
	return append(warnings, releaseStepWarnings(contract.GetReleaseSteps())...), nil
}

// The patterns of a release step's text. The CI rule matches a whole word; the version rule matches a lowercase v with
// three numbers, as the contract writes a version. The phrase is removed before the CI rule reads.
var (
	releaseVersionPattern = regexp.MustCompile(`\bv[0-9]+\.[0-9]+\.[0-9]+`)
	releaseWaitPattern    = regexp.MustCompile(`(?i)do not wait for CI`)
	releaseCIPattern      = regexp.MustCompile(`\bCI\b`)
)

// releaseStepWarnings returns a warning for each release step whose text names a fixed version or waits on CI, in the
// order of the steps.
func releaseStepWarnings(steps []*v1.EpicReleaseStep) []string {
	var warnings []string
	for i, step := range steps {
		if reason := releaseStepReason(step.GetStep()); reason != "" {
			warnings = append(warnings, fmt.Sprintf("%s: %s", epicRowField("release_steps", i, "step"), reason))
		}
	}
	return warnings
}

// releaseStepReason returns the reason a release step's text is warned, or "" when it passes. The rules run in order,
// and the first one that matches gives the reason.
func releaseStepReason(text string) string {
	if version := releaseVersionPattern.FindString(text); version != "" {
		return fmt.Sprintf(epicReasonReleaseVersion, version)
	}
	if releaseCIPattern.MatchString(releaseWaitPattern.ReplaceAllString(text, "")) {
		return epicReasonReleaseCI
	}
	return ""
}

// checkArgvElements returns the first refusal of an argv list, whose elements are named list[j]. An element that holds
// white space is refused unless the element before it is exactly -c, since the shell reads the line after -c as one
// command. A clustered flag before it, such as -lc, does not exempt it. An element whose quotes the splitter cannot
// close is refused whether or not it holds white space.
//
// It also returns the words splitShellWords read from each element that comes right after an element exactly -c, in
// order, so a caller reads a shell line's words without splitting it again.
func checkArgvElements(list string, argv []string) ([][]string, error) {
	var lines [][]string
	for j, element := range argv {
		field := fmt.Sprintf("%s[%d]", list, j)
		if strings.IndexFunc(element, unicode.IsSpace) >= 0 && (j == 0 || argv[j-1] != "-c") {
			return nil, &epicFieldError{field: field, reason: argvSpaceReason(argv, j)}
		}
		words, err := splitShellWords(element)
		if err != nil {
			return nil, &epicFieldError{field: field, reason: epicReasonArgvQuote}
		}
		if j > 0 && argv[j-1] == "-c" {
			lines = append(lines, words)
		}
	}
	return lines, nil
}

// argvSpaceReason returns the reason for the element at j, which holds white space and is not after -c. After a
// clustered flag it names the fix: the flags written apart, with -c last.
func argvSpaceReason(argv []string, j int) string {
	if j == 0 || !isClusteredFlag(argv[j-1]) {
		return epicReasonArgvSpace
	}
	return fmt.Sprintf(epicReasonArgvClustered, argv[j-1], apartFlags(argv[j-1]))
}

// isClusteredFlag reports whether arg is a short flag cluster that holds c, such as -lc.
func isClusteredFlag(arg string) bool {
	return clusteredFlagPattern.MatchString(arg) && strings.Contains(arg, "c")
}

// apartFlags writes the flags of a clustered flag such as -lc as separate flags, with -c last: -l -c.
func apartFlags(cluster string) string {
	var flags []string
	for _, letter := range cluster[1:] {
		if letter != 'c' {
			flags = append(flags, "-"+string(letter))
		}
	}
	return strings.Join(append(flags, "-c"), " ")
}

// goTestNamePattern matches the line of a Test function. It captures the name: Test, then nothing or a character that
// is not a lowercase letter, then the rest of the name.
var goTestNamePattern = regexp.MustCompile(`^func (Test(?:[^a-z(][A-Za-z0-9_]*)?)\(`)

// shellExpansion matches a backtick, or a $ that starts an expansion of a name, a command or arithmetic. A pattern or
// package holding one is not literal: the shell rewrites it before go test reads it.
var shellExpansion = regexp.MustCompile("`|\\$[A-Za-z_{(]")

// goTestArgs returns the arguments after each go test the capture runs: those of each go test in the capture argv, and
// those of each go test in a line after -c.
func goTestArgs(argv []string, lines [][]string) [][]string {
	found := goTestArgsIn(argv, false)
	for _, line := range lines {
		found = append(found, goTestArgsIn(line, true)...)
	}
	return found
}

// goTestArgsIn returns the arguments after each go test in words. A go test is a word whose base name is go, followed
// by the word test. Its arguments run to the end of words, or, in a shell line, to the next word that ends the
// invocation.
func goTestArgsIn(words []string, shell bool) [][]string {
	var found [][]string
	for k := 0; k+1 < len(words); k++ {
		if filepath.Base(words[k]) != "go" || words[k+1] != "test" {
			continue
		}
		args := words[k+2:]
		if shell {
			if end := slices.IndexFunc(args, isShellOperator); end >= 0 {
				args = args[:end]
			}
		}
		found = append(found, args)
	}
	return found
}

// isShellOperator reports whether word ends a shell invocation: &&, ||, ; or |.
func isShellOperator(word string) bool {
	switch word {
	case "&&", "||", ";", "|":
		return true
	}
	return false
}

// takesSeparateValue reports whether a go test or go build flag takes a separate value, which is skipped with the flag.
// A flag written as name=value takes no separate value, and the caller checks that.
func takesSeparateValue(flag string) bool {
	switch flag {
	case "count", "timeout", "tags", "p", "parallel", "cpu", "bench", "benchtime", "skip", "shuffle",
		"coverprofile", "covermode", "coverpkg", "o", "exec", "fuzz", "fuzztime", "list", "C", "ldflags",
		"gcflags", "mod", "modfile", "outputdir", "cpuprofile", "memprofile", "blockprofile", "mutexprofile",
		"trace":
		return true
	}
	return false
}

// globEscape escapes the characters git's glob pathspec magic reads as wildcards, so a directory name matches as
// written.
func globEscape(dir string) string {
	return strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`).Replace(dir)
}

// goTestCall is the -run pattern of one go test and the words that name its packages.
type goTestCall struct {
	pattern  string
	hasRun   bool
	packages []string
}

// parseGoTestArgs reads the -run pattern and the packages of a go test's arguments. The last -run names the pattern, as
// go test takes it. Arguments from -args on belong to the test binary and are not read.
func parseGoTestArgs(args []string) goTestCall {
	var call goTestCall
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-args" || arg == "--args" {
			break
		}
		if len(arg) < 2 || !strings.HasPrefix(arg, "-") {
			call.packages = append(call.packages, arg)
			continue
		}
		flag, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		switch {
		case flag == "run" && hasValue:
			call.pattern, call.hasRun = value, true
		case flag == "run" && i+1 < len(args):
			i++
			call.pattern, call.hasRun = args[i], true
		case !hasValue && takesSeparateValue(flag):
			i++
		}
	}
	return call
}

// topLevelElement returns the first element of a -run pattern, split as the testing package splits it: on a / outside
// brackets and parentheses. An escaped \/ stays in the element.
func topLevelElement(pattern string) string {
	brackets, parens := 0, 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '[':
			brackets++
		case ']':
			if brackets--; brackets < 0 {
				brackets = 0
			}
		case '(':
			if brackets == 0 {
				parens++
			}
		case ')':
			if brackets == 0 {
				parens--
			}
		case '\\':
			i++
		case '/':
			if brackets == 0 && parens == 0 {
				return pattern[:i]
			}
		}
	}
	return pattern
}

// goTestScope is the directories of a package word at the base: dir alone, or dir and every directory below it.
type goTestScope struct {
	dir       string
	recursive bool
}

// goTestScopes returns the scope of each package word. With no package word, the scope is the repository root alone. It
// returns false when a package word is not literal.
func goTestScopes(packages []string) ([]goTestScope, bool) {
	if len(packages) == 0 {
		return []goTestScope{{dir: "."}}, true
	}
	scopes := make([]goTestScope, 0, len(packages))
	for _, word := range packages {
		scope, ok := goTestScopeOf(word)
		if !ok {
			return nil, false
		}
		scopes = append(scopes, scope)
	}
	return scopes, true
}

// goTestScopeOf returns the scope of a package word, and false when the word is not literal: it holds an expansion, or
// it neither starts with ./ nor is a bare dot.
func goTestScopeOf(word string) (goTestScope, bool) {
	if shellExpansion.MatchString(word) {
		return goTestScope{}, false
	}
	if word == "." {
		return goTestScope{dir: "."}, true
	}
	rest, ok := strings.CutPrefix(word, "./")
	if !ok {
		return goTestScope{}, false
	}
	if rest == "..." {
		return goTestScope{dir: ".", recursive: true}, true
	}
	dir, recursive := strings.CutSuffix(rest, "/...")
	return goTestScope{dir: path.Clean(dir), recursive: recursive}, true
}

// pathspec returns the git pathspec that selects the *_test.go files of the scope: those directly in dir, or those in
// dir and below. Glob magic keeps a * from matching across a slash.
func (s goTestScope) pathspec() string {
	prefix := ""
	if s.dir != "." {
		prefix = globEscape(s.dir) + "/"
	}
	if s.recursive {
		return ":(glob)" + prefix + "**/*_test.go"
	}
	return ":(glob)" + prefix + "*_test.go"
}

// goTestFunc is a top-level Test function a test file declares at the base.
type goTestFunc struct {
	name, file string
}

// goTestFuncsAtBase returns the top-level Test functions that the test files of scope declare at base, in the order
// git grep lists them. TestMain is not a test. A git grep that finds no line is not an error; any other failure of
// git grep is returned, so no check passes on it.
func goTestFuncsAtBase(ctx context.Context, repo, base string, scope goTestScope) ([]goTestFunc, error) {
	out, err := stepGit(ctx, repo, "grep", "-z", "-e", "^func Test", base, "--", scope.pathspec())
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	var funcs []goTestFunc
	for record := range strings.SplitSeq(out, "\n") {
		// Each record is <base>:<path>, a NUL, then the line.
		location, line, ok := strings.Cut(record, "\x00")
		if !ok {
			continue
		}
		_, file, _ := strings.Cut(location, ":")
		match := goTestNamePattern.FindStringSubmatch(line)
		if len(match) == 0 || match[1] == "TestMain" {
			continue
		}
		funcs = append(funcs, goTestFunc{name: match[1], file: file})
	}
	return funcs, nil
}

// goTestWarning returns the warning of one go test whose arguments are args, when its -run pattern matches a Test
// function at base in the packages it names, and "" when it does not. It gives no warning when its pattern or a package
// is not literal, or when the top-level element of the pattern does not compile.
func goTestWarning(ctx context.Context, repo, base, list string, args []string) (string, error) {
	call := parseGoTestArgs(args)
	if !call.hasRun || shellExpansion.MatchString(call.pattern) {
		return "", nil
	}
	matcher, compiles := topLevelMatcher(call.pattern)
	if !compiles {
		return "", nil
	}
	scopes, literal := goTestScopes(call.packages)
	if !literal {
		return "", nil
	}
	for _, scope := range scopes {
		funcs, err := goTestFuncsAtBase(ctx, repo, base, scope)
		if err != nil {
			return "", err
		}
		for _, fn := range funcs {
			if matcher.MatchString(fn.name) {
				return fmt.Sprintf("%s: -run pattern %q matches %s in %s at the base: name a new top-level test",
					list, call.pattern, fn.name, displayDir(path.Dir(fn.file))), nil
			}
		}
	}
	return "", nil
}

// topLevelMatcher returns the matcher of the top-level element of a -run pattern, and false when that element does not
// compile.
func topLevelMatcher(pattern string) (*regexp.Regexp, bool) {
	matcher, err := regexp.Compile(topLevelElement(pattern))
	return matcher, err == nil
}

// displayDir names a package directory as a refusal does: ./dir, or ./ for the repository root.
func displayDir(dir string) string {
	if dir == "." {
		return "./"
	}
	return "./" + dir
}

// splitShellWords splits line into the words a POSIX shell reads from it, expanding nothing. White space outside
// quotes ends a word, and a backslash-newline outside quotes is removed. A single-quoted run is taken literally. In a
// double-quoted run a backslash before \, ", $, ` or a newline escapes that character, and any other backslash is
// kept. A backslash outside quotes escapes the character after it, and a trailing one is kept. It returns
// errShellQuote for a quote it cannot close.
func splitShellWords(line string) ([]string, error) {
	var words []string
	var word strings.Builder
	inWord := false
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == ' ' || line[i] == '\t' || line[i] == '\n':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '\n':
			i++
		default:
			inWord = true
			end, err := readShellChars(line, i, &word)
			if err != nil {
				return nil, err
			}
			i = end
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words, nil
}

// readShellChars writes what the character at from contributes to its word into word: a single-quoted run, a
// double-quoted run, an escaped character, or the character itself. It returns the index of the last character it
// read, which is the closing quote of a run.
func readShellChars(line string, from int, word *strings.Builder) (int, error) {
	switch line[from] {
	case '\'':
		end := strings.IndexByte(line[from+1:], '\'')
		if end < 0 {
			return 0, errShellQuote
		}
		word.WriteString(line[from+1 : from+1+end])
		return from + 1 + end, nil
	case '"':
		return readDoubleQuoted(line, from+1, word)
	case '\\':
		if from+1 == len(line) {
			word.WriteByte('\\')
			return from, nil
		}
		word.WriteByte(line[from+1])
		return from + 1, nil
	default:
		word.WriteByte(line[from])
		return from, nil
	}
}

// readDoubleQuoted writes the characters of a double-quoted run that begins at from into word, and returns the index
// of the quote that closes it. It returns errShellQuote when the run is never closed.
func readDoubleQuoted(line string, from int, word *strings.Builder) (int, error) {
	for i := from; i < len(line); i++ {
		switch {
		case line[i] == '"':
			return i, nil
		case line[i] == '\\' && i+1 < len(line) && strings.IndexByte("\\\"$`\n", line[i+1]) >= 0:
			i++
			if line[i] != '\n' {
				word.WriteByte(line[i])
			}
		default:
			word.WriteByte(line[i])
		}
	}
	return 0, errShellQuote
}
