package server

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// epicsScopeLock makes dir unreadable for the rest of the test and restores it to 0o700 before the temporary directory
// is removed. It fails the test when the mode does not lock dir, as it does not for root.
func epicsScopeLock(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restore %s: %v", dir, err)
		}
	})
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	if _, err := os.ReadDir(dir); err == nil {
		t.Fatalf("%s is readable at mode 000; run the test as a user without root privileges", dir)
	}
}

func TestEpicsGetScopeReadsOnlyTheAskedEpic(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "asked", `{"outcome":"shipped"}`, epicsNow())
	writeEffortState(t, filepath.Join(root, "repoa", "asked"), "1",
		`{"tasks":[{"slug":"one-a","subject":"One A","status":"gated"}]}`)
	writeHead(t, root, "repoa", "sibling", `{"outcome":"shipped"}`, epicsNow())
	writeHead(t, root, "repob", "other", `{"outcome":"shipped"}`, epicsNow())
	writeHead(t, root, "repoc", "other", `{"outcome":"shipped"}`, epicsNow())
	steps := &epicsFake{steps: []*enginev1.Step{
		{Id: "run-a", Epic: "repoa/asked", Task: "one-a", Started: epicsAgo(time.Hour)},
	}}
	client := epicsServe(t, root, steps)

	before := epicsGet(t, client, "repoa/asked")
	if before.GetEpic().GetId() != "repoa/asked" || len(before.GetTasks()) != 1 || len(before.GetSteps()) != 1 {
		t.Fatalf("Get before the lock = %v, want repoa/asked with one task and one step", before)
	}

	epicsScopeLock(t, filepath.Join(root, "repob"))
	epicsScopeLock(t, filepath.Join(root, "repoc"))
	epicsScopeLock(t, filepath.Join(root, "repoa", "sibling"))

	after := epicsGet(t, client, "repoa/asked")
	if !proto.Equal(before, after) {
		t.Fatalf("Get after the lock = %v, want %v", after, before)
	}
}

func TestEpicsGetScopeAnswersNotFoundForUnlistedIDs(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	root := filepath.Join(parent, "records")
	writeHead(t, root, "repo", "epic", `{"outcome":"shipped"}`, epicsNow())
	writeHead(t, root, "repo", ".hidden", `{"outcome":"shipped"}`, epicsNow())
	writeHead(t, root, ".dotrepo", "epic", `{"outcome":"shipped"}`, epicsNow())
	// A head outside the record root that an id would reach only by leaving it.
	writeHead(t, parent, "outside", "epic", `{"outcome":"shipped"}`, epicsNow())
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "repo", "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	links := []struct{ target, name string }{
		{filepath.Join(root, "repo"), filepath.Join(root, "linkrepo")},
		{filepath.Join(root, "repo", "epic"), filepath.Join(root, "repo", "linkepic")},
		{filepath.Join(parent, "outside"), filepath.Join(root, "outsiderepo")},
		{filepath.Join(parent, "outside", "epic"), filepath.Join(root, "repo", "outsideepic")},
	}
	for _, link := range links {
		if err := os.Symlink(link.target, link.name); err != nil {
			t.Fatal(err)
		}
	}
	client := epicsServe(t, root, &epicsFake{})

	epics := epicsList(t, client)
	listed := make([]string, 0, len(epics))
	for _, epic := range epics {
		listed = append(listed, epic.GetId())
	}
	if !reflect.DeepEqual(listed, []string{"repo/epic"}) {
		t.Fatalf("List ids = %v, want [repo/epic]", listed)
	}

	ids := []string{
		"",
		"repo",
		"repo/epic/",
		"a/b/c",
		"/epic",
		"repo/",
		"./epic",
		"repo/.",
		".",
		"../outside/epic",
		"../outside",
		"repo/..",
		"..",
		".dotrepo/epic",
		"repo/.hidden",
		"linkrepo/epic",
		"repo/linkepic",
		"outsiderepo/epic",
		"repo/outsideepic",
		"notes.txt/epic",
		"repo/notes.txt",
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			_, err := client.Get(context.Background(), connect.NewRequest(&orreryv1.EpicsGetRequest{Id: id}))
			requireConnectError(t, err, connect.CodeNotFound, "epic not found")
		})
	}
}
