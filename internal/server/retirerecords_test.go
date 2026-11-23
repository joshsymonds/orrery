package server_test

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/reflect/protoreflect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
)

const (
	// retireEpic is the one epic the store holds in the record-retirement tests.
	retireEpic = "repo-store/epic-store"
	// retireRecord is the id of an epic that exists only as a gambit record under the home directory.
	retireRecord = "repo-record/epic-record"
)

// retireNow is the fixed clock of the record-retirement tests, so no test reads the real time.
var retireNow = time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC)

// retireStore is the epics store of the record-retirement tests. It holds retireEpic alone: ListEpics lists it, GetEpic
// answers it, and GetEpic answers not_found for any other id.
type retireStore struct{}

func (retireStore) ListEpics(
	context.Context,
	*connect.Request[enginev1.ListEpicsRequest],
) (*connect.Response[enginev1.ListEpicsResponse], error) {
	summary := &enginev1.EpicSummary{Id: retireEpic, Phase: "accepted", Changed: retireNow.Add(-time.Hour).Unix()}
	return connect.NewResponse(&enginev1.ListEpicsResponse{Epics: []*enginev1.EpicSummary{summary}}), nil
}

func (retireStore) GetEpic(
	_ context.Context,
	req *connect.Request[enginev1.GetEpicRequest],
) (*connect.Response[enginev1.GetEpicResponse], error) {
	if req.Msg.GetId() != retireEpic {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("synthetic not found"))
	}
	epic := &enginev1.StoredEpic{
		Contract: &enginev1.EpicContract{Id: retireEpic, Title: "store epic"},
		Phase:    "accepted",
		Changed:  retireNow.Add(-time.Hour).Unix(),
	}
	return connect.NewResponse(&enginev1.GetEpicResponse{Epic: epic}), nil
}

// retireSteps is the Steps client of the record-retirement tests. It lists no step, and every other call answers
// unimplemented, since the tests send none.
type retireSteps struct{}

func (retireSteps) StepStatus(
	context.Context,
	*connect.Request[enginev1.StepStatusRequest],
) (*connect.Response[enginev1.StepStatusResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (retireSteps) ListSteps(
	context.Context,
	*connect.Request[enginev1.ListStepsRequest],
) (*connect.Response[enginev1.ListStepsResponse], error) {
	return connect.NewResponse(&enginev1.ListStepsResponse{}), nil
}

func (retireSteps) HoldStep(
	context.Context,
	*connect.Request[enginev1.HoldStepRequest],
) (*connect.Response[enginev1.HoldStepResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (retireSteps) NudgeStep(
	context.Context,
	*connect.Request[enginev1.NudgeStepRequest],
) (*connect.Response[enginev1.NudgeStepResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (retireSteps) StopStep(
	context.Context,
	*connect.Request[enginev1.StopStepRequest],
) (*connect.Response[enginev1.StopStepResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

// TestRetireNoGambitRecordsListsTheStoreAlone serves the guarded handler with a home directory that holds a gambit
// record, and an epics store that holds one other epic. Epics.List answers the store's epic alone, with source orreryd,
// and Epics.Get of the record's id answers not_found.
func TestRetireNoGambitRecordsListsTheStoreAlone(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	recordDir := filepath.Join(home, ".gambit", "repo-record", "epic-record")
	if err := os.MkdirAll(recordDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	state := `{"outcome":"accepted","outcome_detail":"waiting to start","workspace":"/home/user/repo-record"}`
	if err := os.WriteFile(filepath.Join(recordDir, "state.json"), []byte(state), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	handler, err := server.New(server.Config{
		Logins:    []string{allowedLogin},
		Tmux:      newTmuxClient(t),
		Push:      newPushStore(t),
		EpicStore: retireStore{},
		Steps:     retireSteps{},
		Home:      home,
		Now:       func() time.Time { return retireNow },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	epics := orreryv1connect.NewEpicsClient(
		&http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}},
		srv.URL,
	)

	list, err := epics.List(t.Context(), connect.NewRequest(&orreryv1.EpicsListRequest{}))
	if err != nil {
		t.Fatalf("Epics.List: %v", err)
	}
	got := list.Msg.GetEpics()
	if len(got) != 1 || got[0].GetId() != retireEpic {
		t.Fatalf("Epics.List ids = %v, want exactly [%s]", epicIDsOf(got), retireEpic)
	}
	if got[0].GetSource() != "orreryd" {
		t.Errorf("%s source = %q, want %q", retireEpic, got[0].GetSource(), "orreryd")
	}

	_, err = epics.Get(t.Context(), connect.NewRequest(&orreryv1.EpicsGetRequest{Id: retireRecord}))
	if err == nil || connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("Epics.Get %s: %v, want not_found", retireRecord, err)
	}
}

// epicIDsOf returns the ids of epics, in order.
func epicIDsOf(epics []*orreryv1.Epic) []string {
	ids := make([]string, 0, len(epics))
	for _, epic := range epics {
		ids = append(ids, epic.GetId())
	}
	return ids
}

// TestRetireNoGambitRecordsServeRecordsFilesGone checks that the server package holds no records.go and no
// recordtasks.go, the files that read gambit records.
func TestRetireNoGambitRecordsServeRecordsFilesGone(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"records.go", "recordtasks.go"} {
		if _, err := os.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("os.Stat(%s) = %v, want not-exist", name, err)
		}
	}
}

// recordMarkers are the names the record-only client code used. None may appear in a web or Kotlin source.
var recordMarkers = []string{"outcomeDetail", "EpicTaskRow", "recordTaskOf", "isOrreryd"}

// orrerydCompared matches a source compared with "orreryd" in either order, as TypeScript or Kotlin writes it.
var orrerydCompared = regexp.MustCompile(
	`(?i:source)(\(\))?\s*[!=]=+\s*"orreryd"|"orreryd"\s*[!=]=+\s*[\w.()]*(?i:source)`,
)

// recordHitsUnder walks root and returns each file whose text holds a record marker or compares a source with
// "orreryd". It reads only files whose extension is in exts, and skips every directory named gen, the generated code.
func recordHitsUnder(t *testing.T, root string, exts []string) []string {
	t.Helper()
	var hits []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "gen" {
				return filepath.SkipDir
			}
			return nil
		}
		if !slices.Contains(exts, filepath.Ext(path)) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)
		for _, marker := range recordMarkers {
			if strings.Contains(text, marker) {
				hits = append(hits, path+": "+marker)
			}
		}
		if orrerydCompared.MatchString(text) {
			hits = append(hits, path+`: source compared with "orreryd"`)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return hits
}

// TestRetireNoGambitRecordsClientsCarryNoRecordFields checks the contract the clients build against: the Epic and
// EpicsGetResponse descriptors carry none of the retired fields, the epics.proto file has no EpicTask message, and no
// web source outside gen and no Kotlin source of the app holds a record-only marker.
func TestRetireNoGambitRecordsClientsCarryNoRecordFields(t *testing.T) {
	t.Parallel()
	epic := (&orreryv1.Epic{}).ProtoReflect().Descriptor().Fields()
	for _, name := range []protoreflect.Name{"problem", "outcome_detail", "waiting"} {
		if epic.ByName(name) != nil {
			t.Errorf("orrery.v1.Epic has field %s, want it retired", name)
		}
	}
	response := (&orreryv1.EpicsGetResponse{}).ProtoReflect().Descriptor().Fields()
	if response.ByName("tasks") != nil {
		t.Errorf("orrery.v1.EpicsGetResponse has field tasks, want it retired")
	}
	if orreryv1.File_orrery_v1_epics_proto.Messages().ByName("EpicTask") != nil {
		t.Errorf("orrery/v1/epics.proto has message EpicTask, want it deleted")
	}

	web := recordHitsUnder(t, filepath.Join("..", "..", "web", "src"), []string{".ts", ".tsx", ".js", ".jsx", ".mjs"})
	kotlin := recordHitsUnder(t, filepath.Join("..", "..", "android", "app", "src", "main", "kotlin"), []string{".kt"})
	tests := recordHitsUnder(t, filepath.Join("..", "..", "android", "app", "src", "test", "kotlin"), []string{".kt"})
	for _, hit := range slices.Concat(web, kotlin, tests) {
		t.Errorf("record-only code remains: %s", hit)
	}
}
