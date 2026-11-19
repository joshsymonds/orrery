package push

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// optionsPayloadFields decodes a payload into its raw values, so a test can tell an absent key from one set to "".
func optionsPayloadFields(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("payload is not a JSON object: %v", err)
	}
	return fields
}

// optionsPayloadString returns the string value of key, failing the test when the key is absent or not a string.
func optionsPayloadString(t *testing.T, fields map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, ok := fields[key]
	if !ok {
		t.Fatalf("payload has no %s key", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("%s is not a JSON string: %v", key, err)
	}
	return value
}

// optionsPayloadList returns the options array of fields, failing the test when it is absent or not an array of
// strings.
func optionsPayloadList(t *testing.T, fields map[string]json.RawMessage) []string {
	t.Helper()
	raw, ok := fields[keyOptions]
	if !ok {
		t.Fatal("payload has no options key")
	}
	var options []string
	if err := json.Unmarshal(raw, &options); err != nil {
		t.Fatalf("options is not a JSON array of strings: %v", err)
	}
	return options
}

// optionsBounded returns boundedPayload's payload for n, failing the test when it does not fit one record.
func optionsBounded(t *testing.T, n Notification) []byte {
	t.Helper()
	body, err := boundedPayload(n)
	if err != nil {
		t.Fatalf("boundedPayload: %v", err)
	}
	if len(body) > MaxPlaintextBytes {
		t.Fatalf("payload is %d bytes, want at most %d", len(body), MaxPlaintextBytes)
	}
	return body
}

func TestPushOptionsKeyIsArrayInOrder(t *testing.T) {
	n := pubNotification("synthetic question?")
	n.Options = []string{"option-two", "option-one", "option-three"}

	got := optionsPayloadList(t, optionsPayloadFields(t, optionsBounded(t, n)))
	if !slices.Equal(got, n.Options) {
		t.Errorf("options = %q, want %q in answer order", got, n.Options)
	}
}

func TestPushOptionsKeyAbsentWithoutOptions(t *testing.T) {
	for _, options := range [][]string{nil, {}} {
		n := pubNotification("synthetic question?")
		n.Options = options
		if _, ok := optionsPayloadFields(t, optionsBounded(t, n))[keyOptions]; ok {
			t.Errorf("payload carries options for Options = %q, want no options key", options)
		}
	}
}

func TestPushOptionsRequestKeyPresentWhenSet(t *testing.T) {
	n := pubNotification("synthetic question?")
	n.Request = "synthetic-request"

	fields := optionsPayloadFields(t, optionsBounded(t, n))
	if got := optionsPayloadString(t, fields, keyRequest); got != n.Request {
		t.Errorf("request = %q, want %q", got, n.Request)
	}
}

func TestPushOptionsRequestKeyAbsentWhenUnset(t *testing.T) {
	n := pubNotification("synthetic question?")
	n.Options = []string{"option-one"}

	if _, ok := optionsPayloadFields(t, optionsBounded(t, n))[keyRequest]; ok {
		t.Error("payload carries a request key with no Request set")
	}
}

func TestPushOptionsKeptWhileLongQuestionIsCut(t *testing.T) {
	question := pubEscapedQuestion(6000)
	n := pubNotification(question)
	n.Options = []string{"synthetic option one", "synthetic option two"}

	fields := optionsPayloadFields(t, optionsBounded(t, n))
	if got := optionsPayloadList(t, fields); !slices.Equal(got, n.Options) {
		t.Errorf("options = %q, want %q kept whole", got, n.Options)
	}
	got := optionsPayloadString(t, fields, keyQuestion)
	if !utf8.ValidString(got) || !strings.HasPrefix(question, got) || len(got) >= len(question) {
		t.Errorf("question is %d bytes, want a valid truncated prefix of the %d-byte original", len(got), len(question))
	}
	if hash := optionsPayloadString(t, fields, keyQuestionHash); hash != protocol.QuestionHash(question) {
		t.Error("question_hash does not hash the original, untruncated question")
	}
}

func TestPushOptionsDroppedWhenOptionsDoNotFit(t *testing.T) {
	question := pubEscapedQuestion(6000)
	n := pubNotification(question)
	n.Request = "synthetic-request"
	n.Options = []string{strings.Repeat("a", 2000), strings.Repeat("b", 2000)}

	fields := optionsPayloadFields(t, optionsBounded(t, n))
	if _, ok := fields[keyOptions]; ok {
		t.Error("payload carries options, want them dropped when they do not fit with an empty question and summary")
	}
	if got := optionsPayloadString(t, fields, keyRequest); got != n.Request {
		t.Errorf("request = %q, want %q kept", got, n.Request)
	}
	if hash := optionsPayloadString(t, fields, keyQuestionHash); hash != protocol.QuestionHash(question) {
		t.Error("question_hash does not hash the original, untruncated question")
	}
}
