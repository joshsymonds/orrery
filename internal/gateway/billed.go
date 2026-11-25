package gateway

import "github.com/joshsymonds/orrery/internal/llmroutes"

// The billing names a seat may carry that bill nothing to the gateway's ledger. They are the names llmroutes validates
// a seat's billing against; a metered seat bills by the cost its upstream reports, and has no name read here.
const (
	billingSubscription = "subscription"
	billingSelfHosted   = "self_hosted"
)

// BilledCost is what one request costs in nano US dollars, and nil when that cost is unknown. Its rules are read in
// order: an answer that returned no usage costs nothing; an answer whose upstream reported a cost costs that cost; a
// forward seat, which is a subscription forward and sets no billing, costs nothing, and so does a seat billed as a
// subscription or self-hosted; anything else, such as a metered seat with no reported cost or a sigv4 seat, is unknown.
func BilledCost(seat llmroutes.Seat, forward bool, summary Summary) *int64 {
	if !summary.UsagePresent {
		return new(int64)
	}

	if summary.ProviderCostNanoUSD != nil {
		reported := *summary.ProviderCostNanoUSD

		return &reported
	}

	if forward || seat.Billing == billingSubscription || seat.Billing == billingSelfHosted {
		return new(int64)
	}

	return nil
}
