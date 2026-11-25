package gateway

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/llmroutes"
)

// fakeAnswer summarizes a fake upstream's JSON answer the way the gateway reads a captured one.
func fakeAnswer(body string) Summary {
	return Summarize([]byte(body), "application/json")
}

// requireBilledCost fails the test unless got is the known cost want, in nano US dollars.
func requireBilledCost(t *testing.T, got *int64, want int64) {
	t.Helper()

	if got == nil {
		t.Fatalf("BilledCost() = unknown; want %d", want)
	}

	if *got != want {
		t.Fatalf("BilledCost() = %d; want %d", *got, want)
	}
}

// requireUnknownBilledCost fails the test unless got is unknown.
func requireUnknownBilledCost(t *testing.T, got *int64) {
	t.Helper()

	if got != nil {
		t.Fatalf("BilledCost() = %d; want unknown", *got)
	}
}

// The fake answers are invented: each reports the same invented tokens, and the reported cost is $0.0012, which is
// 1200000 nano US dollars.
const (
	fakeAnswerWithUsage = `{"id":"msg_example","model":"example-model",` +
		`"usage":{"input_tokens":3,"output_tokens":4}}`
	fakeAnswerWithCost = `{"id":"msg_example","model":"example-model",` +
		`"usage":{"input_tokens":3,"output_tokens":4,"cost":0.0012}}`
	fakeAnswerWithZeroCost = `{"id":"msg_example","model":"example-model",` +
		`"usage":{"input_tokens":3,"output_tokens":4,"cost":0}}`
	fakeAnswerWithoutUsage = `{"id":"msg_example","model":"example-model"}`
)

const fakeReportedCostNanoUSD = 1200000

// fakeMeteredSeat is an inject seat billed by the metered rate, against an invented upstream.
func fakeMeteredSeat() llmroutes.Seat {
	return llmroutes.Seat{
		ID: "example-metered", AuthMode: llmroutes.AuthInject, Upstream: "https://upstream.example.test",
		Model: "example-model", APIKeyEnvFile: "EXAMPLE_API_KEY", Billing: "metered",
	}
}

// fakeSigV4Seat is a sigv4 seat, which sets no billing.
func fakeSigV4Seat() llmroutes.Seat {
	return llmroutes.Seat{
		ID: "example-sigv4", AuthMode: llmroutes.AuthSigV4, AWSProfile: "example-profile", AWSRegion: "example-region",
	}
}

// fakeForwardSeat is a forward seat, which sets no billing.
func fakeForwardSeat() llmroutes.Seat {
	return llmroutes.Seat{ID: "example-forward", AuthMode: llmroutes.AuthForward}
}

func TestGatewayBilledCostReportedCost(t *testing.T) {
	t.Run("metered seat takes the reported cost", func(t *testing.T) {
		got := BilledCost(fakeMeteredSeat(), false, fakeAnswer(fakeAnswerWithCost))

		requireBilledCost(t, got, fakeReportedCostNanoUSD)
	})

	t.Run("sigv4 seat takes the reported cost", func(t *testing.T) {
		got := BilledCost(fakeSigV4Seat(), false, fakeAnswer(fakeAnswerWithCost))

		requireBilledCost(t, got, fakeReportedCostNanoUSD)
	})

	t.Run("subscription forward takes the reported cost rather than zero", func(t *testing.T) {
		got := BilledCost(fakeForwardSeat(), true, fakeAnswer(fakeAnswerWithCost))

		requireBilledCost(t, got, fakeReportedCostNanoUSD)
	})

	t.Run("reported zero is zero on a metered seat", func(t *testing.T) {
		got := BilledCost(fakeMeteredSeat(), false, fakeAnswer(fakeAnswerWithZeroCost))

		requireBilledCost(t, got, 0)
	})
}

func TestGatewayBilledCostNoUsageIsZero(t *testing.T) {
	t.Run("metered seat with no usage is zero", func(t *testing.T) {
		got := BilledCost(fakeMeteredSeat(), false, fakeAnswer(fakeAnswerWithoutUsage))

		requireBilledCost(t, got, 0)
	})

	t.Run("sigv4 seat with no usage is zero rather than unknown", func(t *testing.T) {
		got := BilledCost(fakeSigV4Seat(), false, fakeAnswer(fakeAnswerWithoutUsage))

		requireBilledCost(t, got, 0)
	})
}

func TestGatewayBilledCostSubscriptionForwardIsZero(t *testing.T) {
	got := BilledCost(fakeForwardSeat(), true, fakeAnswer(fakeAnswerWithUsage))

	requireBilledCost(t, got, 0)
}

func TestGatewayBilledCostSelfHostedIsZero(t *testing.T) {
	seat := fakeMeteredSeat()
	seat.Billing = "self_hosted"

	got := BilledCost(seat, false, fakeAnswer(fakeAnswerWithUsage))

	requireBilledCost(t, got, 0)
}

func TestGatewayBilledCostSubscriptionSeatIsZero(t *testing.T) {
	seat := fakeMeteredSeat()
	seat.Billing = "subscription"

	got := BilledCost(seat, false, fakeAnswer(fakeAnswerWithUsage))

	requireBilledCost(t, got, 0)
}

func TestGatewayBilledCostMeteredWithoutReportedCostIsUnknown(t *testing.T) {
	got := BilledCost(fakeMeteredSeat(), false, fakeAnswer(fakeAnswerWithUsage))

	requireUnknownBilledCost(t, got)
}

func TestGatewayBilledCostSigV4WithoutReportedCostIsUnknown(t *testing.T) {
	got := BilledCost(fakeSigV4Seat(), false, fakeAnswer(fakeAnswerWithUsage))

	requireUnknownBilledCost(t, got)
}
