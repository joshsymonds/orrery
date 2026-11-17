package enginehost

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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

// errShellQuote is what splitShellWords returns for a quote it cannot close.
var errShellQuote = errors.New("quote not closed")

// clusteredFlagPattern matches a short flag cluster: a dash and two or more letters.
var clusteredFlagPattern = regexp.MustCompile(`^-[A-Za-z]{2,}$`)

// checkContractLint returns the first refusal of wording that would strand the epic when it runs. It runs after every
// other check of checkEpic, so each element it reads has passed them: the argv elements of the gates in order, then
// the capture elements of the requirements in order.
func checkContractLint(_ context.Context, contract *v1.EpicContract) error {
	for i, gate := range contract.GetGates() {
		if err := checkArgvElements(fmt.Sprintf("gates[%d].argv", i), gate.GetArgv()); err != nil {
			return err
		}
	}
	for i, requirement := range contract.GetRequirements() {
		list := fmt.Sprintf("requirements[%d].item.capture", i)
		if err := checkArgvElements(list, requirement.GetItem().GetCapture()); err != nil {
			return err
		}
	}
	return nil
}

// checkArgvElements returns the first refusal of an argv list, whose elements are named list[j]. An element that holds
// white space is refused unless the element before it is exactly -c, since the shell reads the line after -c as one
// command. A clustered flag before it, such as -lc, does not exempt it. An element whose quotes the splitter cannot
// close is refused whether or not it holds white space.
func checkArgvElements(list string, argv []string) error {
	for j, element := range argv {
		field := fmt.Sprintf("%s[%d]", list, j)
		if strings.IndexFunc(element, unicode.IsSpace) >= 0 && (j == 0 || argv[j-1] != "-c") {
			return &epicFieldError{field: field, reason: argvSpaceReason(argv, j)}
		}
		if _, err := splitShellWords(element); err != nil {
			return &epicFieldError{field: field, reason: epicReasonArgvQuote}
		}
	}
	return nil
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
