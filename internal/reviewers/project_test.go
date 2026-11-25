package reviewers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// projectText is a project file with a gates table, a pools table and two reviewers. The first sets the required
// keys only. The second sets every key, and sets must to an empty array, which counts as set.
const projectText = `[gates.alpha]
argv = ["/opt/tools/run", "-c", "check"]
limit = 60

[pools.beta]
host = "box-a"
cap = 1
members = ["m1"]

[[reviewers]]
name = "alpha"
stage = ["task"]
must = ["the diff holds"]
agent = "code-reviewer"

[[reviewers]]
name = "gamma"
enabled = false
stage = ["task", "epic"]
must = []
must_not = ["the tests are weakened"]
prompt = "review it"
model = "m-1"
tools = ["read"]
effort = "high"
limit = 120
verify = false
`

// validEntry is one reviewer entry that ParseProject accepts, with the given line appended after it.
func validEntry(extra string) string {
	return `[[reviewers]]
name = "alpha"
stage = ["task"]
must = ["the diff holds"]
agent = "code-reviewer"
` + extra
}

// noMachineFile is a getenv that points the machine's pool file at an empty directory, so Project reads only the
// project file.
func noMachineFile(t *testing.T) func(string) string {
	t.Helper()
	config := t.TempDir()
	return func(name string) string {
		if name == "XDG_CONFIG_HOME" {
			return config
		}
		return ""
	}
}

func TestParseProjectReadsEachEntryWithItsSetKeys(t *testing.T) {
	t.Parallel()
	got, err := ParseProject(project, []byte(projectText))
	if err != nil {
		t.Fatalf("ParseProject: %v", err)
	}
	want := []Entry{
		{
			Source: project, Index: 0, Name: "alpha", Stage: []string{StageTask}, Must: []string{"the diff holds"},
			Agent: str("code-reviewer"),
		},
		{
			Source: project, Index: 1, Name: "gamma", Enabled: on(false), Stage: []string{StageTask, StageEpic},
			Must: []string{}, MustNot: []string{"the tests are weakened"}, Prompt: str("review it"), Model: str("m-1"),
			Tools: []string{"read"}, Effort: str("high"), Limit: secs(120), Verify: on(false),
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseProject =\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseProjectWithoutReviewersGivesNoEntries(t *testing.T) {
	t.Parallel()
	got, err := ParseProject(project, []byte("[gates.alpha]\nargv = [\"/opt/tools/run\"]\nlimit = 60\n"))
	if err != nil {
		t.Fatalf("ParseProject: %v", err)
	}
	if got != nil {
		t.Fatalf("ParseProject = %+v, want no entries", got)
	}
}

func TestParseProjectRefusesBadEntries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		text  string
		index int
		want  string
		key   string
	}{
		{
			name:  "unknown key",
			text:  validEntry("timeout = 5\n"),
			index: 0,
			want:  "alpha",
			key:   "timeout",
		},
		{
			name:  "unknown key in a later entry",
			text:  validEntry("") + "\n[[reviewers]]\nname = \"beta\"\nmust = [\"x\"]\ntimeout = 5\n",
			index: 1,
			want:  "beta",
			key:   "timeout",
		},
		{
			name:  "scalar of the wrong type",
			text:  validEntry("enabled = \"yes\"\n"),
			index: 0,
			want:  "alpha",
			key:   "enabled",
		},
		{
			name:  "limit that is not an integer",
			text:  validEntry("limit = \"soon\"\n"),
			index: 0,
			want:  "alpha",
			key:   "limit",
		},
		{
			name:  "array with an element of the wrong type",
			text:  "[[reviewers]]\nname = \"alpha\"\nmust = [\"the diff holds\", 3]\nagent = \"code-reviewer\"\n",
			index: 0,
			want:  "alpha",
			key:   "must",
		},
		{
			name:  "name of the wrong type",
			text:  "[[reviewers]]\nname = 7\n",
			index: 0,
			want:  "",
			key:   "name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseProject(project, []byte(tc.text))
			var refusal *Error
			if !errors.As(err, &refusal) {
				t.Fatalf("ParseProject error = %v, want an *Error", err)
			}
			got := *refusal
			want := Error{Source: project, Index: tc.index, Name: tc.want, Key: tc.key}
			if got.Source != want.Source || got.Index != want.Index || got.Name != want.Name || got.Key != want.Key {
				t.Fatalf("ParseProject error = %+v, want %+v", got, want)
			}
			if !strings.Contains(err.Error(), project) || !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error %q does not name %s and %s", err, project, tc.key)
			}
		})
	}
}

func TestParseProjectRefusesReviewersThatAreNotAnArrayOfTables(t *testing.T) {
	t.Parallel()
	for _, text := range []string{`reviewers = "alpha"`, "[reviewers]\nname = \"alpha\"\n"} {
		_, err := ParseProject(project, []byte(text))
		if err == nil || !strings.Contains(err.Error(), project) || !strings.Contains(err.Error(), "reviewers") {
			t.Errorf("ParseProject(%q) error = %v, want one naming %s and reviewers", text, err, project)
		}
	}
}

func TestProjectReadsTheProjectFileOfDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, ".orrery", "project.toml")
	writeFile(t, file, projectText)

	got, err := Project(context.Background(), hostexec.NewLocal(), dir, noMachineFile(t))
	if err != nil {
		t.Fatalf("Project(%s): %v", dir, err)
	}
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "gamma" || got[0].Source != file {
		t.Fatalf("Project = %+v, want alpha and gamma read from %s", got, file)
	}
}

func TestProjectWithoutProjectFileGivesNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got, err := Project(context.Background(), hostexec.NewLocal(), dir, noMachineFile(t))
	if err != nil {
		t.Fatalf("Project(%s): %v", dir, err)
	}
	if got != nil {
		t.Fatalf("Project(%s) = %+v, want nil", dir, got)
	}
}

func TestProjectRefusesUnreadableFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, ".orrery", "project.toml")
	if err := os.MkdirAll(file, os.ModePerm); err != nil {
		t.Fatal(err)
	}
	_, err := Project(context.Background(), hostexec.NewLocal(), dir, noMachineFile(t))
	if err == nil || !strings.Contains(err.Error(), file) {
		t.Fatalf("Project error = %v, want an error naming %s", err, file)
	}
}

// releaseProject is an invented project file with a pool, a gate, a reviewer and a release: a [release] table with
// then and two steps.
const releaseProject = `[pools.alpha]
host = "box-a"
cap = 2
members = ["m1", "m2"]
wake = ["wake-m1", "--flag"]
wake_limit = 5
status = ["status-m1"]
status_limit = 3
admit = ["admit-m1"]
admit_limit = 4

[gates.alpha]
argv = ["/opt/tools/run", "-c", "check"]
limit = 1800
needs = { gate = 1, builds = 2 }

[[reviewers]]
name = "alpha"
stage = ["task"]
must = ["the diff holds"]
agent = "code-reviewer"

[release]
then = "the build ships"

[[release.steps]]
step = "Tag the build."
target = "origin"
undo = "Delete the tag."

[[release.steps]]
step = "Publish the build."
target = "origin"
undo = "Unpublish the build."
`

func TestProjectRelease_ParseProjectReadsEntryBesideRelease(t *testing.T) {
	t.Parallel()
	got, err := ParseProject(project, []byte(releaseProject))
	if err != nil {
		t.Fatalf("ParseProject of a project file with [release]: %v", err)
	}
	want := []Entry{{
		Source: project, Index: 0, Name: "alpha", Stage: []string{StageTask}, Must: []string{"the diff holds"},
		Agent: str("code-reviewer"),
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseProject =\n%+v\nwant\n%+v", got, want)
	}
}

// writeFile creates path with text, along with any missing parent directories.
func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}
