package enginehost

import (
	"errors"
	"fmt"
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
			t.Logf("checkEpic accepted capture %q", tc.capture)
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

// contractLintRunRepo returns a synthetic repository whose base declares TestX in pkg, the test the go test captures
// of the ContractLintRun tests name.
func contractLintRunRepo(t *testing.T) string {
	t.Helper()
	repo := stepGitTestRepo(t)
	stepGitTestCommit(t, repo, map[string]string{
		"pkg/x_test.go": "package pkg\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n",
	})
	return repo
}

// contractLintRunRefusal is the refusal of a capture's go test whose -run pattern matches TestX in ./pkg.
func contractLintRunRefusal(pattern string) string {
	return fmt.Sprintf("requirements[0].item.capture: -run pattern %q matches TestX in ./pkg at the base: "+
		"name a new top-level test", pattern)
}

// TestContractLintRunRefusesGoTestAtBase asserts that a go test whose -run pattern matches a Test function present at
// the base in the packages it names is refused under the capture's field. It holds whether the go test is in the argv
// or in a line after -c, and whether the packages are named or are ./....
func TestContractLintRunRefusesGoTestAtBase(t *testing.T) {
	t.Parallel()
	repo := contractLintRunRepo(t)
	runEpicRefusals(t, repo, []epicRefusal{
		{
			name: "env runs go test in argv",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements[0].Item.Capture = []string{
					"/usr/bin/env", "go", "test", "-run", "X", "./pkg/",
				}
			},
			want: contractLintRunRefusal("X"),
		},
		{
			name: "shell line runs go test after -c",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements[0].Item.Capture = []string{
					"scripts/in-devenv", "-c", "go test -race -count=1 -v -run X ./pkg/",
				}
			},
			want: contractLintRunRefusal("X"),
		},
		{
			name: "-run=X joined to its value",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements[0].Item.Capture = []string{"/usr/bin/env", "go", "test", "-run=X", "./pkg/"}
			},
			want: contractLintRunRefusal("X"),
		},
		{
			name: "--run X with two dashes",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements[0].Item.Capture = []string{
					"/usr/bin/env", "go", "test", "--run", "X", "./pkg/",
				}
			},
			want: contractLintRunRefusal("X"),
		},
		{
			name: "./... names every package",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements[0].Item.Capture = []string{"/usr/bin/env", "go", "test", "-run", "X", "./..."}
			},
			want: contractLintRunRefusal("X"),
		},
		{
			name: "subtest of a matching top-level test",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements[0].Item.Capture = []string{
					"/usr/bin/env", "go", "test", "-run", "X/Sub", "./pkg/",
				}
			},
			want: contractLintRunRefusal("X/Sub"),
		},
	})
}

// TestContractLintRunAcceptsCaptures asserts that a go test is not refused when its -run pattern matches no test at the
// base, or when the pattern is not literal or its top-level element does not compile.
func TestContractLintRunAcceptsCaptures(t *testing.T) {
	t.Parallel()
	repo := contractLintRunRepo(t)
	for _, tc := range []struct {
		name    string
		capture []string
	}{
		{name: "pattern matches no test", capture: []string{"/usr/bin/env", "go", "test", "-run", "Y", "./pkg/"}},
		// Read as written, $PAT|X matches TestX through its X alternative, so only the shell-expansion check accepts it.
		{
			name:    "pattern holds a shell variable",
			capture: []string{"scripts/in-devenv", "-c", `go test -run "$PAT|X" ./pkg/`},
		},
		{name: "pattern does not compile", capture: []string{"scripts/in-devenv", "-c", "go test -run '(' ./pkg/"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := epicTestRequest(repo)
			req.Contract.Requirements[0].Item.Capture = tc.capture
			if err := checkEpic(t.Context(), req, epicTestGetenv); err != nil {
				t.Fatalf("checkEpic refused %q: %v", tc.capture, err)
			}
			t.Logf("checkEpic accepted capture %q", tc.capture)
		})
	}
}

// TestContractLintRunAcceptsNonLiteralPackages asserts that a go test is not refused when a package word is not
// literal: it holds a shell expansion, or it neither starts with ./ nor is a bare dot. The directories the words name
// hold TestX at the base, so each capture would be refused if its word were read as written.
func TestContractLintRunAcceptsNonLiteralPackages(t *testing.T) {
	t.Parallel()
	repo := contractLintRunRepo(t)
	stepGitTestCommit(t, repo, map[string]string{
		"pkg$V/x_test.go": "package pkg\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n",
	})
	for _, tc := range []struct {
		name    string
		capture []string
	}{
		{name: "package holds a shell variable", capture: []string{"scripts/in-devenv", "-c", "go test -run X ./pkg$V/"}},
		{name: "package is not a ./ path", capture: []string{"/usr/bin/env", "go", "test", "-run", "X", "pkg"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := epicTestRequest(repo)
			req.Contract.Requirements[0].Item.Capture = tc.capture
			if err := checkEpic(t.Context(), req, epicTestGetenv); err != nil {
				t.Fatalf("checkEpic refused %q: %v", tc.capture, err)
			}
			t.Logf("checkEpic accepted capture %q", tc.capture)
		})
	}
}

// TestContractLintRunReturnsGitFailure asserts that a go test whose package git grep cannot read is not accepted: the
// failure is returned, and it is not a refusal of the field.
func TestContractLintRunReturnsGitFailure(t *testing.T) {
	t.Parallel()
	repo := contractLintRunRepo(t)
	req := epicTestRequest(repo)
	req.Contract.Requirements[0].Item.Capture = []string{"/usr/bin/env", "go", "test", "-run", "X", "./../outside/"}
	err := checkEpic(t.Context(), req, epicTestGetenv)
	if err == nil {
		t.Fatal("checkEpic accepted a go test whose package git grep cannot read")
	}
	var fieldErr *epicFieldError
	if errors.As(err, &fieldErr) {
		t.Fatalf("checkEpic = %q, want git's failure, not a field refusal", err)
	}
}

// contractLintReleaseFast is the first release step of the contract that a release of this repository writes. It
// passes the lint.
const contractLintReleaseFast = "Fast-forward main to the epic's release candidate and push main to origin, so main " +
	"holds the change; do not wait for CI."

// contractLintReleaseTag is the second release step of that contract. It passes the lint.
const contractLintReleaseTag = "Create a signed annotated tag at that main commit, named the next patch version above " +
	"the newest v* tag on origin when this step runs, its message one sentence naming the change, and push it, so " +
	"nix-config can pin it; do not wait for CI."

// contractLintReleaseSteps returns the release steps a lint test gives its contract: the contract's first step, which
// passes, then one step holding text. A refusal of that second step names release_steps[1].step.
func contractLintReleaseSteps(text string) []*v1.EpicReleaseStep {
	return []*v1.EpicReleaseStep{
		{Step: contractLintReleaseFast, Target: "origin", Undo: "Synthetic undo."},
		{Step: text, Target: "origin", Undo: "Synthetic undo."},
	}
}

// contractLintReleaseRefusal is the refusal of the second release step, whose reason is reason.
func contractLintReleaseRefusal(reason string) string {
	return "release_steps[1].step: " + reason
}

// TestContractLintReleaseRefusesSteps asserts that a release step whose text says merge, names a fixed version or waits
// on CI is refused under its field, with the reason that gives the accepted wording. Each refused step is the second,
// so the field is release_steps[1].step. Where a step breaks two rules, the earlier rule gives the reason.
func TestContractLintReleaseRefusesSteps(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	runEpicRefusals(t, repo, []epicRefusal{
		{
			name: "lowercase merge",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.ReleaseSteps = contractLintReleaseSteps(
					"Push main to origin, then merge the release branch.",
				)
			},
			want: contractLintReleaseRefusal(
				"says merge: a release step fast-forwards main to the release candidate; write fast-forward"),
		},
		{
			name: "capitalised Merge",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.ReleaseSteps = contractLintReleaseSteps("Merge the release candidate into main.")
			},
			want: contractLintReleaseRefusal(
				"says merge: a release step fast-forwards main to the release candidate; write fast-forward"),
		},
		{
			name: "negated merge",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.ReleaseSteps = contractLintReleaseSteps("Fast-forward main; do not merge.")
			},
			want: contractLintReleaseRefusal(
				"says merge: a release step fast-forwards main to the release candidate; write fast-forward"),
		},
		{
			name: "fixed version v0.86.3",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.ReleaseSteps = contractLintReleaseSteps(
					"Tag v0.86.3 at the release candidate, then push it.",
				)
			},
			want: contractLintReleaseRefusal(
				"names the fixed version v0.86.3: a release step's tag is chosen when the step runs; " +
					"name the next version above the newest tag instead"),
		},
		{
			name: "versioned path rel/v0.86.2",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.ReleaseSteps = contractLintReleaseSteps("Push rel/v0.86.2 to origin.")
			},
			want: contractLintReleaseRefusal(
				"names the fixed version v0.86.2: a release step's tag is chosen when the step runs; " +
					"name the next version above the newest tag instead"),
		},
		{
			name: "so CI runs",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.ReleaseSteps = contractLintReleaseSteps("Push main to origin, so CI runs.")
			},
			want: contractLintReleaseRefusal(
				`a release does not wait for CI; write "do not wait for CI"`),
		},
		{
			name: "Merge, v0.86.3 and so CI runs",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.ReleaseSteps = contractLintReleaseSteps("Merge v0.86.3 into main, so CI runs.")
			},
			want: contractLintReleaseRefusal(
				"says merge: a release step fast-forwards main to the release candidate; write fast-forward"),
		},
		{
			name: "tag v0.86.3 and so CI runs",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.ReleaseSteps = contractLintReleaseSteps("Tag v0.86.3, so CI runs.")
			},
			want: contractLintReleaseRefusal(
				"names the fixed version v0.86.3: a release step's tag is chosen when the step runs; " +
					"name the next version above the newest tag instead"),
		},
	})
}

// TestContractLintReleaseAcceptsSteps asserts that the contract's own two release steps, verbatim, pass. It also
// asserts that the rules leave alone merged, merges and merging, a ci inside a path, and the phrase in capitals.
func TestContractLintReleaseAcceptsSteps(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	for _, tc := range []struct {
		name string
		step string
	}{
		{name: "contract's first step verbatim", step: contractLintReleaseFast},
		{name: "contract's second step verbatim", step: contractLintReleaseTag},
		{
			name: "merged, merges and merging",
			step: "Fast-forward main to the release candidate; the notes list what is merged, merges and merging.",
		},
		{name: "ci in a path and Do not wait for CI", step: "Push main to origin/ci-circle; Do not wait for CI."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := epicTestRequest(repo)
			req.Contract.ReleaseSteps = []*v1.EpicReleaseStep{
				{Step: tc.step, Target: "origin", Undo: "Synthetic undo."},
			}
			if err := checkEpic(t.Context(), req, epicTestGetenv); err != nil {
				t.Fatalf("checkEpic refused release step %q: %v", tc.step, err)
			}
			t.Logf("checkEpic accepted release step %q", tc.step)
		})
	}
}
