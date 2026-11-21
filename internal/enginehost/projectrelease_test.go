package enginehost

import (
	"errors"
	"testing"

	"github.com/BurntSushi/toml"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// projectReleaseText is a project file with a pools table, a gates table, a reviewers entry, and a release that gives
// two steps and then. The second step has no target.
const projectReleaseText = `[pools.x]
host = "box-a"
cap = 1

[gates.y]
argv = ["/opt/tools/run", "-c", "check"]
limit = 60

[[reviewers]]
name = "alpha"
stage = ["task"]

[release]
then = "announce the build"

[[release.steps]]
step = "publish the bundle"
target = "mirror-one"
undo = "remove the bundle"

[[release.steps]]
step = "tag the commit"
undo = "delete the tag"
`

func TestProjectRelease_ReadsStepsAndThen(t *testing.T) {
	steps, then, found, err := parseProjectRelease([]byte(projectReleaseText))
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
		{Step: "tag the commit", Undo: "delete the tag"},
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

func TestProjectRelease_NoReleaseTable(t *testing.T) {
	const text = `[pools.x]
host = "box-a"
cap = 1

[gates.y]
argv = ["/opt/tools/run"]
limit = 60
`
	steps, then, found, err := parseProjectRelease([]byte(text))
	if err != nil {
		t.Fatalf("parseProjectRelease error = %v, want nil", err)
	}
	if found {
		t.Error("parseProjectRelease found = true, want false")
	}
	if steps != nil || then != "" {
		t.Errorf("parseProjectRelease = (%v, %q), want no steps and no then", steps, then)
	}
}

func TestProjectRelease_SyntaxErrorWraps(t *testing.T) {
	_, _, found, err := parseProjectRelease([]byte("[release\n"))
	if _, ok := errors.AsType[toml.ParseError](err); !ok {
		t.Fatalf("parseProjectRelease error = %v, want a wrapped toml.ParseError", err)
	}
	if found {
		t.Error("parseProjectRelease found = true, want false")
	}
}

func TestProjectRelease_RefusesMalformed(t *testing.T) {
	// oneStep is a valid step table, appended where a release needs a step that is not under test.
	const oneStep = "[[release.steps]]\nstep = \"publish\"\nundo = \"unpublish\"\n"
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
			name:   "unknown key under release",
			text:   "[release]\nwhen = \"later\"\n" + oneStep,
			field:  "release.when",
			reason: "unknown key",
		},
		{
			name:   "several unknown keys report the first in name order",
			text:   "[release]\nzeta = 1\nalpha = 2\n" + oneStep,
			field:  "release.alpha",
			reason: "unknown key",
		},
		{
			name:   "then not a string",
			text:   "[release]\nthen = 3\n" + oneStep,
			field:  "release.then",
			reason: "not a string",
		},
		{
			name:   "steps missing",
			text:   "[release]\nthen = \"announce\"\n",
			field:  "release.steps",
			reason: epicReasonMissing,
		},
		{
			name:   "steps not an array of tables",
			text:   "[release]\nsteps = [\"publish\"]\n",
			field:  "release.steps",
			reason: "not an array of tables",
		},
		{
			name:   "steps empty",
			text:   "[release]\nsteps = []\n",
			field:  "release.steps",
			reason: epicReasonNone,
		},
		{
			name:   "step unknown key",
			text:   "[release]\n[[release.steps]]\nstep = \"publish\"\nundo = \"unpublish\"\nwhen = \"later\"\n",
			field:  "release.steps[0].when",
			reason: "unknown key",
		},
		{
			name:   "step not a string",
			text:   "[release]\n" + oneStep + "[[release.steps]]\nstep = 1\nundo = \"unpublish\"\n",
			field:  "release.steps[1].step",
			reason: "not a string",
		},
		{
			name:   "target not a string",
			text:   "[release]\n[[release.steps]]\nstep = \"publish\"\ntarget = 2\nundo = \"unpublish\"\n",
			field:  "release.steps[0].target",
			reason: "not a string",
		},
		{
			name:   "undo not a string",
			text:   "[release]\n[[release.steps]]\nstep = \"publish\"\nundo = true\n",
			field:  "release.steps[0].undo",
			reason: "not a string",
		},
		{
			name:   "step blank",
			text:   "[release]\n[[release.steps]]\nstep = \"  \"\nundo = \"unpublish\"\n",
			field:  "release.steps[0].step",
			reason: epicReasonEmpty,
		},
		{
			name:   "step missing",
			text:   "[release]\n[[release.steps]]\nundo = \"unpublish\"\n",
			field:  "release.steps[0].step",
			reason: epicReasonEmpty,
		},
		{
			name:   "undo blank",
			text:   "[release]\n[[release.steps]]\nstep = \"publish\"\nundo = \"\"\n",
			field:  "release.steps[0].undo",
			reason: epicReasonEmpty,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := parseProjectRelease([]byte(tc.text))
			var fieldErr *epicFieldError
			if !errors.As(err, &fieldErr) {
				t.Fatalf("parseProjectRelease error = %v, want *epicFieldError", err)
			}
			if fieldErr.field != tc.field || fieldErr.reason != tc.reason {
				t.Errorf("refused %q: %q, want %q: %q", fieldErr.field, fieldErr.reason, tc.field, tc.reason)
			}
		})
	}
}
