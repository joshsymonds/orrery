package enginehost

import (
	"slices"
	"testing"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestContractLintArgvRefusesElements asserts that an argv element holding white space outside a line after -c, or a
// quote the splitter cannot close, is refused under the field that names it. A clustered flag before the line does not
// exempt it, and the reason names the fix.
func TestContractLintArgvRefusesElements(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	runEpicRefusals(t, repo, []epicRefusal{
		{
			name: "capture element holds white space",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements[0].Item.Capture = []string{"scripts/in-devenv", "go test -run X ./pkg/"}
			},
			want: "requirements[0].item.capture[1]: holds white space: split the element into argv elements, " +
				"or pass the line to a shell after -c",
		},
		{
			name: "capture element holds an unclosed quote after -c",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements[0].Item.Capture = []string{"scripts/in-devenv", "-c", "echo 'open"}
			},
			want: "requirements[0].item.capture[2]: holds a quote the shell cannot close: split the element into " +
				"argv elements, or pass the line to a shell after -c",
		},
		{
			name: "gate argv element holds white space",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Gates[0].Argv = []string{"scripts/check", "go test ./pkg/"}
			},
			want: "gates[0].argv[1]: holds white space: split the element into argv elements, " +
				"or pass the line to a shell after -c",
		},
		{
			name: "white space after clustered -lc",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements[0].Item.Capture = []string{
					"scripts/in-devenv", "-lc", "go test -run X ./pkg/",
				}
			},
			want: "requirements[0].item.capture[2]: holds white space after -lc, and only -c exempts it: " +
				"write the flags apart, as -l -c",
		},
	})
}

// TestContractLintArgvAcceptsLineAfterDashC asserts that a line right after -c passes, including one that holds an
// apostrophe inside double quotes.
func TestContractLintArgvAcceptsLineAfterDashC(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	for _, tc := range []struct {
		name    string
		capture []string
	}{
		{name: "plain line", capture: []string{"scripts/in-devenv", "-c", "go test -run X ./pkg/"}},
		{name: "apostrophe inside double quotes", capture: []string{"scripts/in-devenv", "-c", `echo "it's fine"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := epicTestRequest(repo)
			req.Contract.Requirements[0].Item.Capture = tc.capture
			if err := checkEpic(t.Context(), req, epicTestGetenv); err != nil {
				t.Fatalf("checkEpic refused %q: %v", tc.capture, err)
			}
		})
	}
}

// TestContractLintArgvSplitsShellWords asserts the splitter reads words the way a POSIX shell does, and refuses a quote
// it cannot close.
func TestContractLintArgvSplitsShellWords(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		line    string
		want    []string
		wantErr bool
	}{
		{name: "plain words", line: "go test -run X ./pkg/", want: []string{"go", "test", "-run", "X", "./pkg/"}},
		{name: "single quotes keep white space", line: "echo 'a b' c", want: []string{"echo", "a b", "c"}},
		{name: "single quotes keep a double quote", line: `echo 'say "hi"'`, want: []string{"echo", `say "hi"`}},
		{name: "double quotes keep an apostrophe", line: `echo "it's"`, want: []string{"echo", "it's"}},
		{
			name: "double quotes escape a quote and a backslash",
			line: `echo "a \"b\" \\c"`,
			want: []string{"echo", `a "b" \c`},
		},
		{name: "double quotes keep other backslashes", line: `echo "a\nb"`, want: []string{"echo", `a\nb`}},
		{name: "backslash outside quotes escapes white space", line: `echo a\ b`, want: []string{"echo", "a b"}},
		{name: "backslash outside quotes escapes an apostrophe", line: `echo it\'s`, want: []string{"echo", "it's"}},
		{name: "empty quotes make empty words", line: `a '' ""`, want: []string{"a", "", ""}},
		{name: "trailing backslash is kept", line: `echo open\`, want: []string{"echo", `open\`}},
		{name: "unclosed single quote", line: "echo 'open", wantErr: true},
		{name: "unclosed double quote", line: `echo "open`, wantErr: true},
		{name: "escaped quote does not close", line: `echo "a\"`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := splitShellWords(tc.line)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("splitShellWords(%q) = %q, want an error", tc.line, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitShellWords(%q): %v", tc.line, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("splitShellWords(%q) = %q, want %q", tc.line, got, tc.want)
			}
		})
	}
}
