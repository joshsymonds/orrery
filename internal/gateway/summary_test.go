package gateway

import (
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"testing"

	"github.com/joshsymonds/orrery/internal/ledger"
)

// int64Ptr returns a pointer to v, for an optional count or cost.
func int64Ptr(v int64) *int64 {
	return &v
}

// float64Ptr returns a pointer to v, for an optional utilization.
func float64Ptr(v float64) *float64 {
	return &v
}

// requireSummary fails the test unless got is want.
func requireSummary(t *testing.T, got, want Summary) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Summarize() = %s; want %s", describeSummary(got), describeSummary(want))
	}
}

// describeSummary renders a Summary for a failure message, with its cost as a number rather than an address.
func describeSummary(summary Summary) string {
	cost := "nil"
	if summary.ProviderCostNanoUSD != nil {
		cost = strconv.FormatInt(*summary.ProviderCostNanoUSD, 10)
	}

	return fmt.Sprintf(
		"{model %q usage %t input %d output %d cacheRead %d cacheWrite %d (5m %d, 1h %d) cost %s complete %t}",
		summary.ResolvedModel, summary.UsagePresent, summary.InputTokens, summary.OutputTokens,
		summary.CacheReadTokens, summary.CacheWriteTokens, summary.CacheWrite5mTokens, summary.CacheWrite1hTokens,
		cost, summary.Complete)
}

// describeClaim renders a ledger.Claim for a failure message, with its pointers as numbers rather than addresses.
func describeClaim(claim ledger.Claim) string {
	utilization, reset := "nil", "nil"
	if claim.Utilization != nil {
		utilization = strconv.FormatFloat(*claim.Utilization, 'g', -1, 64)
	}
	if claim.Reset != nil {
		reset = strconv.FormatInt(*claim.Reset, 10)
	}

	return fmt.Sprintf("{%s utilization %s reset %s status %q}", claim.Name, utilization, reset, claim.Status)
}

func TestGatewayLedgerSummaryJSONAnswer(t *testing.T) {
	body := []byte(`{
		"id": "msg_01",
		"type": "message",
		"role": "assistant",
		"model": "claude-sonnet-5-0",
		"content": [{"type": "text", "text": "hi"}],
		"stop_reason": "end_turn",
		"usage": {
			"input_tokens": 12,
			"output_tokens": 34,
			"cache_read_input_tokens": 56,
			"cache_creation_input_tokens": 7
		}
	}`)

	got := Summarize(body, "application/json")

	requireSummary(t, got, Summary{
		ResolvedModel:    "claude-sonnet-5-0",
		UsagePresent:     true,
		InputTokens:      12,
		OutputTokens:     34,
		CacheReadTokens:  56,
		CacheWriteTokens: 7,
		Complete:         true,
	})
}

func TestGatewayLedgerSummarySSEAnswer(t *testing.T) {
	// The message_delta revises output_tokens from the 1 that message_start reported to the final 34, and the answer
	// keeps that final figure. Its input and cache counts come from message_start, which is the only event to name them.
	stream := "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_02","model":"claude-sonnet-5-0","usage":{"input_tokens":12,"output_tokens":1,"cache_read_input_tokens":56}}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":34}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	got := Summarize([]byte(stream), "text/event-stream; charset=utf-8")

	requireSummary(t, got, Summary{
		ResolvedModel:   "claude-sonnet-5-0",
		UsagePresent:    true,
		InputTokens:     12,
		OutputTokens:    34,
		CacheReadTokens: 56,
		Complete:        true,
	})
}

func TestGatewayLedgerSummaryOneHourCacheWrite(t *testing.T) {
	t.Run("splits the 5-minute and 1-hour writes and sums them", func(t *testing.T) {
		body := []byte(`{"model":"claude-sonnet-5-0","usage":{"input_tokens":1,"output_tokens":2,` +
			`"cache_creation":{"ephemeral_5m_input_tokens":300,"ephemeral_1h_input_tokens":4000}}}`)

		got := Summarize(body, "application/json")

		requireSummary(t, got, Summary{
			ResolvedModel:      "claude-sonnet-5-0",
			UsagePresent:       true,
			InputTokens:        1,
			OutputTokens:       2,
			CacheWrite5mTokens: 300,
			CacheWrite1hTokens: 4000,
			CacheWriteTokens:   4300,
			Complete:           true,
		})
	})

	t.Run("falls back to cache_creation_input_tokens without a split", func(t *testing.T) {
		body := []byte(`{"usage":{"cache_creation_input_tokens":50}}`)

		got := Summarize(body, "application/json")

		requireSummary(t, got, Summary{
			UsagePresent:     true,
			CacheWriteTokens: 50,
			Complete:         true,
		})
	})
}

func TestGatewayLedgerSummaryReportedCost(t *testing.T) {
	// Costs are read as decimal text and rounded half to even to whole nano US dollars, so no float ever holds one.
	valid := []struct {
		name string
		raw  string
		want int64
	}{
		{name: "a fractional cent", raw: "0.00001875", want: 18750},
		{name: "a tie rounds down to the even amount", raw: "0.0000000025", want: 2},
		{name: "a tie rounds up to the even amount", raw: "0.0000000015", want: 2},
		{name: "a tie at zero stays zero", raw: "0.0000000005", want: 0},
		{name: "an exponent", raw: "1e-9", want: 1},
	}

	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"usage":{"output_tokens":1,"cost":` + tc.raw + `}}`)

			got := Summarize(body, "application/json")

			requireSummary(t, got, Summary{
				UsagePresent:        true,
				OutputTokens:        1,
				ProviderCostNanoUSD: int64Ptr(tc.want),
				Complete:            true,
			})
		})
	}

	// A cost that is not a nonnegative JSON number is not recorded, and makes the answer incomplete.
	for _, raw := range []string{`"0.01"`, "-0.01", "true", "{}"} {
		t.Run("unreadable "+raw, func(t *testing.T) {
			body := []byte(`{"usage":{"output_tokens":1,"cost":` + raw + `}}`)

			got := Summarize(body, "application/json")

			requireSummary(t, got, Summary{
				UsagePresent: true,
				OutputTokens: 1,
				Complete:     false,
			})
		})
	}
}

func TestGatewayLedgerSummaryNoUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage string
	}{
		{name: "an answer without a usage field", usage: ""},
		{name: "an answer whose usage is null", usage: `"usage":null,`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"claude-sonnet-5-0",` + tc.usage + `"content":[],"stop_reason":"end_turn"}`)

			got := Summarize(body, "application/json")

			requireSummary(t, got, Summary{
				ResolvedModel: "claude-sonnet-5-0",
				Complete:      true,
			})
		})
	}
}

func TestGatewayLedgerSummaryUnifiedClaims(t *testing.T) {
	header := http.Header{}
	for name, value := range map[string]string{
		"anthropic-ratelimit-unified-status":                  "allowed",
		"anthropic-ratelimit-unified-reset":                   "1794549600",
		"anthropic-ratelimit-unified-representative-claim":    "seven_day",
		"anthropic-ratelimit-unified-fallback-percentage":     "0.5",
		"anthropic-ratelimit-unified-overage-disabled-reason": "org_level_disabled",
		"anthropic-ratelimit-unified-overage-status":          "rejected",
		"anthropic-ratelimit-unified-5h-utilization":          "0",
		"anthropic-ratelimit-unified-7d-utilization":          "0.59",
		"anthropic-ratelimit-unified-7d-reset":                "1794549600",
		"anthropic-ratelimit-unified-7d-status":               "allowed",
		"anthropic-ratelimit-unified-7d_oi-utilization":       "0.14",
		"anthropic-ratelimit-unified-7d_oi-reset":             "1794549600",
		"anthropic-ratelimit-unified-7d_oi-status":            "allowed",
		"request-id": "req_011CgsvmJtxQNDrU2KXkehLU",
	} {
		header.Set(name, value)
	}
	// A repeated header is ambiguous, so the 5h reset it names is left out and the 5h claim keeps only its utilization.
	header.Add("Anthropic-Ratelimit-Unified-5h-Reset", "1794549600")
	header.Add("Anthropic-Ratelimit-Unified-5h-Reset", "1794549601")

	got := UnifiedClaims(header)

	want := []ledger.Claim{
		{Name: "5h", Utilization: float64Ptr(0)},
		{Name: "7d", Utilization: float64Ptr(0.59), Reset: int64Ptr(1794549600), Status: "allowed"},
		{Name: "7d_oi", Utilization: float64Ptr(0.14), Reset: int64Ptr(1794549600), Status: "allowed"},
		{Name: "overage", Status: "rejected"},
	}

	if len(got) != len(want) {
		t.Fatalf("UnifiedClaims() has %d claims; want %d", len(got), len(want))
	}
	for index := range want {
		if !reflect.DeepEqual(got[index], want[index]) {
			t.Fatalf("UnifiedClaims()[%d] = %s; want %s", index, describeClaim(got[index]), describeClaim(want[index]))
		}
	}
}

func TestGatewayLedgerSummaryMetadataUserID(t *testing.T) {
	t.Run("reads the account and session", func(t *testing.T) {
		body := []byte(`{"model":"claude-sonnet-5-0","metadata":{"user_id":` +
			`"{\"device_id\":\"dev-1\",\"account_uuid\":\"acct-1\",\"session_id\":\"sess-1\"}"}}`)

		account, session := Caller(body)

		if account != "acct-1" || session != "sess-1" {
			t.Fatalf("Caller() = (%q, %q); want (%q, %q)", account, session, "acct-1", "sess-1")
		}
	})

	t.Run("refuses an identifier that is not printable", func(t *testing.T) {
		body := []byte(`{"metadata":{"user_id":` +
			`"{\"account_uuid\":\"acct 1\",\"session_id\":\"sess-1\"}"}}`)

		account, session := Caller(body)

		if account != "" || session != "sess-1" {
			t.Fatalf("Caller() = (%q, %q); want (%q, %q)", account, session, "", "sess-1")
		}
	})

	t.Run("names nobody without metadata", func(t *testing.T) {
		account, session := Caller([]byte(`{"model":"claude-sonnet-5-0"}`))

		if account != "" || session != "" {
			t.Fatalf("Caller() = (%q, %q); want no one", account, session)
		}
	})

	t.Run("names nobody when user_id is not an object", func(t *testing.T) {
		account, session := Caller([]byte(`{"metadata":{"user_id":"not-json"}}`))

		if account != "" || session != "" {
			t.Fatalf("Caller() = (%q, %q); want no one", account, session)
		}
	})

	t.Run("names nobody when an identifier is not a string", func(t *testing.T) {
		body := []byte(`{"metadata":{"user_id":"{\"account_uuid\":7,\"session_id\":\"sess-1\"}"}}`)

		account, session := Caller(body)

		if account != "" || session != "" {
			t.Fatalf("Caller() = (%q, %q); want no one", account, session)
		}
	})
}
