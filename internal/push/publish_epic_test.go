package push

import (
	"encoding/json"
	"maps"
	"os"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// epicPayloadsPath is the fixture shared with the app test, which pushes the same payloads.
const epicPayloadsPath = "testdata/epic_payloads.json"

// epicPayloads is the decoded fixture: the exact notice and clear payload objects.
type epicPayloads struct {
	Notice map[string]string `json:"notice"`
	Clear  map[string]string `json:"clear"`
}

func loadEpicPayloads(t *testing.T) epicPayloads {
	t.Helper()
	data, err := os.ReadFile(epicPayloadsPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture epicPayloads
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return fixture
}

// decodePayload decodes one payload body into a JSON object of strings.
func decodePayload(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var payload map[string]string
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("payload is not a JSON object of strings: %v", err)
	}
	return payload
}

func epicNotice() Notification {
	return Notification{
		Host:    "synthetic-host",
		Epic:    "demo-0a1b2c3/epic-login",
		Project: "Login flow",
		State:   protocol.StateWaiting,
		Summary: "Q1: which store?\nQ2: which port?",
		Time:    1700000200,
	}
}

func epicClear() Clear {
	return Clear{Host: "synthetic-host", Epic: "demo-0a1b2c3/epic-login", Seen: 1700000300}
}

func TestPushEpicNoticePayloadMatchesFixture(t *testing.T) {
	t.Parallel()
	body, err := boundedPayload(epicNotice())
	if err != nil {
		t.Fatalf("boundedPayload: %v", err)
	}
	want := loadEpicPayloads(t).Notice
	if got := decodePayload(t, body); !maps.Equal(got, want) {
		t.Errorf("notice payload = %v, want exactly %v", got, want)
	}
}

func TestPushEpicClearPayloadMatchesFixture(t *testing.T) {
	t.Parallel()
	body, err := clearPayload(epicClear())
	if err != nil {
		t.Fatalf("clearPayload: %v", err)
	}
	want := loadEpicPayloads(t).Clear
	if got := decodePayload(t, body); !maps.Equal(got, want) {
		t.Errorf("clear payload = %v, want exactly %v", got, want)
	}
}

func TestPushEpicKeyOmittedWithoutEpic(t *testing.T) {
	t.Parallel()
	n := epicNotice()
	n.Epic = ""
	notice, err := boundedPayload(n)
	if err != nil {
		t.Fatalf("boundedPayload: %v", err)
	}
	if _, ok := decodePayload(t, notice)["epic"]; ok {
		t.Error("notice without Epic carries an epic key")
	}

	c := epicClear()
	c.Epic = ""
	clearBody, err := clearPayload(c)
	if err != nil {
		t.Fatalf("clearPayload: %v", err)
	}
	if _, ok := decodePayload(t, clearBody)["epic"]; ok {
		t.Error("clear without Epic carries an epic key")
	}
}
