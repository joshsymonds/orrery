package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// filesTaskJSON is a planner return task titled title that plans file alone, under a gate and judge testResolver
// resolves.
func filesTaskJSON(title, file string) string {
	return fmt.Sprintf(`{"title":%q,"deliverable":"d","files":[%q],"requirements":[],`+
		`"gate":"check","evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],`+
		`"supplied":[],"conclusion":"c","judge":"a judge","kind":"capture"}}`, title, file)
}

// TestReturnCheckAcceptsFilesOfTasksOfEveryStatus checks that a plan touching a file of an open, a done and a failed
// epic task passes, since the check no longer holds a file to the epic task that owns it.
func TestReturnCheckAcceptsFilesOfTasksOfEveryStatus(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{
		Requirements: []store.EpicRequirement{{ID: "R1"}},
		Tasks: []store.EpicTask{
			{Seq: 1, Title: "open", Files: []string{"internal/a.go"}, Status: "open"},
			{Seq: 2, Title: "done", Files: []string{"internal/b.go"}, Status: "done"},
			{Seq: 3, Title: "failed", Files: []string{"internal/c.go"}, Status: "failed"},
		},
	}
	check := returnCheck(context.Background(), epic, store.Review{}, testResolver{}, nil, false)
	plan := `{"status":"done","decisions":[],"tasks":[` + strings.Join([]string{
		filesTaskJSON("Touch a", "internal/a.go"),
		filesTaskJSON("Touch b", "internal/b.go"),
		filesTaskJSON("Touch c", "internal/c.go"),
	}, ",") + `],"outline":"o","questions":[],"outcome":"work"}`

	if got := check(json.RawMessage(plan)); got != nil {
		t.Errorf("ReturnCheck of a plan touching an open, a done and a failed task = %q, want none", got)
	}
}
