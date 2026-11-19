package enginehost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// resultRowsSteps is a StepCommands whose Status answers each id in rows with its row, and any other id with ErrNoStep.
// The service calls only Status, so the other methods are left to the embedded nil interface.
type resultRowsSteps struct {
	StepCommands
	rows map[string]store.StepRow
}

func (f resultRowsSteps) Status(_ context.Context, id string) (store.StepRow, error) {
	row, ok := f.rows[id]
	if !ok {
		return store.StepRow{}, fmt.Errorf("step %s: %w", id, store.ErrNoStep)
	}
	return row, nil
}

// stepResultFixture is the result the subtests read. Its title is repeated, its meta holds whitespace to compact, its
// verdicts are objects with a string id and a number id, its lines are strings, and its log holds multi-byte runes.
const stepResultFixture = `{"title":"draft","rounds":[1,2,3],"count":42,"ok":true,"gap":null,` +
	`"meta": {"a": 1, "b": "x"},"note":"line one\nline two",` +
	`"verdicts":[{"id":"v1","ok":false},{"id":7,"ok":true},{"id":"v2","ok":true}],` +
	`"lines":["alpha","beta","gamma"],"log":"abcdéfghij","title":"review"}`

// readPart calls ReadStepResult with req and returns its reply, failing t when the call fails.
func readPart(
	t *testing.T,
	client enginev1connect.StepsClient,
	req *v1.ReadStepResultRequest,
) *v1.ReadStepResultResponse {
	t.Helper()
	resp, err := client.ReadStepResult(context.Background(), connect.NewRequest(req))
	if err != nil {
		t.Fatalf("ReadStepResult(%v): %v", req, err)
	}
	return resp.Msg
}

// requireInvalidArgument fails t unless err is an invalid_argument connect error whose message contains each of parts.
func requireInvalidArgument(t *testing.T, err error, parts ...string) {
	t.Helper()
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want %v (error %v)", got, connect.CodeInvalidArgument, err)
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error %T is not a connect error", err)
	}
	for _, part := range parts {
		if !strings.Contains(connectErr.Message(), part) {
			t.Fatalf("message %q does not contain %q", connectErr.Message(), part)
		}
	}
}

func TestStepByPartResult(t *testing.T) {
	steps := resultRowsSteps{rows: map[string]store.StepRow{
		"main":  {ID: "main", Result: stepResultFixture},
		"long":  {ID: "long", Result: `{"body":"` + strings.Repeat("ab", 2500) + `"}`},
		"plain": {ID: "plain", Result: `[1, 2, 3]`},
	}}
	path, stop := startServerWithSteps(t, nil, steps)
	client := newStepClient(t, path)

	t.Run("outline", func(t *testing.T) {
		got := readPart(t, client, &v1.ReadStepResultRequest{Id: "main"})
		want := &v1.ReadStepResultResponse{Outline: []*v1.StepResultField{
			{Name: "title", Type: "string", Size: 6},
			{Name: "rounds", Type: "array", Size: 3},
			{Name: "count", Type: "number", Size: 2},
			{Name: "ok", Type: "boolean", Size: 4},
			{Name: "gap", Type: "null", Size: 4},
			{Name: "meta", Type: "object", Size: 2},
			{Name: "note", Type: "string", Size: 17},
			{Name: "verdicts", Type: "array", Size: 3},
			{Name: "lines", Type: "array", Size: 3},
			{Name: "log", Type: "string", Size: 10},
		}}
		if !proto.Equal(got, want) {
			t.Fatalf("outline = %v, want %v", got, want)
		}
	})

	t.Run("string field raw", func(t *testing.T) {
		got := readPart(t, client, &v1.ReadStepResultRequest{Id: "main", Field: "note"})
		want := &v1.ReadStepResultResponse{Value: "line one\nline two", Total: 17}
		if !proto.Equal(got, want) {
			t.Fatalf("note = %v, want %v", got, want)
		}
	})

	t.Run("object field JSON", func(t *testing.T) {
		got := readPart(t, client, &v1.ReadStepResultRequest{Id: "main", Field: "meta"})
		want := &v1.ReadStepResultResponse{Value: `{"a":1,"b":"x"}`, Json: true, Total: 15}
		if !proto.Equal(got, want) {
			t.Fatalf("meta = %v, want %v", got, want)
		}
	})

	t.Run("item by id", func(t *testing.T) {
		byString := readPart(t, client, &v1.ReadStepResultRequest{Id: "main", Field: "verdicts", Item: "v2"})
		wantString := &v1.ReadStepResultResponse{Value: `{"id":"v2","ok":true}`, Json: true, Total: 21}
		if !proto.Equal(byString, wantString) {
			t.Fatalf("verdicts item v2 = %v, want %v", byString, wantString)
		}
		byNumber := readPart(t, client, &v1.ReadStepResultRequest{Id: "main", Field: "verdicts", Item: "7"})
		wantNumber := &v1.ReadStepResultResponse{Value: `{"id":7,"ok":true}`, Json: true, Total: 18}
		if !proto.Equal(byNumber, wantNumber) {
			t.Fatalf("verdicts item 7 = %v, want %v", byNumber, wantNumber)
		}
	})

	t.Run("item by index", func(t *testing.T) {
		object := readPart(t, client, &v1.ReadStepResultRequest{Id: "main", Field: "verdicts", Item: "0"})
		wantObject := &v1.ReadStepResultResponse{Value: `{"id":"v1","ok":false}`, Json: true, Total: 22}
		if !proto.Equal(object, wantObject) {
			t.Fatalf("verdicts item 0 = %v, want %v", object, wantObject)
		}
		str := readPart(t, client, &v1.ReadStepResultRequest{Id: "main", Field: "lines", Item: "2"})
		wantStr := &v1.ReadStepResultResponse{Value: "gamma", Total: 5}
		if !proto.Equal(str, wantStr) {
			t.Fatalf("lines item 2 = %v, want %v", str, wantStr)
		}
	})

	t.Run("cut at limit with next offset", func(t *testing.T) {
		first := readPart(t, client, &v1.ReadStepResultRequest{Id: "main", Field: "log", Limit: 4})
		wantFirst := &v1.ReadStepResultResponse{Value: "abcd", NextOffset: 4, Total: 10}
		if !proto.Equal(first, wantFirst) {
			t.Fatalf("log limit 4 = %v, want %v", first, wantFirst)
		}
		second := readPart(t, client, &v1.ReadStepResultRequest{Id: "main", Field: "log", Offset: 4, Limit: 4})
		wantSecond := &v1.ReadStepResultResponse{Value: "éfgh", Offset: 4, NextOffset: 8, Total: 10}
		if !proto.Equal(second, wantSecond) {
			t.Fatalf("log offset 4 limit 4 = %v, want %v", second, wantSecond)
		}
		last := readPart(t, client, &v1.ReadStepResultRequest{Id: "main", Field: "log", Offset: 8, Limit: 4})
		wantLast := &v1.ReadStepResultResponse{Value: "ij", Offset: 8, Total: 10}
		if !proto.Equal(last, wantLast) {
			t.Fatalf("log offset 8 limit 4 = %v, want %v", last, wantLast)
		}
	})

	t.Run("default and clamped limit", func(t *testing.T) {
		want := &v1.ReadStepResultResponse{Value: strings.Repeat("ab", 2000), NextOffset: 4000, Total: 5000}
		def := readPart(t, client, &v1.ReadStepResultRequest{Id: "long", Field: "body"})
		if !proto.Equal(def, want) {
			t.Fatalf("body with no limit = %v, want %v", def, want)
		}
		clamped := readPart(t, client, &v1.ReadStepResultRequest{Id: "long", Field: "body", Limit: 9000})
		if !proto.Equal(clamped, want) {
			t.Fatalf("body with limit 9000 = %v, want %v", clamped, want)
		}
	})

	t.Run("non-object result", func(t *testing.T) {
		got := readPart(t, client, &v1.ReadStepResultRequest{Id: "plain"})
		want := &v1.ReadStepResultResponse{Value: "[1, 2, 3]", Total: 9}
		if !proto.Equal(got, want) {
			t.Fatalf("plain result = %v, want %v", got, want)
		}
	})

	t.Run("unknown field error names fields", func(t *testing.T) {
		_, err := client.ReadStepResult(context.Background(), connect.NewRequest(&v1.ReadStepResultRequest{
			Id: "main", Field: "missing",
		}))
		requireInvalidArgument(t, err,
			"title", "rounds", "count", "ok", "gap", "meta", "note", "verdicts", "lines", "log")
	})

	t.Run("unknown item error names count", func(t *testing.T) {
		_, err := client.ReadStepResult(context.Background(), connect.NewRequest(&v1.ReadStepResultRequest{
			Id: "main", Field: "verdicts", Item: "nope",
		}))
		requireInvalidArgument(t, err, "3 elements")
	})

	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
}
