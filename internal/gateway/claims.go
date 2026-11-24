package gateway

import (
	"maps"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/ledger"
)

// limitHeaderPrefix starts every header an Anthropic response uses to report a subscription credential's rate-limit
// state. The rest of the name is a claim name followed by one of that claim's fields, so
// anthropic-ratelimit-unified-7d-utilization is the seven-day claim's utilization.
const limitHeaderPrefix = "anthropic-ratelimit-unified-"

// The claim fields a header can name.
const (
	limitFieldUtilization = "utilization"
	limitFieldReset       = "reset"
	limitFieldStatus      = "status"
)

// UnifiedClaims reads the subscription rate-limit claims a response's headers report, one per claim name, sorted by
// name. A header that is repeated is ambiguous and is left out, and so is a value its field cannot read. A header that
// names no claim, such as the top-level status, is not a claim and is left out too.
func UnifiedClaims(header http.Header) []ledger.Claim {
	claims := make(map[string]ledger.Claim)
	for name, values := range header {
		suffix, isLimit := strings.CutPrefix(strings.ToLower(name), limitHeaderPrefix)
		if !isLimit || len(values) != 1 {
			continue
		}

		claimName, field, found := cutLastDash(suffix)
		if !found || claimName == "" {
			continue
		}

		claim := claims[claimName]
		claim.Name = claimName
		if setClaimField(&claim, field, values[0]) {
			claims[claimName] = claim
		}
	}

	names := slices.Sorted(maps.Keys(claims))
	result := make([]ledger.Claim, 0, len(names))
	for _, name := range names {
		result = append(result, claims[name])
	}

	return result
}

// cutLastDash splits a header suffix at its last dash: the claim name before it and the field after it.
func cutLastDash(suffix string) (string, string, bool) {
	separator := strings.LastIndexByte(suffix, '-')
	if separator < 0 {
		return "", "", false
	}

	return suffix[:separator], suffix[separator+1:], true
}

// setClaimField records one header value in the claim it belongs to. It reports false, and leaves the claim as it was,
// for a field that is not one a claim has or a value that field cannot read.
func setClaimField(claim *ledger.Claim, field, value string) bool {
	switch field {
	case limitFieldUtilization:
		utilization, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(utilization) || math.IsInf(utilization, 0) || utilization < 0 {
			return false
		}

		claim.Utilization = &utilization
	case limitFieldReset:
		reset, err := strconv.ParseInt(value, 10, 64)
		if err != nil || reset <= 0 {
			return false
		}

		claim.Reset = &reset
	case limitFieldStatus:
		claim.Status = value
	default:
		return false
	}

	return true
}
