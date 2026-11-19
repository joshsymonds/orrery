package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
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

func TestReturnCheckOwnsFilesOfOpenTasksOnly(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{
		Requirements: []store.EpicRequirement{{ID: "R1"}},
		Tasks: []store.EpicTask{
			{Seq: 1, Title: "open", Files: []string{"internal/a.go"}, Status: "open"},
			{Seq: 2, Title: "done", Files: []string{"internal/b.go"}, Status: "done"},
			{Seq: 3, Title: "failed", Files: []string{"internal/c.go"}, Status: "failed"},
		},
	}
	check := returnCheck(context.Background(), epic, testResolver{}, nil)
	plan := `{"status":"done","decisions":[],"tasks":[` + strings.Join([]string{
		filesTaskJSON("Touch a", "internal/a.go"),
		filesTaskJSON("Touch b", "internal/b.go"),
		filesTaskJSON("Touch c", "internal/c.go"),
	}, ",") + `],"outline":"o","questions":[],"outcome":"work"}`

	want := []string{`tasks[0] "Touch a" files[0]: owned by an unfinished task`}
	if got := check(json.RawMessage(plan)); !reflect.DeepEqual(got, want) {
		t.Errorf("ReturnCheck of a plan touching an open, a done and a failed task = %q, want %q", got, want)
	}
}
