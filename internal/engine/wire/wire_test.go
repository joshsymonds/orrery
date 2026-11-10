package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

type recordingValue struct {
	text string
}

func (r *recordingValue) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &r.text)
}

type base struct {
	ID string `wire:"id"`
}

type item struct {
	Name string `wire:"item_name"`
}

func TestWireTagWinsOverJSONName(t *testing.T) {
	var got struct {
		Name string `wire:"display_name" json:"name"`
	}
	if err := unmarshalWire([]byte(`{"display_name":"alpha","name":"beta"}`), &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.Name != "alpha" {
		t.Fatalf("Name = %q, want %q", got.Name, "alpha")
	}
}

func TestJSONTagNameDecodesWithoutWireTag(t *testing.T) {
	var got struct {
		Label string `json:"label"`
		Count int    `json:"count,omitempty"`
	}
	if err := unmarshalWire([]byte(`{"label":"gamma","count":4}`), &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.Label != "gamma" || got.Count != 4 {
		t.Fatalf("got %+v, want Label gamma and Count 4", got)
	}
}

func TestSkippedFieldsStayUnset(t *testing.T) {
	var got struct {
		Visible string `wire:"visible"`
		Hidden  string `json:"-"`
		secret  string `wire:"secret"`
	}
	data := []byte(`{"visible":"v","-":"dash","Hidden":"z","secret":"y"}`)
	if err := unmarshalWire(data, &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.Visible != "v" {
		t.Fatalf("Visible = %q, want %q", got.Visible, "v")
	}
	if got.Hidden != "" {
		t.Fatalf("Hidden = %q, want empty for json:\"-\"", got.Hidden)
	}
	if got.secret != "" {
		t.Fatalf("secret = %q, want empty for unexported field", got.secret)
	}
}

func TestEmbeddedStructFieldsDecode(t *testing.T) {
	var got struct {
		base
		Kind string `wire:"kind"`
	}
	if err := unmarshalWire([]byte(`{"id":"00000000-0000-4000-8000-000000000001","kind":"node"}`), &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.ID != "00000000-0000-4000-8000-000000000001" || got.Kind != "node" {
		t.Fatalf("got %+v, want embedded ID and Kind node", got)
	}
}

func TestEmbeddedPointerStructReturnsError(t *testing.T) {
	var got struct {
		*base
	}
	err := unmarshalWire([]byte(`{"id":"00000000-0000-4000-8000-000000000002"}`), &got)
	if err == nil {
		t.Fatal("unmarshalWire succeeded for an embedded pointer struct, want error")
	}
	if !strings.HasPrefix(err.Error(), "wire: ") {
		t.Fatalf("error %q does not start with %q", err.Error(), "wire: ")
	}
}

func TestPointerAllocatesAndNullZeroes(t *testing.T) {
	var got struct {
		Count *int `wire:"count"`
	}
	if err := unmarshalWire([]byte(`{"count":3}`), &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.Count == nil || *got.Count != 3 {
		t.Fatalf("Count = %v, want pointer to 3", got.Count)
	}
	if err := unmarshalWire([]byte(`{"count":null}`), &got); err != nil {
		t.Fatalf("unmarshalWire with null: %v", err)
	}
	if got.Count != nil {
		t.Fatalf("Count = %d, want nil after JSON null", *got.Count)
	}
}

func TestSliceOfStructsDecodesAndNullYieldsNil(t *testing.T) {
	var got struct {
		Items []item `wire:"items"`
	}
	if err := unmarshalWire([]byte(`{"items":[{"item_name":"a"},{"item_name":"b"}]}`), &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if len(got.Items) != 2 || got.Items[0].Name != "a" || got.Items[1].Name != "b" {
		t.Fatalf("Items = %+v, want names a and b", got.Items)
	}
	if err := unmarshalWire([]byte(`{"items":null}`), &got); err != nil {
		t.Fatalf("unmarshalWire with null: %v", err)
	}
	if got.Items != nil {
		t.Fatalf("Items = %+v, want nil after JSON null", got.Items)
	}
}

func TestRawMessageHoldsCopyOfBytes(t *testing.T) {
	var got struct {
		Payload json.RawMessage `wire:"payload"`
	}
	data := []byte(`{"payload":{"k":"v"}}`)
	if err := unmarshalWire(data, &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	for index := range data {
		data[index] = 'x'
	}
	if string(got.Payload) != `{"k":"v"}` {
		t.Fatalf("Payload = %s, want a copy of the object bytes", got.Payload)
	}
}

func TestUnmarshalerFieldUsesItsMethod(t *testing.T) {
	var got struct {
		Flag recordingValue `wire:"flag"`
	}
	if err := unmarshalWire([]byte(`{"flag":"anything"}`), &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.Flag.text != "anything" {
		t.Fatalf("Flag.text = %q, want the value decoded by UnmarshalJSON", got.Flag.text)
	}
}

func TestNonPointerTargetReturnsError(t *testing.T) {
	var nilPointer *struct{}
	targets := []any{struct{}{}, nilPointer, nil}
	for _, target := range targets {
		err := unmarshalWire([]byte(`{}`), target)
		if err == nil {
			t.Fatalf("unmarshalWire(%T) succeeded, want error", target)
		}
		if !strings.HasPrefix(err.Error(), "wire: ") {
			t.Fatalf("error %q does not start with %q", err.Error(), "wire: ")
		}
	}
}

func TestTypeMismatchReturnsError(t *testing.T) {
	var got struct {
		Count int `wire:"count"`
	}
	err := unmarshalWire([]byte(`{"count":"three"}`), &got)
	if err == nil {
		t.Fatal("unmarshalWire succeeded for a string into an int field, want error")
	}
	if !strings.HasPrefix(err.Error(), "wire: ") {
		t.Fatalf("error %q does not start with %q", err.Error(), "wire: ")
	}
}
