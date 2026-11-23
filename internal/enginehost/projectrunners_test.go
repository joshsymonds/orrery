package enginehost

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/pools"
)

// runnersReleaseText is a project file whose release gives a then, two steps and two runners, in file order.
const runnersReleaseText = `[release]
then = "announce the build"

[[release.steps]]
step = "publish the bundle"
target = "mirror-one"
undo = "remove the bundle"

[[release.runners]]
name = "box"
argv = ["scripts/gate-remote.sh"]
note = "Use box for module-wide Go tests."

[[release.runners]]
name = "mirror_two"
argv = ["/opt/tools/run", "--mirror", "two"]
note = "Use mirror_two for the second mirror."
`

// runnerTable returns one runner table of a project file, with the lines given as its keys.
func runnerTable(lines ...string) string {
	return "[[release.runners]]\n" + strings.Join(lines, "\n") + "\n"
}

func TestProjectRunner_ParsesValidFile(t *testing.T) {
	t.Parallel()
	runners, err := ParseProjectRunners([]byte(runnersReleaseText))
	if err != nil {
		t.Fatalf("ParseProjectRunners error = %v, want nil", err)
	}
	want := []ProjectRunner{
		{Name: "box", Argv: []string{"scripts/gate-remote.sh"}, Note: "Use box for module-wide Go tests."},
		{Name: "mirror_two", Argv: []string{"/opt/tools/run", "--mirror", "two"}, Note: "Use mirror_two for the second mirror."},
	}
	if !slices.EqualFunc(runners, want, func(a, b ProjectRunner) bool {
		return a.Name == b.Name && a.Note == b.Note && slices.Equal(a.Argv, b.Argv)
	}) {
		t.Fatalf("ParseProjectRunners = %+v, want %+v", runners, want)
	}
}

func TestProjectRunner_NoRunnersNamesNone(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"[pools.x]\nhost = \"box-a\"\ncap = 1\n",
		"[release]\nthen = \"announce\"\n",
		"[release]\nrunners = []\n",
	} {
		runners, err := ParseProjectRunners([]byte(text))
		if err != nil {
			t.Fatalf("ParseProjectRunners(%q) error = %v, want nil", text, err)
		}
		if len(runners) != 0 {
			t.Errorf("ParseProjectRunners(%q) = %+v, want none", text, runners)
		}
	}
}

func TestProjectRunner_SyntaxErrorWraps(t *testing.T) {
	t.Parallel()
	_, err := ParseProjectRunners([]byte("[release\n"))
	if _, ok := errors.AsType[toml.ParseError](err); !ok {
		t.Fatalf("ParseProjectRunners error = %v, want a wrapped toml.ParseError", err)
	}
}

func TestProjectRunner_RefusesMalformed(t *testing.T) {
	t.Parallel()
	const box = `name = "box"`
	const argv = `argv = ["scripts/gate-remote.sh"]`
	const note = `note = "Use box for heavy tests."`
	cases := []struct {
		name, text, field, reason string
	}{
		{
			name:   "release not a table",
			text:   "release = \"soon\"\n",
			field:  "release",
			reason: "not a table",
		},
		{
			name:   "runners not an array of tables",
			text:   "[release]\nrunners = \"box\"\n",
			field:  "release.runners",
			reason: "not an array of tables",
		},
		{
			name:   "runners an inline array of strings",
			text:   "[release]\nrunners = [\"box\"]\n",
			field:  "release.runners",
			reason: "not an array of tables",
		},
		{
			name:   "unknown key in a runner",
			text:   runnerTable(box, argv, note, `when = "later"`),
			field:  "release.runners[0].when",
			reason: "unknown key",
		},
		{
			name:   "several unknown keys report the first in name order",
			text:   runnerTable(box, argv, note, `zeta = 1`, `alpha = 2`),
			field:  "release.runners[0].alpha",
			reason: "unknown key",
		},
		{
			name:   "name missing",
			text:   runnerTable(argv, note),
			field:  "release.runners[0].name",
			reason: epicReasonMissing,
		},
		{
			name:   "name not a string",
			text:   runnerTable(`name = 1`, argv, note),
			field:  "release.runners[0].name",
			reason: "not a string",
		},
		{
			name:   "name blank",
			text:   runnerTable(`name = "  "`, argv, note),
			field:  "release.runners[0].name",
			reason: epicReasonEmpty,
		},
		{
			name:   "name does not match the pattern",
			text:   runnerTable(`name = "Box"`, argv, note),
			field:  "release.runners[0].name",
			reason: "must match [a-z][a-z0-9_]*",
		},
		{
			name:   "name is local",
			text:   runnerTable(`name = "local"`, argv, note),
			field:  "release.runners[0].name",
			reason: `"local" is reserved`,
		},
		{
			name:   "name repeated by a later runner",
			text:   runnerTable(box, argv, note) + runnerTable(box, argv, note),
			field:  "release.runners[1].name",
			reason: epicReasonDuplicate,
		},
		{
			name:   "argv missing",
			text:   runnerTable(box, note),
			field:  "release.runners[0].argv",
			reason: epicReasonMissing,
		},
		{
			name:   "argv not an array",
			text:   runnerTable(box, `argv = "scripts/gate-remote.sh"`, note),
			field:  "release.runners[0].argv",
			reason: "not an array of strings",
		},
		{
			name:   "argv empty",
			text:   runnerTable(box, `argv = []`, note),
			field:  "release.runners[0].argv",
			reason: epicReasonEmpty,
		},
		{
			name:   "argv blank element",
			text:   runnerTable(box, `argv = ["scripts/gate-remote.sh", "  "]`, note),
			field:  "release.runners[0].argv[1]",
			reason: epicReasonEmpty,
		},
		{
			name:   "argv element not a string",
			text:   runnerTable(box, `argv = [1]`, note),
			field:  "release.runners[0].argv[0]",
			reason: "not a string",
		},
		{
			name:   "note missing",
			text:   runnerTable(box, argv),
			field:  "release.runners[0].note",
			reason: epicReasonMissing,
		},
		{
			name:   "note blank",
			text:   runnerTable(box, argv, `note = ""`),
			field:  "release.runners[0].note",
			reason: epicReasonEmpty,
		},
		{
			name:   "note not a string",
			text:   runnerTable(box, argv, `note = true`),
			field:  "release.runners[0].note",
			reason: "not a string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseProjectRunners([]byte(tc.text))
			var fieldErr *epicFieldError
			if !errors.As(err, &fieldErr) {
				t.Fatalf("ParseProjectRunners error = %v, want *epicFieldError", err)
			}
			if fieldErr.field != tc.field || fieldErr.reason != tc.reason {
				t.Errorf("refused %q: %q, want %q: %q", fieldErr.field, fieldErr.reason, tc.field, tc.reason)
			}
		})
	}
}

func TestProjectRunner_ReleaseParsesWithRunners(t *testing.T) {
	t.Parallel()
	steps, then, found, err := parseProjectRelease([]byte(runnersReleaseText))
	if err != nil {
		t.Fatalf("parseProjectRelease error = %v, want nil", err)
	}
	if !found {
		t.Fatal("parseProjectRelease found = false, want true")
	}
	if then != "announce the build" {
		t.Errorf("then = %q, want %q", then, "announce the build")
	}
	want := []*v1.EpicReleaseStep{
		{Step: "publish the bundle", Target: "mirror-one", Undo: "remove the bundle"},
	}
	if len(steps) != len(want) {
		t.Fatalf("got %d steps, want %d", len(steps), len(want))
	}
	for i := range want {
		if !proto.Equal(steps[i], want[i]) {
			t.Errorf("step %d = %v, want %v", i, steps[i], want[i])
		}
	}
}

func TestProjectRunner_ReadsRunnersAtCommit(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	base := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	withFile := stepGitTestCommit(t, repo, map[string]string{".orrery/project.toml": runnersReleaseText})

	runners, err := ReadProjectRunners(t.Context(), repo, withFile)
	if err != nil {
		t.Fatalf("ReadProjectRunners at the commit with the file: %v", err)
	}
	if len(runners) != 2 || runners[0].Name != "box" || runners[1].Name != "mirror_two" {
		t.Fatalf("ReadProjectRunners = %+v, want box and mirror_two", runners)
	}

	none, err := ReadProjectRunners(t.Context(), repo, base)
	if err != nil {
		t.Fatalf("ReadProjectRunners at the commit without the file: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("ReadProjectRunners at the commit without the file = %+v, want none", none)
	}
}

func TestProjectRunner_RepoProjectFileParses(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", ".orrery", "project.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	runners, err := ParseProjectRunners(data)
	if err != nil {
		t.Fatalf("ParseProjectRunners error = %v, want nil", err)
	}
	if _, _, _, err = parseProjectRelease(data); err != nil {
		t.Fatalf("parseProjectRelease error = %v, want nil", err)
	}
	if _, err = pools.Parse(path, data); err != nil {
		t.Fatalf("pools.Parse error = %v, want nil", err)
	}
	if len(runners) != 1 || runners[0].Name != "box" {
		t.Fatalf("ParseProjectRunners = %+v, want one runner named box", runners)
	}
	if !slices.Equal(runners[0].Argv, []string{"scripts/gate-remote.sh"}) {
		t.Errorf("box argv = %q, want [scripts/gate-remote.sh]", runners[0].Argv)
	}
}
