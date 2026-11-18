package reviewers

import (
	"slices"
	"strings"
	"testing"
)

func TestStepSendBackFloorRole(t *testing.T) {
	t.Parallel()
	t.Run("the floor role says the number of commits is no defect", func(t *testing.T) {
		t.Parallel()
		rs := mergedDefaults(t)
		i := slices.IndexFunc(rs, func(r Reviewer) bool { return r.Name == "floor" })
		if i < 0 {
			t.Fatalf("no floor reviewer in %v", names(rs))
		}
		if !strings.Contains(rs[i].Prompt, "The number of commits a task makes is no defect.") {
			t.Errorf("floor role text does not say the number of commits a task makes is no defect")
		}
	})
}
