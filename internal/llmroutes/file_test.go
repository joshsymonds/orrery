package llmroutes

import (
	"slices"
	"strings"
	"testing"
)

// routesDoc returns a registry document with default_context ctx-main and the given seats and contexts objects.
func routesDoc(seats, contexts string) string {
	return `{"default_context":"ctx-main","seats":` + seats + `,"contexts":` + contexts + `}`
}

// Seat and context objects the refusal cases build their documents from. seatA is the one seat, a forward seat, and
// contextMain routes the default context through it.
const (
	seatA       = `{"seat-a":{"auth_mode":"forward","upstream":"https://api.anthropic.com"}}`
	contextMain = `{"ctx-main":{"default_seat":"seat-a","models":{}}}`
)

// TestRoutesImportParseRefuses checks that ParseFile refuses each document the routes file grammar does not admit, and
// that the refusal names the member at fault.
func TestRoutesImportParseRefuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, doc, want string
	}{
		{
			name: "duplicate root member",
			doc:  `{"default_context":"ctx-main","default_context":"ctx-other","seats":{},"contexts":{}}`,
			want: "default_context",
		},
		{
			name: "duplicate seat id",
			doc: routesDoc(
				`{"seat-a":{"auth_mode":"forward"},"seat-a":{"auth_mode":"forward"}}`, contextMain),
			want: "seat-a",
		},
		{
			name: "duplicate seat member",
			doc: routesDoc(
				`{"seat-a":{"auth_mode":"forward","auth_mode":"inject"}}`, contextMain),
			want: "auth_mode",
		},
		{
			name: "duplicate context id",
			doc: routesDoc(seatA,
				`{"ctx-main":{"default_seat":"seat-a","models":{}},"ctx-main":{"default_seat":"seat-a","models":{}}}`),
			want: "ctx-main",
		},
		{
			name: "duplicate model selector",
			doc: routesDoc(seatA,
				`{"ctx-main":{"default_seat":"seat-a","models":{"model-x":"seat-a","model-x":"seat-a"}}}`),
			want: "model-x",
		},
		{
			name: "duplicate subagents member",
			doc: routesDoc(seatA,
				`{"ctx-main":{"default_seat":"seat-a","models":{},`+
					`"subagents":{"default_seat":"seat-a","default_seat":"seat-a"}}}`),
			want: "default_seat",
		},
		{
			name: "object after the document",
			doc:  routesDoc(seatA, contextMain) + `{}`,
			want: "after the registry document",
		},
		{
			name: "text after the document",
			doc:  routesDoc(seatA, contextMain) + ` trailing`,
			want: "after the registry document",
		},
		{
			name: "extra_body",
			doc: routesDoc(
				`{"seat-a":{"auth_mode":"forward","upstream":"https://api.anthropic.com","extra_body":{}}}`,
				contextMain),
			want: "extra_body",
		},
		{
			name: "max_output_tokens",
			doc: routesDoc(
				`{"seat-a":{"auth_mode":"forward","upstream":"https://api.anthropic.com","max_output_tokens":4096}}`,
				contextMain),
			want: "max_output_tokens",
		},
		{
			name: "capture_bodies",
			doc: routesDoc(
				`{"seat-a":{"auth_mode":"forward","upstream":"https://api.anthropic.com","capture_bodies":true}}`,
				contextMain),
			want: "capture_bodies",
		},
		{
			name: "unknown seat member",
			doc: routesDoc(
				`{"seat-a":{"auth_mode":"forward","upstream":"https://api.anthropic.com","priority":"high"}}`,
				contextMain),
			want: "priority",
		},
		{
			name: "unknown context member",
			doc: routesDoc(seatA,
				`{"ctx-main":{"default_seat":"seat-a","models":{},"extra":true}}`),
			want: "extra",
		},
		{
			name: "unknown subagents member",
			doc: routesDoc(seatA,
				`{"ctx-main":{"default_seat":"seat-a","models":{},`+
					`"subagents":{"default_seat":"seat-a","extra_body":{}}}}`),
			want: "extra_body",
		},
		{
			name: "unknown root member",
			doc:  `{"default_context":"ctx-main","seats":{},"contexts":{},"version":1}`,
			want: "version",
		},
		{
			name: "missing default_context",
			doc:  `{"seats":{},"contexts":{}}`,
			want: "default_context",
		},
		{
			name: "missing seats",
			doc:  `{"default_context":"ctx-main","contexts":{}}`,
			want: "seats",
		},
		{
			name: "missing contexts",
			doc:  `{"default_context":"ctx-main","seats":{}}`,
			want: "contexts",
		},
		{
			name: "missing context default_seat",
			doc:  routesDoc(seatA, `{"ctx-main":{"models":{}}}`),
			want: "default_seat",
		},
		{
			name: "missing context models",
			doc:  routesDoc(seatA, `{"ctx-main":{"default_seat":"seat-a"}}`),
			want: "models",
		},
		{
			name: "missing subagents default_seat",
			doc: routesDoc(seatA,
				`{"ctx-main":{"default_seat":"seat-a","models":{},"subagents":{"models":{}}}}`),
			want: "default_seat",
		},
		{
			name: "seats is null",
			doc:  `{"default_context":"ctx-main","seats":null,"contexts":{}}`,
			want: "seats",
		},
		{
			name: "seat member of the wrong type",
			doc: routesDoc(
				`{"seat-a":{"auth_mode":"forward","upstream":"https://api.anthropic.com","max_input_tokens":"many"}}`,
				contextMain),
			want: "max_input_tokens",
		},
		{
			name: "binding of the wrong type",
			doc: routesDoc(seatA,
				`{"ctx-main":{"default_seat":"seat-a","models":{"model-x":1}}}`),
			want: "model-x",
		},
		{
			name: "document that is not an object",
			doc:  `[]`,
			want: "registry document",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseFile([]byte(tc.doc))
			if err == nil {
				t.Fatalf("ParseFile = nil error, want a refusal naming %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ParseFile error = %q, want it to name %q", err, tc.want)
			}
		})
	}
}

// TestRoutesImportParseOrdersByID checks that ParseFile returns the seats and contexts in id order, whatever order the
// document gives them in.
func TestRoutesImportParseOrdersByID(t *testing.T) {
	t.Parallel()
	doc := `{"default_context":"ctx-b",` +
		`"seats":{"seat-b":{"auth_mode":"forward","upstream":"https://api.anthropic.com"},` +
		`"seat-a":{"auth_mode":"forward","upstream":"https://api.anthropic.com"}},` +
		`"contexts":{"ctx-b":{"default_seat":"seat-b","models":{}},"ctx-a":{"default_seat":"seat-a","models":{}}}}`
	reg, err := ParseFile([]byte(doc))
	if err != nil {
		t.Fatalf("ParseFile = %v, want nil", err)
	}
	if reg.DefaultContext != "ctx-b" {
		t.Errorf("DefaultContext = %q, want %q", reg.DefaultContext, "ctx-b")
	}
	seatIDs := make([]string, 0, len(reg.Seats))
	for _, seat := range reg.Seats {
		seatIDs = append(seatIDs, seat.ID)
	}
	contextIDs := make([]string, 0, len(reg.Contexts))
	for _, ctx := range reg.Contexts {
		contextIDs = append(contextIDs, ctx.ID)
	}
	if !slices.Equal(seatIDs, []string{"seat-a", "seat-b"}) {
		t.Errorf("seat ids = %v, want [seat-a seat-b]", seatIDs)
	}
	if !slices.Equal(contextIDs, []string{"ctx-a", "ctx-b"}) {
		t.Errorf("context ids = %v, want [ctx-a ctx-b]", contextIDs)
	}
}
