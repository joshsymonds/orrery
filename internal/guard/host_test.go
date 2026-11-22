package guard

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
)

// TestGuardCheckThroughHost checks that Check runs its git in the repository through the Host it is given. The Host is
// a Recorder over the local Host, so a git call that bypassed it would leave no record: every call Check makes is a
// git run in repo, and it reads the changed guarded file at each side through the host.
func TestGuardCheckThroughHost(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	base := commitFiles(t, repo, map[string]string{testFile: goFile(testA2)})
	head := commitFiles(t, repo, map[string]string{testFile: goFile(testA1)})
	recorder := hostexectest.NewRecorder(hostexec.NewLocal())

	if _, err := Check(t.Context(), recorder, repo, base, head, Spec{Guarded: []string{testFile}}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	calls := recorder.Calls()
	for _, call := range calls {
		if call.Op != "Git" || call.Path != repo {
			t.Errorf("Check made a %s call on %q, want a git call in %s", call.Op, call.Path, repo)
		}
	}
	for _, want := range []string{"diff", base + ":" + testFile, head + ":" + testFile} {
		if !slices.ContainsFunc(calls, func(call hostexectest.Call) bool { return slices.Contains(call.Args, want) }) {
			t.Errorf("Check made no git call through the host with argument %q", want)
		}
	}
}
